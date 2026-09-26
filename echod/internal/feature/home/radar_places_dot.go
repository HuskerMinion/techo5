//go:build dot

package home

// A Dot has no screen, so no rain map and no town names to carry.
func placesIn(x0, y0, w, h int) []RadarPlace { return nil }
