package speaker

// Louder top volume (#304): the Dot's full volume goes into the tuning at -18.2 dB (firstCurves in
// paths_dot.go), and music stays well short of what the stock firmware plays, with Speaker gain unable
// to make up for it. The switch takes the steps above 16 in hotter, rising evenly to the set's lift at
// 30 (step s gains lift*(s-16)/14 dB), and leaves the steps up to 16, where replies usually play, as
// they are. The tuning's compressor and limiter take the extra peaks. Off by default, so a Dot plays as
// it did unless someone wants more; TestVolumeInFrontKeepsTheLoudness holds both, off and on.
//
// Listened to on a Dot with radio at step 30 and 8 dB: clearly louder, no audible distortion; and
// with Speaker gain at its top (config.SpeakerGainMax) and bass-heavy music at step 30: no audible
// clipping.

// topLifts is how many dB hotter Louder top volume takes a tuning set's top step. A set with none
// (the Echo 2nd gen's radar, every other board's) leaves the switch doing nothing.
var topLifts = map[string]float64{"dot": 8}

// liftedFrom is the step the lift starts above.
const liftedFrom = 16

// liftTop is curve with its steps above liftedFrom raised evenly to db at the top, never past the
// vendor's own full volume.
func liftTop(curve [VolumeSteps + 1]float64, db float64) [VolumeSteps + 1]float64 {
	for s := liftedFrom + 1; s <= VolumeSteps; s++ {
		curve[s] = min(0, curve[s]+db*float64(s-liftedFrom)/float64(VolumeSteps-liftedFrom))
	}
	return curve
}

// LouderTopPossible is whether this build has a tuning set the switch lifts: where it is not, the
// switch is not put up.
func LouderTopPossible() bool {
	for name := range topLifts {
		if _, ok := firstCurves[name]; ok {
			return true
		}
	}
	return false
}

// SetLouderTop turns Louder top volume on or off. The write loop picks the curve up with the next
// period, so the level changes at once; on a tuning with no lift, or played untuned, it does nothing.
func (p *Player) SetLouderTop(on bool) { p.louderTop.Store(on) }

// inFront is the curve to put in front of the tuning now: the lifted one while Louder top volume is
// on and this tuning has one, else first (nil when there is none).
func (p *Player) inFront() *[VolumeSteps + 1]float64 {
	if p.louderTop.Load() && p.lifted != nil {
		return p.lifted
	}
	return p.first
}
