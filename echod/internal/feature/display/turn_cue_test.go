//go:build !dot

package display

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/voice"
)

// A cue turn leaves the page at idle and keeps the turn's phase for the light; the words stay only
// for captions, and outside a turn nothing changes.
func TestCueTurn(t *testing.T) {
	for _, c := range []struct {
		phase, heard, reply string
		captions            bool
		cue, page, h, r     string
	}{
		{"idle", "", "", true, "", "idle", "", ""},
		{"listening", "", "", false, "listening", "idle", "", ""},
		{"thinking", "lights on", "", false, "thinking", "idle", "", ""},
		{"thinking", "lights on", "", true, "thinking", "idle", "lights on", ""},
		{"replying", "lights on", "Turned on.", true, "replying", "idle", "lights on", "Turned on."},
		{"lingering", "lights on", "Turned on.", true, "lingering", "idle", "lights on", "Turned on."},
		// With no captions a finished turn leaves nothing on the screen: no light, no words.
		{"lingering", "lights on", "Turned on.", false, "", "idle", "", ""},
	} {
		cue, page, h, r := cueTurn(c.phase, c.heard, c.reply, c.captions)
		if cue != c.cue || page != c.page || h != c.h || r != c.r {
			t.Errorf("cueTurn(%q, captions %v) = %q %q %q %q, want %q %q %q %q",
				c.phase, c.captions, cue, page, h, r, c.cue, c.page, c.h, c.r)
		}
	}
	for cue, lit := range map[string]bool{"": false, "listening": true, "thinking": true, "replying": true, "lingering": false} {
		if cueLit(cue) != lit {
			t.Errorf("cueLit(%q) = %v, want %v", cue, !lit, lit)
		}
	}
	if w := cueWords("thinking", "lights on", ""); w != "lights on" {
		t.Errorf("thinking captions %q, want what was heard", w)
	}
	if w := cueWords("lingering", "lights on", "Turned on."); w != "Turned on." {
		t.Errorf("lingering captions %q, want the answer", w)
	}
}

// The cue styles, Rainbow among them, are cues and never the equalizer; the pictures are not cues.
func TestCueStyles(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	t.Cleanup(func() { _ = config.Set().Screen().TurnStyle("") })
	for _, c := range []struct {
		value         string
		cue, captions bool
		equalizerWhen bool
	}{
		{"", false, false, false},
		{"wave", false, false, hasEqualizer},
		{"cue", true, false, false},
		{"captions", true, true, false},
		{"rainbow", hasRainbow, hasRainbow, false}, // Classic where it is not offered
	} {
		if err := config.Set().Screen().TurnStyle(c.value); err != nil {
			t.Fatal(err)
		}
		if cueOn() != c.cue || captionsOn() != c.captions || equalizerOn() != c.equalizerWhen {
			t.Errorf("style %q: cue %v captions %v equalizer %v, want %v %v %v", c.value,
				cueOn(), captionsOn(), equalizerOn(), c.cue, c.captions, c.equalizerWhen)
		}
	}
}

// Under a cue the page stays up through a turn, and the touch side hands fingers to it as the frame
// drew it; a finished answer is not a page of its own to clear with the first tap, captions or not.
func TestCuePageKeepsItsTaps(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	t.Cleanup(func() { _ = config.Set().Screen().TurnStyle("") })
	d := &Display{poke: make(chan struct{}, 1)}
	now := time.Now()
	d.view, d.viewAt = voice.State{Phase: "idle", Heard: "what time is it", Reply: "It's noon."}, now
	if pageIdle("thinking") || !d.answerUp(now) {
		t.Fatal("classic: a turn is not the page, and its answer is up")
	}
	styles := []string{"cue", "captions"}
	if hasRainbow {
		styles = append(styles, "rainbow")
	}
	for _, style := range styles {
		if err := config.Set().Screen().TurnStyle(style); err != nil {
			t.Fatal(err)
		}
		for _, phase := range []string{"listening", "thinking", "replying", "idle"} {
			if !pageIdle(phase) {
				t.Errorf("%s: the page is not up while %s", style, phase)
			}
		}
		if d.answerUp(now) {
			t.Errorf("%s: the finished answer takes the first tap", style)
		}
	}
}
