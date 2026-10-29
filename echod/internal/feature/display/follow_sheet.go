//go:build !dot

package display

import (
	"fmt"
	"sync"

	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
)

// The "Now playing follows" list: None, then each of Home Assistant's other players marked followed or
// not, as the Calendars list marks its calendars. None follows nothing and closes the list; a player
// is ticked on or off, and with several players the list stays open for the next, Done at its foot
// closing it.

// followListed are the entities the list last showed, in its order ("" for None): a choice in it is
// one of these, whatever Home Assistant's list has become since.
var followListed struct {
	sync.Mutex
	entities []string
}

func followPicker(demo bool) pickerView {
	entities, names, on := home.Get().FollowChoices()
	followListed.Lock()
	followListed.entities = entities
	followListed.Unlock()
	if demo {
		names = demoPlayers(names)
	}
	p := pickerView{title: tr("now_playing_follows"), cur: -1}
	for i, n := range names {
		switch {
		case entities[i] == "":
			p.opts = append(p.opts, n)
			if on[i] {
				p.cur = i
			}
		case on[i]:
			p.opts = append(p.opts, calendarOn+n)
		default:
			p.opts = append(p.opts, calendarOff+n)
		}
	}
	if len(entities) > 2 {
		p.opts = append(p.opts, calendarsDone())
	}
	return p
}

// chooseFollow is a choice in the list: None, or a player ticked on or off. It reports whether the list
// should stay open for another: only after a tick, with several players to choose from.
func chooseFollow(i int) bool {
	followListed.Lock()
	entities := followListed.entities
	followListed.Unlock()
	if i < 0 || i >= len(entities) {
		return false // Done
	}
	if entities[i] == "" {
		home.Get().ChooseFollow("")
		return false
	}
	home.Get().ToggleFollow(entities[i])
	return len(entities) > 2
}

// followText is the "Now playing follows" row's value: the player's name, how many, or a stand-in for
// screenshots.
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
