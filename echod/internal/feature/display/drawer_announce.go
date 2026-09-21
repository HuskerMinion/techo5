//go:build !dot && !spot

package display

import (
	"context"
	"fmt"

	"github.com/HuskerMinion/techo5/echod/internal/feature/announce"
)

// The drawer's Announce tab: say something in every room.
//
// It is one button, because it is one action. Tapping it opens the microphone here and sends what is
// said to the other devices in the house — no Home Assistant, no internet, nobody typing. While it
// is recording the row says so, since a microphone that is open should never be a secret, and the
// ring says the same thing on every device.

// announceRows is what that tab holds: the button, and the reason it will not work when there is one.
func announceRows(s scene) ([]settingRow, string) {
	if !s.announceReady {
		return nil, "Announcements need a house word — the same word on every device here, set on " +
			"each one's setup page. Until then this device neither sends them nor takes them."
	}
	if s.announceRecording {
		return []settingRow{{
			id:     "announce:0",
			label:  "Speak now",
			sub:    "It sends when you stop talking",
			kind:   ctlValue,
			value:  "Listening",
			rowTap: false,
		}}, ""
	}

	// None found is said plainly. It read "Heard in every room here" before, which is a promise the
	// device cannot keep when it has not found anybody - and that is exactly the state somebody is
	// in while they are working out why nothing arrives.
	sub := "No other devices found yet"
	if s.announcePeers > 0 {
		sub = fmt.Sprintf("Heard on %s", devicesText(s.announcePeers))
	}
	return []settingRow{{
		id:     "announce:0",
		label:  "Say something",
		sub:    sub,
		kind:   ctlButton,
		button: "Speak",
		rowTap: true,
	}}, ""
}

// announceTap is the button: it starts the recording, and the drawer closes so the screen is not a
// list while somebody is talking to the room.
func (d *Display) announceTap() {
	if announce.Get().Recording() {
		return
	}
	d.closeDrawer()
	go announce.Get().Speak(context.Background())
}
