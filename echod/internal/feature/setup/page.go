package setup

import (
	"context"
	"crypto/subtle"
	"fmt"
	"html"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/announce"
	"github.com/HuskerMinion/techo5/echod/internal/feature/api"
	"github.com/HuskerMinion/techo5/echod/internal/feature/diag"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
	"github.com/HuskerMinion/techo5/echod/internal/feature/timezone"
	"github.com/HuskerMinion/techo5/echod/internal/feature/web"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
	"github.com/HuskerMinion/techo5/echod/internal/lib/wifi"
)

// The page itself: one document served from the binary, no framework and nothing fetched from the
// internet, so it works on a device with no route out. Forms post and the page reloads; the only
// script polls while a browser waits to be let in, and there is a button to do the same by hand.

// maxBody is the most a request may carry. Everything here is a few short fields.
const maxBody = 16 << 10

// maxDeckBody is a deck page's form: up to 24 buttons of several boxes each, some long.
const maxDeckBody = 128 << 10

// cookieName is the session a press hands out.
const cookieName = "techo5_setup"

// sweptUploads is the sweep of what sound uploads a power cut stopped left behind, made once a process.
// The supervisor runs Run again after it fails, and by then the page may be open with an upload half
// written under its temporary name: a sweep at every Run would delete it from under its writer.
var sweptUploads sync.Once

// Run closes the page when it has been left alone, and keeps Home Assistant's switch honest.
func (f *Feature) Run(ctx context.Context) error {
	// The first thing at a start, before the page can be opened to send one: what an upload a power cut
	// stopped left behind. The wake word library sweeps its own as it is made.
	sweptUploads.Do(speaker.SweepUploads)

	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
		f.showAsking() // a browser that gave up asking is not still asking in Home Assistant
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
	// Only a browser that has been let in keeps the page open. Anything else on the network can ask
	// for this path as often as it likes, and the idle time is there to close the page when the
	// person who opened it has walked away — a scanner or a stale tab must not hold it open for them.
	// A view polling on its own is not somebody there either (watching).
	if _, in := f.session(r); in && !watching[strings.TrimSuffix(r.URL.Path, "/")] {
		f.used()
	}
	switch strings.TrimSuffix(r.URL.Path, "/") {
	case "/setup":
		f.index(w, r)
	case "/setup/wait":
		f.wait(w, r)
	case "/setup/state":
		f.state(w, r)
	case "/setup/save":
		f.save(w, r)
	case "/setup/photo":
		f.photo(w, r)
	case "/setup/font":
		f.font(w, r)
	case "/setup/sound":
		f.sound(w, r)
	case "/setup/wakeword":
		f.wakeWordUpload(w, r)
	case "/setup/diagnostics.txt":
		f.diagnostics(w, r)
	case "/setup/settings.json":
		f.settingsFile(w, r)
	case "/setup/restore":
		f.restore(w, r)
	default:
		if route, ok := routes[strings.TrimSuffix(r.URL.Path, "/")]; ok {
			route(f, w, r)
			return
		}
		http.NotFound(w, r)
	}
}

// watching is the paths a view left up on the page asks for by itself. Answering them is not the page
// being used: a tab left watching the scores would otherwise hold the page open for as long as it ran.
var watching = map[string]bool{"/setup/scores": true}

// diagnostics hands over everything worth having when something is wrong, with the addresses, names,
// keys and serial numbers already replaced — so that somebody can send it to an issue without
// reading it line by line first. Behind the press, like everything else here.
func (f *Feature) diagnostics(w http.ResponseWriter, r *http.Request) {
	if _, in := f.session(r); !in {
		http.Error(w, "not let in", http.StatusForbidden)
		return
	}
	slog.Info("setup page: diagnostics collected")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", `attachment; filename="techo5-diagnostics.txt"`)
	fmt.Fprint(w, diag.Bundle())
}

// letInRequest is what the rest of the web port asks about a request: whether the browser making it
// has been let in by a press on the device. Handed to web.Guard as this feature is built, so that a
// page in another feature can put its own device-changing options behind the same press.
func (f *Feature) letInRequest(r *http.Request) bool {
	_, in := f.session(r)
	return in
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
	q := r.URL.Query()
	f.settingsPage(r.Context(), w, token, tabOf(q.Get("tab")), q.Get("saved"), q.Get("renamed"), q.Get("problem"), q.Get("scan") != "")
}

