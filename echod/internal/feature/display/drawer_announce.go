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
		return nil, tr("drawer_announce_setup")
	}
	if s.announceRecording {
		return []settingRow{{
			id:     "announce:0",
			label:  tr("drawer_speak_now"),
			sub:    tr("drawer_auto_send"),
			kind:   ctlValue,
			value:  tr("drawer_recording"),
			rowTap: false,
		}}, ""
	}

	// None found is said plainly. It read "Heard in every room here" before, which is a promise the
	// device cannot keep when it has not found anybody - and that is exactly the state somebody is
	// in while they are working out why nothing arrives.
	sub := tr("drawer_no_peers")
	if s.announcePeers > 0 {
		sub = fmt.Sprintf(tr("drawer_heard_on"), announcementDevices(s.announcePeers))
	}
	return []settingRow{{
		id:     "announce:0",
		label:  tr("drawer_say_something"),
		sub:    sub,
		kind:   ctlButton,
		button: tr("drawer_speak"),
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

func announcementDevices(n int) string {
	count := tr("drawer_one_device")
	if n != 1 {
		count = fmt.Sprintf(tr("drawer_device_count"), n)
	}
	return count
}
