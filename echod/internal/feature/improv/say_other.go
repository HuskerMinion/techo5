//go:build !dot && linux && !panel

package improv

import "github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"

// said is nothing on a device with a screen: what it would say is on the screen.
func said(string) []speaker.Note { return nil }
