package setup

import (
	"context"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/timezone"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
)

// The page itself: one document served from the binary, no framework and nothing fetched from the
// internet, so it works on a device with no route out. Forms post and the page reloads; the only
// script polls while a browser waits to be let in, and there is a button to do the same by hand.

// maxBody is the most a request may carry. Everything here is a few short fields.
const maxBody = 16 << 10

// cookieName is the session a press hands out.
const cookieName = "techo5_setup"

// Run closes the page when it has been left alone, and keeps Home Assistant's switch honest.
func (f *Feature) Run(ctx context.Context) error {
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
		if !f.On() {
			f.mu.Lock()
			stale := !f.openedAt.IsZero()
			f.mu.Unlock()
			if stale {
				f.Close() // it ran out on its own; say so and forget the sessions
			}
		}
	}
}

func (f *Feature) serve(w http.ResponseWriter, r *http.Request) {
	f.used()
	switch strings.TrimSuffix(r.URL.Path, "/") {
	case "/setup":
		f.index(w, r)
	case "/setup/wait":
		f.wait(w, r)
	case "/setup/state":
		f.state(w, r)
	case "/setup/save":
		f.save(w, r)
	default:
		http.NotFound(w, r)
	}
}

// session is the cookie this request carries, and whether it has been let in.
func (f *Feature) session(r *http.Request) (string, bool) {
	c, err := r.Cookie(cookieName)
	if err != nil {
		return "", false
	}
	return c.Value, f.letIn(c.Value)
}

func (f *Feature) index(w http.ResponseWriter, r *http.Request) {
	token, in := f.session(r)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if !in {
		f.lockedPage(w)
		return
	}
	f.settingsPage(w, token, r.URL.Query().Get("saved"), r.URL.Query().Get("problem"))
}

// wait starts this browser waiting for a press and gives it the cookie the press will let in.
func (f *Feature) wait(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "post to ask", http.StatusMethodNotAllowed)
		return
	}
	token, ok := f.await()
	if !ok {
		http.Error(w, "another browser is already waiting for a press; try again in a minute", http.StatusConflict)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: token, Path: "/setup",
		HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: int(life / time.Second),
	})
	f.Changed.Emit(struct{}{}) // the device says a browser is asking
	slog.Info("setup page: a browser is asking to be let in")
	http.Redirect(w, r, "/setup", http.StatusSeeOther)
}

// state is what the waiting page polls: whether the press has happened yet.
func (f *Feature) state(w http.ResponseWriter, r *http.Request) {
	_, in := f.session(r)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	fmt.Fprintf(w, `{"in":%t,"waiting":%t}`+"\n", in, f.Waiting())
}

// save takes the forms. Every write names the setting it changes; nothing here touches keys, runs a
// command or writes anything the page does not list.
func (f *Feature) save(w http.ResponseWriter, r *http.Request) {
	token, in := f.session(r)
	if !in {
		http.Error(w, "not let in", http.StatusForbidden)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "post to save", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "that form was too big or malformed", http.StatusBadRequest)
		return
	}
	// The form carries the session it came from, so a page on another site cannot post here with a
	// browser's cookie.
	if r.PostFormValue("token") != token {
		slog.Warn("setup page: a save arrived without the page's own token")
		http.Error(w, "that form did not come from this page", http.StatusForbidden)
		return
	}

	var problem string
	switch r.PostFormValue("what") {
	case "timezone":
		zone := strings.TrimSpace(r.PostFormValue("zone"))
		switch {
		case zone == "":
			if err := timezone.Get().Follow(); err != nil {
				problem = err.Error()
			}
		default:
			if err := timezone.Get().Choose(zone); err != nil {
				problem = "that is not a time zone this device knows: " + err.Error()
			}
		}
	default:
		problem = "nothing to save"
	}

	to := "/setup?saved=1"
	if problem != "" {
		slog.Warn("setup page: a change was refused", "problem", problem)
		to = "/setup?problem=" + urlQuery(problem)
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
}

func urlQuery(s string) string { return strings.ReplaceAll(html.EscapeString(s), " ", "+") }

