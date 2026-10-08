//go:build dot

// Package presence is somebody near, seen by the camera; the Dot has no camera, so it has nothing here.
package presence

import "time"

// ScreenOffAfter is never: no camera, no screen.
func ScreenOffAfter() time.Duration { return 0 }

// Present is never known on a Dot.
func Present() bool { return false }
