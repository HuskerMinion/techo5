//go:build spot

package display

import esphome "github.com/ygelfand/go-esphome-device"

// hasClockTap is false on the Spot, whose clock is the middle of its ring menu (clock_tap.go).
const hasClockTap = false

// clockTapSel is the setting in Home Assistant, which the Spot does not have.
func (d *Display) clockTapSel() *esphome.Select { return nil }