// wait starts this browser waiting for a press and gives it the cookie the press will let in.
func (f *Feature) wait(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "post to ask", http.StatusMethodNotAllowed)
		return
	}
	from, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		from = r.RemoteAddr
	}
	token, ok := f.awaitFrom(from, web.AwayRequest(r)) // for Home Assistant's let in (remote.go)
	if !ok {
		if f.ShutOut() {
			http.Error(w, "too many tries without a press on the device; try again in a few minutes",
				http.StatusTooManyRequests)
			return
		}
		http.Error(w, "another browser is already waiting for a press; try again in a minute", http.StatusConflict)
		return
	}
	// The cookie covers the whole port, not just this page: the screenshot page asks the same
	// question about its device-changing options, and a cookie scoped to /setup would never be sent
	// there. Nothing else is served on this port, it cannot be read by script, and Strict keeps it
	// off requests another site started.
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: token, Path: "/",
		HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: int(life / time.Second),
	})
	f.showAsking()
	f.Changed.Emit(struct{}{}) // the device says a browser is asking
	slog.Info("setup page: a browser is asking to be let in", "from", from)
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
	limit := int64(maxBody)
	if r.URL.Query().Get("big") == "deck" {
		limit = maxDeckBody
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "that form was too big or malformed", http.StatusBadRequest)
		return
	}
	// The form carries the session it came from, so a page on another site cannot post here with a
	// browser's cookie.
	if !sameToken(r.PostFormValue("token"), token) {
		slog.Warn("setup page: a save arrived without the page's own token")
		http.Error(w, "that form did not come from this page", http.StatusForbidden)
		return
	}

	var problem string
	var renamed bool
	switch what := r.PostFormValue("what"); what {
	case "wifi":
		ssid := strings.TrimSpace(r.PostFormValue("ssid"))
		if other := strings.TrimSpace(r.PostFormValue("other")); other != "" {
			ssid = other
		}
		problem = joinWifi(r.Context(), ssid, r.PostFormValue("passphrase"))
	case "address":
		var handled bool
		if problem, handled = saveAddress(w, r); handled {
			return
		}
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
	case "stations":
		// Play and Stop are buttons in the same form as Save, so that a row can be tried with what is
		// typed in it rather than only with what was last kept. Neither of them saves anything.
		switch {
		case r.PostFormValue("stop") != "":
			media.Get().Stop()
		case r.PostFormValue("play") != "":
			problem = playRow(r)
		default:
			problem = saveStations(r)
		}
	case "house":
		problem = saveHouse(r.PostFormValue("word"))
	case "update-install", "update-check", "update-auto":
		problem = saveUpdates(what, r.PostFormValue("auto") == "yes")
	case "brain":
		problem = saveBrain(r)
	case "music":
		problem = saveMusic(r)
	case "streaming":
		problem = saveStreaming(r)
	case "bluetooth":
		problem = saveBluetooth(r)
	case "speaker":
		problem = saveSpeaker(r)
	case "screen":
		problem = saveScreen(r)
	case "themes":
		problem = saveThemes(r)
	case "theme-colors":
		problem = saveThemeColors(r)
	case "listening":
		problem = saveListening(r)
	case "photos-remove":
		problem = removePhotos(r)
	case "immich":
		problem = saveImmich(r)
	case "immich-album":
		problem = chooseImmichAlbum(r)
	case "immich-show":
		problem = saveImmichShow(r)
	case "immich-forget":
		problem = forgetImmich()
	case "font-remove":
		problem = removeFont(r)
	case "sound-play":
		problem = playSound(r)
	case "sound-remove":
		problem = removeSound(r)
	case "wakeword-remove":
		problem = removeWakeWord(r)
	case "hacameras":
		problem = saveHACameras(r)
	case "reolink":
		problem = saveReolink(r)
	case "doorbell":
		problem = saveDoorbell(r)
	case "doorbell-chime":
		chimeDoorbell()
	case "talkback":
		problem = saveTalkBack(r)
	case "lock":
		problem = saveLock(r)
	case "place":
		problem = savePlace(r)
	case "calendar":
		problem = saveCalendarLink(r)
	case "calendar-remove":
		problem = removeCalendarLink(r)
	case "calendar-look":
		problem = saveCalendarLook(r)
	case "adopt":
		if _, err := api.Get().OpenAdoption(); err != nil {
			problem = "could not open the device to a Home Assistant: " + err.Error()
		}
	case "dashboard":
		problem = saveDashboard(r)
	case "dashpanel":
		problem = saveDashboardPanel(r)
	case "deckobs":
		problem = saveDeckOBS(r)
	case "deckgrid":
		problem = saveDeckGrid(r)
	case "deckpage":
		problem = saveDeckPage(r)
	case "deckpc":
		problem = saveDeckPC(r)
	case "video":
		problem = saveVideo(r)
	case "tailscale":
		problem = saveTailscale(r)
	case "netbird":
		problem = saveNetbird(r)
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
	case "timeserver":
		if err := timezone.SetServer(r.PostFormValue("server")); err != nil {
			problem = err.Error()
		}
	default:
		if save, ok := saves[what]; ok {
			problem = save(r)
		} else if !alarmSave(r, what, &problem) {
			problem = "nothing to save"
		}
	}

	tab := r.PostFormValue("tab")
	to := back(tab, "saved", "1")
	if renamed {
		to = back(tab, "renamed", "1")
	}
	if problem != "" {
		slog.Warn("setup page: a change was refused", "problem", problem)
		to = back(tab, "problem", problem)
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
}

