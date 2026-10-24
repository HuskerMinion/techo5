package speaker

import (
	"testing"
)

// fakeSink takes what is written and counts pauses and resumes.
type fakeSink struct{ pauses, resumes int }

func (f *fakeSink) Write(b []byte) (int, error) { return len(b), nil }
func (f *fakeSink) Pause() error                { f.pauses++; return nil }
func (f *fakeSink) Resume() error               { f.resumes++; return nil }
func (f *fakeSink) Close() error                { return nil }

// Whether sound is going to the sink: not while it is quiet or paused for silence, and from the first
// period with something in it, told once, through the short gaps in speech.
func TestSinkSoundingFollowsTheSound(t *testing.T) {
	p := New()
	told := 0
	p.OnSinkSound.Listen(func(struct{}) { told++ })
	sink := &fakeSink{}
	p.SetSink(sink, "test", Rate, Channels)
	s := p.sink.Load()

	silent := make([]byte, period*Channels*2)
	sound := make([]byte, period*Channels*2)
	for i := range sound {
		sound[i] = 0x10
	}
	if _, ok := p.SinkSounding(); ok {
		t.Fatal("a sink that was just attached is sounding")
	}
	s.push(silent)
	if _, ok := p.SinkSounding(); ok {
		t.Fatal("a sink given silence is sounding")
	}
	s.push(sound)
	if _, ok := p.SinkSounding(); !ok || told != 1 {
		t.Fatalf("sound started: sounding %t, told %d times", ok, told)
	}
	// A gap shorter than the pause keeps it sounding, and is not a new start.
	for range sinkQuietAfter - 1 {
		s.push(silent)
	}
	s.push(sound)
	if _, ok := p.SinkSounding(); !ok || told != 1 {
		t.Fatalf("after a short gap: sounding %t, told %d times", ok, told)
	}
	// Quiet long enough to pause: no longer sounding, and sound after it is a new start.
	for range sinkQuietAfter {
		s.push(silent)
	}
	if _, ok := p.SinkSounding(); ok || sink.pauses != 1 {
		t.Fatalf("paused for silence: sounding %t, pauses %d", ok, sink.pauses)
	}
	s.push(sound)
	if _, ok := p.SinkSounding(); !ok || told != 2 || sink.resumes != 1 {
		t.Fatalf("sound after the pause: sounding %t, told %d, resumes %d", ok, told, sink.resumes)
	}
	p.SetSink(nil, "", 0, 0)
	if _, ok := p.SinkSounding(); ok {
		t.Fatal("no sink, and sounding")
	}
}

// levelSink is a sink with a volume of its own: it takes the steps it is told when takes is set.
type levelSink struct {
	fakeSink
	takes bool
	steps []int
}

func (l *levelSink) Level(step int) bool {
	l.steps = append(l.steps, step)
	return l.takes
}

// A sink with a volume of its own is told the volume step and given the audio at full scale; one
// without, or one that does not take it, gets the audio turned down here as before.
func TestALevelSinkTakesTheStepAndFullScale(t *testing.T) {
	for _, step := range []int{1, 12, VolumeSteps} {
		if got := sinkScale(sinkGain(step), step); got != 1 {
			t.Errorf("step %d: scale %v, want full scale", step, got)
		}
	}
	if got := sinkScale(0, 0); got != 0 {
		t.Errorf("muted: scale %v, want 0", got)
	}
	if got := sinkScale(2, 12); got != 1 {
		t.Errorf("a gain above the step's was boosted to %v", got)
	}

	p := New()
	sink := &levelSink{takes: true}
	p.SetSink(sink, "test", Rate, Channels)
	p.SetVolume(12)
	p.fill(make([]byte, period*Channels*2))
	if len(sink.steps) == 0 || sink.steps[len(sink.steps)-1] != 12 {
		t.Errorf("the sink was told %v, want step 12", sink.steps)
	}
}
