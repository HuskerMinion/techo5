//go:build !dot

package display

import (
	"fmt"
	"sync"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/plexcast"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
)

// The Plex link on the screen: the four characters plex.tv gives the device, shown as large as the
// screen takes them, with where to type them and a way out. It is the settings lock's pad (pin.go) in
// shape -- an overlay the frame draws over everything, and the touch loop gives every finger to -- but
// there is nothing to type here: the typing happens on a phone or a computer, at plex.tv/link, and the
// screen's only part is to show the code and then say the account is linked.
//
// Both screens draw it, each to its own shape (plex_show.go, plex_spot.go).

// plexLinkFor is how long the card stays up with no code typed anywhere: the code itself lives five
// minutes (plexcast's linkFor), so a card left up after that is showing nothing worth reading.
const plexLinkFor = 5 * time.Minute

// linkView is the card as a frame draws it.
type linkView struct {
	open bool
	code string // the four characters, empty until plex.tv has answered with them
	user string // who it would be linked as, once it is
	msg  string // why a link did not happen, or empty
}

var plexCard struct {
	mu   sync.Mutex
	open bool
	at   time.Time
	msg  string
}

// startPlexLink asks plex.tv for a code and puts the card up. The answer takes a moment, which is what
// the card's "Waiting for link..." is for: the code appears in its place when it comes.
func startPlexLink() {
	plexCard.mu.Lock()
	plexCard.open, plexCard.at, plexCard.msg = true, time.Now(), ""
	plexCard.mu.Unlock()
	safe.Go("plex link", func() {
		if _, err := plexcast.StartPinLink(); err != nil {
			plexCard.mu.Lock()
			plexCard.msg = "plex.tv could not be reached"
			plexCard.mu.Unlock()
		}
	})
}

// closePlexLink puts the card away, and stops the poll behind it when the link was abandoned rather
// than made.
func closePlexLink(stopPoll bool) {
	if stopPoll {
		plexcast.CancelPinLink()
	}
	plexCard.mu.Lock()
	plexCard.open, plexCard.msg = false, ""
	plexCard.mu.Unlock()
}

// linkNow is the card as it is to be drawn now. It closes itself when the account is linked, when the
// code has run out, and when it has been up long enough to be no use.
func linkNow(now time.Time) linkView {
	st := plexcast.Current()
	plexCard.mu.Lock()
	open, at, msg := plexCard.open, plexCard.at, plexCard.msg
	plexCard.mu.Unlock()
	if !open {
		return linkView{}
	}
	switch {
	case st.Linked:
		closePlexLink(false)
		return linkView{}
	case st.Err != "":
		msg = st.Err
	case now.Sub(at) > plexLinkFor:
		closePlexLink(true)
		return linkView{}
	}
	return linkView{open: true, code: st.Code, user: st.User, msg: msg}
}

// plexLinkOpen is whether the card is up, for a gesture before a frame has been drawn.
func plexLinkOpen() bool { return linkNow(time.Now()).open }

// plexRow is the Connections card's Plex row: whether the device casts for an account, and the button
// that links it (a code on this screen) or unlinks it. While a code is out it says so, since the card
// may be behind the settings.
func plexRow() settingRow {
	ps := plexcast.Current()
	row := settingRow{id: "plex", label: "Plex", sub: tr("plex_not_linked"), kind: ctlButton, button: tr("plex_link_account")}
	switch {
	case ps.Linked && ps.User != "":
		row.sub, row.button = fmt.Sprintf(tr("plex_linked_as"), ps.User), tr("plex_unlink_account")
	case ps.Linked:
		row.sub, row.button = tr("plex_linked"), tr("plex_unlink_account")
	case ps.Code != "":
		row.sub = tr("plex_waiting")
	}
	return row
}