func (f *Feature) lockedPage(w http.ResponseWriter) {
	waiting := f.Waiting()
	head(w)
	fmt.Fprintf(w, `<div class="wrap"><h1>%s</h1><p class="sub">Setup</p>`, html.EscapeString(deviceName()))
	defer fmt.Fprint(w, `</div>`)
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

func (f *Feature) settingsPage(ctx context.Context, w http.ResponseWriter, token, tab, saved, renamed, problem string, scan bool) {
	head(w)
	fmt.Fprintf(w, `<div class="wrap"><h1>%s</h1><p class="sub">Setup</p><div class="layout">`, html.EscapeString(deviceName()))
	nav(w, tab)
	fmt.Fprint(w, `<section>`)
	for _, t := range shownTabs() {
		if t.id == tab {
			fmt.Fprintf(w, `<h2>%s</h2>`, html.EscapeString(t.title))
		}
	}
	if saved != "" {
		fmt.Fprint(w, `<div class="banner ok">Saved.</div>`)
	}
	if renamed != "" {
		fmt.Fprint(w, `<div class="banner ok">Renamed. The device is restarting and will be back in a minute or
		 so. Home Assistant keeps it as the same device, under its old entity ids.</div>`)
	}
	if problem != "" {
		fmt.Fprintf(w, `<div class="banner bad">%s</div>`, html.EscapeString(problem))
	}

	switch tab {
	// Within a tab, what is changed most comes first; a long tab is split under group headings.
	case "sound":
		speakerSection(w, token)
		fmt.Fprint(w, `<h3>Music</h3>`)
		stationsSection(w, token)
		musicSection(ctx, w, token)
		streamingSection(w, token)
		// A device that cannot hear has no voice assistant, wake words or listening to set up: only the
		// announcements it plays and its sounds are left of the group.
		if layout.HasMicrophone() {
			fmt.Fprint(w, `<h3>Voice</h3>`)
			brainSection(w, token)
			realtimeSection(w, token)
			listeningSection(w, token)
			houseSection(w, token)
			phoneBookSection(w, token)
			wakeWordsSection(w, token)
			scoresSection(w)
		} else {
			fmt.Fprint(w, `<h3>Announcements and sounds</h3>`)
			houseSection(w, token)
		}
		soundsSection(w, token)
	case "alarms":
		alarmsSection(w, token)
	case "connections":
		if wifi.Available() {
			f.wifiSection(w, token, scan)
			addressSection(w, token)
		} else {
			fmt.Fprint(w, `<fieldset><legend>Wi-Fi</legend><p class="note" style="margin:0">This device's network
			 is not one this page can change.</p></fieldset>`)
		}
		bluetoothSection(w, token)
		haCamerasSection(w, token)
		reolinkSection(w, token)
		go2rtcSection(w, token)
		cameraListSection(w, token)
		cameraButtonsSection(w, token)
		doorbellSection(w, token)
		tailscaleSection(w, token)
		netbirdSection(w, token)
		if layout.HasMicrophone() {
			talkBackSection(w, token) // the microphones to a camera's speaker
		}
		dashboardSection(w, token)
	case "photos":
		screenSection(w, token)
		themeSection(w, token)
		ownFontsSection(w, token)
		dashboardPanelSection(w, token)
		deckSection(w, token)
		videoSection(w, token)
		immichSection(w, token)
		photosSection(w, token)
	case "weather":
		placeSection(w, token)
		calendarLinksSection(w, token)
		calendarLookSection(w, token)
	case "privacy":
		lockSection(w, token)
		camStreamSection(w, token)
		privacySection(w)
		remoteSection(w, token)
	case "general":
		nameSection(w, token)
		updatesSection(w, token)
		timezoneSection(w, token)
		timeServerSection(w, token)
		homeAssistantSection(w, token)
		settingsFileSection(w, token)
		shareSection(w, token)
		diagnosticsSection(w)
	}
	fmt.Fprint(w, `</section></div></div>`)
}

// timezoneSection is which zone the clock shows.
func timezoneSection(w http.ResponseWriter, token string) {
	cur := timezone.Get().Current()
	fmt.Fprint(w, `<form method="post" action="/setup/save"><fieldset><legend>Time zone</legend>`)
	hidden(w, token, "timezone", "general")
	fmt.Fprint(w, `<label for="zone">Zone</label><select id="zone" name="zone">`)
	fmt.Fprintf(w, `<option value=""%s>Follow Home Assistant</option>`, selected(!timezone.Get().SetHere()))
	if !timezone.HasDatabase() {
		// An image without the zone database still puts the common zones in force (timezone.Common).
		fmt.Fprint(w, `<optgroup label="Common">`)
		for _, full := range timezone.Common {
			fmt.Fprintf(w, `<option value="%s"%s>%s</option>`,
				html.EscapeString(full), selected(full == cur && timezone.Get().SetHere()), html.EscapeString(full))
		}
		fmt.Fprint(w, `</optgroup>`)
	}
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
}

// timeServerSection is a time server of the home's own, asked before the internet's.
func timeServerSection(w http.ResponseWriter, token string) {
	if !timezone.ServerSettable {
		return
	}
	fmt.Fprint(w, `<form method="post" action="/setup/save"><fieldset><legend>Time server</legend>`)
	hidden(w, token, "timeserver", "general")
	fmt.Fprintf(w, `<label for="ntp">Your own time server (optional)</label>
	 <input id="ntp" name="server" value="%s" placeholder="192.168.1.1" maxlength="253" autocomplete="off">
	 <p class="note">The device sets its clock from time servers on the internet. A device that cannot
	  reach them can use one on your network, like your router: it is asked first, and the internet's
	  after it. %s Leave it empty for the internet's only. Without any time server, the clock is set
	  from Home Assistant's when it connects.</p>
	 <p><button type="submit">Save</button></p></fieldset></form>`, html.EscapeString(timezone.Server()), timezone.ServerWhen)
}

// privacySection says what the device has open, and what this page will never do. It shows and does
// not change: a web page that could open SSH would be a bigger hole than the convenience is worth, so
// these stay on the device's own screen and in Home Assistant.
func privacySection(w http.ResponseWriter) {
	s := config.Get().Security
	onOff := func(on bool) string {
		if on {
			return "<strong>on</strong>"
		}
		return "off"
	}
	// A panel has no SSH and no camera page at all (privacy_panel.go): "off" would send somebody
	// looking for a switch that is not there.
	ssh, camera, missing := onOff(s.SSH), onOff(s.Camera), ""
	if !sshHere {
		ssh = "not available on this device"
	}
	if !cameraWebHere {
		camera = "not available on this device"
	}
	if !sshHere || !cameraWebHere {
		missing = " " + notHereWhy
	}
	fmt.Fprintf(w, `<fieldset><legend>What is switched on</legend>
	 <p style="margin:0">SSH: %s · Camera on the network: %s · Screen on the network: %s</p>
	 <p class="note">Shown here, not changed here. Change them on the device's own screen, or in Home
	  Assistant.%s</p></fieldset>`, ssh, camera, onOff(s.Screen), missing)
	fmt.Fprint(w, `<fieldset><legend>This page</legend>
	 <p class="note" style="margin:0">It opens only after a press on the device, and closes itself when it
	  is left alone. It is on your own network, without encryption. It never touches SSH keys or the
	  software the device runs, and never shows the Home Assistant key. It can let a Home Assistant add
	  the device for 15 minutes (General), which sets a new key.</p></fieldset>`)
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
			fmt.Fprintf(w, `<li>%s <form method="post" action="/setup/save" style="display:inline">`, html.EscapeString(ssid))
			hidden(w, token, "forget", "connections")
			fmt.Fprintf(w, `<input type="hidden" name="ssid" value="%s">
			 <button type="submit" class="quiet">Forget</button></form></li>`, html.EscapeString(ssid))
		}
		fmt.Fprint(w, `</ul>`)
	}

	fmt.Fprint(w, `<form method="post" action="/setup/save">`)
	hidden(w, token, "wifi", "connections")
	fmt.Fprint(w, `<label for="ssid">Network</label><select id="ssid" name="ssid">`)
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
		fmt.Fprint(w, `<p><a href="/setup?tab=connections&amp;scan=1">Scan for networks</a> — it takes a few seconds.</p>`)
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