const pageHead = `<!doctype html><html lang="en"><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>TECHO5 setup</title>
<style>
 body{font:16px/1.5 system-ui,sans-serif;max-width:34rem;margin:2rem auto;padding:0 1rem;background:#1a110d;color:#f2e6df}
 h1{font-size:1.4rem;margin:0 0 .2rem} p.sub{color:#b59c8f;margin:0 0 1.5rem}
 fieldset{border:1px solid #4a372e;border-radius:10px;margin:0 0 1rem;padding:1rem}
 legend{padding:0 .4rem;color:#ff7043}
 label{display:block;margin:.6rem 0 .2rem;color:#b59c8f}
 select,input{font:inherit;width:100%;padding:.5rem;border-radius:8px;border:1px solid #4a372e;background:#241813;color:inherit}
 button{font:inherit;padding:.55rem 1.1rem;border:0;border-radius:999px;background:#ff7043;color:#1a110d;font-weight:600;cursor:pointer}
 .note{color:#b59c8f;font-size:.9rem} .ok{color:#8bc34a} .bad{color:#ff8a65}
</style>`

func (f *Feature) lockedPage(w http.ResponseWriter) {
	waiting := f.Waiting()
	fmt.Fprint(w, pageHead)
	fmt.Fprintf(w, `<h1>%s</h1><p class="sub">Setup</p>`, html.EscapeString(deviceName()))
	if !waiting {
		fmt.Fprint(w, `<form method="post" action="/setup/wait"><p>To change anything here, press the
		 action button on the device. That is what proves you are standing in front of it.</p>
		 <p><button type="submit">Ask to be let in</button></p></form>`)
	} else {
		fmt.Fprint(w, `<p><strong>Press the action button on the device now.</strong></p>
		 <p class="note">Waiting for the press. The device is showing that a browser is asking.</p>
		 <p><a href="/setup">Check again</a></p>
		 <script>setInterval(async()=>{try{const r=await fetch('/setup/state');const s=await r.json();
		 if(s.in)location.href='/setup';}catch(e){}},2000)</script>`)
	}
	fmt.Fprint(w, `<p class="note">This page is on your own network, without encryption, and closes
	 itself when it is left alone.</p>`)
}

func (f *Feature) settingsPage(w http.ResponseWriter, token, saved, problem string) {
	fmt.Fprint(w, pageHead)
	fmt.Fprintf(w, `<h1>%s</h1><p class="sub">Setup</p>`, html.EscapeString(deviceName()))
	if saved != "" {
		fmt.Fprint(w, `<p class="ok">Saved.</p>`)
	}
	if problem != "" {
		fmt.Fprintf(w, `<p class="bad">%s</p>`, html.EscapeString(strings.ReplaceAll(problem, "+", " ")))
	}

	cur := timezone.Get().Current()
	fmt.Fprintf(w, `<form method="post" action="/setup/save"><fieldset><legend>Time zone</legend>
	 <input type="hidden" name="token" value="%s"><input type="hidden" name="what" value="timezone">
	 <label for="zone">Zone</label><select id="zone" name="zone">`, html.EscapeString(token))
	fmt.Fprintf(w, `<option value=""%s>Follow Home Assistant</option>`, selected(!timezone.Get().SetHere()))
	for _, region := range timezone.Regions() {
		fmt.Fprintf(w, `<optgroup label="%s">`, html.EscapeString(region))
		for _, z := range timezone.Zones(region) {
			full := region + "/" + z
			fmt.Fprintf(w, `<option value="%s"%s>%s</option>`,
				html.EscapeString(full), selected(full == cur && timezone.Get().SetHere()), html.EscapeString(full))
		}
		fmt.Fprint(w, `</optgroup>`)
	}
	fmt.Fprint(w, `</select><p class="note">The device's clock keeps time on its own; this is only
	 which zone it shows.</p><p><button type="submit">Save</button></p></fieldset></form>`)

	fmt.Fprint(w, `<p class="note">Radio stations and the phone account belong here too and are still
	 to come. This page never touches SSH keys, the Home Assistant key, or the software the device
	 runs.</p>`)
}

func selected(on bool) string {
	if on {
		return " selected"
	}
	return ""
}

func deviceName() string {
	if n := config.Get().Device.Name; n != "" {
		return n
	}
	return layout.DefaultName
}
