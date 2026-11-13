package setup

import (
	"bytes"
	"fmt"
	"html"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
)

// Sounds of the owner's own (hardware/speaker/own_sounds.go): a WAVE file for any of the sounds a
// request makes, sent from the page one at a time and checked the way the player checks it before it
// is kept, so a file that would only play the stock sound in its place is refused with the reason
// instead. Nothing is converted here: the device has no converter on every model, and a page that
// says what format it needs is better than one that works on some devices only.
//
// Each row is a plain form with an Upload button, so it works without script and the page comes back
// saying what happened, as every save does. A sound is small enough to send as it is.

// soundTitles is what each sound is called on the page, and when it plays.
var soundTitles = map[string][2]string{
	"wake_word_triggered": {"Wake", "when the wake word is heard, where Wake sound is Home Assistant"},
	"failure":             {"Failure", "when a request cannot be served"},
	"canceled":            {"Canceled", "when a request is dropped"},
	"timer_finished":      {"Timer finished", "a finished timer, and the Home Assistant alarm sound"},
	"alarm":               {"Alarm", "an alarm, where Alarm sound is Your own"},
	"mute_switch_on":      {"Mute", "muting the microphones"},
	"mute_switch_off":     {"Unmute", "unmuting the microphones"},
	"listening_ended":     {"Listening ended", "when the device stops listening and waits for the answer"},
	"button_press":        {"Button", "a button held (the mute button's hold)"},
	"volume_change":       {"Volume", "each press of volume up or down"},
	"bluetooth_connected": {"Bluetooth connected", "a Bluetooth device connecting"},
	"drop_in":             {"Drop In", "the chime before a Drop In opens the microphones"},
	"announcement":        {"Announcement", "the chime before an announcement"},
	"intercom_ring":       {"Intercom ringtone", "the ring for any incoming call when Call ring is Your own"},
}

// soundsSection lists the sounds, whether each is the device's own or one sent here, and takes a file
// for any of them.
func soundsSection(w http.ResponseWriter, token string) {
	fmt.Fprint(w, `<fieldset><legend>Sounds of your own</legend>
	 <p class="note" style="margin-top:0">A recording of your own plays in place of the device's sound. Play
	  tries what plays now, quietly. To change one, choose a file beside it and press Upload.</p>`)
	for _, name := range speaker.OwnSounds() {
		title := soundTitles[name]
		has, problem := speaker.OwnState(name)
		state := "the device's own"
		switch {
		case has && problem != "":
			state = `<span class="bad">yours, but it does not play: ` + html.EscapeString(problem) + `</span>`
		case has:
			state = `<span class="ok">yours</span>`
		}
		fmt.Fprintf(w, `<p style="margin:1rem 0 0"><strong>%s</strong>, %s. Now: %s</p><div class="btns" style="margin-top:.4rem">`,
			html.EscapeString(title[0]), html.EscapeString(title[1]), state)
		fmt.Fprint(w, `<form method="post" action="/setup/save" style="margin:0">`)
		hidden(w, token, "sound-play", "sound")
		fmt.Fprintf(w, `<input type="hidden" name="sound" value="%s"><button type="submit" class="quiet">Play</button></form>`,
			html.EscapeString(name))
		// The token comes first and the sound's name next, ahead of the file: the order the fields are in
		// is the order the browser sends them, and the order the device reads them in.
		fmt.Fprintf(w, `<form method="post" action="/setup/sound" enctype="multipart/form-data"
		 style="margin:0;display:flex;gap:.5rem;flex:1;flex-wrap:wrap;align-items:center">
		 <input type="hidden" name="token" value="%s"><input type="hidden" name="sound" value="%s">
		 <input type="file" name="file" accept=".wav,audio/wav" required style="width:auto;flex:1;min-width:12rem"
		  aria-label="A recording for %s">
		 <button type="submit" class="quiet">Upload</button></form>`,
			html.EscapeString(token), html.EscapeString(name), html.EscapeString(title[0]))
		if has {
			fmt.Fprint(w, `<form method="post" action="/setup/save" style="margin:0">`)
			hidden(w, token, "sound-remove", "sound")
			fmt.Fprintf(w, `<input type="hidden" name="sound" value="%s"><button type="submit" class="quiet">Use the device's own</button></form>`,
				html.EscapeString(name))
		}
		fmt.Fprint(w, `</div>`)
	}
	soundPackSection(w, token)
	fmt.Fprint(w, `<p class="note">A WAVE (.wav) file, 16-bit, 48 kHz, mono or stereo, at most 10 seconds
	  (2 seconds for the volume, button, Bluetooth, listening-ended and Drop In sounds).
	  Other formats are not converted here; ffmpeg makes one from anything:
	  <code>ffmpeg -i chime.mp3 -ac 1 -ar 48000 -c:a pcm_s16le chime.wav</code>. Keep the wake sound short: the
	  device listens for the request only once it has played.</p></fieldset>`)
}

