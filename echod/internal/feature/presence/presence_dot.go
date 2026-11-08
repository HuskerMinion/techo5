//go:build dot

// Package presence is somebody near, seen by the camera; the Dot has no camera, so it has nothing here.
package presence

import (
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// Available reports whether this board has a camera; replaceable in tests.
var Available = func() bool { return false }

// ScreenOffAfter is never: no camera, no screen.
func ScreenOffAfter() time.Duration { return 0 }

// Present is never known on a Dot.
func Present() bool { return false }

// HasCamera is false on a Dot unless replaced in tests.
func HasCamera() bool { return Available() }

// SetMotion saves the motion setting when called in tests.
func SetMotion(on bool) { _ = config.Set().Presence().On(on) }

// OnMotion is a no-op on a Dot.
func OnMotion(fn func(present bool)) func() { return func() {} }
