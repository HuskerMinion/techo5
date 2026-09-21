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
	"github.com/HuskerMinion/techo5/echod/internal/lib/wifi"
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
	f.settingsPage(w, token, r.URL.Query().Get("saved"), r.URL.Query().Get("renamed"),
		r.URL.Query().Get("problem"), r.URL.Query().Get("scan") != "")
}

// wait starts this browser waiting for a press and gives it the cookie the press will let in.
func (f *Feature) wait(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "post to ask", http.StatusMethodNotAllowed)
		return
	}
	token, ok := f.await()
	if !ok {
		if f.ShutOut() {
			http.Error(w, "too many tries without a press on the device; try again in a few minutes",
				http.StatusTooManyRequests)
			return
		}
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
	var renamed bool
	switch r.PostFormValue("what") {
	case "wifi":
		ssid := strings.TrimSpace(r.PostFormValue("ssid"))
		if other := strings.TrimSpace(r.PostFormValue("other")); other != "" {
			ssid = other
		}
		problem = joinWifi(r.Context(), ssid, r.PostFormValue("passphrase"))
	case "forget":
		ssid := strings.TrimSpace(r.PostFormValue("ssid"))
		if err := wifi.Forget(r.Context(), ssid); err != nil {
			problem = "could not forget " + ssid + ": " + err.Error()
		} else {
			slog.Info("setup page: a network was forgotten", "ssid", ssid)
		}
	case "name":
		problem = rename(r.PostFormValue("name"), r.PostFormValue("understood") == "yes")
		if problem == "" {
			renamed = true
		}
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
	if renamed {
		to = "/setup?renamed=1"
	}
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

func (f *Feature) settingsPage(w http.ResponseWriter, token, saved, renamed, problem string, scan bool) {
	fmt.Fprint(w, pageHead)
	fmt.Fprintf(w, `<h1>%s</h1><p class="sub">Setup</p>`, html.EscapeString(deviceName()))
	if saved != "" {
		fmt.Fprint(w, `<p class="ok">Saved.</p>`)
	}
	if renamed != "" {
		fmt.Fprint(w, `<p class="ok">Renamed. The device is restarting and will be back in a minute or
		 so. Home Assistant keeps it as the same device, under its old entity ids.</p>`)
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

	f.wifiSection(w, token, scan)
	nameSection(w, token)

	fmt.Fprint(w, `<p class="note">Radio stations and the phone account belong here too and are still
	 to come. This page never touches SSH keys, the Home Assistant key, or the software the device
	 runs.</p>`)
}

// wifiSection is the networks: what the device is on, what it remembers, and how to add another.
// Adding one does not drop the network it is on, so a device can be given the network it is going to
// while it is still on the one it is at.
func (f *Feature) wifiSection(w http.ResponseWriter, token string, scan bool) {
	if !wifi.Available() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	st := wifi.Current(ctx)

	fmt.Fprint(w, `<fieldset><legend>Wi-Fi</legend>`)
	switch {
	case st.Connected:
		fmt.Fprintf(w, `<p>On <strong>%s</strong>, at %s.</p>`, html.EscapeString(st.SSID), html.EscapeString(st.Address))
	default:
		fmt.Fprintf(w, `<p class="bad">Not on a network (%s).</p>`, html.EscapeString(st.State))
	}

	if saved := wifi.Saved(); len(saved) > 0 {
		fmt.Fprint(w, `<p class="note">Remembered, in the order it tries them:</p><ul>`)
		for _, ssid := range saved {
			fmt.Fprintf(w, `<li>%s <form method="post" action="/setup/save" style="display:inline">
			 <input type="hidden" name="token" value="%s"><input type="hidden" name="what" value="forget">
			 <input type="hidden" name="ssid" value="%s">
			 <button type="submit">Forget</button></form></li>`,
				html.EscapeString(ssid), html.EscapeString(token), html.EscapeString(ssid))
		}
		fmt.Fprint(w, `</ul>`)
	}

	fmt.Fprintf(w, `<form method="post" action="/setup/save">
	 <input type="hidden" name="token" value="%s"><input type="hidden" name="what" value="wifi">
	 <label for="ssid">Network</label><select id="ssid" name="ssid">`, html.EscapeString(token))
	if scan {
		nets, err := wifi.Scan(ctx)
		if err != nil {
			fmt.Fprint(w, `<option value="">(the scan failed)</option>`)
		}
		for _, n := range nets {
			fmt.Fprintf(w, `<option value="%s">%s%s</option>`,
				html.EscapeString(n.SSID), html.EscapeString(n.SSID), lock(n.Secured))
		}
	} else {
		fmt.Fprint(w, `<option value="">(not scanned yet)</option>`)
	}
	fmt.Fprint(w, `</select>`)
	if !scan {
		fmt.Fprint(w, `<p><a href="/setup?scan=1">Scan for networks</a> — it takes a few seconds.</p>`)
	}
	fmt.Fprint(w, `<label for="other">…or a name it did not find</label>
	 <input id="other" name="other" autocomplete="off" placeholder="Network name">
	 <label for="passphrase">Passphrase</label>
	 <input id="passphrase" name="passphrase" type="password" autocomplete="new-password">
	 <p class="note">The networks it already remembers are kept. If the one you add is somewhere else,
	  nothing changes here until the device is taken there. If it is a network in range, the device
	  moves to it — and this page goes with it, so you will have to find it again at its new address.</p>
	 <p><button type="submit">Add network</button></p></form></fieldset>`)
}

// nameSection renames the device, behind what it does and the box that has to be ticked.
//
// What it does was worth checking rather than assuming: Home Assistant keys its entities on the
// device's MAC address (unique ids read "a8:e6:21:77:3f:ae/0/media_player/Speaker"), so a renamed
// device is the same device to it and keeps the entity ids it was given. Automations go on working.
// What changes is what Home Assistant shows, and the entity ids then no longer look like the name —
// which is confusing enough to warn about, and reason to rename in Home Assistant as well.
func nameSection(w http.ResponseWriter, token string) {
	name := deviceName()
	fmt.Fprintf(w, `<fieldset><legend>Name</legend>
	 <form method="post" action="/setup/save">
	 <input type="hidden" name="token" value="%s"><input type="hidden" name="what" value="name">
	 <label for="name">This device is called</label>
	 <input id="name" name="name" value="%s" maxlength="31" autocomplete="off">
	 <p class="bad"><strong>Home Assistant keeps the entity ids it already gave this device.</strong>
	  It knows the device by its address, not its name, so <code>%s</code> stays as it is and your
	  automations keep working — but it will no longer look like the new name, and Home Assistant will
	  go on showing the old one in places until you rename the device there too.</p>
	 <p class="note">On a device that does not use Home Assistant, none of that applies: the name is
	  only what the screen says. Naming a device before you hand it to somebody is what this is for.</p>
	 <p><label><input type="checkbox" name="understood" value="yes" style="width:auto">
	  I understand the entity ids in Home Assistant do not change with it</label></p>
	 <p class="note">The device restarts to announce the new name, and this page goes with it.</p>
	 <p><button type="submit">Rename and restart</button></p></form></fieldset>`,
		html.EscapeString(token), html.EscapeString(name),
		html.EscapeString("media_player."+layout.EntitySlug(name)+"_speaker"))
}

func lock(secured bool) string {
	if secured {
		return " 🔒"
	}
	return ""
}

// joinWifi adds a network and reports what went wrong, if anything. The library puts the old
// configuration back when the new network never comes up, so a wrong passphrase does not strand it.
func joinWifi(ctx context.Context, ssid, passphrase string) string {
	if ssid == "" {
		return "no network was named"
	}
	wifi.SettingUp(true)
	defer wifi.SettingUp(false)
	ctx, cancel := context.WithTimeout(ctx, 70*time.Second)
	defer cancel()
	if err := wifi.Join(ctx, ssid, passphrase); err != nil {
		return err.Error()
	}
	slog.Info("setup page: a network was added", "ssid", ssid)
	return ""
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
