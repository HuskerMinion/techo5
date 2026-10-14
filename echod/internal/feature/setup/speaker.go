package setup

import (
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
	"github.com/HuskerMinion/techo5/echod/internal/feature/phone"
	"github.com/HuskerMinion/techo5/echod/internal/feature/voice"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
	"github.com/HuskerMinion/techo5/echod/internal/lib/wake"
)

// speakerSection is what is changed most on Sound & Voice: the volume, the wake word, quiet hours, do
// not disturb, and on a device with a jack where the sound goes. The Dot has no screen to set these on.
//
// Each field carries what the page showed beside it, and a save changes only what was changed on the
// page: a volume turned up by voice while the page sat open is not put back by saving the wake word.
func speakerSection(w http.ResponseWriter, token string) {
	c := config.Get()
	p := media.Get()
	fmt.Fprint(w, `<fieldset><legend>Speaker and voice</legend><form method="post" action="/setup/save">`)
	hidden(w, token, "speaker", "sound")
	vol := p.Volume()
	fmt.Fprintf(w, `<label for="volume">Volume, 0 to %d</label>
	 <input id="volume" name="volume" type="number" min="0" max="%d" value="%d"><input type="hidden" name="was_volume" value="%d">`,
		media.VolumeSteps, media.VolumeSteps, vol, vol)
	if p.SpeakerGainAvailable() {
		gain := p.SpeakerGain()
		fmt.Fprintf(w, `<label for="gain">Speaker gain, dB (0 is normal)</label>
	 <input id="gain" name="gain" type="number" min="0" max="%d" step="1" value="%d"><input type="hidden" name="was_gain" value="%d">
	 <p>Boosts can distort, especially at high volume or with raised bass or treble. Set 0 dB to restore normal gain.</p>`,
			config.SpeakerGainMax, gain, gain)
	}

	if models := wake.Lib().Ours(); len(models) > 0 {
		cur := c.Wake.Slot(0).ID
		fmt.Fprint(w, `<label for="wakeword">Wake word</label><select id="wakeword" name="wakeword">`)
		// What is in force now is always the one shown, even when it is none of these (off, or a word
		// since removed): otherwise the list would show the first, and a save would put it in force.
		listed := false
		for _, m := range models {
			listed = listed || m.ID == cur
		}
		if !listed {
			label := "Off"
			if cur != "" {
				label = strings.ReplaceAll(cur, "_", " ")
			}
			fmt.Fprintf(w, `<option value="%s" selected>%s</option>`, html.EscapeString(cur), html.EscapeString(label))
		}
		for _, m := range models {
			label := m.Phrase
			if label == "" {
				label = strings.ReplaceAll(m.ID, "_", " ")
			}
			fmt.Fprintf(w, `<option value="%s"%s>%s</option>`, html.EscapeString(m.ID), selected(m.ID == cur), html.EscapeString(label))
		}
		fmt.Fprintf(w, `</select><input type="hidden" name="was_wakeword" value="%s">`, html.EscapeString(cur))
	}

	choiceField(w, "quiet", "Quiet hours: the device's own sounds only; alarms and timers still sound", p.QuietChoices(), p.Quiet())
	night := p.NightVolume()
	fmt.Fprintf(w, `<label for="night">Night volume: turned down to this as quiet hours start, and back after; 0 for none</label>
	 <input id="night" name="night" type="number" min="0" max="%d" value="%d"><input type="hidden" name="was_night" value="%d">`,
		media.VolumeSteps, night, night)
	if speaker.HasJack {
		choiceField(w, "output", "Audio output", media.OutputChoices(), p.Output())
	}

	dnd := c.Home.DoNotDisturb
	fmt.Fprintf(w, `<p><label><input type="checkbox" name="dnd" value="yes" style="width:auto"%s> Do not disturb: intercom
	  calls from other rooms are turned away</label><input type="hidden" name="was_dnd" value="%t"></p>`, checkedIf(dnd), dnd)
	fmt.Fprint(w, `<p><button type="submit">Save</button></p></form></fieldset>`)
}

