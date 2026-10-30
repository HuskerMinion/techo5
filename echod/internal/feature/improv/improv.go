//go:build linux && !panel

package improv

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/bluetooth"
	"github.com/HuskerMinion/techo5/echod/internal/feature/web"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/buttons"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
	"github.com/HuskerMinion/techo5/echod/internal/lib/bluez"
	proto "github.com/HuskerMinion/techo5/echod/internal/lib/improv"
	"github.com/HuskerMinion/techo5/echod/internal/lib/wifi"
)

func init() {
	component.Register(component.Network, Get(), component.Order(96))
}

const (
	// bootGrace is how long after starting an address may take before the device offers Improv by
	// itself: a DHCP server can be slow, and a device that is merely late joining must not open up.
	bootGrace = 45 * time.Second

	// idle is how long it stays open with nothing sent; a connect or any command starts it over.
	idle = 5 * time.Minute

	// most is how long it stays open at all, whatever is sent.
	most = 15 * time.Minute

	// doneHold is how long it stays up after a network was joined, so the phone can read the address.
	doneHold = 10 * time.Second

	// joinFor is how long a join may take: wifi.Join waits a minute for the association itself.
	joinFor = 80 * time.Second

	root = dbus.ObjectPath("/org/techo5/improv")
)

// Why it opened, which decides whether a network is taken at once.
type why int

const (
	offline why = iota // by itself, at boot: a button press is needed first
	asked              // the mute button held: taken at once
)

func (w why) String() string {
	if w == asked {
		return "mute button held"
	}
	return "no network after boot"
}

// Improv is the session, at most one at a time.
type Improv struct {
	asks    chan why
	pressed chan struct{}
	stop    chan struct{}

	listen sync.Once

	mu   sync.Mutex
	open bool

	// cannot is set once neither bluetoothd nor the proxy's radio could advertise: that does not
	// change while the kernel stays the same, so it is said once and not tried again.
	cannot bool
}

var (
	once   sync.Once
	shared *Improv
)

// Get is the one Improv.
func Get() *Improv {
	once.Do(func() {
		shared = &Improv{asks: make(chan why, 1), pressed: make(chan struct{}, 1), stop: make(chan struct{}, 1)}
	})
	return shared
}

func (m *Improv) Name() string { return "improv wi-fi" }

// Start listens to the buttons, once: Start runs again on every restart of the loop, and a second
// listener would hear every press twice. Listeners must not block, so this only drops a note in a
// channel.
func (m *Improv) Start(context.Context) error {
	if !Here {
		return nil
	}
	m.listen.Do(func() { buttons.Get().Events.Listen(m.button) })
	return nil
}

// button is the mute button held, which opens Improv or, while it is open, closes it again; and any
// press while it is open, which is the authorization an offer made by itself waits for.
func (m *Improv) button(e buttons.Event) {
	if e.Name == buttons.Mute && e.Kind == buttons.LongHold {
		ch := m.asks
		if m.isOpen() {
			ch = nil
			select {
			case m.stop <- struct{}{}:
			default:
			}
		}
		select {
		case ch <- asked:
		default:
		}
		return
	}
	if m.isOpen() && (e.Kind == buttons.Tap || e.Kind == buttons.Hold) {
		select {
		case m.pressed <- struct{}{}:
		default:
		}
	}
}

func (m *Improv) isOpen() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.open
}

// Run offers Improv once if the boot leaves the device offline, then whenever it is asked for.
func (m *Improv) Run(ctx context.Context) error {
	if !Here {
		<-ctx.Done()
		return nil
	}
	if !wifi.Available() {
		slog.Info("improv: no supplicant on this device, nothing to offer")
		<-ctx.Done()
		return nil
	}
	boot := time.NewTimer(bootGrace)
	defer boot.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-boot.C:
			if wifi.Current(ctx).Address == "" {
				m.session(ctx, offline)
			}
		case w := <-m.asks:
			m.session(ctx, w)
		}
	}
}

