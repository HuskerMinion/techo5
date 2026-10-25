package speaker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/lib/alsa"
)

type testGainMixer struct {
	control alsa.Control
	missing bool
	findErr error // the mixer failing to be read, as an ioctl does
	fail    error
	writes  []uint32
}

func (m *testGainMixer) Find(name string) (alsa.Control, error) {
	if m.findErr != nil {
		return alsa.Control{}, m.findErr
	}
	if m.missing || name != gainControl {
		return alsa.Control{}, fmt.Errorf("%w named %q", alsa.ErrNoControl, name)
	}
	return m.control, nil
}

func (m *testGainMixer) Set(_ alsa.Control, v uint32) error {
	if m.fail != nil {
		return m.fail
	}
	m.writes = append(m.writes, v)
	return nil
}

func (m *testGainMixer) Close() error { return nil }

func gainTestMixer() *testGainMixer {
	return &testGainMixer{control: alsa.Control{Name: gainControl, Type: alsa.TypeInteger,
		Count: 1, Min: 0, Max: 158, Step: 1}}
}

func TestSpeakerGainWritesDBAndCanReset(t *testing.T) {
	m := gainTestMixer()
	for _, db := range []int{0, 5, 6, 0} {
		if err := writeSpeakerGain(m, db); err != nil {
			t.Fatal(err)
		}
		if got := m.writes[len(m.writes)-1]; got != uint32(110+db) {
			t.Fatalf("%d dB wrote %d", db, got)
		}
	}
	for _, db := range []int{-1, 7, 10, 158} {
		if err := writeSpeakerGain(m, db); err == nil {
			t.Fatalf("accepted %d dB", db)
		}
	}
	if len(m.writes) != 4 {
		t.Fatal("invalid gain reached hardware")
	}
}

func TestSpeakerGainRejectsUnsupportedControls(t *testing.T) {
	for _, change := range []func(*testGainMixer){
		func(m *testGainMixer) { m.missing = true },
		func(m *testGainMixer) { m.control.Type = alsa.TypeEnumerated },
		func(m *testGainMixer) { m.control.Count = 2 },
		func(m *testGainMixer) { m.control.Max = 115 },
		func(m *testGainMixer) { m.control.Min = 111 },
		func(m *testGainMixer) { m.control.Step = 2 },
	} {
		m := gainTestMixer()
		change(m)
		if err := writeSpeakerGain(m, 5); err == nil {
			t.Fatal("unsupported control accepted")
		}
		if len(m.writes) != 0 {
			t.Fatal("unsupported control written")
		}
	}
	m := gainTestMixer()
	m.fail = errors.New("write failed")
	if err := writeSpeakerGain(m, 5); !errors.Is(err, m.fail) {
		t.Fatalf("lost write failure: %v", err)
	}
}

func TestSpeakerGainIsQueuedBeforeStart(t *testing.T) {
	gainThere(t, true, true)
	p := &Player{}
	if err := p.SetSpeakerGain(5); err != nil {
		t.Fatal(err)
	}
	if p.gainDB != 5 {
		t.Fatal("gain was not remembered before the mixer opens")
	}
	if err := p.SetSpeakerGain(7); err == nil || p.gainDB != 5 {
		t.Fatal("bad gain changed the queued value")
	}
}