// sound takes one recording from a row's form: the token, then which sound it is for, then the file.
// The token is checked before anything else is read, and the file is never read past the most a sound
// can be. The page comes back to the tab, saying whether it was kept and if not why, as a save does.
func (f *Feature) sound(w http.ResponseWriter, r *http.Request) {
	token, in := f.session(r)
	if !in {
		http.Error(w, "not let in", http.StatusForbidden)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "post a sound", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, speaker.MaxOwnUpload+64<<10)
	mr, err := r.MultipartReader()
	if err != nil {
		http.Error(w, "that was not a sound sent from this page", http.StatusBadRequest)
		return
	}
	// The form carries the session it came from, as every save does, so another site's page cannot
	// post a sound here with a browser's cookie.
	p, err := mr.NextPart()
	if err != nil || p.FormName() != "token" {
		http.Error(w, "that did not come from this page", http.StatusForbidden)
		return
	}
	got, err := io.ReadAll(io.LimitReader(p, 256))
	if err != nil || !sameToken(string(got), token) {
		http.Error(w, "that did not come from this page", http.StatusForbidden)
		return
	}
	problem := receiveSound(mr)
	if problem == "" {
		f.used()
		http.Redirect(w, r, back("sound", "saved", "1"), http.StatusSeeOther)
		return
	}
	slog.Warn("setup page: a sound was refused", "problem", problem)
	http.Redirect(w, r, back("sound", "problem", problem), http.StatusSeeOther)
}

// receiveSound reads which sound, then the file, and keeps it; what went wrong, or "". Which sound is
// a name from the list, looked up there; what the browser called its file is not read at all.
func receiveSound(mr *multipart.Reader) string {
	p, err := mr.NextPart()
	if err != nil || p.FormName() != "sound" {
		return "no sound was named"
	}
	name, err := io.ReadAll(io.LimitReader(p, 64))
	if err != nil {
		return "no sound was named"
	}
	if _, ok := soundTitles[string(name)]; !ok {
		return "that is not one of the sounds"
	}
	p, err = mr.NextPart()
	if tooLarge(err) {
		return tooBigSound
	}
	if err != nil || p.FormName() != "file" {
		return "no file was sent"
	}
	body := &readErr{r: p}
	if err := speaker.SaveOwn(string(name), body); err != nil {
		if tooLarge(body.err) {
			return tooBigSound
		}
		return err.Error()
	}
	return ""
}

// tooBigSound is what a sound over the most a request may be is told.
var tooBigSound = fmt.Sprintf("that file is too big: the most is %d KB, about ten seconds of sound", speaker.MaxOwnUpload>>10)

// readErr is a reader that keeps the error it stopped with, for when what reads it says only that it
// failed.
type readErr struct {
	r   io.Reader
	err error
}

func (r *readErr) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	if err != nil && err != io.EOF {
		r.err = err
	}
	return n, err
}

