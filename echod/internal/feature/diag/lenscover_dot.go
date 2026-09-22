//go:build dot

package diag

// The Dot has no camera, so it has nothing to put a shutter over. These exist so the shared code
// compiles without asking which device it is on.

func lensCover() (present, covered bool) { return false, false }

func watchLensCover(func(covered bool)) {}
