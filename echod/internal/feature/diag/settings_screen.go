//go:build !dot

package diag

import (
	"fmt"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// screenSettings are the ones only a device with a panel has. A bundle that reports a brightness for
// a Dot invites somebody to go looking for the screen that is set to it.
func screenSettings(c config.Config) []string {
	return []string{
		fmt.Sprintf("screen: on=%t brightness=%d auto=%t night=%q language=%q",
			c.Screen.On, c.Screen.Brightness, c.Screen.Auto, c.Screen.Night, c.Screen.Language),
		fmt.Sprintf("slideshow: mode=%q every=%ds subfolders=%t",
			c.Home.Slideshow.Mode, c.Home.Slideshow.EverySeconds, !c.Home.Slideshow.TopOnly),
	}
}
