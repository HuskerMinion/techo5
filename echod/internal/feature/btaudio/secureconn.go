//go:build linux

package btaudio

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
)

// Secure Connections (#183). The Shows' MediaTek controller has Secure Connections on, makes a P-256
// link key with a phone, and encrypts with E0 all the same; btbridge passes the key on as the P-192
// kind so the kernel keeps the phone (#105, cmd/btbridge/h4.go p192Key). Most phones then stay. Some
// newer ones (a POCO F8 Pro on Android 16) authenticate the link once more in Secure Connections
// mode, fail with "Authentication Failed" (0x05), and are dropped a second or two after pairing.
// With Secure Connections off they pair the older way and stay. That is not for every device: it is
// a setting, on by default as before, that a device with such a phone turns off.
//
// The daemon puts it on the controller with btmgmt, as techo5-lib.sh does with the rest of the
// controller: power off, sc on or off, power on. The kernel keeps it across power cycles but not
// across a new hci0, and bluetoothd turns it back on as it takes an adapter (its main.conf default),
// so it is put back each time hci0 comes up or comes back (bluez.Adapter.Returned). Powering off
// drops whatever is connected for a moment, so nothing is done when the controller already has what
// the setting says, and nothing on a controller without Secure Connections (btmgmt info's supported
// settings). Athena on mainline has it off for good (device.conf BT_SC=off, t5_bt_stack, in place of
// btbridge's -p192-keys) and offers no setting.

const (
	// scSettle is how long after hci0 comes up or comes back the controller is left alone first. At
	// boot the Shows and the Spot bring Bluetooth up before the daemon starts (boot.sh t5_bt_up), and
	// the Dot's techo5-bt is waited for; a new hci0 has t5_bt_again setting its class and making it
	// unfindable, twice, 5 s apart, and bluetoothd setting it up. A power cycle in the middle of
	// either is what this waits out.
	scSettle = 12 * time.Second

	// busyFor caps the wait for t5_bt_again's marker (/run/techo5-bt-again.busy): it retries the
	// class for up to a minute on a Spot that is slow to take it, and a pass whose every btmgmt runs
	// into its 5 s timeout takes about 140 s.
	busyFor = 150 * time.Second

	// The btmgmt timeouts techo5-lib.sh uses: 3 s to read, 5 s to set.
	btmgmtRead = 3 * time.Second
	btmgmtSet  = 5 * time.Second
)

// btmgmtRunner runs btmgmt on hci0 with args and a timeout, and gives what it printed.
type btmgmtRunner func(ctx context.Context, timeout time.Duration, args ...string) (string, error)

func runBtmgmt(ctx context.Context, timeout time.Duration, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "btmgmt", append([]string{"-i", "hci0"}, args...)...).CombinedOutput()
	return string(out), err
}

// haveBtmgmt reports whether btmgmt is in this image. Every TECHO5 image carries it (bluez-btmgmt in
// the Shows' and the Spot's packages-rootfs.txt and the Dot's packages-bt.txt); one without it skips
// the setting with a line in the log.
func haveBtmgmt() bool {
	_, err := exec.LookPath("btmgmt")
	return err == nil
}

// secureConn keeps the controller's Secure Connections where the setting says, one change at a time.
type secureConn struct {
	run    btmgmtRunner
	have   func() bool
	wanted func() bool // the setting: true is on
	settle time.Duration
	sleep  func(time.Duration)
	busy   func() bool // t5_bt_again still at work on hci0

	wake chan struct{}

	mu        sync.Mutex
	notBefore time.Time
	said      map[string]bool // what has been logged once
}

func newSecureConn() *secureConn {
	return &secureConn{
		run:    runBtmgmt,
		have:   haveBtmgmt,
		wanted: func() bool { return !config.Get().Bluetooth.SecureConnectionsOff },
		settle: scSettle,
		sleep:  time.Sleep,
		busy:   btAgainBusy,
		wake:   make(chan struct{}, 1),
		said:   map[string]bool{},
	}
}

