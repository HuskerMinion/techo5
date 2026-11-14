package setup

import (
	"fmt"
	"html"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/web"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
	"github.com/HuskerMinion/techo5/echod/internal/update"
)

// The settings as a file, from the General tab: saved, and given back to this device after a reinstall,
// or to another to start it off the same way (config.Export, config.Restored). Restoring asks first,
// with what it would change, and then restarts the daemon, since everything reads its settings as it
// starts.

const (
	// maxSettings is far more than any device's settings.
	maxSettings = 512 << 10

	// restoreFor is how long a file looked at waits to be applied.
	restoreFor = 10 * time.Minute
)

// waiting is a settings file that has been looked at and not yet applied: one, for the browser that
// sent it.
//
// What is held is the file itself, not what it made of the settings then: it is applied to the settings
// as they are when somebody says yes (config.Writer.Restore), so nothing changed in between is undone.
var waiting struct {
	sync.Mutex
	token string
	file  []byte
	at    time.Time
}

// forgetWaiting drops the file held, applied or not.
func forgetWaiting() {
	waiting.Lock()
	waiting.token, waiting.file, waiting.at = "", nil, time.Time{}
	waiting.Unlock()
}

// settingsFileSection offers the file and takes one back.
func settingsFileSection(w http.ResponseWriter, token string) {
	fieldsetOpen(w, "settingsfile", "Settings")
	fmt.Fprint(w, `<p><a href="/setup/settings.json">Download settings</a> — everything this device is set to, as a file.</p>
	 <p class="note">Keys, passwords, the settings lock's PIN, the house word, calendars' private addresses
	  and cameras' addresses are left out of it, and so are this device's privacy switches, what it lets in
	  over the network (Sendspin, AirPlay, Spotify Connect, DLNA, video, the Bluetooth proxy), what its
	  microphones and camera may do (drop-in, sending the room to Home Assistant, keeping clips, presence,
	  no wake word), its paired speaker and what it is in the middle of. It still says where the device is and holds addresses on
	  your network: keep it, don't post it.</p>`)
	fmt.Fprintf(w, `<form method="post" action="/setup/restore" enctype="multipart/form-data">
	 <input type="hidden" name="token" value="%s">
	 <label for="settingsfile">Restore settings from a file</label>
	 <input id="settingsfile" name="file" type="file" accept=".json,application/json" required>
	 <p class="note">You are shown what would change before anything does. Restoring keeps this device's
	  own keys, passwords and privacy switches, then restarts it.</p>
	 <div class="btns"><button type="submit">Look at this file</button></div></form></fieldset>`,
		html.EscapeString(token))
}

// settingsFile hands over the settings as a file.
func (f *Feature) settingsFile(w http.ResponseWriter, r *http.Request) {
	if _, in := f.session(r); !in {
		notLetIn(w)
		return
	}
	b, err := config.Export(config.Get(), layout.Model, layout.Version)
	if err != nil {
		http.Error(w, "the settings could not be written: "+err.Error(), http.StatusInternalServerError)
		return
	}
	slog.Info("setup page: settings downloaded")
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", `attachment; filename="techo5-settings.json"`)
	_, _ = w.Write(b)
}

// restore looks at a settings file and asks, or applies the one looked at once asked.
func (f *Feature) restore(w http.ResponseWriter, r *http.Request) {
	token, in := f.session(r)
	if !in {
		notLetIn(w)
		return
	}
	if r.Method != http.MethodPost {
		web.Refuse(w, "post a settings file", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxSettings+64<<10)
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
		if err := r.ParseMultipartForm(maxSettings); err != nil {
			http.Error(w, "that file was too big", http.StatusBadRequest)
			return
		}
	} else if err := r.ParseForm(); err != nil {
		http.Error(w, "that form was too big or malformed", http.StatusBadRequest)
		return
	}
	// The form carries the session it came from, as every save does, so another site's page cannot
	// restore settings here with a browser's cookie.
	if !sameToken(r.FormValue("token"), token) {
		http.Error(w, "that did not come from this page", http.StatusForbidden)
		return
	}
	f.used()

	if r.FormValue("apply") == "yes" {
		f.applyRestore(w, r, token)
		return
	}

	file, _, err := r.FormFile("file")
	if err != nil {
		http.Redirect(w, r, back("settingsfile", "problem", "choose a settings file first"), http.StatusSeeOther)
		return
	}
	defer file.Close()
	b, err := io.ReadAll(io.LimitReader(file, maxSettings))
	if err != nil {
		http.Redirect(w, r, back("settingsfile", "problem", "the file did not arrive whole"), http.StatusSeeOther)
		return
	}
	rs, err := config.Restored(config.Get(), b, layout.Model)
	if err != nil {
		http.Redirect(w, r, back("settingsfile", "problem", err.Error()), http.StatusSeeOther)
		return
	}
	if len(rs.Changed) == 0 {
		http.Redirect(w, r, back("settingsfile", "problem", "that file is this device's settings already; nothing to change"), http.StatusSeeOther)
		return
	}
	waiting.Lock()
	waiting.token, waiting.file, waiting.at = token, b, time.Now()
	waiting.Unlock()
	slog.Info("setup page: a settings file looked at", "changes", rs.Changed, "reenter", rs.Reenter, "from_model", rs.FromModel)
	restorePage(w, token, rs)
}

