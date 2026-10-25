//go:build linux

package btaudio

import (
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/lib/bluealsa"
)

// fakeAlsa records what a stream's volume mode set. gate, when set, holds the first SetSoftVolume
// until it is closed, and held is closed once that one is being held.
type fakeAlsa struct {
	mu     sync.Mutex
	soft   []bool
	levels []int
	gate   chan struct{}
	held   chan struct{}
}

func (a *fakeAlsa) SetSoftVolume(_ bluealsa.PCM, on bool) error {
	a.mu.Lock()
	gate, held := a.gate, a.held
	a.gate = nil
	a.mu.Unlock()
	if gate != nil {
		close(held)
		<-gate
	}
	a.mu.Lock()
	a.soft = append(a.soft, on)
	a.mu.Unlock()
	return nil
}

func (a *fakeAlsa) SetLevel(_ bluealsa.PCM, level int) error {
	a.mu.Lock()
	a.levels = append(a.levels, level)
	a.mu.Unlock()
	return nil
}

func (a *fakeAlsa) got() ([]bool, []int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]bool(nil), a.soft...), append([]int(nil), a.levels...)
}

// volumeFeature is a Feature with what the volume needs, the device's volume at step 12, and the
// device's volume set by a phone's slider counted rather than taken.
func volumeFeature(t *testing.T) (*Feature, *[]int) {
	t.Helper()
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	was := volumeNow
	volumeNow = func() int { return 12 }
	t.Cleanup(func() { volumeNow = was })
	set := []int{}
	wasTo := sliderTo
	sliderTo = func(level int) { set = append(set, level) }
	t.Cleanup(func() { sliderTo = wasTo })
	f := &Feature{levels: map[string]int{}, ours: map[string]time.Time{}, volModes: map[string]volMode{},
		setsVolume: newSetsVolumeSwitch()}
	f.setsVolume.OnCommand = func(on bool) { _ = f.SetSetsVolume(on) }
	return f, &set
}

const phonePath = "/org/bluealsa/hci0/dev_X/a2dpsrc/source"

// phonePCM is a phone's stream at level on both channels, scaled by bluez-alsa or not (soft).
func phonePCM(level int, soft bool) bluealsa.PCM {
	return bluealsa.PCM{Path: phonePath, Volume: uint16(level)<<8 | uint16(level), SoftVolume: soft}
}

// Bluetooth sets the volume is on by default, so nothing changes on an update, and off with its switch,
// saved as the speaker's (#245).
func TestTheBluetoothVolumeSetting(t *testing.T) {
	f, _ := volumeFeature(t)
	if !bluetoothSetsVolume() || !f.SetsVolume() {
		t.Fatal("off on a new device")
	}
	f.Restore(config.Get())
	if !f.setsVolume.Get() {
		t.Error("the switch is off on a new device")
	}
	f.setsVolume.OnCommand(false)
	if bluetoothSetsVolume() || f.setsVolume.Get() || !config.Get().Speaker.NoBluetoothVolume {
		t.Error("still on after the switch went off")
	}
	f.setsVolume.OnCommand(true)
	if !bluetoothSetsVolume() || !f.setsVolume.Get() || config.Get().Speaker.NoBluetoothVolume {
		t.Error("still off after the switch went on")
	}
}

// With the setting off a phone's level never sets the device's volume, however it moved; on, it does
// as sliderMove says.
func TestSliderSetsOnlyWhenShared(t *testing.T) {
	for _, c := range []struct {
		level, last int
		known, ours bool
	}{
		{127, 60, true, false}, {60, 127, true, false}, {levelFor(20), levelFor(8), true, true}, {80, 0, false, false},
	} {
		if sliderSets(false, c.level, c.last, c.known, c.ours) {
			t.Errorf("%+v: set the volume with the setting off", c)
		}
		if got, want := sliderSets(true, c.level, c.last, c.known, c.ours), sliderMove(c.level, c.last, c.known, c.ours); got != want {
			t.Errorf("%+v: %v with the setting on, want %v", c, got, want)
		}
	}
}

