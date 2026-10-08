//go:build !dot

package display

import (
	"fmt"

	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
)

// followText is the "Now playing follows" row's value: the player's name, or a stand-in for screenshots.
func followText(demo bool) string {
	name := home.Get().FollowSource()
	if demo && name != "None" {
		return "Kitchen speaker"
	}
	return name
}

// demoPlayers names the players in a screenshot by number, since their names are often people's and
// rooms'. The first option, None, stays.
func demoPlayers(names []string) []string {
	out := make([]string, len(names))
	for i, n := range names {
		if i == 0 {
			out[i] = n
			continue
		}
		out[i] = fmt.Sprintf("Speaker %d", i)
	}
	return out
}