func playSound(r *http.Request) string {
	name := r.PostFormValue("sound")
	if _, ok := soundTitles[name]; !ok {
		return "that is not one of the sounds"
	}
	if err := speaker.PreviewOwn(name); err != nil {
		return err.Error()
	}
	return ""
}

func removeSound(r *http.Request) string {
	name := r.PostFormValue("sound")
	if _, ok := soundTitles[name]; !ok {
		return "that is not one of the sounds"
	}
	if err := speaker.RemoveOwn(name); err != nil {
		return "could not remove it: " + err.Error()
	}
	return ""
}

// soundPackSection saves every sound of your own as one file, and loads such a file.
func soundPackSection(w http.ResponseWriter, token string) {
	fmt.Fprintf(w, `<p style="margin:1.2rem 0 0"><strong>Sound pack</strong>. All your sounds in one file, to keep or to
	 use on another device. Loading one replaces the sounds it holds and leaves the others as they are.</p>
	 <div class="btns" style="margin-top:.4rem">
	 <a href="/setup/soundpack" download>Save sound pack</a>
	 <form method="post" action="/setup/soundpack" enctype="multipart/form-data"
	  style="margin:0;display:flex;gap:.5rem;flex:1;flex-wrap:wrap;align-items:center">
	  <input type="hidden" name="token" value="%s">
	  <input type="file" name="file" accept="%s" required style="width:auto;flex:1;min-width:12rem"
	   aria-label="A sound pack">
	  <button type="submit" class="quiet">Load sound pack</button></form></div>`,
		html.EscapeString(token), speaker.PackExt)
}

// soundPack is the pack for download (GET) and for loading (POST). Both need a browser let in, and a
// load needs the page's token ahead of the file, as a sound does.
func (f *Feature) soundPack(w http.ResponseWriter, r *http.Request) {
	token, in := f.session(r)
	if !in {
		http.Error(w, "not let in", http.StatusForbidden)
		return
	}
	if r.Method == http.MethodGet {
		var buf bytes.Buffer
		if err := speaker.PackSounds(&buf); err != nil {
			http.Redirect(w, r, back("sound", "problem", err.Error()), http.StatusSeeOther)
			return
		}
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", `attachment; filename="sounds`+speaker.PackExt+`"`)
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(buf.Bytes())
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "get or post a sound pack", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, speaker.MaxPack+64<<10)
	mr, err := r.MultipartReader()
	if err != nil {
		http.Error(w, "that was not a sound pack sent from this page", http.StatusBadRequest)
		return
	}
	p, err := mr.NextPart()
	if err != nil || p.FormName() != "token" {
		http.Error(w, "that did not come from this page", http.StatusForbidden)
		return
	}
	got, err := io.ReadAll(io.LimitReader(p, 256))
	if err != nil || !sameToken(string(got), token) {
		http.Error(w, "that did not come from this page", http.StatusForbidden)
		return
	}
	problem := ""
	p, err = mr.NextPart()
	switch {
	case tooLarge(err):
		problem = fmt.Sprintf("that file is too big for a sound pack: the most is %d MB", speaker.MaxPack>>20)
	case err != nil || p.FormName() != "file":
		problem = "no file was sent"
	default:
		body := &readErr{r: p}
		b, _ := io.ReadAll(io.LimitReader(body, speaker.MaxPack+1))
		if tooLarge(body.err) {
			problem = fmt.Sprintf("that file is too big for a sound pack: the most is %d MB", speaker.MaxPack>>20)
		} else if _, err := speaker.LoadPack(b); err != nil {
			problem = err.Error()
		}
	}
	if problem != "" {
		slog.Warn("setup page: a sound pack was refused", "problem", problem)
		http.Redirect(w, r, back("sound", "problem", problem), http.StatusSeeOther)
		return
	}
	f.used()
	http.Redirect(w, r, back("sound", "saved", "1"), http.StatusSeeOther)
}
