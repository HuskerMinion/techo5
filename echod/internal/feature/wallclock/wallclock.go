package wallclock

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/bluetooth"
	"github.com/HuskerMinion/techo5/echod/internal/feature/timer"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
	"github.com/HuskerMinion/techo5/echod/internal/lib/bluez"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
)

// Here is whether this device has the Bluetooth stack the clock needs: BlueZ over BR/EDR, and a
// kernel with RFCOMM (boardHasRFCOMM). Not a panel, whose Bluetooth is Android's.
func Here() bool { return !layout.OnAndroid() && boardHasRFCOMM() }

func init() {
	if !Here() {
		return
	}
	component.Register(component.Device, Get(), component.Order(32))
}

const (
	// connectTimeout bounds one attempt at the clock. Out of range it keeps paging until the page
	// timeout, which is the kernel's, and then the loop waits before trying again.
	connectTimeout = 10 * time.Second

	// handshakeTimeout bounds the Discover handshake once the link is up.
	handshakeTimeout = 10 * time.Second

	// pairingWindow is how long "Pair the clock" looks for a clock in pairing mode, matching the
	// standalone bridge: enough time to hold the clock's button and put it down again.
	pairingWindow = 2 * time.Minute

	// reconnectDelay is how long to wait after a link ends or a clock is not there before trying
	// again, so an absent clock costs a page timeout every so often rather than a busy loop.
	reconnectDelay = 10 * time.Second

	// idleWait is how long the loop sleeps with nothing to do. A switch or a pairing wakes it.
	idleWait = 30 * time.Second

	// pollInterval is the safety net under the timer hook: one look a second, which is also how
	// often the finish pulse has to be cleared.
	pollInterval = time.Second
)

// Feature is the wall clock bridge.
type Feature struct {
	sw         *esphome.Switch
	pairButton *esphome.Button
	status     *esphome.TextSensor

	// wake is a switch, pairing or saved address asking the loop to look at the config again.
	wake chan struct{}

	// timerWake is the timer feature saying a timer started, changed, ended or rang. It is separate
	// from wake so a timer change cannot be mistaken for a config change by a live link.
	timerWake chan struct{}

	// gate is the radio: held by the loop while it dials and looks after a link, and by a pairing
	// attempt while it searches. One at a time, so a reconnect cannot take the adapter from a
	// pairing and a pairing cannot search with a live link up.
	gate sync.Mutex

	mu      sync.Mutex
	pairing bool
	live    *conn // the link the loop is serving, while it holds the gate
	shown   string
}

var (
	once   sync.Once
	shared *Feature
)

// Get is the wall clock bridge.
func Get() *Feature {
	once.Do(func() { shared = build() })
	return shared
}

func build() *Feature {
	f := &Feature{
		wake:      make(chan struct{}, 1),
		timerWake: make(chan struct{}, 1),
		sw: &esphome.Switch{Base: esphome.Base{
			ObjectID: "wall_clock", Name: "Wall clock", Icon: "mdi:clock-outline",
			Category: esphome.CategoryConfig,
		}},
		pairButton: &esphome.Button{Base: esphome.Base{
			ObjectID: "wall_clock_pair", Name: "Pair wall clock", Icon: "mdi:bluetooth-connect",
			Category: esphome.CategoryConfig,
		}},
		status: &esphome.TextSensor{Base: esphome.Base{
			ObjectID: "wall_clock_status", Name: "Wall clock status", Icon: "mdi:bluetooth",
			Category: esphome.CategoryDiagnostic,
		}},
	}
	f.sw.OnCommand = f.SetOn
	f.pairButton.OnPress = func() {
		if err := f.Pair(); err != nil {
			slog.Warn("wall clock: pairing not started", "err", err)
		}
	}
	return f
}

func (f *Feature) Name() string { return "wall clock" }

func (f *Feature) Entities() []esphome.Entity { return []esphome.Entity{f.sw, f.pairButton, f.status} }

// Restore puts the switch back the way the device was left.
func (f *Feature) Restore(c config.Config) {
	f.sw.Set(c.WallClock.Enabled)
	if c.WallClock.Enabled {
		f.setStatus("Connecting")
	} else {
		f.setStatus("Off")
	}
}

// Enabled is the switch's state, as saved.
func (f *Feature) Enabled() bool { return config.Get().WallClock.Enabled }

// Address is the paired clock's address, or empty.
func (f *Feature) Address() string { return config.Get().WallClock.Address }

// Here is whether this device has the Bluetooth stack the clock needs; the setup page asks before it
// offers the section.
func (f *Feature) Here() bool { return Here() }

