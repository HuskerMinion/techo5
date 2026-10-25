//go:build linux && !dot && !spot

package btaudio

import "github.com/HuskerMinion/techo5/echod/internal/layout"

// isAthena is whether this is the 2nd gen Show 8, on either kernel.
func isAthena() bool { return layout.Athena() }