// session is one offer, from the advertisement going up to it coming down.
func (m *Improv) session(ctx context.Context, w why) {
	if m.cannot {
		return
	}
	m.mu.Lock()
	m.open = true
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		m.open = false
		m.mu.Unlock()
	}()
	// Drain a press from before it opened: it was for something else.
	select {
	case <-m.pressed:
	default:
	}
	select {
	case <-m.stop:
	default:
	}

	state := proto.AuthorizationRequired
	if w == asked {
		state = proto.Authorized
	}
	caps := proto.CanIdentify | proto.CanDeviceInfo | proto.CanScan

	commands := make(chan []byte, 8)
	write := func(b []byte) {
		select {
		case commands <- b:
		default:
			slog.Warn("improv: dropped a command, the last ones are still being handled")
		}
	}
	// The proxy's radio goes quiet first: on the Dot it sends an iBeacon of its own, straight to the
	// controller, which would talk over the advertisement bluetoothd is about to send for this.
	if bluetooth.Here {
		release := bluetooth.Get().Hold()
		defer release()
	}

	name := deviceName()
	p, err := bluez.OpenPeripheral(root, proto.ServiceUUID, []bluez.CharSpec{
		{UUID: proto.StateUUID, Flags: []string{"read", "notify"}, Value: []byte{byte(state)}},
		{UUID: proto.ErrorUUID, Flags: []string{"read", "notify"}, Value: []byte{byte(proto.ErrNone)}},
		{UUID: proto.RPCCommandUUID, Flags: []string{"write", "write-without-response"}, Write: write},
		{UUID: proto.RPCResultUUID, Flags: []string{"read", "notify"}},
		{UUID: proto.CapabilitiesUUID, Flags: []string{"read"}, Value: []byte{byte(caps)}},
	}, bluez.AdvertSpec{
		ServiceUUIDs: []string{proto.ServiceUUID},
		ServiceData:  map[string][]byte{proto.ServiceDataUUID: proto.ServiceData(state, caps)},
		LocalName:    name,
	})
	if err != nil {
		slog.Warn("improv: could not offer Wi-Fi setup over Bluetooth", "err", err)
		chime(speaker.FailureSound())
		return
	}
	defer p.Close()
	// bluetoothd serves the service but cannot advertise it on a kernel before Add Advertising (the
	// Dot's 3.18): the proxy's radio, held above, advertises it instead, over the same hci0.
	if !p.Advertising() {
		done, err := offer(proto.Advertisement(state, caps), proto.ScanResponse(name))
		if err != nil {
			slog.Info("improv: this device's Bluetooth cannot advertise, Wi-Fi setup over Bluetooth is off", "err", err)
			m.cannot = true
			return
		}
		defer done()
	}

	wifi.SettingUp(true)
	defer wifi.SettingUp(false)
	slog.Info("improv: offering Wi-Fi setup over Bluetooth", "why", w, "name", name, "state", state)
	chime(speaker.TonePairing)

	s := &run{p: p, state: state, scan: wifi.Scan}
	idleT := time.NewTimer(idle)
	defer idleT.Stop()
	mostT := time.NewTimer(most)
	defer mostT.Stop()
	var doneC <-chan time.Time
	joined := make(chan joinResult, 1)

	for {
		select {
		case <-ctx.Done():
			return
		case <-idleT.C:
			slog.Info("improv: nothing sent, closing")
			chime(speaker.TonePairingOff)
			return
		case <-mostT.C:
			slog.Info("improv: open long enough, closing")
			chime(speaker.TonePairingOff)
			return
		case <-doneC:
			return
		case <-m.stop:
			slog.Info("improv: closed with the mute button")
			chime(speaker.TonePairingOff)
			return
		case <-m.pressed:
			if s.state == proto.AuthorizationRequired {
				slog.Info("improv: allowed by a button press")
				s.setState(proto.Authorized)
				chime(speaker.ToneBTConnect)
				resetTimer(idleT, idle)
			}
		case b := <-commands:
			resetTimer(idleT, idle)
			if wf, ok := s.command(ctx, b); ok {
				go func() {
					jctx, cancel := context.WithTimeout(ctx, joinFor)
					defer cancel()
					err := wifi.Join(jctx, wf.SSID, wf.Passphrase)
					joined <- joinResult{err: err}
				}()
			}
		case r := <-joined:
			if r.err != nil {
				slog.Warn("improv: could not join the network sent", "err", r.err)
				s.setError(proto.ErrUnableToConnect)
				s.setState(proto.Authorized)
				chime(speaker.FailureSound())
				resetTimer(idleT, idle)
				continue
			}
			url := setupURL(ctx)
			slog.Info("improv: joined", "setup", url)
			s.setState(proto.Provisioned)
			s.result(proto.Result(proto.CmdWiFiSettings, url))
			chime(speaker.TonePaired)
			idleT.Stop()
			doneC = time.After(doneHold)
		}
	}
}

