//go:build !dot

package display

import (
	"context"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
	"github.com/HuskerMinion/techo5/echod/internal/lib/wyoming"
)

// The Sound card's Speaking voice row: which voice answers in, where the device asks its own speech
// server rather than Home Assistant (config.Brain.Direct). Under Home Assistant the voice belongs to
// the assistant there, and the row says so.
//
// The list is the speech server's own, asked for in the background: the card is drawn many times a
// second and cannot wait on the network. Until it arrives, or when the server does not answer, a
// short list of common Piper voices stands in.

// voicesFresh is how long a list from the server is used before it is asked again.
const voicesFresh = 10 * time.Minute

// pickerHeld is how long after its last frame the open picker keeps the list it showed. A tap chooses
// by position, so the list under the finger must not change because the server answered meanwhile;
// the picker is drawn at least once a second while it is open.
const pickerHeld = 3 * time.Second

// commonVoices stand in for the server's list where the language is English. Piper's English voices
// most servers have.
var commonVoices = []string{
	"en_US-amy-medium", "en_US-hfc_female-medium", "en_US-hfc_male-medium", "en_US-joe-medium",
	"en_US-john-medium", "en_US-kristin-medium", "en_US-l2arctic-medium", "en_US-lessac-medium",
	"en_US-norman-medium", "en_US-ryan-medium", "en_US-sam-medium",
}

var serverVoices struct {
	sync.Mutex
	addr, lang string
	names      []string
	at         time.Time
	asking     bool
}

// shownVoices is the list the picker last drew, which is what a tap chooses from.
var shownVoices struct {
	sync.Mutex
	names []string
	at    time.Time
}

// voiceLanguage is the language the voices are asked for: the brain's, en_US style, English when unset.
// The setup page takes it as free text, so "en-US" means the same as "en_US".
func voiceLanguage(b config.Brain) string {
	return strings.ReplaceAll(cmpOr(b.Language, "en"), "-", "_")
}

// refreshVoices asks the speech server for its voices, in the background, when the list held is
// for another server or language, or old. It never waits.
func refreshVoices(b config.Brain) {
	if !b.Direct() {
		return
	}
	lang := voiceLanguage(b)
	v := &serverVoices
	v.Lock()
	if v.asking || (v.addr == b.TTS && v.lang == lang && time.Since(v.at) < voicesFresh) {
		v.Unlock()
		return
	}
	v.asking = true
	v.Unlock()
	safe.Go("speaking voices", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		names, err := wyoming.Voices(ctx, b.TTS, lang)
		v.Lock()
		defer v.Unlock()
		v.asking = false
		if err != nil {
			slog.Info("speaking voices: the speech server did not list them", "err", err)
			// Try again on the next look, but not on every frame of this one. A list this server gave
			// for this language before is still the best there is, so it is kept.
			if v.addr != b.TTS || v.lang != lang {
				v.names = nil
			}
			v.addr, v.lang, v.at = b.TTS, lang, time.Now().Add(-voicesFresh+30*time.Second)
			return
		}
		slices.Sort(names)
		v.addr, v.lang, v.names, v.at = b.TTS, lang, names, time.Now()
	})
}

// voiceChoices is the list the row opens: the server's default first, then the server's voices when
// it has answered for this language, else the common English ones where the language is English, with
// the voice in use always in it.
func voiceChoices(b config.Brain) []string {
	lang := voiceLanguage(b)
	v := &serverVoices
	v.Lock()
	names := v.names
	if v.addr != b.TTS || v.lang != lang {
		names = nil
	}
	names = slices.Clone(names)
	v.Unlock()
	if len(names) == 0 && strings.HasPrefix(strings.ToLower(lang), "en") {
		names = slices.Clone(commonVoices)
	}
	if b.Voice != "" && !slices.Contains(names, b.Voice) {
		names = append([]string{b.Voice}, names...)
	}
	return append([]string{""}, names...)
}

// pickerVoices is what the picker shows: the list it showed last while it stays open, else a fresh one.
func pickerVoices(b config.Brain) []string {
	sv := &shownVoices
	sv.Lock()
	defer sv.Unlock()
	if sv.names == nil || time.Since(sv.at) > pickerHeld {
		sv.names = voiceChoices(b)
	}
	sv.at = time.Now()
	return slices.Clone(sv.names)
}

// voiceLabel is how a voice reads on the screen: en_US-ryan-high is "Ryan, US, high". Medium, the
// quality nearly every voice comes in, is left unsaid.
func voiceLabel(name string) string {
	if name == "" {
		return tr("ui_server_default")
	}
	parts := strings.Split(name, "-")
	if len(parts) != 3 {
		return name
	}
	who := strings.ReplaceAll(parts[1], "_", " ")
	label := capitalize(who)
	if _, region, ok := strings.Cut(parts[0], "_"); ok {
		label += ", " + region
	}
	if parts[2] != "medium" {
		label += ", " + strings.ReplaceAll(parts[2], "_", " ")
	}
	return label
}

// voiceRow is the Sound card's Speaking voice row.
func voiceRow() settingRow {
	b := config.Get().Brain
	if !b.Direct() {
		return settingRow{label: tr("ui_speaking_voice"), sub: tr("ui_home_assistant_settings_voice_assistants"), kind: ctlValue, value: tr("ui_set_there")}
	}
	refreshVoices(b)
	return settingRow{id: "ttsvoice", label: tr("ui_speaking_voice"), sub: tr("ui_how_answers_sound"), kind: ctlChoice, value: voiceLabel(b.Voice)}
}

func voicePicker() (pickerView, bool) {
	b := config.Get().Brain
	if !b.Direct() {
		return pickerView{}, false
	}
	p := pickerView{title: tr("ui_speaking_voice"), cur: -1}
	for i, name := range pickerVoices(b) {
		p.opts = append(p.opts, voiceLabel(name))
		if name == b.Voice {
			p.cur = i
		}
	}
	return p, len(p.opts) > 0
}

// chooseVoice saves the i'th voice of the list the picker showed; the first is the server's default.
// The next answer is spoken in it: the direct pipeline reads the voice afresh for every reply.
func chooseVoice(i int) {
	sv := &shownVoices
	sv.Lock()
	names := sv.names
	sv.Unlock()
	if i < 0 || i >= len(names) {
		return
	}
	if err := config.Set().Brain().SetVoice(names[i]); err != nil {
		slog.Warn("saving the speaking voice failed", "err", err)
		return
	}
	slog.Info("speaking voice", "voice", cmpOr(names[i], "server default"))
}