// Pairing is whether "Pair the clock" is looking now.
func (f *Feature) Pairing() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pairing
}

// Status is what the clock is doing, for Home Assistant and the setup page.
func (f *Feature) Status() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.shown
}

// Set is the switch and the setup page: turn the bridge on or off, and save an address typed in.
func (f *Feature) Set(on bool, address string) error {
	if address != "" {
		hw, err := net.ParseMAC(strings.TrimSpace(address))
		if err != nil || len(hw) != 6 {
			return fmt.Errorf("%q is not a Bluetooth address", address)
		}
	}
	if err := config.Set().WallClock().Set(on, strings.TrimSpace(address)); err != nil {
		return err
	}
	f.sw.Set(on)
	if on {
		f.setStatus("Connecting")
	} else {
		f.setStatus("Off")
	}
	f.wakeLoop()
	return nil
}

// SetOn is the switch.
func (f *Feature) SetOn(on bool) {
	if err := f.Set(on, ""); err != nil {
		slog.Error("wall clock: saving the switch failed", "err", err)
		f.sw.Set(!on)
	}
}

// Pair looks for a clock in pairing mode, pairs with it, trusts it and saves its address. It returns
// once the search has started: the window lasts pairingWindow and its progress is in Status.
func (f *Feature) Pair() error {
	if !f.Enabled() {
		return errors.New("turn the wall clock on first")
	}
	f.mu.Lock()
	if f.pairing {
		f.mu.Unlock()
		return errors.New("already looking for a clock")
	}
	f.pairing = true
	live := f.live
	f.mu.Unlock()

	f.setStatus("Pairing")

	// Let go of the clock first: a live link is the loop's, and searching with it up would leave the
	// clock connected while bluetoothd is asked to pair it again. Closing it ends serve, and the gate
	// below waits for the loop to let go of the adapter.
	if live != nil {
		live.close()
	}

	safe.Go("wall clock pairing", func() {
		defer f.wakeLoop()
		f.gate.Lock()
		defer f.gate.Unlock()
		pairClock(f)
	})
	return nil
}

// pairClock is Pair's search. A variable for the tests, which have no Bluetooth adapter.
var pairClock = func(f *Feature) { f.pair(f.Address()) }

// dialClock connects to the clock. A variable for the tests, which have no Bluetooth adapter.
var dialClock = dial

// pair is Pair's search, on its own goroutine.
func (f *Feature) pair(known string) {
	defer func() {
		f.mu.Lock()
		f.pairing = false
		f.mu.Unlock()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), pairingWindow)
	defer cancel()

	a, err := bluez.Open(ctx)
	if err != nil {
		slog.Warn("wall clock: no bluetooth adapter to pair with", "err", err)
		f.failed("Pairing failed: no Bluetooth")
		return
	}
	defer a.Close()
	if !a.IsPowered() {
		if err := a.Powered(true); err != nil {
			slog.Warn("wall clock: could not power bluetooth", "err", err)
			f.failed("Pairing failed: Bluetooth is off")
			return
		}
	}

	// The Bluetooth proxy scans through this adapter as well. Take the radio off for the search and
	// put it back afterwards, so its scan stops and starts with ours rather than being stopped
	// underneath it by the Discover(false) below.
	release, free := holdRadio()
	if !free {
		slog.Warn("wall clock: pairing while Wi-Fi setup has the Bluetooth radio")
		f.failed("Pairing failed: Bluetooth is busy with Wi-Fi setup")
		return
	}
	defer release()

	// The clock may still be connected at the link level after the loop's RFCOMM socket was closed:
	// bluetoothd keeps a paired device's ACL up, and a channel the clock has not acknowledged
	// disconnecting stays busy for the next connect. Drop the link here, before discovery, so the
	// clock is free and the loop's next connect starts cleanly.
	if known != "" {
		if err := a.Disconnect(ctx, known); err != nil {
			slog.Debug("wall clock: no link to drop before pairing", "address", known, "err", err)
		}
	}

	if err := a.Discover(true); err != nil {
		slog.Warn("wall clock: could not start discovery", "err", err)
		f.failed("Pairing failed: could not search")
		return
	}
	defer func() {
		if err := a.Discover(false); err != nil {
			slog.Debug("wall clock: stopping discovery", "err", err)
		}
	}()

	d, ok := f.findClock(ctx, a, known)
	if !ok {
		f.failed("No clock found in pairing mode")
		return
	}
	f.setStatus("Pairing with " + displayName(d))

	// The agent says no to pairings outside pairing mode; this is the one device it should say yes
	// to for the length of this attempt.
	done := bluez.AllowPairing(d.Path)
	err = a.Pair(ctx, d.Address)
	done()
	if err != nil {
		// bluetoothd can report an error after the bond was made, so the clock's own state is what
		// decides whether to go on.
		if now, ok := a.Device(d.Address); ok && now.Paired {
			slog.Info("wall clock: paired, though bluetoothd reported an error", "err", err)
		} else {
			slog.Warn("wall clock: pairing failed", "address", d.Address, "err", err)
			f.failed("Pairing failed")
			return
		}
	}
	if err := a.Trust(d.Address, true); err != nil {
		slog.Warn("wall clock: could not trust the clock", "address", d.Address, "err", err)
	}

	// Keep the switch as it is: somebody may have turned the clock off while this was looking.
	on := f.Enabled()
	if err := config.Set().WallClock().Set(on, d.Address); err != nil {
		slog.Error("wall clock: saving the clock's address failed", "err", err)
		f.failed("Paired, but the address could not be saved")
		return
	}
	slog.Info("wall clock: paired", "address", d.Address, "name", d.Name)

	f.mu.Lock()
	f.pairing = false
	f.mu.Unlock()
	if on {
		f.setStatus("Connecting")
		f.wakeLoop()
	} else {
		f.setStatus("Off")
	}
}