// A gain restore failure must reach the normal playback loop rather than fail the
// service and repeatedly restart it. Cancel at the first wait after mixer setup;
// an unopened mixer forces the same error branch as a failed amplifier write.
func TestSpeakerRunContinuesAfterGainRestoreFails(t *testing.T) {
	gainThere(t, true, true)
	for _, db := range []int{0, 5} {
		p := New()
		p.pb = &alsa.Playback{}
		p.gainDB = db
		if err := p.applySpeakerGain(); err == nil {
			t.Fatal("test did not force a gain restore failure")
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := p.Run(ctx); err != nil {
			t.Fatalf("gain restore at %d dB stopped playback: %v", db, err)
		}
	}
}

// A board that cannot have the amplifier is a sure no without opening the mixer;
// one that may have it is answered by the mixer, and a mixer that does not open
// yet is not taken as a no for good.
func TestSpeakerGainLooksOnlyWhereTheAmplifierCanBe(t *testing.T) {
	opened := 0
	open := func(m *testGainMixer, err error) func() (gainMixerCloser, error) {
		return func() (gainMixerCloser, error) {
			opened++
			if err != nil {
				return nil, err
			}
			return m, nil
		}
	}
	if ok, sure := findGain(false, open(gainTestMixer(), nil)); ok || !sure || opened != 0 {
		t.Fatalf("no amplifier possible: ok %v, sure %v, opened %d", ok, sure, opened)
	}
	if ok, sure := findGain(true, open(gainTestMixer(), nil)); !ok || !sure {
		t.Fatalf("the amplifier's control: ok %v, sure %v", ok, sure)
	}
	missing := gainTestMixer()
	missing.missing = true
	if ok, sure := findGain(true, open(missing, nil)); ok || !sure {
		t.Fatalf("a card without the control: ok %v, sure %v", ok, sure)
	}
	if ok, sure := findGain(true, open(nil, errors.New("no card yet"))); ok || sure {
		t.Fatalf("a card not up yet: ok %v, sure %v", ok, sure)
	}
}

// The amplifier is looked for among the I2C devices by name; only a list read
// whole without it says no.
func TestTAS5805MIsLookedForOnTheI2CBus(t *testing.T) {
	bus := func(names ...string) string {
		dir := t.TempDir()
		for i, n := range names {
			d := filepath.Join(dir, "2-00"+string(rune('a'+i)))
			if err := os.Mkdir(d, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(d, "name"), []byte(n+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return dir
	}
	if !tas5805mListed(bus("max98396", "tas5805m")) {
		t.Error("the amplifier listed was not found")
	}
	if tas5805mListed(bus("max98396", "mt6323")) {
		t.Error("a bus without the amplifier said it may have it")
	}
	if !tas5805mListed(filepath.Join(t.TempDir(), "absent")) || !tas5805mListed(bus()) {
		t.Error("a list that could not be read said no")
	}
	unreadable := bus("max98396")
	if err := os.Mkdir(filepath.Join(unreadable, "i2c-2"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !tas5805mListed(unreadable) {
		t.Error("a device whose name could not be read was taken as not the amplifier")
	}
}

// GainAvailable keeps a sure answer and asks again after a card that did not open;
// on a build whose boards cannot have the amplifier it never opens the mixer.
func TestSpeakerGainAsksAgainUntilSure(t *testing.T) {
	wasDir, wasOpen := i2cDevices, openGainMixer
	t.Cleanup(func() {
		i2cDevices, openGainMixer = wasDir, wasOpen
		gainSeen.ok, gainSeen.sure = false, false
	})
	i2cDevices = filepath.Join(t.TempDir(), "absent") // unknown: the mixer says
	gainSeen.ok, gainSeen.sure = false, false
	opened := 0
	var openErr error = errors.New("no card yet")
	openGainMixer = func() (gainMixerCloser, error) {
		opened++
		if openErr != nil {
			return nil, openErr
		}
		return gainTestMixer(), nil
	}
	if gainOnPath {
		if !GainAvailable() || opened != 0 {
			t.Fatalf("the gain goes with the codec's path: available %v, opened the mixer %d times", GainAvailable(), opened)
		}
		return
	}
	if !mayHaveTAS5805M {
		openErr = nil
		if GainAvailable() || opened != 0 {
			t.Fatalf("this build's boards cannot have the amplifier: opened the mixer %d times", opened)
		}
		return
	}
	if GainAvailable() {
		t.Fatal("available with no card")
	}
	openErr = nil
	// Asked twice: the first answer is found and kept, the second comes from what was kept.
	first, kept := GainAvailable(), GainAvailable()
	if !first || !kept || opened != 2 {
		t.Fatalf("the card once up: opened %d times, want 2", opened)
	}
}

// gainThere stands in for the mixer: whether the gain control may be there, and whether it is.
func gainThere(t *testing.T, possible, available bool) {
	wasP, wasA := GainPossible, GainAvailable
	GainPossible = func() bool { return possible }
	GainAvailable = func() bool { return available }
	t.Cleanup(func() { GainPossible, GainAvailable = wasP, wasA })
}

// A gain set while the card is not up yet, the control not sure to be there, is kept for Run to write
// once it is sure, rather than refused and the saved gain lost; a board that cannot have the
// amplifier refuses it.
func TestSpeakerGainIsKeptUntilTheCardIsSure(t *testing.T) {
	gainThere(t, true, false)
	p := &Player{}
	if err := p.SetSpeakerGain(4); err != nil || p.gainDB != 4 {
		t.Fatalf("a gain set before the card was up: err %v, kept %d", err, p.gainDB)
	}
	gainThere(t, false, false)
	if err := p.SetSpeakerGain(2); err == nil || p.gainDB != 4 {
		t.Fatalf("a board without the amplifier took a gain: err %v, kept %d", err, p.gainDB)
	}
}

// GainPossible is a sure answer once there is one, and until then whether the board may have the
// amplifier, without opening the mixer.
func TestSpeakerGainPossibleBeforeSure(t *testing.T) {
	wasDir, wasOpen := i2cDevices, openGainMixer
	t.Cleanup(func() {
		i2cDevices, openGainMixer = wasDir, wasOpen
		gainSeen.ok, gainSeen.sure = false, false
	})
	i2cDevices = filepath.Join(t.TempDir(), "absent")
	openGainMixer = func() (gainMixerCloser, error) {
		t.Fatal("GainPossible opened the mixer")
		return nil, nil
	}
	gainSeen.ok, gainSeen.sure = false, false
	if gainOnPath {
		if !GainPossible() {
			t.Error("the codec's own gain was not possible")
		}
		return
	}
	if GainPossible() != mayHaveTAS5805M {
		t.Errorf("not sure yet: possible %v, want %v", GainPossible(), mayHaveTAS5805M)
	}
	gainSeen.ok, gainSeen.sure = true, true
	if !GainPossible() {
		t.Error("a sure yes was not possible")
	}
	gainSeen.ok = false
	if GainPossible() {
		t.Error("a sure no was possible")
	}
}

// A card the mixer walk could not open on its own, with the playback device held, is answered by the
// mixer the speaker holds: the saved gain is written then, not at the next start.
func TestSpeakerGainIsAskedOfTheHeldMixer(t *testing.T) {
	t.Cleanup(func() { gainSeen.ok, gainSeen.sure = false, false })
	gainThere(t, true, false)
	gainSeen.ok, gainSeen.sure = false, false
	if gainOn(nil) {
		t.Fatal("available with no mixer to ask")
	}
	if !gainOn(gainTestMixer()) || !gainSeen.ok || !gainSeen.sure {
		t.Fatal("the held mixer's control was not taken as a sure yes")
	}
	gainSeen.ok, gainSeen.sure = false, false
	missing := gainTestMixer()
	missing.missing = true
	if gainOn(missing) || !gainSeen.sure {
		t.Fatal("a held mixer without the control was not a sure no")
	}
	if (&Player{}).heldMixer() != nil {
		t.Fatal("a speaker holding no mixer handed one over")
	}
	gainSeen.ok, gainSeen.sure = false, false
	gainThere(t, false, false)
	if gainOn(gainTestMixer()) || gainSeen.sure {
		t.Fatal("a board that cannot have the amplifier asked the mixer")
	}
}

// A mixer that fails to be read (an ioctl error, the mixer closed under it) says nothing about the
// control: the answer stays unsure and is asked again, where a mixer read whole without it is a sure no.
func TestSpeakerGainMixerErrorsAreNotANo(t *testing.T) {
	t.Cleanup(func() { gainSeen.ok, gainSeen.sure = false, false })
	gainThere(t, true, false)
	gainSeen.ok, gainSeen.sure = false, false
	broken := gainTestMixer()
	broken.findErr = syscall.EBADF
	if gainOn(broken) || gainSeen.sure {
		t.Fatalf("a mixer that could not be read was taken as sure: ok %v, sure %v", gainSeen.ok, gainSeen.sure)
	}
	if !gainOn(gainTestMixer()) || !gainSeen.sure {
		t.Fatal("asked again with the mixer readable, the control was not found")
	}
	if ok, sure := findGain(true, func() (gainMixerCloser, error) { return broken, nil }); ok || sure {
		t.Fatalf("the mixer walk took a read error as sure: ok %v, sure %v", ok, sure)
	}
	shape := gainTestMixer()
	shape.control.Max = gainUnity
	if ok, sure := gainAnswer(func() error { _, err := gainControlOn(shape); return err }()); ok || !sure {
		t.Fatalf("a control of another shape: ok %v, sure %v", ok, sure)
	}
}