// A stream's volume mode is put on again when it never was, was put on the other way, or bluez-alsa
// says otherwise once it has had time to.
func TestModeStale(t *testing.T) {
	at := time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)
	soon, later := at.Add(time.Second), at.Add(modeSettle)
	on, off := volMode{true, at}, volMode{false, at}
	for name, c := range map[string]struct {
		m            volMode
		applied      bool
		shared, soft bool
		now          time.Time
		want         bool
	}{
		"never put on":                    {volMode{}, false, true, false, later, true},
		"on, still on":                    {on, true, true, false, later, false},
		"off, still off":                  {off, true, false, true, later, false},
		"on, turned off":                  {on, true, false, false, soon, true},
		"off, turned on":                  {off, true, true, true, soon, true},
		"on, bluez-alsa scales, settled":  {on, true, true, true, later, true},
		"on, bluez-alsa scales, just put": {on, true, true, true, soon, false},
		"off, bluez-alsa does not scale":  {off, true, false, false, later, true},
	} {
		if got := modeStale(c.m, c.applied, c.shared, c.soft, c.now); got != c.want {
			t.Errorf("%s: %v, want %v", name, got, c.want)
		}
	}
}

// Shared, bluez-alsa leaves the audio unscaled and the phone is handed the device's level; not shared,
// bluez-alsa scales the audio by the phone's own level and nothing is handed to the phone.
func TestVolumeMode(t *testing.T) {
	f, _ := volumeFeature(t)
	p := phonePCM(127, false)

	al := &fakeAlsa{}
	f.volumeMode(al, p, true, 12)
	soft, levels := al.got()
	if len(soft) != 1 || soft[0] || len(levels) != 1 || levels[0] != levelFor(12) {
		t.Errorf("shared: soft volume %v, levels %v", soft, levels)
	}
	if f.levels[phonePath] != levelFor(12) || !f.volModes[phonePath].shared || f.ours[phonePath].IsZero() {
		t.Errorf("shared: level %d, mode %+v, ours %v", f.levels[phonePath], f.volModes[phonePath], f.ours[phonePath])
	}

	al = &fakeAlsa{}
	f.volumeMode(al, p, false, 12)
	soft, levels = al.got()
	if len(soft) != 1 || !soft[0] || len(levels) != 0 {
		t.Errorf("own: soft volume %v, levels %v", soft, levels)
	}
	if _, ours := f.ours[phonePath]; f.levels[phonePath] != 127 || f.volModes[phonePath].shared || ours {
		t.Errorf("own: level %d, mode %+v, ours %v", f.levels[phonePath], f.volModes[phonePath], ours)
	}
}

// The switch turned off while a phone plays: the next look puts the stream over to its own volume and
// hands the phone nothing, and a phone then reporting full level leaves the device's volume alone.
// Turned on again, the next look hands the phone the device's level.
func TestALookFollowsTheSwitch(t *testing.T) {
	f, set := volumeFeature(t)
	al := &fakeAlsa{}
	f.volumeMode(al, phonePCM(127, true), true, 12)

	if err := config.Set().Speaker().NoBluetoothVolume(true); err != nil {
		t.Fatal(err)
	}
	al = &fakeAlsa{}
	f.phoneSlider(al, phonePCM(levelFor(12), false))
	soft, levels := al.got()
	if len(soft) != 1 || !soft[0] || len(levels) != 0 {
		t.Fatalf("turned off: soft volume %v, levels %v", soft, levels)
	}
	f.phoneSlider(al, phonePCM(127, true))
	f.phoneSlider(al, phonePCM(40, true))
	if soft, levels = al.got(); len(soft) != 1 || len(levels) != 0 || len(*set) != 0 {
		t.Errorf("off, a look set more: soft volume %v, levels %v, device volume %v", soft, levels, *set)
	}
	if f.levels[phonePath] != 40 {
		t.Errorf("the phone's level not noted: %d", f.levels[phonePath])
	}

	if err := config.Set().Speaker().NoBluetoothVolume(false); err != nil {
		t.Fatal(err)
	}
	f.phoneSlider(al, phonePCM(40, true))
	if soft, levels = al.got(); len(soft) != 2 || soft[1] || len(levels) != 1 || levels[0] != levelFor(12) {
		t.Errorf("turned on: soft volume %v, levels %v", soft, levels)
	}
}

