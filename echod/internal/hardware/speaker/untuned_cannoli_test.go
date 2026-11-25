//go:build dot && cannoli

package speaker

// untunedCurves are the curves a tuning's firstCurves were matched to with no tuning in the way: the
// Echo Dot 5th gen played untuned on cannoliCurve, Fire OS's own, before its tuning was read.
var untunedCurves = map[string][VolumeSteps + 1]float64{"cannoli": cannoliCurve}