// diagnosticsSection offers the bundle. It is first because somebody who came here to ask for help
// should not have to read past the radio stations to find it.
func diagnosticsSection(w http.ResponseWriter) {
	fmt.Fprint(w, `<fieldset><legend>Something wrong?</legend>
	 <p><a href="/setup/diagnostics.txt">Download diagnostics</a> — what this device knows about
	  itself, the last of its log, its recent wake words, and what it is set to.</p>
	 <p class="note">Addresses, network names, keys and serial numbers are replaced before you get it,
	  so it can go straight into an issue. Worth a look before you send it all the same.</p>
	 </fieldset>`)
}

// stationsSection is the radio stations kept on the device: a name and the address of the stream,
// which is the thing nobody wants to type on the device's own screen and the reason this page earns
// its place. They play without Home Assistant, which is the only radio a device on its own has.
func stationsSection(w http.ResponseWriter, token string) {
	list := home.OwnStations()
	fmt.Fprint(w, `<fieldset><legend>Radio stations on this device</legend><form method="post" action="/setup/save">`)
	hidden(w, token, "stations", "sound")
	// One more row than there are stations, so there is always somewhere to add one; clearing a
	// name takes that station out.
	for i := 0; i <= len(list) && i < config.MaxOwnStations; i++ {
		var st config.Station
		if i < len(list) {
			st = list[i]
		}
		fmt.Fprintf(w, `<label for="n%d">Name</label>
		 <input id="n%d" name="name" value="%s" maxlength="40" autocomplete="off" placeholder="Station name">
		 <label for="u%d">Stream address</label>
		 <input id="u%d" name="url" value="%s" autocomplete="off" placeholder="https://…">
		 <p><button type="submit" name="play" value="%d" class="quiet">Play this one</button></p>`,
			i, i, html.EscapeString(st.Name), i, i, html.EscapeString(st.URL), i)
	}

	// What the device is doing, because a page with play buttons and no answer is a remote control
	// with no display — and on a Dot it is the only place this can be seen at all.
	if station, playing := home.Get().NowPlaying(); playing {
		if station == "" {
			station = "something"
		}
		fmt.Fprintf(w, `<p class="playing">Playing %s
		 <button type="submit" name="stop" value="1" class="quiet">Stop</button></p>`,
			html.EscapeString(station))
	}

	fmt.Fprint(w, `<p class="note">Play uses what is typed in the row, saved or not, so an address can
	 be tried before it is kept. Clear a name to take a station out. They show on the device under
	 Radio, as "On this device".</p>
	 <p><button type="submit">Save stations</button></p></form></fieldset>`)
}