// restorePage asks before a restore.
func restorePage(w http.ResponseWriter, token string, rs config.Restoration) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	head(w)
	fmt.Fprintf(w, `<div class="wrap"><h1>%s</h1><p class="sub">Restore settings</p>
	 <fieldset><legend>This file would change</legend><ul>`, html.EscapeString(deviceName()))
	for _, part := range rs.Changed {
		fmt.Fprintf(w, `<li>%s</li>`, html.EscapeString(partName(part)))
	}
	fmt.Fprint(w, `</ul>`)
	if rs.FromModel != "" {
		fmt.Fprintf(w, `<p class="note">The file is from a different model, %s. What this one does not have is
		 left as it is.</p>`, html.EscapeString(rs.FromModel))
	}
	if len(rs.Servers) > 0 {
		fmt.Fprint(w, `<p>It points these somewhere else, which is where what they handle will go:</p><ul>`)
		for _, sc := range rs.Servers {
			fmt.Fprintf(w, `<li>%s: %s → %s</li>`, html.EscapeString(sc.What), html.EscapeString(orNone(sc.Old)), html.EscapeString(orNone(sc.New)))
		}
		fmt.Fprint(w, `</ul>`)
	}
	if len(rs.Reenter) > 0 {
		fmt.Fprint(w, `<p>These will need entering again on this device: keys for a server the file changes,
		 which are not sent there, and calendar links and computers only the device the file came from has:</p><ul>`)
		for _, what := range rs.Reenter {
			fmt.Fprintf(w, `<li>%s</li>`, html.EscapeString(what))
		}
		fmt.Fprint(w, `</ul>`)
	}
	fmt.Fprintf(w, `
	 <p class="note">Everything in those parts is replaced with what the file says. This device keeps its own
	  keys, passwords, house word, privacy switches and paired speaker, and restarts to use the rest: back
	  in about a minute.</p>
	 <form method="post" action="/setup/restore"><input type="hidden" name="token" value="%s">
	  <input type="hidden" name="apply" value="yes">
	  <div class="btns"><button type="submit">Restore and restart</button>
	  <a href="/setup?tab=general">Cancel</a></div></form></fieldset></div>`, html.EscapeString(token))
}

// applyRestore applies the file this browser looked at, and restarts.
func (f *Feature) applyRestore(w http.ResponseWriter, r *http.Request, token string) {
	waiting.Lock()
	ok := sameToken(waiting.token, token) && time.Since(waiting.at) < restoreFor
	file := waiting.file
	waiting.Unlock()
	forgetWaiting()
	if !ok {
		http.Redirect(w, r, back("settingsfile", "problem", "that file was not looked at here, or it was too long ago; send it again"), http.StatusSeeOther)
		return
	}
	rs, err := config.Set().Restore(file, layout.Model)
	if err != nil {
		http.Redirect(w, r, back("settingsfile", "problem", "the settings could not be restored: "+err.Error()), http.StatusSeeOther)
		return
	}
	slog.Warn("settings restored from a file on the setup page; restarting to use them", "changes", rs.Changed, "reenter", rs.Reenter)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	head(w)
	fmt.Fprintf(w, `<div class="wrap"><h1>%s</h1><p class="sub">Restore settings</p>
	 <div class="banner ok">Restored. The device is restarting to use them and will be back in about a minute.</div>
	 <p><a href="/setup?tab=general">Back to the setup page</a></p></div>`, html.EscapeString(deviceName()))

	restartAfterRestore()
}

// restartAfterRestore restarts the daemon once the page has answered: the daemon, not the device, as a
// rename does. A variable for the tests.
var restartAfterRestore = func() {
	safe.Go("restart after restoring settings", func() {
		time.Sleep(3 * time.Second)
		update.Restart("settings restored")
	})
}

// orNone is an address, or a word for none.
func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

// partName is a part of the settings as people know it.
func partName(part string) string {
	if name, ok := partNames[part]; ok {
		return name
	}
	return strings.ReplaceAll(part, "_", " ")
}

var partNames = map[string]string{
	"alarms":          "Alarms and their settings",
	"brain":           "The device's own assistant",
	"calendar":        "Calendars",
	"dashboard":       "The dashboard",
	"deck":            "TECHO5 Deck",
	"doorbell":        "The doorbell",
	"home":            "Home screen, weather, radio and photos",
	"media":           "Music and radio",
	"microphone":      "The microphones",
	"music_assistant": "Music Assistant",
	"presence":        "Presence and gestures",
	"ring":            "The light ring",
	"screen":          "The screen",
	"sendspin":        "Sendspin",
	"speaker":         "Sound",
	"talk_back":       "Talking through cameras",
	"update":          "Updates",
	"video":           "Video",
	"wake":            "Wake words",
}
