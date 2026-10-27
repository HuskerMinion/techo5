package setup

import (
	"bytes"
	"fmt"
	"html"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/takes"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
)

// Recording takes of a word (feature/takes): asking for a recording, and downloading what it was cut
// into. Every route here is the browser's that asked, and no other's: takes are not shared between the
// browsers let in.
func init() {
	routes["/setup/takes"] = (*Feature).takes
	routes["/setup/takes/state"] = (*Feature).takesState
	routes["/setup/takes.zip"] = (*Feature).takesZip
	routes["/setup/takes/take"] = (*Feature).takesTake
}

// recorder is feature/takes.
var recorder = takes.Get

// What the rest of the page asks of the recorder: asking for a recording, the press, and the page closing.
// Variables, so that a test can see them reach it without a microphone to record from.
var (
	armTakes       = func(word string, kind takes.Kind, session string) error { return takes.Get().Arm(word, kind, session) }
	takesOwner     = func() string { return takes.Get().Owner() }
	takesPressed   = func() bool { return takes.Get().Pressed() }
	takesTookPress = func() bool { return takes.Get().TookPress() }
	discardTakes   = func(why string) { takes.Get().Discard(why) }
)

// takesWait is how long a download may take to write: three and a half megabytes over the slowest Wi-Fi
// in a house, with room to spare. A browser that stops reading must not hold the takes forever.
const takesWait = 2 * time.Minute

// takes is the section's buttons: record, cancel, stop and throw away.
func (f *Feature) takes(w http.ResponseWriter, r *http.Request) {
	token, in := f.session(r)
	if !in {
		http.Error(w, "not let in", http.StatusForbidden)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "post to record", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	if err := r.ParseForm(); err != nil || !sameToken(r.PostFormValue("token"), token) {
		http.Error(w, "that did not come from this page", http.StatusForbidden)
		return
	}
	rec := recorder()
	problem := ""
	switch r.PostFormValue("do") {
	case "record":
		kind := takes.Word
		if r.PostFormValue("kind") == "not" {
			kind = takes.NotWord
		}
		// Another browser's takes are its own, but not once it has gone: forgotten to make room for
		// another, or left alone for as long as the page would close by itself. Nobody else could ever
		// throw them away until the page closed.
		if owner := takesOwner(); owner != "" && owner != token && !f.stillHere(owner) {
			discardTakes("the browser that recorded them has gone")
		}
		if err := armTakes(r.PostFormValue("word"), kind, token); err != nil {
			problem = err.Error()
		} else if _, in := f.session(r); !in {
			// The page closed while this was being asked for, and threw away the takes before there was
			// an asking to throw away: nothing it asked for outlives it.
			discardTakes("the setup page closed")
		}
	case "stop":
		if rec.State(token).Mine {
			rec.Stop()
		}
	case "discard":
		if rec.State(token).Mine {
			rec.Discard("thrown away on the setup page")
		}
	default:
		problem = "that is not something this section does"
	}
	to := back("sound", "", "")
	if problem != "" {
		to = back("sound", "problem", problem)
	}
	http.Redirect(w, r, to+"#takes", http.StatusSeeOther)
}

// takesState is what the section polls while a recording waits or runs. It is not the page being used
// (watching): the most a recording runs is two minutes, well inside the page's idle time.
func (f *Feature) takesState(w http.ResponseWriter, r *http.Request) {
	token, in := f.session(r)
	if !in {
		http.Error(w, "not let in", http.StatusForbidden)
		return
	}
	st := recorder().State(token)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	phase := st.Phase.String()
	if !st.Mine && st.Phase != takes.Idle {
		phase = "another"
	}
	fmt.Fprintf(w, `{"phase":%q,"seconds":%.1f,"about":%d,"kept":%d}`+"\n", phase, st.Elapsed.Seconds(), st.About, st.Takes)
}

// takesZip is the takes as a zip, for the browser that recorded them.
func (f *Feature) takesZip(w http.ResponseWriter, r *http.Request) {
	token, in := f.session(r)
	if !in {
		http.Error(w, "not let in", http.StatusForbidden)
		return
	}
	name, write, ok := recorder().Zip(token)
	if !ok {
		http.Error(w, takes.ErrNone.Error(), http.StatusNotFound)
		return
	}
	sendZip(w, name, write)
}

// sendZip writes the zip, and makes a download that could not finish fail as one rather than leave the
// browser a file it keeps as whole: takes thrown away before anything went are a 404, and part of a
// zip, which has no directory to open it by, is a connection broken off.
func sendZip(w http.ResponseWriter, name string, write func(io.Writer) error) {
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(takesWait))
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, name))
	sent := &counted{w: w}
	if err := write(sent); err != nil {
		slog.Warn("setup page: the takes did not all go", "err", err)
		if sent.n == 0 {
			w.Header().Del("Content-Disposition")
			http.Error(w, takes.ErrNone.Error(), http.StatusNotFound)
			return
		}
		panic(http.ErrAbortHandler)
	}
	slog.Info("setup page: takes downloaded")
}

