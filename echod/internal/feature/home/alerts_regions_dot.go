//go:build dot

package home

// A Dot has no screen, so no weather alerts and no regions to find them by.
func loadRegions() []maRegion { return nil }
