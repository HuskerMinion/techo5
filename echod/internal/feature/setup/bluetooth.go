package setup

import (
	"fmt"
	"log/slog"
	"net/http"

	"github.com/HuskerMinion/techo5/echod/internal/feature/btaudio"
)

// secureConnSetting is the part of btaudio the section uses: Secure Connections, and Bluetooth sets
// the volume.
type secureConnSetting interface {
	SecureConnectionsHere() bool
	SecureConnections() bool
	SetSecureConnections(on bool) error
	AudioHere() bool
	SetsVolume() bool
	SetSetsVolume(on bool) error
}

// secureConns is btaudio's setting. A variable for the tests, which leave btaudio unstarted: Get
// starts its Secure Connections loop, which reads the config the other tests swap.
var secureConns = func() secureConnSetting { return btaudio.Get() }

// bluetoothSection is the controller's Secure Connections (#183): on, as it comes, and off for a phone
// that pairs and is dropped a second or two later. On every device that offers it, the Dot too, whose
// only place for it besides Home Assistant this is. Not on athena on mainline, which has it off for good.
//
// And Bluetooth sets the volume (#245): on, as it comes, a phone's volume slider is the device's volume;
// off, the device keeps its own. On every device a phone plays to, athena on mainline too; not on a
// panel, whose Bluetooth is Android's.
func bluetoothSection(w http.ResponseWriter, token string) {
	bt := secureConns()
	sc, vol := bt.SecureConnectionsHere(), bt.AudioHere()
	if !sc && !vol {
		return
	}
	fmt.Fprint(w, `<fieldset><legend>Bluetooth</legend><form method="post" action="/setup/save">`)
	hidden(w, token, "bluetooth", "connections")
	if vol {
		fmt.Fprintf(w, `
	 <p><label><input type="checkbox" name="setsvolume" value="yes" style="width:auto"%s> Bluetooth sets the volume</label></p>
	 <p class="note">On, a phone or a computer playing to this device has one volume, this device's: its
	  volume slider moves this device's volume, and the volume buttons and Home Assistant move its slider.
	  Off, this device keeps its own volume, and the phone's or the computer's own level only makes its own
	  sound quieter. Turn it off for a computer that takes this device to full volume.</p>`, checkedIf(bt.SetsVolume()))
	}
	if sc {
		fmt.Fprintf(w, `
	 <p><label><input type="checkbox" name="sc" value="yes" style="width:auto"%s> Secure Connections</label></p>
	 <p class="note">Leave it on unless a phone pairs with this device and is dropped a second or two later.
	  Off, such a phone pairs the older way and stays. A phone set to pair only with Secure Connections then
	  cannot pair at all.</p>
	 <p class="note">Changing it turns Bluetooth off and on again: whatever is connected drops for a moment.</p>`, checkedIf(bt.SecureConnections()))
	}
	fmt.Fprint(w, `
	 <p><button type="submit">Save</button></p></form></fieldset>`)
}

func saveBluetooth(r *http.Request) string {
	bt := secureConns()
	sc, vol := bt.SecureConnectionsHere(), bt.AudioHere()
	if !sc && !vol {
		return "this device has no such setting"
	}
	if vol {
		if on := r.PostFormValue("setsvolume") == "yes"; on != bt.SetsVolume() {
			if err := bt.SetSetsVolume(on); err != nil {
				return "could not save it"
			}
			slog.Info("setup page: bluetooth sets the volume set", "on", on)
		}
	}
	if sc {
		if on := r.PostFormValue("sc") == "yes"; on != bt.SecureConnections() {
			if err := bt.SetSecureConnections(on); err != nil {
				return "could not save it"
			}
			slog.Info("setup page: bluetooth secure connections set", "on", on)
		}
	}
	return ""
}
