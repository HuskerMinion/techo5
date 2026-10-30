package detect

import (
	"github.com/zserge/microwakeword"

	"github.com/HuskerMinion/techo5/echod/internal/lib/mwwfront"
	"github.com/HuskerMinion/techo5/echod/internal/lib/wake"
)

// Detector is one microWakeWord model on the feature front end the daemon would run it on, for the
// tools that score a wake word outside the engine (echod wake). Unlike the engine it fires on the
// model's own cutoff. Not safe for concurrent use.
type Detector struct {
	det   *microwakeword.Detector
	front *microFront // nil: the detector's own front end, zserge's
}

// NewDetector loads a model for a tool, on the front end the daemon would run it on (exactFor).
func NewDetector(m wake.Model) (*Detector, error) {
	det, err := microwakeword.NewDetector(m.Config)
	if err != nil {
		return nil, err
	}
	d := &Detector{det: det}
	if exactFor(m, ExactFrontEnd) {
		d.front = newMicroBackend(true).front(frontKey{mwwfront.StepMs(m.Config.FeaturesStepMs), true})
	}
	return d, nil
}

// ProcessAudio feeds samples and reports whether the score reached the cutoff on any of them.
func (d *Detector) ProcessAudio(samples []int16) bool {
	if d.front == nil {
		return d.det.ProcessAudio(samples)
	}
	f, hit := d.front, false
	f.buf = append(f.buf, samples...)
	for len(f.buf) >= f.window {
		if d.det.ProcessFeatures([][]int8{features(f.fe.ProcessFrame(f.buf[:f.window]))}) {
			hit = true
		}
		f.buf = f.buf[f.step:]
	}
	return hit
}

// SlidingAverage is the model's score: see microwakeword.Detector.
func (d *Detector) SlidingAverage() float64 { return d.det.SlidingAverage() }

// FrontEnd names the feature front end ExactFrontEnd picks, for a tool's output.
func FrontEnd(m wake.Model) string {
	if exactFor(m, ExactFrontEnd) {
		return "exact (mwwfront)"
	}
	return "approximate (zserge audiofrontend)"
}
