//go:build linux

package btaudio

import (
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/lib/bluez"
)

func TestWhatIsAPhone(t *testing.T) {
	const speaker = "00:11:22:33:44:55"
	source := []string{"0000110A-0000-1000-8000-00805F9B34FB"}
	for name, c := range map[string]struct {
		d    bluez.Device
		want bool
	}{
		"sends audio":           {bluez.Device{Paired: true, UUIDs: source}, true},
		"says it is a phone":    {bluez.Device{Paired: true, Icon: "phone"}, true},
		"not paired":            {bluez.Device{Icon: "phone", UUIDs: source}, false},
		"plays audio too":       {bluez.Device{Paired: true, UUIDs: source, AudioSink: true}, false},
		"the remembered device": {bluez.Device{Paired: true, Address: speaker, UUIDs: source}, false},
		"a headset, no sink":    {bluez.Device{Paired: true, Icon: "audio-headset", UUIDs: source}, false},
		"headphones":            {bluez.Device{Paired: true, Icon: "audio-headphones"}, false},
		"an audio card":         {bluez.Device{Paired: true, Icon: "audio-card"}, false},
		"neither":               {bluez.Device{Paired: true, Icon: "input-keyboard"}, false},
	} {
		if got := isPhone(c.d, speaker); got != c.want {
			t.Errorf("%s: isPhone = %v, want %v", name, got, c.want)
		}
	}
}
