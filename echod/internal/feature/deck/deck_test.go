//go:build !dot && !spot

package deck

import (
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/lib/obsws"
)

func TestLitFollowsOBS(t *testing.T) {
	cam := obsws.Item{Scene: "Main", Source: "Webcam"}
	st := obsws.State{Connected: true, Scene: "Main", Scenes: []string{"Main", "BRB"}, Streaming: true,
		Muted: map[string]bool{"Mic": true, "Desktop": false}, Shown: map[obsws.Item]bool{cam: false}}
	for _, c := range []struct {
		b          config.DeckButton
		lit, known bool
	}{
		{config.DeckButton{Action: config.DeckOBSScene, Scene: "Main"}, true, true},
		{config.DeckButton{Action: config.DeckOBSScene, Scene: "BRB"}, false, true},
		{config.DeckButton{Action: config.DeckOBSScene, Scene: "Gone"}, false, false},
		{config.DeckButton{Action: config.DeckOBSStream}, true, true},
		{config.DeckButton{Action: config.DeckOBSRecord}, false, true},
		{config.DeckButton{Action: config.DeckOBSMute, Input: "Mic"}, true, true},
		{config.DeckButton{Action: config.DeckOBSMute, Input: "Nope"}, false, false},
		{config.DeckButton{Action: config.DeckOBSSource, Scene: "Main", Source: "Webcam"}, false, true},
		{config.DeckButton{}, false, false},
	} {
		lit, known := Lit(c.b, st, nil)
		if lit != c.lit || known != c.known {
			t.Errorf("%+v: lit %v known %v, want %v %v", c.b, lit, known, c.lit, c.known)
		}
	}
	// OBS away: nothing is known, whatever was last seen.
	st.Connected = false
	if _, known := Lit(config.DeckButton{Action: config.DeckOBSStream}, st, nil); known {
		t.Error("known while OBS is away")
	}
}

func TestLabelsAndIconsFallBackToTheAction(t *testing.T) {
	mic := config.DeckButton{Action: config.DeckOBSMute, Input: "Mic"}
	if Label(mic) != "Mic" || Icon(mic, true) != "microphone-off" || Icon(mic, false) != "microphone" {
		t.Errorf("mic: %q %q %q", Label(mic), Icon(mic, true), Icon(mic, false))
	}
	if l := Label(config.DeckButton{Action: config.DeckPCType, Value: "hunter2"}); l != "Type text" {
		t.Errorf("a Type text button without a label shows %q", l)
	}
	own := config.DeckButton{Action: config.DeckOBSStream, Label: "Go live", Icon: "rocket"}
	if Label(own) != "Go live" || Icon(own, true) != "rocket" {
		t.Error("a button's own label and icon were not used")
	}
}

func TestTrackedItemsOnce(t *testing.T) {
	src := config.DeckButton{Action: config.DeckOBSSource, Scene: "Main", Source: "Webcam"}
	d := config.Deck{Pages: []config.DeckPage{
		{Buttons: []config.DeckButton{src, {Action: config.DeckOBSSource, Scene: "Main"}}},
		{Buttons: []config.DeckButton{src}},
	}}
	if got := trackedItems(d); len(got) != 1 || got[0] != (obsws.Item{Scene: "Main", Source: "Webcam"}) {
		t.Errorf("tracked %v", got)
	}
}