// choiceField is a list of choices with what the page showed beside it.
func choiceField(w http.ResponseWriter, name, label string, choices []string, cur string) {
	fmt.Fprintf(w, `<label for="%s">%s</label><select id="%s" name="%s">`, name, html.EscapeString(label), name, name)
	for _, o := range choices {
		fmt.Fprintf(w, `<option%s>%s</option>`, selected(o == cur), html.EscapeString(o))
	}
	fmt.Fprintf(w, `</select><input type="hidden" name="was_%s" value="%s">`, name, html.EscapeString(cur))
}

func checkedIf(on bool) string {
	if on {
		return " checked"
	}
	return ""
}

// changed is a field's value when the page sent one that differs from what it showed.
func changed(r *http.Request, name string) (string, bool) {
	if _, ok := r.PostForm[name]; !ok {
		return "", false
	}
	v := r.PostFormValue(name)
	return v, v != r.PostFormValue("was_"+name)
}

// level reads a 0..VolumeSteps field that changed, and whether it did; a bad one is a problem to show.
func level(r *http.Request, name, what string) (n int, ok bool, problem string) {
	v, ok := changed(r, name)
	if !ok {
		return 0, false, ""
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n < 0 || n > media.VolumeSteps {
		return 0, false, fmt.Sprintf("the %s is a number from 0 to %d", what, media.VolumeSteps)
	}
	return n, true, ""
}

func saveSpeaker(r *http.Request) string {
	_ = r.ParseForm()
	p := media.Get()
	// The numbers are read before anything is saved, so a mistyped one saves nothing rather than half.
	vol, volChanged, problem := level(r, "volume", "volume")
	if problem != "" {
		return problem
	}
	night, nightChanged, problem := level(r, "night", "night volume")
	if problem != "" {
		return problem
	}
	gain, gainChanged, problem := gainLevel(r, p.SpeakerGainAvailable())
	if problem != "" {
		return problem
	}
	if gainChanged {
		if err := p.SetSpeakerGain(gain); err != nil {
			return "the speaker gain could not be saved: " + err.Error()
		}
	}
	if volChanged {
		p.Adjust(vol - p.Volume())
	}
	// Off is shown, but not chosen here: the page offers words, and the screen or Home Assistant turns it off.
	if v, ok := changed(r, "wakeword"); ok && v != "" {
		known := false
		for _, m := range wake.Lib().Ours() {
			known = known || m.ID == v
		}
		if !known {
			return "that wake word is not on this device"
		}
		// Loading a model takes a moment; the screen does it aside too.
		safe.Go("wake word from the setup page", func() { voice.Get().ChooseWakeWord(v) })
	}
	if v, ok := changed(r, "quiet"); ok {
		if !oneOf(v, p.QuietChoices()) {
			return "that is not one of the quiet hours"
		}
		p.SetQuiet(v)
	}
	if nightChanged {
		p.SetNightVolume(night)
	}
	if v, ok := changed(r, "output"); ok && speaker.HasJack {
		if !oneOf(v, media.OutputChoices()) {
			return "that is not one of the outputs"
		}
		p.SetOutput(v)
	}
	// A checkbox left clear sends nothing, so do not disturb is read from whether it came.
	if was := r.PostFormValue("was_dnd"); was != "" {
		if on := r.PostFormValue("dnd") == "yes"; strconv.FormatBool(on) != was {
			phone.Get().SetDoNotDisturb(on)
		}
	}
	slog.Info("setup page: speaker and voice saved")
	return ""
}

func gainLevel(r *http.Request, available bool) (n int, ok bool, problem string) {
	v, ok := changed(r, "gain")
	if !ok {
		return 0, false, ""
	}
	if !available {
		return 0, false, "speaker gain is not supported on this amplifier"
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n < 0 || n > config.SpeakerGainMax {
		return 0, false, fmt.Sprintf("the speaker gain is a whole number from 0 to %d dB", config.SpeakerGainMax)
	}
	return n, true, ""
}

func oneOf(v string, list []string) bool {
	for _, o := range list {
		if o == v {
			return true
		}
	}
	return false
}
