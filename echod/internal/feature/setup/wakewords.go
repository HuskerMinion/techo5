package setup

import (
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"slices"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/wakeword"
	"github.com/HuskerMinion/techo5/echod/internal/lib/wake"
)

// Wake words of the owner's own (lib/wake/own.go): a microWakeWord model and its .json, sent from the
// page together and kept on the device, so a wake word can be added without Home Assistant's
// custom_wake_words folder and a restart of Home Assistant.
//
// Home Assistant learns what the device can hear only when it asks, which it does as it connects, so
// a model added or removed here is followed by a reconnect once the device is idle (reconnect.go): the
// satellite's Wake word list has it soon after rather than at the next restart of either side.

// models is the device's wake word library; a test has one of its own.
var models = wake.Lib

// wakeWordsSection lists the wake words on the device, with Remove for the ones sent from here, and
// takes a new one.
func wakeWordsSection(w http.ResponseWriter, token string) {
	fmt.Fprint(w, `<fieldset><legend>Wake words of your own</legend>`)
	ours := models().Ours()
	if len(ours) == 0 {
		fmt.Fprint(w, `<p style="margin-top:0">None on the device yet.</p>`)
	}
	for _, m := range ours {
		fmt.Fprint(w, `<form method="post" action="/setup/save" style="margin:0">`)
		hidden(w, token, "wakeword-remove", "sound")
		fmt.Fprintf(w, `<input type="hidden" name="id" value="%s"><p style="margin:.3em 0"><strong>%s</strong>
		 <span class="note">(%s)</span>`, html.EscapeString(m.ID), html.EscapeString(m.Phrase), html.EscapeString(m.ID))
		if m.Uploaded {
			fmt.Fprint(w, ` sent from this page <button type="submit" class="quiet" style="margin-left:.5em">Remove</button>`)
		} else {
			fmt.Fprint(w, ` <span class="note">came with the device or from Home Assistant</span>`)
		}
		fmt.Fprint(w, `</p></form>`)
	}
	fmt.Fprintf(w, `<div class="row"><div><label for="wwjson">The model's .json</label>
	 <input id="wwjson" type="file" accept=".json,application/json"></div>
	 <div><label for="wwmodel">The model's .tflite</label>
	 <input id="wwmodel" type="file" accept=".tflite"></div></div>
	 <p><button type="button" id="wwsend">Add wake word</button></p>
	 <p id="wwsays" class="note">A microWakeWord model: the two files with the same name, for example
	  <code>echo.json</code> and <code>echo.tflite</code>. The phrase in the .json is what it is called. It can be
	  chosen above straight away. Home Assistant picks it up once the device is idle, usually within
	  seconds, and then has it in the device's Wake word list.</p>
	 <script>
	 document.getElementById('wwsend').addEventListener('click', async () => {
	   const say = document.getElementById('wwsays');
	   const j = document.getElementById('wwjson').files[0], m = document.getElementById('wwmodel').files[0];
	   if (!j || !m) { say.textContent = 'Choose both files first.'; return; }
	   say.textContent = 'Sending...';
	   const fd = new FormData(); fd.append('token', %q); fd.append('manifest', j, 'model.json'); fd.append('model', m, 'model.tflite');
	   try {
	     const r = await fetch('/setup/wakeword', {method: 'POST', body: fd});
	     say.textContent = r.ok ? 'Kept: ' + await r.text() : await r.text();
	     if (r.ok) setTimeout(() => location.reload(), 1500);
	   } catch (e) { say.textContent = 'It could not be sent.'; }
	 });
	 </script></fieldset>`, token)
}

// tooLarge is whether err is a request body stopped at the most it may be (http.MaxBytesReader).
func tooLarge(err error) bool {
	var big *http.MaxBytesError
	return errors.As(err, &big)
}

// listening is whether a slot is listening for the model, as the selection was last saved, or it is the
// hang-up word or a command word: a model any of them has loaded would go on running whatever replaced
// it on disk.
func listening(id string) bool {
	wake := config.Get().Wake
	for _, s := range wake.Slots(wakeword.Slots) {
		if s.ID == id {
			return true
		}
	}
	if id == "" {
		return false
	}
	return wake.Hangup.ModelID() == id || slices.ContainsFunc(wake.CommandWords(), func(w config.CommandWord) bool {
		return w.ID == id
	})
}

// wakeWordUpload takes a model from the page's script: the token, then the .json, then the .tflite,
// each read no further than the most it may be. The names the browser gives the files are not read.
func (f *Feature) wakeWordUpload(w http.ResponseWriter, r *http.Request) {
	token, in := f.session(r)
	if !in {
		http.Error(w, "not let in", http.StatusForbidden)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "post a wake word", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, wake.MaxOwnManifest+wake.MaxOwnModel+64<<10)
	mr, err := r.MultipartReader()
	if err != nil {
		http.Error(w, "that was not a wake word sent from this page", http.StatusBadRequest)
		return
	}
	// The form carries the session it came from, as every save does, so another site's page cannot
	// post a model here with a browser's cookie.
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
	// Past the most the request may be, the body stops with an error of its own, which is the files
	// being too big rather than anything going wrong on the way.
	tooBig := fmt.Sprintf("the files are too big: the most is %d KB for the .tflite and %d KB for the .json",
		wake.MaxOwnModel>>10, wake.MaxOwnManifest>>10)
	p, err = mr.NextPart()
	if tooLarge(err) {
		http.Error(w, tooBig, http.StatusRequestEntityTooLarge)
		return
	}
	if err != nil || p.FormName() != "manifest" {
		http.Error(w, "no .json was sent", http.StatusBadRequest)
		return
	}
	manifest, err := io.ReadAll(io.LimitReader(p, wake.MaxOwnManifest+1))
	if tooLarge(err) {
		http.Error(w, tooBig, http.StatusRequestEntityTooLarge)
		return
	}
	if err != nil {
		http.Error(w, "the .json did not arrive whole", http.StatusBadRequest)
		return
	}
	p, err = mr.NextPart()
	if tooLarge(err) {
		http.Error(w, tooBig, http.StatusRequestEntityTooLarge)
		return
	}
	if err != nil || p.FormName() != "model" {
		http.Error(w, "no .tflite was sent", http.StatusBadRequest)
		return
	}
	model, err := io.ReadAll(io.LimitReader(p, wake.MaxOwnModel+1))
	if tooLarge(err) {
		http.Error(w, tooBig, http.StatusRequestEntityTooLarge)
		return
	}
	if err != nil {
		http.Error(w, "the .tflite did not arrive whole", http.StatusBadRequest)
		return
	}
	m, err := models().AddOwn(manifest, model, listening)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	f.used()
	whenIdle.ask() // so Home Assistant asks again what the device can hear
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	fmt.Fprintf(w, "%s (%s)", m.Phrase, m.ID)
}

// removeWakeWord takes away one sent from the page. One a slot is listening for stays until another
// is chosen (RemoveOwn).
func removeWakeWord(r *http.Request) string {
	id := r.PostFormValue("id")
	if err := models().RemoveOwn(id, listening); err != nil {
		return err.Error()
	}
	whenIdle.ask()
	return ""
}
