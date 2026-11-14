//go:build !dot && !spot

package display

import (
	"image"
	"strings"
	"testing"
	"time"
)

// linkLooks swaps the three looks for the test.
func linkLooks(t *testing.T, home, wifiUp, known *bool) {
	t.Helper()
	wasHome, wasWifi, wasKnown := linkHome, linkWifi, linkHomeKnown
	linkHome = func() bool { return *home }
	linkWifi = func() bool { return *wifiUp }
	linkHomeKnown = func() bool { return *known }
	t.Cleanup(func() { linkHome, linkWifi, linkHomeKnown = wasHome, wasWifi, wasKnown })
}

// Home Assistant gone is said once it has lasted, and no longer once it is back; a short absence, a
// restart for an update, is never said.
func TestALostHomeAssistantIsSaidOnceItLasts(t *testing.T) {
	home, wifiUp, known := true, true, false
	linkLooks(t, &home, &wifiUp, &known)
	var l linkWatch
	t0 := time.Unix(1_700_000_000, 0)
	l.look(t0)
	if got := l.down(t0); got != linkUp {
		t.Fatalf("connected, the header says %q is down", got)
	}
	home = false
	l.look(t0.Add(10 * time.Second))
	if got := l.down(t0.Add(20 * time.Second)); got != linkUp {
		t.Errorf("ten seconds gone, the header already says %q", got)
	}
	changed := l.look(t0.Add(10*time.Second + linkDownAfter))
	if got := l.down(t0.Add(10*time.Second + linkDownAfter)); got != linkNoHome || !changed {
		t.Errorf("gone for %v: the header says %q (changed %v), want Home Assistant", linkDownAfter, got, changed)
	}
	home = true
	if !l.look(t0.Add(2*time.Minute)) || l.down(t0.Add(2*time.Minute)) != linkUp {
		t.Error("back again, the header still says it is gone")
	}
}

// With no address it is the Wi-Fi that is named, whatever Home Assistant's part in it.
func TestALostWifiIsNamedAsThat(t *testing.T) {
	home, wifiUp, known := false, false, true
	linkLooks(t, &home, &wifiUp, &known)
	var l linkWatch
	t0 := time.Unix(1_700_000_000, 0)
	l.look(t0)
	l.look(t0.Add(linkDownAfter))
	if got := l.down(t0.Add(linkDownAfter)); got != linkNoWifi {
		t.Errorf("no address: the header says %q, want the Wi-Fi", got)
	}
	wifiUp = true
	l.look(t0.Add(linkDownAfter + time.Second))
	if got := l.down(t0.Add(linkDownAfter + time.Second)); got != linkNoHome {
		t.Errorf("an address but no Home Assistant: the header says %q", got)
	}
}

// A device that has never had a Home Assistant is not told it has lost one.
func TestNoHomeAssistantEverIsNotALoss(t *testing.T) {
	home, wifiUp, known := false, true, false
	linkLooks(t, &home, &wifiUp, &known)
	var l linkWatch
	t0 := time.Unix(1_700_000_000, 0)
	l.look(t0)
	l.look(t0.Add(time.Hour))
	if got := l.down(t0.Add(time.Hour)); got != linkUp {
		t.Errorf("never connected and no access held: the header says %q", got)
	}
	known = true // an access is held: it had one before this start
	if got := l.down(t0.Add(time.Hour)); got != linkNoHome {
		t.Errorf("an access held and nothing connected: the header says %q", got)
	}
}

// The pill says what is lost, alone or beside the microphone's line, and nothing on a good day.
func TestTheHeaderSaysWhatIsLost(t *testing.T) {
	if got := headerLine(scene{}); got != "" {
		t.Errorf("nothing lost and not muted: the header says %q", got)
	}
	if got := headerLine(scene{linkDown: linkNoWifi}); got != tr("no_wifi") {
		t.Errorf("no Wi-Fi: the header says %q", got)
	}
	both := headerLine(scene{muted: true, linkDown: linkNoHome})
	if !strings.Contains(both, tr("microphone_off")) || !strings.Contains(both, tr("no_home_assistant")) {
		t.Errorf("muted and no Home Assistant: the header says %q", both)
	}
	for _, panel := range showPanels() {
		img := image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high))
		r := newRenderer(img)
		s := scene{now: time.Now(), phase: "idle", muted: true, linkDown: linkNoHome}
		if right := r.micPillRight(s); right >= panel.wide-r.margin {
			t.Errorf("%s: the pill for both runs to %d, off a panel %d wide", panel.name, right, panel.wide)
		}
	}
}
