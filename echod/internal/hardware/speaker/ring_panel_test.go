//go:build panel

package speaker

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// roomGain is the device's own volume on top of what is written: on a panel, Android's level.
func roomGain() float64 { return float64(curve(int(deviceStep.Load()))) }

// roomShare is how far apart two levels may be, as a share of them, beyond a step of a sample: 1% on a
// panel, where one level is scaled in software and the other by Android.
const roomShare = 0.01

// androidHears records what the app is told, at once rather than androidLag later, for one test.
func androidHears(t *testing.T) func() []string {
	t.Helper()
	var mu sync.Mutex
	var got []string
	send, lag := sendToApp, androidLag
	sendToApp = func(line string) {
		mu.Lock()
		got = append(got, line)
		mu.Unlock()
	}
	androidLag = 0
	t.Cleanup(func() { sendToApp, androidLag = send, lag })
	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), got...)
	}
}

func last(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return lines[len(lines)-1]
}

// Muted is silent here, over a speaker Android still has on: Android is never sent zero.
func TestMuteIsKeptInSoftware(t *testing.T) {
	heard := androidHears(t)
	p := &Player{}
	p.SetVolume(0)
	if want := fmt.Sprintf("volume %d %d", deviceFloor, VolumeSteps); !androidTold(heard, want) {
		t.Errorf("muted, Android was told %q, want %q", last(heard()), want)
	}
	if p.Volume() != 0 {
		t.Errorf("muted, the music's gain is %v", p.Volume())
	}
	p.SetVolume(12)
	if !androidTold(heard, fmt.Sprintf("volume 12 %d", VolumeSteps)) || p.Volume() != 1 {
		t.Errorf("at step 12 Android was told %q and the music is at %v; want step 12 at unity", last(heard()), p.Volume())
	}
}

// A ring over quiet or muted music raises Android to the ring's step while it sounds, turns the music
// down by as much in software, and puts both back when it ends.
func TestARingRaisesAndroidAndPutsItBack(t *testing.T) {
	heard := androidHears(t)
	for _, music := range []int{0, 5} {
		p := &Player{}
		p.SetVolume(music)
		p.bellStep.Store(25)
		p.ringing.Store(true)
		buf := make([]byte, period*Channels*2)
		p.fill(buf)
		if want := fmt.Sprintf("volume 25 %d", VolumeSteps); !androidTold(heard, want) {
			t.Fatalf("music at %d, ringing at 25: Android was told %q, want %q", music, last(heard()), want)
		}
		if want := gainForStep(OutputSpeaker, music); p.Volume() != want || (music > 0 && want >= 1) {
			t.Errorf("music at %d under a ring at 25: gain %v, want %v, below unity", music, p.Volume(), want)
		}
		if g := p.gainFor(25); g != 1 {
			t.Errorf("a ring at 25 with Android at 25: gain %v, want 1", g)
		}

		p.Ringing(false, 25)
		p.fill(buf)
		back := max(music, deviceFloor)
		if want := fmt.Sprintf("volume %d %d", back, VolumeSteps); !androidTold(heard, want) {
			t.Fatalf("after the ring with music at %d: Android was told %q, want %q", music, last(heard()), want)
		}
		if want := gainForStep(OutputSpeaker, music); p.Volume() != want {
			t.Errorf("after the ring the music's gain is %v, want %v", p.Volume(), want)
		}
	}
}

// A ring quieter than the music leaves Android where the music has it.
func TestAQuietRingLeavesAndroidAlone(t *testing.T) {
	heard := androidHears(t)
	p := &Player{}
	p.SetVolume(20)
	if want := fmt.Sprintf("volume 20 %d", VolumeSteps); !androidTold(heard, want) {
		t.Fatalf("music at 20: Android was told %q, want %q", last(heard()), want)
	}
	before := len(heard())
	p.bellStep.Store(8)
	p.ringing.Store(true)
	p.fill(make([]byte, period*Channels*2))
	time.Sleep(20 * time.Millisecond)
	if got := heard()[before:]; len(got) != 0 {
		t.Errorf("a ring at 8 under music at 20 told Android %q", got)
	}
	p.Ringing(false, 8)
	p.fill(make([]byte, period*Channels*2))
}

// A level change made late is not sent over a newer one.
func TestALateLevelIsNotSentOverANewerOne(t *testing.T) {
	heard := androidHears(t)
	androidLag = 50 * time.Millisecond
	tellAndroid(25, androidLag)
	tellAndroid(7, 0)
	time.Sleep(100 * time.Millisecond)
	if got := last(heard()); got != fmt.Sprintf("volume 7 %d", VolumeSteps) {
		t.Errorf("Android was last told %q, want the newer step 7", got)
	}
}

// androidTold is whether the last line Android was told becomes want within a moment (every level
// goes out on a goroutine of its own, even with no lag).
func androidTold(heard func() []string, want string) bool {
	for range 50 {
		if last(heard()) == want {
			return true
		}
		time.Sleep(2 * time.Millisecond)
	}
	return false
}

