//go:build linux

package btaudio

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeController is hci0 as btmgmt would show it, and every btmgmt command it was given.
type fakeController struct {
	mu                     sync.Mutex
	supported, on, powered bool
	failSC                 bool
	failPowerOn            int // how many power-ons fail before one takes
	calls                  []string
}

func (c *fakeController) run(_ context.Context, timeout time.Duration, args ...string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cmd := strings.Join(args, " ")
	c.calls = append(c.calls, cmd)
	want := btmgmtSet
	if cmd == "info" {
		want = btmgmtRead
	}
	if timeout != want {
		return "", errors.New(cmd + " with the wrong timeout")
	}
	switch cmd {
	case "info":
		sup, cur := "powered connectable discoverable bondable ssp br/edr le advertising", "ssp br/edr le"
		if c.supported {
			sup += " secure-conn debug-keys privacy"
		}
		if c.powered {
			cur = "powered " + cur
		}
		if c.on {
			cur += " secure-conn"
		}
		return "hci0:\tPrimary controller\n\taddr 00:11:22:33:44:55 version 8 manufacturer 70 class 0x240414\n" +
			"\tsupported settings: " + sup + "\n\tcurrent settings: " + cur + "\n\tname Kitchen\n", nil
	case "power off":
		c.powered = false
	case "power on":
		if c.failPowerOn > 0 {
			c.failPowerOn--
			return "", errors.New("exit status 1")
		}
		c.powered = true
	case "sc on", "sc off":
		if c.failSC {
			return "", errors.New("exit status 1")
		}
		c.on = cmd == "sc on"
	}
	return "", nil
}

func (c *fakeController) commands() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.calls)
}

// scOffered has the setting offered for one test, as on an Echo: these test how it is applied, which
// a panel (where Android has the radio, so it is never offered) does not do.
func scOffered(t *testing.T) {
	here := secureConnHere
	secureConnHere = func() bool { return true }
	t.Cleanup(func() { secureConnHere = here })
}

func testSC(c *fakeController) *secureConn {
	s := newSecureConn()
	s.run, s.have = c.run, func() bool { return true }
	s.busy = func() bool { return false }
	return s
}

// On athena on mainline the setting is fixed off: a controller bluetoothd turned back on is put
// back off, with no switch offered.
func TestSecureConnectionsKeptOffWhereFixed(t *testing.T) {
	here, fixed := secureConnHere, secureConnFixedOff
	secureConnHere = func() bool { return false }
	secureConnFixedOff = func() bool { return true }
	defer func() { secureConnHere, secureConnFixedOff = here, fixed }()

	c := &fakeController{supported: true, on: true, powered: true}
	if changed, err := testSC(c).apply(context.Background(), true); !changed || err != nil || c.on {
		t.Errorf("changed %v, err %v, on %v, commands %q", changed, err, c.on, c.commands())
	}
}

// The setting waits for t5_bt_again's busy marker to go, a second at a time.
func TestSecureConnectionsWaitsForTheClassPass(t *testing.T) {
	s := testSC(&fakeController{supported: true, powered: true})
	busy := 3
	s.busy = func() bool { busy--; return busy >= 0 }
	var slept []time.Duration
	s.sleep = func(d time.Duration) { slept = append(slept, d) }
	s.settled()
	if len(slept) != 3 {
		t.Errorf("slept %v, want three one-second waits", slept)
	}
}

// A hci0 that comes back during the settle pushes it on.
func TestSecureConnectionsSettleMovesOn(t *testing.T) {
	s := testSC(&fakeController{supported: true, powered: true})
	s.settle = time.Hour
	s.up()
	var slept []time.Duration
	s.sleep = func(d time.Duration) {
		slept = append(slept, d)
		if len(slept) == 1 {
			s.mu.Lock()
			s.notBefore = s.notBefore.Add(time.Minute) // came back meanwhile
			s.mu.Unlock()
		}
	}
	s.settled()
	if len(slept) != 2 {
		t.Errorf("slept %d times, want the settle and its extension", len(slept))
	}
}

func TestReadSC(t *testing.T) {
	info := "hci0:\tPrimary controller\n\taddr 00:11:22:33:44:55 version 8 manufacturer 70 class 0x240414\n" +
		"\tsupported settings: powered connectable fast-connectable discoverable bondable link-security ssp br/edr le advertising secure-conn debug-keys privacy\n" +
		"\tcurrent settings: powered ssp br/edr le secure-conn\n"
	c, ok := readSC(info)
	if !ok || !c.supported || !c.on || !c.powered {
		t.Errorf("%+v %v", c, ok)
	}
	c, ok = readSC(strings.ReplaceAll(info, "le secure-conn\n", "le\n"))
	if !ok || !c.supported || c.on {
		t.Errorf("off: %+v %v", c, ok)
	}
	if _, ok := readSC("Invalid index\n"); ok {
		t.Error("no hci0 read as one")
	}
}

