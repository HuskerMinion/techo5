//go:build dot

package speaker

// untunedCurves are the curves a tuning's firstCurves were matched to with no tuning in the way, by
// tuning set: the Echo 2nd gen played untuned on radarCurves before its tuning was read.
var untunedCurves = map[string][VolumeSteps + 1]float64{"radar": radarCurves[OutputSpeaker]}
