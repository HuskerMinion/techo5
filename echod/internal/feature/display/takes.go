//go:build !dot

package display

import (
	"fmt"
	"hash/fnv"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/security"
	"github.com/HuskerMinion/techo5/echod/internal/feature/setup"
	"github.com/HuskerMinion/techo5/echod/internal/feature/takes"
)

// Takes of a word, asked for on the setup page (feature/takes).
//
// The setup page can ask for a recording but not start one: that is a press at the device, and a Show
// or a Spot has no button for it, so it is Start on a card the screen puts up, the way a browser asking
// to be let in is answered. While the recording runs the card stays up with Stop on it, over whatever
// was on the screen and lit at night too: the microphones going somewhere is not something only the
// browser should know.

// asksAtDevice is whether the setup page is asking something of whoever is at the device: a browser to
// be let in, or a recording of takes to be started or stopped. The screen stays up and lit for it.
func asksAtDevice() bool { return setup.Get().Waiting() || takes.Get().Card().Up() }

// pageAsk: the yes of each of the setup page's askings is in the same place (Allow, Start, Stop), so
// a tap answers only the page that was drawn, and only once it has been up askSettle (askLatch). A
// browser that starts asking, or a recording that moves on, under a finger on its way does not take
// the tap meant for what was there.

// pageKey names the asking a frame draws: the browser asking to be let in when there is one, or else
// the takes' card; 0 for none.
func pageKey(c takes.Card) uint64 {
	if c.Up() {
		return takesKey(c)
	}
	if asking := setup.Get().Asking(); asking != "" {
		return letInKey(asking)
	}
	return 0
}

// letInKey names a browser asking to be let in, and takesKey a takes card: a recording's card is
// another page once it has started, so its Stop is not answered by the tap that started it.
func letInKey(asking string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(asking))
	return h.Sum64() | 1<<63
}

func takesKey(c takes.Card) uint64 {
	if !c.Up() {
		return 0
	}
	return (c.ID<<2 | uint64(c.Phase)) &^ (1 << 63)
}

// answerTakes answers the Start card. Start is behind the settings lock, as letting a browser in is:
// somebody at a locked screen must not start a recording a browser asked for. The PIN starts the
// recording that was asked for when Start was tapped, and no other.
func answerTakes(start bool, id uint64) {
	if !start {
		takes.Get().Decline(id)
		return
	}
	if security.Locked() {
		openPIN("Record takes", func() {
			takes.Get().Begin(id)
			security.Relock()
		})
		return
	}
	takes.Get().Begin(id)
}

// takesWhat is what the card says the takes are of.
func takesWhat(c takes.Card, most int) string {
	w := []rune(c.Word)
	word := c.Word
	if len(w) > most {
		word = string(w[:most-1]) + "…"
	}
	if c.Kind == takes.NotWord {
		return "like “" + word + "”"
	}
	return "of “" + word + "”"
}

// takesTime is how far a recording has come, against its ceiling.
func takesTime(d time.Duration) string {
	s, most := int(d.Seconds()), int(takes.Longest.Seconds())
	s = min(s, most)
	return fmt.Sprintf("%d:%02d of %d:%02d", s/60, s%60, most/60, most%60)
}