// holdRadio takes the radio off the Bluetooth proxy until the returned release, and reports whether
// it got it. A pairing search uses the same adapter, and the proxy's scan must stop and start with it
// rather than have its own discovery stopped by the search's Discover(false). On the Dot the proxy's
// radio is the controller itself; on a Show it is bluetoothd's LE discovery.
//
// False means the radio is lent to an advertisement the proxy cannot stop, such as Improv's Wi-Fi
// setup on a Dot: a search must not start discovery while that is up, since this controller refuses a
// scan that comes after advertising.
func holdRadio() (func(), bool) {
	if !bluetooth.Here {
		return func() {}, true
	}
	b := bluetooth.Get()
	// Ask before holding: Hold waits for a lent radio to stop and it never does, so a search would
	// stall for that wait only to refuse anyway.
	if b.Offering() {
		return func() {}, false
	}
	release := b.Hold()
	// And again after: an offer may have started while the radio was stopping.
	if b.Offering() {
		release()
		return func() {}, false
	}
	return release, true
}

// findClock waits for the clock to answer discovery: the address saved before, or the name a clock
// in pairing mode advertises.
func (f *Feature) findClock(ctx context.Context, a *bluez.Adapter, known string) (bluez.Device, bool) {
	for {
		if !f.Enabled() {
			return bluez.Device{}, false
		}
		if d, ok := clockIn(a.Devices(), known); ok {
			return d, true
		}
		select {
		case <-ctx.Done():
			return bluez.Device{}, false
		case <-time.After(time.Second):
		}
	}
}

// clockIn is the clock among devices: the one at known, or one whose name is a clock's.
func clockIn(devices []bluez.Device, known string) (bluez.Device, bool) {
	for _, d := range devices {
		if known != "" {
			if strings.EqualFold(d.Address, known) {
				return d, true
			}
			continue
		}
		if looksLikeClock(d.Name) {
			return d, true
		}
	}
	return bluez.Device{}, false
}

// looksLikeClock is the name an Echo Wall Clock advertises in pairing mode.
func looksLikeClock(name string) bool {
	n := strings.ToLower(name)
	return strings.Contains(n, "echowallclk") || strings.Contains(n, "ewcmickeymouse")
}

func displayName(d bluez.Device) string {
	if d.Name != "" {
		return d.Name
	}
	return d.Address
}

// failed leaves a pairing failure on the status sensor. The loop puts its own state back the next
// time the config changes.
func (f *Feature) failed(why string) {
	f.mu.Lock()
	f.pairing = false
	f.mu.Unlock()
	f.setStatus(why)
}

// setStatus publishes a change only, so a second-by-second loop does not flood Home Assistant.
func (f *Feature) setStatus(s string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.pairing && !strings.HasPrefix(s, "Pairing") {
		return
	}
	if s == f.shown {
		return
	}
	f.shown = s
	f.status.Set(s)
}

func (f *Feature) wakeLoop() {
	select {
	case f.wake <- struct{}{}:
	default:
	}
}