// counted is a writer that knows whether anything has gone through it.
type counted struct {
	w io.Writer
	n int
}

func (c *counted) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += n
	return n, err
}

// stillHere is whether a browser let in is still on the page: let in, and seen within the time the page
// waits before closing itself.
func (f *Feature) stillHere(token string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	seen, ok := f.live[token]
	return ok && time.Since(seen) < idle
}

// takesTake is one take, to listen to on the page before keeping them. Served with ServeContent, which
// answers the byte ranges a phone's browser asks an audio file for before it will play it.
func (f *Feature) takesTake(w http.ResponseWriter, r *http.Request) {
	token, in := f.session(r)
	if !in {
		http.Error(w, "not let in", http.StatusForbidden)
		return
	}
	n, _ := strconv.Atoi(r.URL.Query().Get("n"))
	wav, at, ok := recorder().Take(n, token)
	if !ok {
		http.NotFound(w, r)
		return
	}
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(takesWait))
	w.Header().Set("Content-Type", "audio/wav")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, "take.wav", at, bytes.NewReader(wav))
}

// takesSection is Record takes of a word, on the Sound & Voice tab of a device with microphones.
func takesSection(w http.ResponseWriter, token string) {
	if !layout.HasMicrophone() {
		return
	}
	fmt.Fprint(w, `<fieldset id="takes"><legend>Record takes of a word</legend>`)
	fmt.Fprint(w, `<p class="note" style="margin-top:0">For training a wake word, or a command word, on what this
	  device really hears: it records up to two minutes from its own microphones, exactly as the wake word
	  hears them, and cuts it into one take each time the word is said. Each recording starts with a press on
	  the device: the action button, or Start on the screen.</p>`)
	if !config.Get().Security.Takes {
		fmt.Fprint(w, `<p class="note">It is off. Turn on <strong>Record takes of a word</strong> under Privacy &amp;
		  Security, on the device's screen or in Home Assistant.</p></fieldset>`)
		return
	}
	st := recorder().State(token)
	if st.Why != "" && st.Phase == takes.Idle {
		fmt.Fprintf(w, `<p><strong>The last recording:</strong> %s.</p>`, html.EscapeString(st.Why))
	}
	switch {
	case !st.Mine && (st.Phase == takes.Armed || st.Phase == takes.Recording):
		fmt.Fprint(w, `<p>Another browser is recording takes on this device right now.</p>`)
	case !st.Mine && st.Phase == takes.Kept:
		fmt.Fprint(w, `<p>Another browser's takes are kept on this device, until it throws them away or this page closes.</p>`)
	case st.Mine && st.Phase == takes.Armed:
		fmt.Fprintf(w, `<p><strong>Press the action button on the device to start</strong>, or tap Start on its screen.
		 Waiting a minute for “%s”.</p>`, html.EscapeString(st.Word))
		takesButtons(w, token, "stop", "Cancel")
		takesPoll(w, takes.Armed)
	case st.Mine && st.Phase == takes.Recording:
		fmt.Fprintf(w, `<p>Recording takes of “%s”: <span id="takestime">%s</span> of %s, about <span id="takesabout">%d</span>
		 so far.</p>`, html.EscapeString(st.Word), minSec(st.Elapsed), minSec(takes.Longest), st.About)
		fmt.Fprint(w, `<p class="note">Say it, wait a second, and say it again, near and far, facing toward and away.
		  A press of the action button, or Stop on the screen, ends it.</p>`)
		takesButtons(w, token, "stop", "Stop and keep the takes", "discard", "Throw away")
		takesPoll(w, takes.Recording)
	case st.Mine && st.Phase == takes.Kept:
		keptSection(w, token, st)
	default:
		recordForm(w, token, st.Word)
	}
	fmt.Fprint(w, `</fieldset>`)
}

// recordForm asks for a recording: the word, and whether the takes are of it or of something like it.
func recordForm(w http.ResponseWriter, token, word string) {
	fmt.Fprintf(w, `<form method="post" action="/setup/takes"><input type="hidden" name="token" value="%s">
	 <input type="hidden" name="do" value="record">
	 <label for="takesword">The word, as you say it</label>
	 <input id="takesword" name="word" maxlength="%d" list="takeswords" value="%s" required>`,
		html.EscapeString(token), takes.MaxWord, html.EscapeString(word))
	fmt.Fprint(w, `<datalist id="takeswords">`)
	for _, m := range models().Ours() {
		fmt.Fprintf(w, `<option value="%s">`, html.EscapeString(m.Phrase))
	}
	fmt.Fprint(w, `</datalist>
	 <div class="kinds"><label><input type="radio" name="kind" value="word" checked>Takes of the word</label>
	 <label><input type="radio" name="kind" value="not">Takes of something that sounds like it, which a model has
	  to learn is not the word</label></div>
	 <p><button type="submit">Record</button></p></form>
	 <p class="note">Say the word, wait a second, and say it again, 20 to 30 times. Mute the other devices nearby
	  first: they hear it too. It won't record while the microphones are muted, during a call, a talk through a
	  camera, an announcement or a ring, or while anything is playing, and the takes don't wake this device or
	  keep wake clips. Nothing is saved on the device: the takes stay in memory until you throw them away, record
	  again or this page closes, and they come to you over this page, on your own network without
	  encryption.</p>`)
}