// Only a controller with Secure Connections, set otherwise than the setting, is touched.
func TestSecureConnectionsOnlyWhenSupportedAndDifferent(t *testing.T) {
	scOffered(t)
	for name, c := range map[string]*fakeController{
		"already on":  {supported: true, on: true, powered: true},
		"unsupported": {supported: false, powered: true},
	} {
		want := name == "already on"
		changed, err := testSC(c).apply(context.Background(), want)
		if changed || err != nil || !slices.Equal(c.commands(), []string{"info"}) {
			t.Errorf("%s: changed %v err %v, commands %q", name, changed, err, c.commands())
		}
	}

	c := &fakeController{supported: true, on: true, powered: true}
	changed, err := testSC(c).apply(context.Background(), false)
	if !changed || err != nil || c.on || !c.powered {
		t.Fatalf("off: changed %v err %v, on %v powered %v", changed, err, c.on, c.powered)
	}
	if want := []string{"info", "power off", "sc off", "power on", "info"}; !slices.Equal(c.commands(), want) {
		t.Errorf("off: %q, want %q", c.commands(), want)
	}

	// Not powered: set as it is, and left as it was.
	c = &fakeController{supported: true, on: false}
	if changed, err := testSC(c).apply(context.Background(), true); !changed || err != nil || !c.on || c.powered {
		t.Errorf("unpowered: changed %v err %v, on %v powered %v", changed, err, c.on, c.powered)
	}
	if want := []string{"info", "sc on", "info"}; !slices.Equal(c.commands(), want) {
		t.Errorf("unpowered: %q, want %q", c.commands(), want)
	}

	c = &fakeController{supported: true, on: true, powered: true}
	s := testSC(c)
	s.have = func() bool { return false }
	if changed, _ := s.apply(context.Background(), false); changed || len(c.commands()) != 0 {
		t.Errorf("no btmgmt: changed %v, commands %q", changed, c.commands())
	}
}

// A controller that was powered is powered again whatever fails in between.
func TestSecureConnectionsPowerComesBack(t *testing.T) {
	scOffered(t)
	c := &fakeController{supported: true, on: true, powered: true, failSC: true}
	if changed, err := testSC(c).apply(context.Background(), false); changed || err == nil {
		t.Errorf("sc refused: changed %v err %v", changed, err)
	}
	if !c.powered || !slices.Contains(c.commands(), "power on") {
		t.Errorf("sc refused: powered %v, commands %q", c.powered, c.commands())
	}

	c = &fakeController{supported: true, on: true, powered: true, failPowerOn: 1}
	changed, err := testSC(c).apply(context.Background(), false)
	if !changed || err != nil || !c.powered || c.on {
		t.Errorf("first power-on refused: changed %v err %v, powered %v on %v, commands %q", changed, err, c.powered, c.on, c.commands())
	}
}

// hci0 coming up has the setting applied once it has settled; a change is applied straight away.
func TestSecureConnectionsAppliedOnAdapterUp(t *testing.T) {
	scOffered(t)
	c := &fakeController{supported: true, on: true, powered: true}
	s := testSC(c)
	var mu sync.Mutex
	want := false
	s.wanted = func() bool { mu.Lock(); defer mu.Unlock(); return want }
	slept := make(chan time.Duration, 4)
	s.sleep = func(d time.Duration) { slept <- d }
	done := make(chan struct{})
	go func() { defer close(done); s.loop() }()
	defer func() { close(s.wake); <-done }()

	waitFor := func(what string, ok func() bool) {
		t.Helper()
		for end := time.Now().Add(5 * time.Second); !ok(); {
			if time.Now().After(end) {
				t.Fatalf("%s: commands %q", what, c.commands())
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	scOffs := func() int { return strings.Count(strings.Join(c.commands(), ","), "sc off") }

	s.up()
	select {
	case d := <-slept:
		if d <= 0 || d > scSettle {
			t.Errorf("waited %v after hci0 came up, want up to %v", d, scSettle)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no settling wait after hci0 came up")
	}
	waitFor("up", func() bool { return scOffs() == 1 })

	// A new hci0 comes up with it on again, as bluetoothd sets it up.
	c.mu.Lock()
	c.on = true
	c.mu.Unlock()
	s.up()
	<-slept
	waitFor("back", func() bool { return scOffs() == 2 })

	// Turned on from the screen, long after: no wait.
	s.mu.Lock()
	s.notBefore = time.Time{}
	s.mu.Unlock()
	mu.Lock()
	want = true
	mu.Unlock()
	s.changed()
	waitFor("changed", func() bool { return slices.Contains(c.commands(), "sc on") })
	select {
	case d := <-slept:
		t.Errorf("a change waited %v", d)
	default:
	}
}

// Where the device has it off for good (athena on mainline), there is no switch and nothing is run.
func TestNoSecureConnectionsSettingWhereItIsFixed(t *testing.T) {
	here := secureConnHere
	secureConnHere = func() bool { return false }
	defer func() { secureConnHere = here }()

	c := &fakeController{supported: true, on: false, powered: true}
	if changed, _ := testSC(c).apply(context.Background(), true); changed || len(c.commands()) != 0 {
		t.Errorf("changed %v, commands %q", changed, c.commands())
	}
	for _, e := range Get().Entities() {
		if e == Get().secure {
			t.Error("the switch is offered")
		}
	}
}