// Run keeps the clock connected and showing the timers. The timer hook is the usual way a change is
// heard; the ticker is the safety net under it and what clears a finish pulse.
func (f *Feature) Run(ctx context.Context) error {
	cancel := timer.Get().Changed.Listen(func(struct{}) {
		select {
		case f.timerWake <- struct{}{}:
		default:
		}
	})
	defer cancel()

	for {
		if ctx.Err() != nil {
			return nil
		}
		cfg := config.Get().WallClock
		switch {
		case !cfg.Enabled:
			f.setStatus("Off")
			f.wait(ctx, idleWait)
			continue
		case cfg.Address == "":
			f.setStatus("No clock paired")
			f.wait(ctx, idleWait)
			continue
		}

		f.setStatus("Connecting")

		// Pairing owns the radio while it searches. Wait for it rather than page a clock it may be
		// about to pair with, and take the gate before dialing so a search that starts now waits for
		// this attempt instead of overlapping with it.
		if f.Pairing() {
			f.wait(ctx, idleWait)
			continue
		}
		f.gate.Lock()
		if f.Pairing() {
			f.gate.Unlock()
			f.wait(ctx, idleWait)
			continue
		}
		// The config may have changed while the gate was held by a pairing.
		cfg = config.Get().WallClock
		if !cfg.Enabled || cfg.Address == "" {
			f.gate.Unlock()
			continue
		}

		c, err := dialClock(ctx, cfg.Address, connectTimeout, handshakeTimeout)
		if err != nil {
			f.gate.Unlock()
			if ctx.Err() != nil {
				return nil
			}
			slog.Warn("wall clock: could not connect", "address", cfg.Address, "err", err)
			f.setStatus("Waiting for the clock")
			f.wait(ctx, reconnectDelay)
			continue
		}
		// A pairing that began while this dial was in flight had no live link to close. Hand the
		// link over only if it did not: otherwise let it go and let the pairing have the adapter.
		if !f.setLive(c) {
			c.close()
			f.gate.Unlock()
			f.wait(ctx, idleWait)
			continue
		}

		b := New(Config{Timezone: time.Local}, ClockOutput{Sink: c})
		if err := b.OnConnect(time.Now()); err != nil {
			slog.Warn("wall clock: the link failed at once", "err", err)
			f.clearLive(c)
			c.close()
			f.gate.Unlock()
			f.wait(ctx, reconnectDelay)
			continue
		}
		f.setStatus("Connected")
		slog.Info("wall clock: connected", "address", cfg.Address)

		f.serve(ctx, c, b, cfg.Address)
		f.clearLive(c)
		c.close()
		f.gate.Unlock()
		if ctx.Err() != nil {
			return nil
		}
		slog.Info("wall clock: link ended", "err", c.Err())
		f.setStatus("Disconnected")
		f.wait(ctx, reconnectDelay)
	}
}

// setLive records the link the loop is serving, unless a pairing started first. It returns whether
// the link was taken: a false answer means the caller must close it and give the radio back.
func (f *Feature) setLive(c *conn) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.pairing {
		return false
	}
	f.live = c
	return true
}

// clearLive forgets a link once it has ended. It only clears the link it was given, so a pairing
// cannot have a later connection's entry taken away by an earlier one.
func (f *Feature) clearLive(c *conn) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.live == c {
		f.live = nil
	}
}

// serve steps the bridge on every timer change and once a second until the link or the reason to
// hold it goes away.
func (f *Feature) serve(ctx context.Context, c *conn, b *Bridge, address string) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	step := func() bool {
		now := time.Now()
		if err := b.Step(timers(now), now); err != nil {
			slog.Warn("wall clock: sending failed", "err", err)
			return false
		}
		return true
	}
	if !step() {
		return
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-c.Done():
			return
		case <-f.timerWake:
		case <-ticker.C:
		case <-f.wake:
			cfg := config.Get().WallClock
			if !cfg.Enabled || cfg.Address != address {
				return
			}
			continue
		}
		if !step() {
			return
		}
	}
}

// timers is the timer feature's list as the clock wants it: running timers only, each with the
// moment it ends, which is what the clock draws.
func timers(now time.Time) []Timer {
	held := timer.Get().List(now)
	out := make([]Timer, 0, len(held))
	for _, t := range held {
		if !t.Active {
			continue
		}
		out = append(out, Timer{ID: t.ID, Name: t.Name, Due: now.Add(t.Left)})
	}
	return out
}

// wait sleeps for d, or until the config changes. It answers false when the context is done.
func (f *Feature) wait(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-f.wake:
		return true
	case <-t.C:
		return true
	}
}
