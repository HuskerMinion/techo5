//go:build dot

package display

import (
	"errors"
	"image"
	"time"

	"github.com/HuskerMinion/techo5/echod/pkg/addonapi"
)

// AddonScreens is empty on a Dot, which has no screen.
var AddonScreens = map[string]image.Point{}

// RenderAddonScene draws nothing on a Dot's build: a Dot has no screen, and its build has no layout code
// for one. Build the fake daemon without the dot tag to draw a scene.
func RenderAddonScene(string, string, string, addonapi.Block, func(*addonapi.Block) ([]byte, string, bool),
	func(string) (time.Duration, time.Duration, bool)) (*image.RGBA, error) {
	return nil, errors.New("display: a Dot's build draws no screen")
}