// playRow plays the row whose Play button was pressed, with the name and address as they stand in
// the form. A row with nothing in it is a press on the spare row at the bottom, which is somebody
// asking for nothing.
func playRow(r *http.Request) string {
	i, err := strconv.Atoi(r.PostFormValue("play"))
	names, urls := r.PostForm["name"], r.PostForm["url"]
	if err != nil || i < 0 || i >= len(names) || i >= len(urls) {
		return "that row is not one of these"
	}
	name, u := strings.TrimSpace(names[i]), strings.TrimSpace(urls[i])
	switch {
	case name == "" && u == "":
		return "fill the row in first"
	case u == "":
		return name + " has no stream address"
	}
	if name == "" {
		name = "that stream"
	}
	if !home.Get().PlayStream(name, u) {
		return name + "'s address has to start with http:// or https://"
	}
	return ""
}

// saveStations takes the rows the form posted, in the order they were in. Names and addresses come
// back as two lists of the same length, one row each. A row whose name was cleared on a station
// already kept is left out, whatever its address still says: that is how the note under the form
// says a station is taken out. A new address typed without a name is a mistake, and said to be.
func saveStations(r *http.Request) string {
	names, urls := r.PostForm["name"], r.PostForm["url"]
	if len(names) != len(urls) {
		return "that form did not arrive whole"
	}
	kept := map[string]bool{}
	for _, s := range home.OwnStations() {
		kept[s.URL] = true
	}
	var list []config.Station
	for i := range names {
		name, u := strings.TrimSpace(names[i]), strings.TrimSpace(urls[i])
		switch {
		case name == "" && (u == "" || kept[u]):
			continue // a row never filled in, or one being taken out
		case name == "":
			return "a station needs a name as well as an address"
		case u == "":
			return name + " has no stream address"
		case !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://"):
			return name + "'s address has to start with http:// or https://"
		}
		list = append(list, config.Station{Name: name, URL: u})
	}
	if err := home.SetOwnStations(list); err != nil {
		return "could not save them: " + err.Error()
	}
	return ""
}