// ringOver starts a ring at 25 over music at 3 and waits for its raise to reach Android.
func ringOver(t *testing.T, heard func() []string) (*Player, []byte) {
	t.Helper()
	p := &Player{}
	p.SetVolume(3)
	if want := fmt.Sprintf("volume 3 %d", VolumeSteps); !androidTold(heard, want) {
		t.Fatalf("music at 3: Android was told %q, want %q", last(heard()), want)
	}
	p.bellStep.Store(25)
	p.ringing.Store(true)
	buf := make([]byte, period*Channels*2)
	p.fill(buf)
	raise := fmt.Sprintf("volume 25 %d", VolumeSteps)
	for i := 0; i < 100 && last(heard()) != raise; i++ {
		time.Sleep(5 * time.Millisecond)
	}
	if last(heard()) != raise {
		t.Fatalf("ringing at 25, Android was told %q, want %q", last(heard()), raise)
	}
	return p, buf
}

// A ring's raise reaches Android androidLag after the samples scaled for it, but the drop when it ends
// goes at once: the music is back at its own gain from that period on, and over a level still raised
// for the ring it would be played out loud. androidLag is long here, so a drop held back by it would
// miss androidTold's moment.
func TestARingEndReachesAndroidAtOnce(t *testing.T) {
	heard := androidHears(t)
	androidLag = 300 * time.Millisecond
	p, buf := ringOver(t, heard)
	p.Ringing(false, 25)
	p.fill(buf)
	if want := fmt.Sprintf("volume 3 %d", VolumeSteps); !androidTold(heard, want) {
		t.Errorf("as the ring ended Android was told %q, want %q at once", last(heard()), want)
	}
}

// An app that stops reading never holds up the write loop: the drop at a ring's end is sent off the
// loop, and the period after it is written while the send is still stuck.
func TestAStuckAppDoesNotStallTheWriteLoop(t *testing.T) {
	heard := androidHears(t)
	p, buf := ringOver(t, heard)
	stuck, release := make(chan struct{}), make(chan struct{})
	record := sendToApp
	var once sync.Once
	sendToApp = func(line string) {
		once.Do(func() { close(stuck) })
		<-release
		record(line)
	}
	done := make(chan struct{})
	go func() {
		p.Ringing(false, 25)
		p.fill(buf)
		p.fill(buf)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("a ring's end waited on an app that is not reading")
	}
	select {
	case <-stuck:
	case <-time.After(2 * time.Second):
		t.Fatal("the drop at the ring's end was never sent")
	}
	close(release)
	if want := fmt.Sprintf("volume 3 %d", VolumeSteps); !androidTold(heard, want) {
		t.Errorf("once the app read again it was told %q, want %q", last(heard()), want)
	}
}

// While a ring has Android raised above the music, AndroidLevel says so, and to what: a press of the
// tablet's buttons then is worked out against it (media/volume_panel.go). Before the raise reaches
// Android, and once the ring ends, Android is at the music's level.
func TestAndroidLevelKnowsARaise(t *testing.T) {
	heard := androidHears(t)
	androidLag = 60 * time.Millisecond
	p := &Player{}
	p.SetVolume(3)
	if want := fmt.Sprintf("volume 3 %d", VolumeSteps); !androidTold(heard, want) {
		t.Fatalf("music at 3: Android was told %q, want %q", last(heard()), want)
	}
	p.bellStep.Store(25)
	p.ringing.Store(true)
	p.fill(make([]byte, period*Channels*2))
	if told, _, raised := AndroidLevel(); raised || told != 3 {
		t.Errorf("before the raise reached Android: told %d, raised %v; want 3, not raised", told, raised)
	}
	p.Ringing(false, 25)
	p.fill(make([]byte, period*Channels*2))

	p, buf := ringOver(t, heard)
	if told, _, raised := AndroidLevel(); !raised || told != 25 {
		t.Errorf("ringing: told %d, raised %v; want 25, raised", told, raised)
	}
	p.Ringing(false, 25)
	p.fill(buf)
	if want := fmt.Sprintf("volume 3 %d", VolumeSteps); !androidTold(heard, want) {
		t.Fatalf("after the ring Android was told %q, want %q", last(heard()), want)
	}
	if told, _, raised := AndroidLevel(); raised || told != 3 {
		t.Errorf("after the ring: told %d, raised %v; want 3, not raised", told, raised)
	}
}

// AndroidLevel says when the level it gives was sent, so a report from Android that crossed it on the
// way can be told apart from a press made after it (media/volume_panel.go).
func TestAndroidLevelSaysWhenItWasSent(t *testing.T) {
	heard := androidHears(t)
	before := time.Now()
	(&Player{}).SetVolume(9)
	if want := fmt.Sprintf("volume 9 %d", VolumeSteps); !androidTold(heard, want) {
		t.Fatalf("Android was told %q, want %q", last(heard()), want)
	}
	if told, at, _ := AndroidLevel(); told != 9 || at.Before(before) || at.After(time.Now()) {
		t.Errorf("told %d at %v, want 9 sent after %v", told, at, before)
	}
}