// Each look reads the phone's level from the stream as that look found it (refresh hands it to the
// stream with seen, and takes it to phoneSlider with followSlider), not from the copy taken when the
// stream was opened. A computer that reported full level as it connected, then took the device's
// level, must not take the device to full volume once the echo window is over, and a later move of
// its slider must be seen (#245; from #181 the copy from the open was all phoneSlider saw).
func TestALookReadsTheLevelAsItIsNow(t *testing.T) {
	f, set := volumeFeature(t)
	al := &fakeAlsa{}
	s := &inStream{item: "Bluetooth: Laptop", pcm: phonePCM(127, true)} // as it was opened
	f.volumeMode(al, s.latest(), true, 12)                              // given the speaker
	f.mu.Lock()
	f.ours[phonePath] = time.Now().Add(-time.Second) // the echo window is over
	f.mu.Unlock()

	// The looks after: bluez-alsa now has the level handed to the computer, and later its slider moved.
	s.seen(phonePCM(levelFor(12), false))
	f.followSlider(al, s)
	if len(*set) != 0 {
		t.Fatalf("a look moved the device's volume to %v with the computer's level unchanged", *set)
	}
	s.seen(phonePCM(60, false))
	f.followSlider(al, s)
	if len(*set) != 1 || (*set)[0] != 60 {
		t.Errorf("the computer's slider moved to 60, the device's volume set by %v", *set)
	}
}

// A stream whose mode bluez-alsa lost (restarted, say) has it put back once bluez-alsa has had time to
// say it took it, while the setting and what was put on still agree.
func TestALookHealsTheModeBluezAlsaLost(t *testing.T) {
	f, _ := volumeFeature(t)
	al := &fakeAlsa{}
	f.volumeMode(al, phonePCM(levelFor(12), false), true, 12)
	f.phoneSlider(al, phonePCM(levelFor(12), true)) // just put on: bluez-alsa may not have said yet
	if soft, _ := al.got(); len(soft) != 1 {
		t.Fatalf("put on again within the settle time: %v", soft)
	}
	f.mu.Lock()
	m := f.volModes[phonePath]
	m.at = m.at.Add(-modeSettle)
	f.volModes[phonePath] = m
	f.mu.Unlock()
	f.phoneSlider(al, phonePCM(levelFor(12), true))
	if soft, _ := al.got(); len(soft) != 2 || soft[1] {
		t.Errorf("bluez-alsa scaling a shared stream not put right: %v", soft)
	}
}

// The switch turned off and on again quickly: while the mode for off is still being put on, the switch
// goes back on, and the stream ends up as the switch did, and so does what is noted for it.
func TestQuickTurnsEndAsTheSwitchDid(t *testing.T) {
	f, _ := volumeFeature(t)
	p := phonePCM(levelFor(12), false)
	al := &fakeAlsa{gate: make(chan struct{}), held: make(chan struct{})}
	held, gate := al.held, al.gate

	if err := config.Set().Speaker().NoBluetoothVolume(true); err != nil {
		t.Fatal(err)
	}
	first := make(chan struct{})
	go func() { defer close(first); f.putMode(al, p) }() // turned off
	<-held
	if err := config.Set().Speaker().NoBluetoothVolume(false); err != nil { // turned on again
		t.Fatal(err)
	}
	second := make(chan struct{})
	go func() { defer close(second); f.putMode(al, p) }()
	close(gate)
	<-first
	<-second

	soft, _ := al.got()
	if len(soft) != 2 || soft[1] {
		t.Errorf("soft volume %v: the last put on is not the switch's on", soft)
	}
	f.mu.Lock()
	shared := f.volModes[phonePath].shared
	f.mu.Unlock()
	if !shared {
		t.Error("noted as not shared with the switch on")
	}
}
