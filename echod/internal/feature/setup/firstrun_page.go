package setup

import (
	"encoding/base64"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"rsc.io/qr"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/firstrun"
	"github.com/HuskerMinion/techo5/echod/internal/feature/timezone"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// First run on the setup page (feature/firstrun): the same steps as a Show's screen, as forms, one
// step a page. It is how a device with no screen is set up (a Dot, an Echo 2nd gen), and any device
// opens on it while first run is still to do. Getting in is the page's own rule: a press on the device
// lets the browser in, or the one-time code a phone is handed over Bluetooth (firstrun_code.go).

const firstRunPath = "/setup/firstrun"

func init() { routes[firstRunPath] = (*Feature).firstRunPage }

func (f *Feature) firstRunPage(w http.ResponseWriter, r *http.Request) {
	token, in := f.session(r)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if !in {
		f.lockedPage(w)
		return
	}
	if r.Method == http.MethodPost {
		f.firstRunSave(w, r, token)
		return
	}
	if !firstrun.Pending() {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}
	firstRunForm(w, token, r.URL.Query().Get("problem"), r.URL.Query().Get("key") != "")
}

// firstRunSave takes a step's form, and sends the browser back to the step it is on now.
func (f *Feature) firstRunSave(w http.ResponseWriter, r *http.Request, token string) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "that form was too big or malformed", http.StatusBadRequest)
		return
	}
	if !sameToken(r.PostFormValue("token"), token) {
		slog.Warn("setup page: a first-run form arrived without the page's own token")
		http.Error(w, "that form did not come from this page", http.StatusForbidden)
		return
	}
	step := firstrun.Step()
	problem, key := "", false
	switch r.PostFormValue("what") {
	case "next":
		firstrun.Go(firstrun.After(step))
	case "finish":
		firstrun.Finish()
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	case "wifi":
		ssid := strings.TrimSpace(r.PostFormValue("ssid"))
		problem = joinWifiFrom(r, ssid)
	case "zone":
		zone := strings.TrimSpace(r.PostFormValue("zone"))
		var err error
		if zone == "" {
			err = timezone.Get().Follow()
		} else {
			err = timezone.Get().Choose(zone)
		}
		if err != nil {
			problem = "that time zone did not take: " + err.Error()
		}
	case "name":
		if to := strings.TrimSpace(r.PostFormValue("name")); to != deviceName() {
			problem = firstrun.Rename(to)
		}
	case "room":
		room := strings.TrimSpace(r.PostFormValue("room"))
		if room == "Other" {
			room = strings.TrimSpace(r.PostFormValue("other"))
		}
		switch {
		case room == "":
			problem = "choose a room, or type one under Other"
		case len([]rune(room)) > firstrun.NameLimit:
			problem = "a room is at most 31 characters"
		default:
			if err := firstrun.SetArea(room); err != nil {
				problem = "could not save the room: " + err.Error()
			}
		}
	case "adopt":
		if _, err := firstrun.OpenAdoption(); err != nil {
			problem = "could not open the device to Home Assistant: " + err.Error()
		}
	case "key":
		key = true
	case "auto":
		firstrun.SetAutoInstall(r.PostFormValue("auto") == "yes")
	case "chime":
		go firstrun.Chime()
	}
	q := url.Values{}
	if problem != "" {
		q.Set("problem", problem)
	}
	if key {
		q.Set("key", "1")
	}
	to := firstRunPath
	if len(q) > 0 {
		to += "?" + q.Encode()
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// firstRunForm is the page for the step first run is on.
func firstRunForm(w http.ResponseWriter, token, problem string, showKey bool) {
	step := firstrun.Step()
	head(w)
	fmt.Fprintf(w, `<div class="wrap"><h1>%s</h1>`, html.EscapeString(deviceName()))
	if n, of := firstrun.Number(step); n > 0 {
		fmt.Fprintf(w, `<p class="sub">Setting up · step %d of %d</p>`, n, of)
	} else {
		fmt.Fprint(w, `<p class="sub">Setting up</p>`)
	}
	defer fmt.Fprint(w, `<p class="note"><a href="/setup?all=1">All settings</a></p></div>`)
	if problem != "" {
		fmt.Fprintf(w, `<div class="banner bad">%s</div>`, html.EscapeString(problem))
	}
	form := func(what, label string, primary bool, fields string) {
		class := ""
		if !primary {
			class = ` class="quiet"`
		}
		fmt.Fprintf(w, `<form method="post" action="%s" style="display:inline-block;margin-right:.6rem">
		 <input type="hidden" name="token" value="%s"><input type="hidden" name="what" value="%s">%s
		 <button type="submit"%s>%s</button></form>`, firstRunPath, html.EscapeString(token), what, fields, class, html.EscapeString(label))
	}
	next := func(label string) { form("next", label, true, "") }

	switch step {
	case firstrun.Hello:
		fmt.Fprintf(w, `<h2>Hello</h2><p>This is %s. A few steps set it up: its Wi-Fi, the time zone, its
		 name and room, Home Assistant, updates, and a sound to check the speaker.</p>`, html.EscapeString(deviceName()))
		next("Begin")
	case firstrun.Wifi:
		st := firstrun.WifiStatus()
		fmt.Fprint(w, `<h2>Wi-Fi</h2>`)
		if st.Connected {
			fmt.Fprintf(w, `<p>On <strong>%s</strong>, at %s.</p>`, html.EscapeString(st.SSID), html.EscapeString(st.Address))
		} else {
			fmt.Fprintf(w, `<p class="bad">Not on a network (%s).</p>`, html.EscapeString(st.State))
		}
		fmt.Fprint(w, `<fieldset><legend>Another network</legend><p class="note">The ones it remembers are
		 kept. If it moves to the network you add, this page goes with it, at the device's new address.</p>`)
		form("wifi", "Add network", false, `<label for="ssid">Network name</label>
		 <input id="ssid" name="ssid" autocomplete="off">
		 <label for="passphrase">Passphrase</label><input id="passphrase" name="passphrase" type="password" autocomplete="new-password">`+
			loginFieldsHTML()+`<br>`)
		macNote(w)
		fmt.Fprint(w, `</fieldset><p>`)
		next("Next")
		fmt.Fprint(w, `</p>`)
	case firstrun.Zone:
		fmt.Fprintf(w, `<h2>Time zone</h2><p>The clock says <strong>%s</strong>. Change the zone if that is not the time here.</p>`,
			html.EscapeString(clockNow()))
		var b strings.Builder
		zoneSelectTo(&b)
		form("zone", "Save", false, b.String()+"<br>")
		next("Next")
	case firstrun.Name:
		fmt.Fprint(w, `<h2>Name and room</h2>`)
		form("name", "Rename", false, fmt.Sprintf(`<label for="name">Name</label>
		 <input id="name" name="name" value="%s" maxlength="31" autocomplete="off">
		 <p class="note">Renaming restarts the device's software to announce the name, and this page comes back on this step.</p>`,
			html.EscapeString(deviceName())))
		if firstrun.HomeConnected() {
			fmt.Fprint(w, `<p>Its room is set in Home Assistant.</p>`)
		} else {
			room := deviceRoom()
			var b strings.Builder
			b.WriteString(`<label for="room">Room</label><select id="room" name="room">`)
			for _, k := range firstrun.Rooms {
				on := k == room || (k == "Other" && room != "" && !isRoom(room))
				fmt.Fprintf(&b, `<option%s>%s</option>`, selected(on), html.EscapeString(k))
			}
			other := ""
			if room != "" && !isRoom(room) {
				other = room
			}
			fmt.Fprintf(&b, `</select><label for="other">Other</label><input id="other" name="other" value="%s" maxlength="31" autocomplete="off">
			 <p class="note">Home Assistant puts the device in this room when it adds it.</p>`, html.EscapeString(other))
			form("room", "Save room", false, b.String())
		}
		fmt.Fprint(w, `<p>`)
		next("Next")
		fmt.Fprint(w, `</p>`)
	case firstrun.Home:
		firstRunHome(w, form, next, showKey)
	case firstrun.Updates:
		firstrun.ArriveAtUpdates(nil)
		fmt.Fprint(w, `<h2>Updates</h2>`)
		v := firstrun.Offered()
		switch {
		case !firstrun.UpdatesChecked():
			fmt.Fprint(w, `<p>Looking for an update…</p><script>setTimeout(()=>location.reload(),3000)</script>`)
		case v == "":
			fmt.Fprint(w, `<p>It is up to date.</p>`)
		case firstrun.AutoInstall():
			fmt.Fprintf(w, `<p>TECHO5 <strong>%s</strong> is ready. It installs tonight, after setup.</p>`, html.EscapeString(v))
		default:
			fmt.Fprintf(w, `<p>TECHO5 <strong>%s</strong> is ready, to install from the General tab.</p>`, html.EscapeString(v))
		}
		checked := ""
		if firstrun.AutoInstall() {
			checked = " checked"
		}
		form("auto", "Save", false, fmt.Sprintf(`<p><label><input type="checkbox" name="auto" value="yes"%s style="width:auto">
		 Install updates automatically, overnight between 2 and 5 in the morning while nothing is playing</label></p>
		 <p class="note">Turned on for you here: whoever has this device next is unlikely to watch for releases.
		  Nothing installs until setup is finished.</p>`, checked))
		next("Next")
	case firstrun.Sound:
		fmt.Fprint(w, `<h2>Sound check</h2><p>Play a short sound on the device. Did you hear it?</p>`)
		form("chime", "Play the sound", false, "")
		next("I heard it")
	case firstrun.Done:
		fmt.Fprintf(w, `<h2>All set</h2><p>%s is set up. Every setting is on this page from now on, behind
		 the same press on the device.</p>`, html.EscapeString(deviceName()))
		form("finish", "Finish", true, "")
	}
}

// firstRunHome is the Home Assistant step: added with nothing typed (the setup page's window, api's
// adopt.go), or its key typed, with a QR code of it to scan; or the device on its own.
func firstRunHome(w http.ResponseWriter, form func(what, label string, primary bool, fields string), next func(string), showKey bool) {
	fmt.Fprint(w, `<h2>Home Assistant</h2>`)
	st := firstrun.WifiStatus()
	switch {
	case firstrun.HomeConnected():
		fmt.Fprint(w, `<p><strong>Home Assistant has added it.</strong></p>`)
		next("Next")
		return
	case st.Address == "":
		fmt.Fprint(w, `<p>Not on a network yet, so Home Assistant cannot reach it.</p>`)
		form("next", "Use it on its own", true, "")
		return
	}
	// The page looks again by itself, so it says so once Home Assistant has added the device.
	fmt.Fprint(w, `<script>setTimeout(()=>location.reload(),5000)</script>`)
	if until := firstrun.AdoptUntil(); !until.IsZero() {
		left := max(1, int(time.Until(until).Minutes()+0.999))
		fmt.Fprintf(w, `<div class="banner ok"><strong>Add it in Home Assistant now:</strong> it appears under
		 <strong>Settings → Devices &amp; services</strong>, under <strong>Discovered</strong>. Choose
		 <strong>Add</strong>; there is nothing to type. Open for %d more minutes.</div>
		 <p class="note">This page says so once Home Assistant has added it. Home Assistant sends the device
		  its new key over your network unencrypted, so do this only on a network you trust. Needs Home
		  Assistant 2026.9 or later.</p>`, left)
		form("next", "Skip for now", false, "")
		return
	}
	if firstrun.Key() == "" {
		// No key at all (a gift install, a reset): any Home Assistant can add it, for as long as that lasts.
		fmt.Fprint(w, `<div class="banner ok"><strong>Add it in Home Assistant now:</strong> it appears under
		 <strong>Settings → Devices &amp; services</strong>, under <strong>Discovered</strong>. Choose
		 <strong>Add</strong>; there is nothing to type.</div>
		 <p class="note">This page says so once Home Assistant has added it. Without Home Assistant, alarms,
		  timers, the radio and the clock still work. Home Assistant sends the device its key over your
		  network unencrypted, so add it on a network you trust. Needs Home Assistant 2026.9 or later.</p>`)
		form("next", "Skip for now", false, "")
		return
	}
	fmt.Fprint(w, `<p>Add it to Home Assistant with nothing to type, or type its key. Or use it on its own:
	 alarms, timers, the radio and the clock work without Home Assistant.</p>`)
	form("adopt", "Add it, nothing to type", true, "")
	form("key", "Type the key", false, "")
	form("next", "Use it on its own", false, "")
	if key := firstrun.Key(); showKey && key != "" {
		fmt.Fprintf(w, `<fieldset><legend>The key</legend><p>In Home Assistant, add <strong>ESPHome</strong>
		 with <strong>%s</strong>, port <strong>6053</strong>, and this key:</p><p><code id="frkey" style="overflow-wrap:anywhere;word-break:break-all">%s</code></p>
		 <p><button type="button" class="quiet" onclick="frCopy(this)">Copy the key</button></p>
		 <script>
		 // The page is plain HTTP, where a browser may refuse the clipboard: then the key is selected to copy.
		 function frCopy(b){const k=document.getElementById('frkey');
		  const pick=()=>{const r=document.createRange();r.selectNodeContents(k);const s=getSelection();s.removeAllRanges();s.addRange(r);b.textContent='Selected: copy it'};
		  if(navigator.clipboard&&window.isSecureContext){navigator.clipboard.writeText(k.textContent).then(()=>{b.textContent='Copied'},pick)}else{pick()}}
		 </script>`,
			html.EscapeString(st.Address), html.EscapeString(key))
		if c, err := qr.Encode(key, qr.M); err == nil {
			c.Scale = 6
			fmt.Fprintf(w, `<p><img alt="The key as a QR code" width="%d" height="%d" src="data:image/png;base64,%s"></p>
			 <p class="note">A phone's camera reads the key from it, to paste into Home Assistant.</p>`,
				(c.Size+8)*6, (c.Size+8)*6, base64.StdEncoding.EncodeToString(c.PNG()))
		}
		fmt.Fprint(w, `</fieldset>`)
	}
}

// zoneSelectTo is zoneSelect, into a string for a form's fields.
func zoneSelectTo(b *strings.Builder) { zoneSelect(b) }

// clockNow is the time the device's clock shows.
func clockNow() string { return time.Now().Format("3:04 PM") }

// deviceRoom is the room first run chose.
func deviceRoom() string { return config.Get().Home.Area }

// isRoom is whether room is one of first run's listed rooms rather than one typed under Other.
func isRoom(room string) bool {
	for _, k := range firstrun.Rooms[:len(firstrun.Rooms)-1] {
		if k == room {
			return true
		}
	}
	return false
}

// resetSection clears the device for somebody else and starts first run (firstrun.Reset).
func resetSection(w http.ResponseWriter, token string) {
	fmt.Fprint(w, `<fieldset><legend>Reset for someone else</legend><form method="post" action="/setup/save">`)
	hidden(w, token, "giftreset", "general")
	fmt.Fprint(w, `<p style="margin:0">Clears this device for whoever has it next, and starts first run:
	 the Wi-Fi networks, the name and room, Home Assistant's key and access, alarms, timers, the phone book,
	 photos, sounds and every setting. The list is in the setup guide.</p>
	 <p class="bad"><strong>It leaves the network and Home Assistant at once, and cannot be undone.</strong></p>`)
	if hass.Get().Ready() {
		fmt.Fprint(w, `<p class="note">It deletes its own entry from your Home Assistant first, if the token it
		 was given is an administrator's. If it's still there afterward, delete it under Settings, Devices &amp;
		 services, ESPHome.</p>`)
	} else {
		fmt.Fprint(w, `<p class="note">Afterward, delete it from your Home Assistant under Settings, Devices &amp;
		 services, ESPHome.</p>`)
	}
	fmt.Fprint(w, `
	 <p><label><input type="checkbox" name="understood" value="yes" style="width:auto">
	  I know everything on the device is cleared</label></p>
	 <p><button type="submit">Reset and restart</button></p></form></fieldset>`)
}
