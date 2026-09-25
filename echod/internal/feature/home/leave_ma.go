package home

import (
	"log/slog"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// Taking this room out of the house it is playing along with.
//
// A room in a Music Assistant group is not playing on its own: a stop sent to it stops the whole group,
// and an unjoin is the one non-destructive way out — measured on a Show grouped with a Denon, where an
// unjoin stopped the departing room and the rest carried on playing.
//
// So starting a station here, while Music Assistant still holds the speaker, leaves the group first and
// stops after. In that order: the other way round the stop takes the house down, and a station somebody
// started in one room silences every other one. A room that is on its own has no group to leave, and the
// stop is what ends Music Assistant's stream to it rather than leaving it waiting behind the new track.
//
// It is asked of Home Assistant rather than over Sendspin because the protocol has no word for it — the
// controller role takes play, pause, stop, next and previous — and Music Assistant's own entity for this
// device does. Same entity as resumeMusicAssistant asks: the player whose active queue is this device's
// own media player.
//
// Nothing here waits on anything: a leave that Home Assistant will not take is a station that plays here
// with the group still attached, which is what happened before any of this.

// leaveGroup takes this room out of the group it has been playing along with, then ends what the group was
// playing to it. The caller has already established that a remote holds the speaker.
//
// The stop is the part that is held back, and two things about it were learned the hard way. It goes only
// while Music Assistant is still what the room hears: Transport routes by who the room's track is, so a stop
// sent once Music Assistant has already stopped is a command about this player's own stream, and it stopped
// the station that had just taken the room. And it goes after the leave, because the stop is what takes a
// group down with it.
func leaveGroup() {
	// The track Music Assistant had here is over the moment this room stops playing along with it, whatever
	// it was left in: the hold a stop of Music Assistant's own records for the screen would otherwise put
	// its track back up, with play offered, as soon as the station this room started ends. Dropped before
	// anything is asked, for the same reason Transport drops it there.
	media.Get().ForgetHeld()

	// One lookup of Music Assistant's player, used for both the group question and the unjoin. Anything
	// that cannot be established is left with known false, which leaveWith stops in: the stop is the only
	// thing that ends the track this room asked to replace, and a Home Assistant that cannot answer must
	// not leave the room held.
	ma, err := musicAssistantPlayer()
	var members []any
	known := false
	switch {
	case err != nil:
		slog.Warn("group: looking for this device's Music Assistant player failed", "err", err)
	case ma == "":
		slog.Warn("group: no Music Assistant player for this device", "device", config.Get().Device.Name)
	default:
		if members, err = groupMembers(ma); err != nil {
			slog.Warn("group: asking Home Assistant whether this room is in a group failed",
				"player", ma, "err", err)
		} else {
			known = true
		}
	}

	if err := leaveWith(members, known, media.Get().Carried(),
		func() error { return unjoinMusicAssistant(ma) },
		func() { media.Get().Transport(media.TransportStop) }); err != nil {
		slog.Warn("group: leaving the group failed, so this room stays in it", "err", err)
	}
}

// leaveWith does the leave: it asks to unjoin first when the room is certainly in a group, then stops what
// it was carrying. A room that could not leave is left alone, because the stop would take the group with
// it; a room whose group could not be established is stopped, because the stop is what ends Music
// Assistant's stream to it rather than leaving it waiting behind the new track. Nothing is stopped when
// there is nothing of this device's to stop, which carried says.
func leaveWith(members []any, known, carried bool, unjoin func() error, stop func()) error {
	if known && len(members) > 0 {
		if err := unjoin(); err != nil {
			return err
		}
	}
	if carried {
		stop()
	}
	return nil
}

// groupMembers is what Home Assistant says this room is playing with: the members of ma's group — this room
// included, and empty when it is on its own.
//
// The group a Sendspin session is given is no help here, which is worth stating because it looks like it
// should be. Music Assistant puts every player in a group — a room on its own is a group of one — and names
// it only when there is a house to name, so the id is always there and the name is always empty. What knows
// is Home Assistant's own view of Music Assistant's player for this device: group_members.
//
// Found on the device all three ways: `[]` while Music Assistant was playing to this room alone, `[this
// room, its partner]` grouped with another of Music Assistant's players, and `[this room]` grouped with
// Music Assistant's own web player, which Home Assistant has no entity for. That last one is why this is
// read as "not empty" rather than "more than one": a room naming itself is what a group looks like when
// the other member is one Home Assistant cannot see.
func groupMembers(ma string) (members []any, err error) {
	st, err := hass.Get().State(ma)
	if err != nil {
		return nil, err
	}
	members, _ = st.Attributes["group_members"].([]any)
	return members, nil
}

// unjoinMusicAssistant asks Home Assistant to take ma out of its group.
func unjoinMusicAssistant(ma string) error {
	if err := hass.Get().Call("media_player", "unjoin", map[string]any{"entity_id": ma}); err != nil {
		return err
	}
	slog.Info("group: left the group, so this room plays on its own", "player", ma)
	return nil
}