// nameSection renames the device, behind what it does and the box that has to be ticked.
//
// What it does was worth checking rather than assuming: Home Assistant keys its entities on the
// device's MAC address (unique ids read "a1:b2:c3:d4:e5:f6/0/media_player/Speaker"), so a renamed
// device is the same device to it and keeps the entity ids it was given. Automations go on working.
// What changes is what Home Assistant shows, and the entity ids then no longer look like the name —
// which is confusing enough to warn about, and reason to rename in Home Assistant as well.
func nameSection(w http.ResponseWriter, token string) {
	name := deviceName()
	fmt.Fprint(w, `<fieldset><legend>Name</legend><form method="post" action="/setup/save">`)
	hidden(w, token, "name", "general")
	fmt.Fprintf(w, `<label for="name">This device is called</label>
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
		html.EscapeString(name), html.EscapeString("media_player."+layout.EntitySlug(name)+"_speaker"))
}

// homeAssistantSection lets a Home Assistant add the device: for a device that left the Home
// Assistant it had, or came from somebody else's, so its key is one nobody here holds (api/adopt.go).
func homeAssistantSection(w http.ResponseWriter, token string) {
	fmt.Fprint(w, `<fieldset><legend>Home Assistant</legend>`)
	if until := api.Get().AdoptionOpenUntil(); !until.IsZero() {
		fmt.Fprintf(w, `<p style="margin:0"><strong>Open until %s.</strong> In Home Assistant, go to
		 <strong>Settings → Devices &amp; services</strong>. This device shows up under
		 <strong>Discovered</strong>: choose <strong>Add</strong>. If it doesn't, choose <strong>Add
		 integration → ESPHome</strong> and type this device's address, port 6053.</p>
		 <p class="note">The first Home Assistant to add it sets its key, and the device keeps that key.
		  If none does by then, it closes with a new key nobody has.</p></fieldset>`,
			html.EscapeString(until.Local().Format("3:04 PM")))
		return
	}
	fmt.Fprint(w, `<form method="post" action="/setup/save">`)
	hidden(w, token, "adopt", "general")
	fmt.Fprint(w, `<p style="margin:0">To add this device to your own Home Assistant when it came from
	 somebody else's, or left one: for 15 minutes the device has no key, and the Home Assistant that adds
	 it sets one.</p>
	 <p class="bad"><strong>A Home Assistant that has this device now loses it.</strong></p>
	 <p><button type="submit">Let a Home Assistant add this device</button></p></form></fieldset>`)
}

// houseSection is the word the devices in one house share.
//
// It is the whole of the security on announcements: a device takes one from anything on the network
// that knows the word, and ignores everything else. That is the right size for the thing — an
// announcement is a voice in a room, not a door — but it does mean the word is worth typing rather
// than leaving as something guessable, and it means the same word has to go on every device here.
func houseSection(w http.ResponseWriter, token string) {
	word := config.Get().Home.HouseWord
	fmt.Fprint(w, `<fieldset><legend>Announcements</legend><form method="post" action="/setup/save">`)
	hidden(w, token, "house", "sound")
	fmt.Fprintf(w, `<label for="word">House word</label>
	 <input id="word" name="word" value="%s" maxlength="63" autocomplete="off">
	 <p class="note">Type the <strong>same word on every device in this house</strong>. They then find
	  each other on the network, and speaking to one plays it on the others. Anything that does not
	  have the word is ignored.</p>
	 <p class="note">Case-sensitive: it has to match the other devices' exactly, capitals included.</p>%s
	 <p class="note">Leave it empty to turn announcements and intercom calls off here: the device takes
	  neither. The other devices can still see it on the network, but what they send it is refused.
	  Reminders set to go off on other devices use it too.</p>
	 <p class="note">Make it a long phrase, %d characters or more. What the devices send each other is
	  signed with it, and anything on the network that hears them can try words until one fits.</p>%s
	 <p><button type="submit">Save</button></p></form></fieldset>`,
		html.EscapeString(word), otherWordNote(announce.OtherWords()), longHouseWord, shortWordNote(word))
}

// otherWordNote names the devices here that refused this one's announcements in the last hour, for
// having another house word.
func otherWordNote(names []string) string {
	if len(names) == 0 {
		return ""
	}
	devices := "other devices here use"
	if len(names) == 1 {
		devices = "other device here uses"
	}
	return fmt.Sprintf(`
	 <p class="note"><strong>%d %s a different house word: %s.</strong> They refuse what this one
	  sends them. Check the word on each, capitals included.</p>`,
		len(names), devices, html.EscapeString(strings.Join(names, ", ")))
}

// longHouseWord is how long a house word has to be before the page stops saying it is short. A short
// one still works: it is a warning, not a refusal.
const longHouseWord = 16

// shortWordNote is the warning for a house word that is set and short.
func shortWordNote(word string) string {
	if word == "" || len([]rune(word)) >= longHouseWord {
		return ""
	}
	return `
	 <p class="note"><strong>This word is short.</strong> It works, but it is easy to guess: a longer one
	  keeps the announcements, the intercom and the other devices' messages yours.</p>`
}

// saveHouse keeps the word, and then tells the web feature to look again: the port announcements
// arrive on is only listening while the word is set, and without the nudge it opens on that
// feature's own next look, up to a minute later. The first announcement after setup is exactly the
// one somebody is standing there waiting for.
func saveHouse(v string) string {
	word := strings.TrimSpace(v)
	if strings.ContainsAny(word, "\r\n") {
		return "a house word is one word on one line"
	}
	if err := config.Set().Home().HouseWord(word); err != nil {
		return "could not save it: " + err.Error()
	}
	slog.Info("setup page: the house word was set", "set", word != "")
	web.Wake()
	return ""
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

// sameToken is whether a form's token is the page's, compared in the same time whatever it holds.
func sameToken(got, token string) bool {
	return subtle.ConstantTimeCompare([]byte(got), []byte(token)) == 1
}