// btAgainBusy is whether t5_bt_again (techo5-lib.sh) is still at work on hci0.
func btAgainBusy() bool {
	_, err := os.Stat("/run/techo5-bt-again.busy")
	return err == nil
}

// secureConnHere is whether this device offers the setting: not athena on mainline, whose controller
// has Secure Connections off for good (device.conf BT_SC=off), and not on Android, whose own stack has
// the radio. A variable for the tests.
var secureConnHere = func() bool { return !isAthena() && !layout.OnAndroid() }

// secureConnFixedOff is where it stays off with no setting: athena on mainline. boot.sh turns it off
// once, and bluetoothd turns it back on when it takes the adapter again (a bridge that restarted), so
// the daemon puts it back off each time hci0 returns. Athena on Amazon's kernel has btbridge's
// -p192-keys instead and is left alone. A variable for the tests.
var secureConnFixedOff = func() bool { return layout.AthenaMainline() }

// up says hci0 has come up or come back: the setting goes on it once the controller has settled.
func (s *secureConn) up() {
	s.mu.Lock()
	s.notBefore = time.Now().Add(s.settle)
	s.mu.Unlock()
	s.poke()
}

// changed says the setting changed: it goes on the controller now, unless hci0 is still settling.
func (s *secureConn) changed() { s.poke() }

func (s *secureConn) poke() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// loop applies the setting each time it is poked; pokes while it works make one more pass.
func (s *secureConn) loop() {
	for range s.wake {
		s.settled()
		s.apply(context.Background(), s.wanted())
	}
}

// settled waits until hci0 has settled: the settle time from the last time it came up (read again
// after each wait, so a hci0 that comes back meanwhile pushes it on), then t5_bt_again's busy marker
// gone, for at most busyFor (a marker left by a pass that was killed must not hold the setting off).
func (s *secureConn) settled() {
	notBefore := func() time.Time {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.notBefore
	}
	for deadline := notBefore(); ; {
		if wait := time.Until(deadline); wait > 0 {
			s.sleep(wait)
		}
		next := notBefore()
		if !next.After(deadline) {
			break
		}
		deadline = next // hci0 came back while this waited
	}
	for end := time.Now().Add(busyFor); s.busy() && time.Now().Before(end); {
		s.sleep(time.Second)
	}
}

// once logs msg the first time only: a device without btmgmt, or whose controller has no Secure
// Connections, would otherwise say so every time hci0 comes back.
func (s *secureConn) once(msg string) {
	s.mu.Lock()
	first := !s.said[msg]
	s.said[msg] = true
	s.mu.Unlock()
	if first {
		slog.Info(msg)
	}
}

// controllerSC is what btmgmt info says about the controller.
type controllerSC struct {
	supported, on, powered bool
}

// readSC reads btmgmt info's two lines of settings:
//
//	supported settings: powered connectable … le advertising secure-conn …
//	current settings: powered ssp br/edr le secure-conn
//
// It reports false when they are not both there (no hci0, or btmgmt said something else).
func readSC(info string) (controllerSC, bool) {
	var c controllerSC
	var sup, cur bool
	for _, line := range strings.Split(info, "\n") {
		line = strings.TrimSpace(line)
		if v, ok := strings.CutPrefix(line, "supported settings:"); ok {
			sup = true
			c.supported = hasSetting(v, "secure-conn")
		}
		if v, ok := strings.CutPrefix(line, "current settings:"); ok {
			cur = true
			c.on, c.powered = hasSetting(v, "secure-conn"), hasSetting(v, "powered")
		}
	}
	return c, sup && cur
}

func hasSetting(list, name string) bool {
	for _, f := range strings.Fields(list) {
		if f == name {
			return true
		}
	}
	return false
}

func onOff(on bool) string {
	if on {
		return "on"
	}
	return "off"
}

var errNotTaken = errors.New("the controller did not take it")

