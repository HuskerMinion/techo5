//go:build !dot

package display

import "github.com/HuskerMinion/techo5/echod/internal/config"

// atNightOptions is what the night does to a Show's screen: puts it out, or leaves a night light.
var atNightOptions = []string{"Screen off", "Night light"}

func atNightIndex() int {
	if config.Get().Screen.NightLight {
		return 1
	}
	return 0
}
