//go:build !dot || donut

package speaker

// untunedCurves are the curves a tuning's firstCurves were matched to with no tuning in the way: none
// on these boards (and the Echo Dot 3, which plays untuned), whose curves were matched to the tuning with the volume behind it.
var untunedCurves = map[string][VolumeSteps + 1]float64{}