// keptSection is the takes a recording was cut into: listen to them, download them, or throw them away.
func keptSection(w http.ResponseWriter, token string, st takes.State) {
	noun := "takes"
	if st.Takes == 1 {
		noun = "take"
	}
	fmt.Fprintf(w, `<p><strong>%d %s of “%s”</strong>, from %s of recording.`, st.Takes, noun,
		html.EscapeString(st.Word), minSec(st.Elapsed))
	if len(st.LeftOut) > 0 {
		counts, order := map[string]int{}, []string{}
		for _, l := range st.LeftOut {
			if counts[l.Why] == 0 {
				order = append(order, l.Why)
			}
			counts[l.Why]++
		}
		var parts []string
		for _, why := range order {
			parts = append(parts, fmt.Sprintf("%d %s", counts[why], why))
		}
		fmt.Fprintf(w, ` Left out: %s.`, html.EscapeString(strings.Join(parts, ", ")))
	}
	fmt.Fprint(w, `</p>`)
	if st.Lost {
		fmt.Fprint(w, `<p class="note"><strong>Some audio was lost while recording.</strong> Listen before using these.</p>`)
	}
	if st.Takes == 0 {
		fmt.Fprint(w, `<p>Nothing stood out from the room. Say it closer, or louder, and record again.</p>`)
	} else {
		fmt.Fprint(w, `<details><summary>Listen to them</summary><div class="body">`)
		for n := 1; n <= st.Takes; n++ {
			fmt.Fprintf(w, `<p style="margin:.3em 0">%d <audio controls preload="none" src="/setup/takes/take?n=%d"></audio></p>`, n, n)
		}
		fmt.Fprintf(w, `</div></details>
		 <p><a href="/setup/takes.zip">Download the takes</a> (.zip, about %s): a folder of 16 kHz mono WAVs, one take
		  each, for wakesmith's Real voices or for training a wake word by hand, and a takes.json of where
		  each came from.</p>`, kb(st.Bytes))
	}
	takesButtons(w, token, "discard", "Throw away")
	fmt.Fprint(w, `<p class="note">Recording again replaces these.</p>`)
	recordForm(w, token, st.Word)
}

// takesButtons is a form of the section's buttons, as pairs of what each does and what it says.
func takesButtons(w http.ResponseWriter, token string, pairs ...string) {
	fmt.Fprintf(w, `<form method="post" action="/setup/takes"><input type="hidden" name="token" value="%s"><p>`,
		html.EscapeString(token))
	for i := 0; i+1 < len(pairs); i += 2 {
		class := ""
		if i > 0 {
			class = ` class="quiet"`
		}
		fmt.Fprintf(w, `<button type="submit" name="do" value="%s"%s>%s</button> `, pairs[i], class, html.EscapeString(pairs[i+1]))
	}
	fmt.Fprint(w, `</p></form>`)
}

// takesPoll keeps the line up to date while a recording waits or runs, and reloads the section when it
// has moved on from shown, the phase the page was drawn in. Without script, Check again does the same.
func takesPoll(w http.ResponseWriter, shown takes.Phase) {
	fmt.Fprintf(w, `<p class="note"><a href="/setup?tab=sound#takes">Check again</a></p>
	 <script>(()=>{const time=document.getElementById('takestime'),about=document.getElementById('takesabout');
	 const t=s=>Math.floor(s/60)+':'+String(Math.floor(s%%60)).padStart(2,'0');const shown=%q;
	 const tick=async()=>{try{const r=await fetch('/setup/takes/state',{cache:'no-store'});if(!r.ok){location.reload();return}
	  const s=await r.json();
	  if(s.phase!==shown){location.reload();return}
	  if(time)time.textContent=t(s.seconds);if(about)about.textContent=s.about;
	 }catch(e){}setTimeout(tick,1000)};setTimeout(tick,1000)})()</script>`, shown.String())
}

// minSec is a duration as minutes and seconds.
func minSec(d time.Duration) string {
	s := int(d.Seconds())
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

// kb is a number of bytes, said the way a download link says it.
func kb(n int) string {
	if n < 1<<20 {
		return fmt.Sprintf("%d KB", (n+512)>>10)
	}
	return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
}
