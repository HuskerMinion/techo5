package room

import (
	"slices"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/led"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
)

func TestWantsMusic(t *testing.T) {
	for name, want := range map[string]bool{
		led.EffectMusic:     true,
		"":                  false,
		led.EffectRoomGlow:  false,
		led.EffectRoomMeter: false,
	} {
		if got := wantsMusic(name); got != want {
			t.Errorf("wantsMusic(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestMusicIsOfferedAndOnlyListensWhileChosen(t *testing.T) {
	r := Get()
	if !slices.Contains(r.sel.Options, led.EffectMusic) {
		t.Fatalf("the select does not offer %q: %v", led.EffectMusic, r.sel.Options)
	}

	p := speaker.Get()
	r.show(led.EffectMusic)
	if !p.MusicTapping() {
		t.Fatal("choosing the music effect did not start the analysis")
	}
	r.show(led.EffectRoomGlow)
	if p.MusicTapping() {
		t.Fatal("another reaction left the analysis running")
	}
	r.show(led.EffectMusic)
	r.show("")
	if p.MusicTapping() {
		t.Fatal("None left the analysis running")
	}
}
