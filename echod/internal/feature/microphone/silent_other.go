//go:build !panel

package microphone

import esphome "github.com/ygelfand/go-esphome-device"

// buildEntities is empty: an Echo's microphone is its own, and Android never takes it away from the
// daemon (silent_panel.go).
func buildEntities() []esphome.Entity { return nil }