// offer advertises the service through the proxy's held radio.
func offer(advertisement, scanResponse []byte) (done func(), err error) {
	if !bluetooth.Here {
		return nil, bluetooth.ErrCannotOffer
	}
	return bluetooth.Get().Offer(advertisement, scanResponse)
}

type joinResult struct{ err error }

// chars is where a session's values go: the Peripheral, or a test's record of them.
type chars interface {
	Set(uuid string, value []byte)
}

// run is the characteristics' side of a session: what they hold, and changing it.
type run struct {
	p     chars
	state proto.State
	scan  func(context.Context) ([]wifi.Network, error)
}

func (s *run) setState(st proto.State) {
	s.state = st
	s.p.Set(proto.StateUUID, []byte{byte(st)})
}

func (s *run) setError(e proto.Error) { s.p.Set(proto.ErrorUUID, []byte{byte(e)}) }
func (s *run) result(b []byte)        { s.p.Set(proto.RPCResultUUID, b) }

// command handles one frame. It returns the network to join when the frame was one, ready to be.
func (s *run) command(ctx context.Context, b []byte) (proto.WiFi, bool) {
	f, err := proto.Parse(b)
	if err != nil {
		s.setError(proto.ErrInvalidRPC)
		return proto.WiFi{}, false
	}
	s.setError(proto.ErrNone)
	switch f.Command {
	case proto.CmdWiFiSettings:
		// Only while Authorized: not before the press, not while one is being joined, and not in
		// the seconds after one was, before the session closes.
		if s.state != proto.Authorized {
			if s.state != proto.Provisioning {
				s.setError(proto.ErrNotAuthorized)
			}
			return proto.WiFi{}, false
		}
		w, err := proto.ParseWiFi(f.Data)
		if err != nil {
			s.setError(proto.ErrInvalidRPC)
			return proto.WiFi{}, false
		}
		slog.Info("improv: joining the network sent", "ssid", w.SSID)
		s.setState(proto.Provisioning)
		return w, true
	case proto.CmdIdentify:
		chime(speaker.TonePairing)
	case proto.CmdDeviceInfo:
		if s.state == proto.AuthorizationRequired {
			s.setError(proto.ErrNotAuthorized)
			return proto.WiFi{}, false
		}
		s.result(proto.Result(proto.CmdDeviceInfo, "TECHO5", layout.Version, hardware(), deviceName()))
	case proto.CmdScan:
		// Before the press, anyone in range could list the networks around the device.
		if s.state == proto.AuthorizationRequired {
			s.setError(proto.ErrNotAuthorized)
			return proto.WiFi{}, false
		}
		nets, err := s.scan(ctx)
		if err != nil {
			slog.Warn("improv: scan failed", "err", err)
		}
		for _, n := range nets {
			secured := "NO"
			if n.Secured {
				secured = "YES"
			}
			s.result(proto.Result(proto.CmdScan, n.SSID, strconv.Itoa(n.Signal), secured))
		}
		s.result(proto.Result(proto.CmdScan))
	default:
		s.setError(proto.ErrUnknownCommand)
	}
	return proto.WiFi{}, false
}

// setupURL is the setup page at the address the device now has: where the phone is sent next.
func setupURL(ctx context.Context) string {
	addr := wifi.Current(ctx).Address
	if addr == "" {
		return ""
	}
	return fmt.Sprintf("http://%s:%d/", addr, web.Port)
}

// deviceName is the name the device was given, or its host name before it has one.
func deviceName() string {
	if n := config.Get().Device.Name; n != "" {
		return n
	}
	h, _ := os.Hostname()
	return h
}

// hardware is the hardware variant for device info: the board as the device tree or the kernel
// names it, which is what tells one speaker from another without a build of its own for each.
func hardware() string {
	if b, err := os.ReadFile("/proc/device-tree/model"); err == nil {
		if m := strings.TrimRight(string(b), "\x00\n "); m != "" {
			return m
		}
	}
	if b, err := os.ReadFile("/proc/cpuinfo"); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			if k, v, ok := strings.Cut(line, ":"); ok && strings.TrimSpace(k) == "Hardware" {
				return strings.TrimSpace(v)
			}
		}
	}
	return "unknown"
}

func chime(notes []speaker.Note) {
	if config.Quiet() {
		return
	}
	speaker.Sound().Chime(notes)
}

func resetTimer(t *time.Timer, d time.Duration) {
	if !t.Stop() {
		select {
		case <-t.C:
		default:
		}
	}
	t.Reset(d)
}