// apply puts on on the controller when it has Secure Connections and something else is set, and
// reports whether it changed it. A controller that was powered is powered on again whatever went
// wrong in between: Bluetooth left off would be worse than the setting not taking.
func (s *secureConn) apply(ctx context.Context, on bool) (bool, error) {
	if secureConnFixedOff() {
		on = false
	} else if !secureConnHere() {
		return false, nil
	}
	if !s.have() {
		s.once("bluetooth secure connections: no btmgmt in this image; the setting is skipped here")
		return false, nil
	}
	info, _ := s.run(ctx, btmgmtRead, "info")
	c, ok := readSC(info)
	if !ok {
		slog.Debug("bluetooth secure connections: no hci0 to set yet")
		return false, nil
	}
	if !c.supported {
		s.once("bluetooth secure connections: this controller has none; the setting does nothing here")
		return false, nil
	}
	if c.on == on {
		return false, nil
	}

	if c.powered {
		if out, err := s.run(ctx, btmgmtSet, "power", "off"); err != nil {
			slog.Warn("bluetooth secure connections: powering off failed; setting it anyway", "err", err, "out", strings.TrimSpace(out))
		}
	}
	out, err := s.run(ctx, btmgmtSet, "sc", onOff(on))
	if err != nil {
		slog.Warn("bluetooth secure connections: btmgmt sc failed", "err", err, "out", strings.TrimSpace(out))
	}
	if c.powered {
		s.powerOn(ctx)
	}

	info, _ = s.run(ctx, btmgmtRead, "info")
	now, ok := readSC(info)
	switch {
	case !ok:
		// Not known to be powered: asked once more all the same (powering on a powered
		// controller does nothing).
		if c.powered {
			s.powerOn(ctx)
		}
		slog.Error("bluetooth secure connections: could not read the controller back", "want", onOff(on))
		return false, errNotTaken
	case c.powered && !now.powered:
		// Asked once more, then said loudly: Bluetooth is off until something powers it on.
		s.powerOn(ctx)
		if info, _ = s.run(ctx, btmgmtRead, "info"); !poweredIn(info) {
			slog.Error("bluetooth secure connections: the controller stayed powered off")
		}
	}
	if now.on != on {
		slog.Error("bluetooth secure connections: the controller did not take it", "want", onOff(on))
		return false, errNotTaken
	}
	msg := "bluetooth secure connections " + onOff(on)
	if c.powered {
		msg += "; what was connected was dropped for a moment"
	}
	slog.Info(msg)
	return true, nil
}

func poweredIn(info string) bool {
	c, ok := readSC(info)
	return ok && c.powered
}

func (s *secureConn) powerOn(ctx context.Context) {
	if out, err := s.run(ctx, btmgmtSet, "power", "on"); err != nil {
		slog.Error("bluetooth secure connections: powering on again failed", "err", err, "out", strings.TrimSpace(out))
	}
}

// newSecureConnSwitch is the setting's switch for Home Assistant.
func newSecureConnSwitch() *esphome.Switch {
	return &esphome.Switch{Base: esphome.Base{
		ObjectID: "bluetooth_secure_connections", Name: "Bluetooth Secure Connections", Icon: "mdi:shield-lock",
		Category: esphome.CategoryConfig,
	}}
}

// SecureConnections reports the setting: true unless turned off.
func (f *Feature) SecureConnections() bool { return !config.Get().Bluetooth.SecureConnectionsOff }

// SecureConnectionsHere reports whether this device offers the setting.
func (f *Feature) SecureConnectionsHere() bool { return secureConnHere() }

// SetSecureConnections turns the controller's Secure Connections on or off, as Home Assistant's switch
// would, for the screen and the setup page. Whatever is connected is dropped for a moment when it
// changes.
func (f *Feature) SetSecureConnections(on bool) error {
	if err := config.Set().Bluetooth().SecureConnections(on); err != nil {
		slog.Error("saving the bluetooth secure connections setting failed", "err", err)
		f.secure.Set(f.SecureConnections())
		return err
	}
	f.secure.Set(on)
	f.sc.changed()
	return nil
}
