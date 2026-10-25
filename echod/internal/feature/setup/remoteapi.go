package setup

import (
	"fmt"
	"html"
	"log/slog"
	"net/http"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/remote"
)

// The remote API's key, on Privacy & Security. The API is turned on and off on the device's screen or in
// Home Assistant, as the other doors are; the key is made here, behind the press on the device, and shown
// once.

func init() { routes["/setup/remote-key"] = (*Feature).remoteKey }

// remoteSection says whether the API is on and offers a key.
func remoteSection(w http.ResponseWriter, token string) {
	state := "off"
	if config.Get().Remote.On {
		state = "<strong>on</strong>"
	}
	key := "No key has been made."
	button := "Make a key"
	if remote.HasKey() {
		key = "A key has been made. Making another stops the old one working."
		button = "Make a new key"
	}
	fmt.Fprintf(w, `<fieldset><legend>Remote API</legend>
	 <p style="margin:0">Remote API: %s. %s</p>
	 <p class="note">For scripts and tools that are not Home Assistant: they read the device and change its
	  everyday settings, never its privacy switches, updates or restarts. Every request is signed with the
	  key, which is shown once. Turn it on in Home Assistant, or on the screen (Show and Spot, Privacy & Security).</p>
	 <form method="post" action="/setup/remote-key"><input type="hidden" name="token" value="%s">
	  <div class="btns"><button type="submit">%s</button></div></form></fieldset>`,
		state, key, html.EscapeString(token), button)
}

// remoteKey makes a key and shows it, once.
func (f *Feature) remoteKey(w http.ResponseWriter, r *http.Request) {
	token, in := f.session(r)
	if !in {
		http.Error(w, "not let in", http.StatusForbidden)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "post to make a key", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	if err := r.ParseForm(); err != nil || !sameToken(r.PostFormValue("token"), token) {
		http.Error(w, "that did not come from this page", http.StatusForbidden)
		return
	}
	f.used()
	key, err := remote.Get().NewKey()
	if err != nil {
		http.Redirect(w, r, back("privacy", "problem", "the key could not be saved: "+err.Error()), http.StatusSeeOther)
		return
	}
	slog.Info("setup page: a remote api key was made")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	head(w)
	fmt.Fprintf(w, `<div class="wrap"><h1>%s</h1><p class="sub">Remote API key</p>
	 <fieldset><legend>The key</legend>
	 <p><code style="font-size:1.1rem;word-break:break-all">%s</code></p>
	 <p class="note">Copy it now: it is not kept on the device and cannot be shown again. Anybody with it can
	  change this device's everyday settings while the Remote API is on. Lost, make a new one.</p>
	 <p><a href="/setup?tab=privacy">Back to the setup page</a></p></fieldset></div>`,
		html.EscapeString(deviceName()), html.EscapeString(key))
}
