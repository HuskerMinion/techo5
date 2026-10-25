package noise

import (
	"math"
	"testing"
	"time"
)

func TestTheFirstReadingIsSent(t *testing.T) {
	next, send := step(held{}, -52.4, true)
	if !send || next != (held{value: -52, known: true}) {
		t.Errorf("first reading: got %+v, send %v; want -52 sent", next, send)
	}
}

func TestASmallMoveIsNotSent(t *testing.T) {
	last := held{value: -52, known: true}
	for _, v := range []float64{-52.4, -53.4, -50.6} {
		if next, send := step(last, v, true); send || next != last {
			t.Errorf("%.1f after -52: got %+v, send %v; want it held", v, next, send)
		}
	}
}

func TestTwoDecibelsIsSent(t *testing.T) {
	last := held{value: -52, known: true}
	for _, c := range []struct{ in, want float64 }{{-54, -54}, {-49.6, -50}, {-40, -40}} {
		next, send := step(last, c.in, true)
		if !send || next.value != c.want {
			t.Errorf("%.1f after -52: got %+v, send %v; want %.0f sent", c.in, next, send, c.want)
		}
	}
}

func TestNoReadingHoldsTheLast(t *testing.T) {
	last := held{value: -52, known: true}
	if next, send := step(last, -20, false); send || next != last {
		t.Errorf("no reading: got %+v, send %v; want -52 held", next, send)
	}
	if next, send := step(held{}, 0, false); send || next.known {
		t.Errorf("no reading at all: got %+v, send %v; want nothing", next, send)
	}
}

// testNoise is the sensor with the device left out: never busy, never muted, the room at -50 dBFS.
func testNoise() *Noise {
	n := build()
	n.busy = func() bool { return false }
	n.muted = func() bool { return false }
	n.read = func() (float64, string) { return -50, "" }
	n.say = func(string, float64) {}
	n.enabled = true
	return n
}

func TestAReadingComesAtTheEndOfAWindow(t *testing.T) {
	n := testNoise()
	start := time.Now()
	w := window{started: start}
	n.beat(start.Add(windowLength-time.Second), &w)
	if n.last.known {
		t.Fatal("a reading came before the window ended")
	}
	n.beat(start.Add(windowLength), &w)
	if !n.last.known || n.last.value != -50 {
		t.Errorf("after a window: %+v, want -50", n.last)
	}
}

func TestMutedReadsUnknown(t *testing.T) {
	n := testNoise()
	n.last = held{value: -50, known: true}
	n.muted = func() bool { return true }
	start := time.Now()
	w := window{started: start}
	n.beat(start.Add(windowLength), &w)
	if n.last.known {
		t.Errorf("muted, the sensor still holds %+v", n.last)
	}
	if v := n.level.Get(); !math.IsNaN(float64(v)) {
		t.Errorf("muted, Home Assistant was left with %v, want unknown", v)
	}
}

func TestABusyWindowHoldsTheReading(t *testing.T) {
	n := testNoise()
	busy := true
	n.busy = func() bool { return busy }
	start := time.Now()
	w := window{started: start}
	n.beat(start.Add(time.Second), &w)
	busy = false
	n.beat(start.Add(windowLength), &w)
	if n.last.known {
		t.Errorf("a window with a request in it gave a reading: %+v", n.last)
	}
}

func TestOffLooksAtNothing(t *testing.T) {
	n := testNoise()
	n.enabled = false
	n.busy = func() bool { t.Error("asked whether the device is busy with the switch off"); return false }
	n.read = func() (float64, string) { t.Error("read the room with the switch off"); return 0, "off" }
	start := time.Now()
	w := window{started: start}
	n.beat(start.Add(windowLength), &w)
}

// told is a test sensor whose log is kept, one line per reason given.
func told(n *Noise) *[]string {
	var said []string
	n.say = func(why string, _ float64) { said = append(said, why) }
	return &said
}

func TestWhyThereIsNoReadingIsLoggedOncePerChange(t *testing.T) {
	n := testNoise()
	said := told(n)
	muted := true
	n.muted = func() bool { return muted }
	start := time.Now()
	w := window{started: start}
	for i := range 3 {
		n.beat(start.Add(time.Duration(i)*time.Second), &w)
	}
	muted = false
	at := start.Add(3 * time.Second)
	w = window{started: at}
	n.beat(at.Add(windowLength), &w)
	n.beat(at.Add(2*windowLength), &w)

	want := []string{mutedWhy, ""}
	if len(*said) != len(want) || (*said)[0] != want[0] || (*said)[1] != want[1] {
		t.Errorf("logged %q, want %q", *said, want)
	}
}

func TestTheMicrophonesReasonIsLogged(t *testing.T) {
	n := testNoise()
	said := told(n)
	n.read = func() (float64, string) { return 0, "the device played something" }
	start := time.Now()
	w := window{started: start}
	n.beat(start.Add(windowLength), &w)
	if len(*said) != 1 || (*said)[0] != "the device played something" {
		t.Errorf("logged %q, want the microphones' reason", *said)
	}
	if n.last.known {
		t.Errorf("a window with no reading gave one: %+v", n.last)
	}
}

func TestABusyWindowSaysBusy(t *testing.T) {
	n := testNoise()
	said := told(n)
	n.busy = func() bool { return true }
	n.read = func() (float64, string) { return 0, "the device played something" }
	start := time.Now()
	w := window{started: start}
	n.beat(start.Add(windowLength), &w)
	if len(*said) != 1 || (*said)[0] != busyWhy {
		t.Errorf("logged %q, want %q", *said, busyWhy)
	}
}

func TestTurningOnLogsAgain(t *testing.T) {
	n := testNoise()
	said := told(n)
	start := time.Now()
	w := window{started: start}
	n.beat(start.Add(windowLength), &w)
	n.set(false)
	n.set(true)
	w = window{started: start}
	n.beat(start.Add(2*windowLength), &w)
	if len(*said) != 2 {
		t.Errorf("logged %q, want the first reading said again after the switch moved", *said)
	}
}
