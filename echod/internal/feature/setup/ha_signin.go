package setup

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"html"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// Connect to Home Assistant (#567): the device's own access to Home Assistant, from Home Assistant's
// own sign-in page, in place of a long-lived token pasted into an action. The browser goes to Home
// Assistant, the person signs in there (a password manager fills it in on a computer), and Home
// Assistant sends the browser back here with a code the device trades for its token itself
// (hass.SignIn): the browser never holds a token.
//
// The way back is the one request on this page that does not carry the page's cookie: the cookie is
// SameSite=Strict, and the browser arrives from Home Assistant's page, another site. So the way back is
// trusted by its state instead: made for a browser that was let in, good once, for ten minutes, and
// only while that browser is still let in.

func init() {
	routes["/setup/ha/connect"] = (*Feature).haConnect
	routes["/setup/ha/back"] = (*Feature).haBack
	routes["/setup/ha/disconnect"] = (*Feature).haDisconnect
}

// signInFor is how long a sign-in may take, from Connect to Home Assistant sending the browser back.
const signInFor = 10 * time.Minute

// signingIn is one sign-in under way: the browser it is for (its session), and what Home Assistant
// is to be asked with.
type signingIn struct {
	session, base, clientID string
	until                   time.Time
}

var signIns = struct {
	sync.Mutex
	by map[string]signingIn // by state
}{by: map[string]signingIn{}}

// signInsMost is how many may be under way at once; more, and the oldest is dropped.
const signInsMost = 4

// haAccessSection is the device's own access to Home Assistant: whether it has one and which kind, and
// the Connect button.
func haAccessSection(w http.ResponseWriter, token string) {
	fieldsetOpen(w, "haaccess", "Home Assistant access")
	c := hass.Get()
	base := c.Address()
	switch {
	case c.SignedIn():
		fmt.Fprintf(w, `<p style="margin:0"><strong>Connected to Home Assistant at %s</strong> by signing in.
		 The device renews its access by itself.</p>
		 <p class="note">It is listed in Home Assistant under the profile of the user who signed in, in
		  <strong>Security, Refresh tokens</strong>, by this device's address. Removing it there disconnects
		  the device too.</p>
		 <form method="post" action="/setup/ha/disconnect"><input type="hidden" name="token" value="%s">
		 <p><button type="submit" class="quiet">Disconnect</button></p></form>`,
			html.EscapeString(base), html.EscapeString(token))
	case c.SignedOut():
		fmt.Fprint(w, `<p class="bad"><strong>Home Assistant took this device's sign-in back</strong>, so the
		 forecast, cameras, photos and dashboards from it have stopped. Connect again below.</p>`)
	case c.Ready():
		fmt.Fprintf(w, `<p style="margin:0">This device has a long-lived token for <strong>%s</strong>, given
		 with the <code>home_assistant</code> action. Connecting by signing in replaces it.</p>`, html.EscapeString(base))
	default:
		fmt.Fprint(w, `<p style="margin:0">This device has no access to Home Assistant of its own yet. It needs
		 one for the forecast, cameras, photos, radio lists and drawn dashboards.</p>`)
	}
	if base == "" {
		base = hass.Suggested()
	}
	label := "Connect to Home Assistant"
	if c.SignedIn() {
		label = "Connect again"
	}
	fmt.Fprintf(w, `<form method="post" action="/setup/ha/connect"><input type="hidden" name="token" value="%s">
	 <label for="ha-url">Home Assistant's address</label>
	 <input id="ha-url" name="url" value="%s" autocomplete="off" spellcheck="false">
	 <p class="note">Home Assistant's sign-in page opens. Sign in as the user this device should act as:
	  a user made for the devices, without administrator rights, is best. Then you come back here.</p>
	 <p><button type="submit">%s</button></p></form></fieldset>`,
		html.EscapeString(token), html.EscapeString(base), label)
}

// haConnect starts a sign-in: a state for it, and the browser sent to Home Assistant's login.
func (f *Feature) haConnect(w http.ResponseWriter, r *http.Request) {
	token, in := f.session(r)
	if !in {
		notLetIn(w)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "post to connect", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	if err := r.ParseForm(); err != nil || !sameToken(r.PostFormValue("token"), token) {
		http.Error(w, "that form did not come from this page", http.StatusForbidden)
		return
	}
	base, err := hass.CheckAddress(r.PostFormValue("url"))
	if err != nil {
		http.Redirect(w, r, back("haaccess", "problem",
			"That is not an address like http://homeassistant.local:8123."), http.StatusSeeOther)
		return
	}
	own, ok := pageOrigin(r)
	if !ok {
		refusalPage(w, http.StatusBadRequest, "Not this address",
			"This page was opened at an address Home Assistant cannot send you back to. Open it at the device's own address and try again.")
		return
	}
	state, err := newState()
	if err != nil {
		http.Error(w, "no randomness", http.StatusInternalServerError)
		return
	}
	// Home Assistant takes an address on the home network as the client, and a way back on the same
	// address with nothing more (IndieAuth); the client is the page's own address, so it is the one the
	// browser used to get here.
	clientID := own + "/"
	signIns.Lock()
	now := time.Now()
	for s, si := range signIns.by {
		if now.After(si.until) || si.session == token {
			delete(signIns.by, s) // run out, or this browser's last try
		}
	}
	if len(signIns.by) >= signInsMost {
		var oldest string
		for s, si := range signIns.by {
			if oldest == "" || si.until.Before(signIns.by[oldest].until) {
				oldest = s
			}
		}
		delete(signIns.by, oldest)
	}
	signIns.by[state] = signingIn{session: token, base: base, clientID: clientID, until: now.Add(signInFor)}
	signIns.Unlock()
	slog.Info("setup page: signing in with Home Assistant", "at", base)
	http.Redirect(w, r, hass.AuthorizeURL(base, clientID, own+"/setup/ha/back", state), http.StatusSeeOther)
}

// haBack is the browser back from Home Assistant: with a code to trade, or having been turned down.
func (f *Feature) haBack(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	state := q.Get("state")
	signIns.Lock()
	si, ok := signIns.by[state]
	delete(signIns.by, state) // once, whatever happens next
	signIns.Unlock()
	if !ok || state == "" || time.Now().After(si.until) {
		haBackPage(w, http.StatusForbidden, "Not connected",
			"This sign-in is not one this device started, or it took longer than ten minutes. Go back to the setup page and connect again.")
		return
	}
	if !f.letIn(si.session) {
		haBackPage(w, http.StatusForbidden, "Not connected",
			"The browser that started this sign-in is no longer let in to the setup page. Go back to it, be let in again, and connect again.")
		return
	}
	code := q.Get("code")
	if code == "" {
		haBackPage(w, http.StatusOK, "Not connected", "Home Assistant did not sign this device in. Nothing was changed.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	if err := hass.Get().SignIn(ctx, si.base, si.clientID, code); err != nil {
		slog.Warn("setup page: signing in with Home Assistant failed", "err", err)
		haBackPage(w, http.StatusOK, "Not connected",
			"Home Assistant signed you in, but the device could not reach it to finish. Check that the device can reach "+
				si.base+" and connect again.")
		return
	}
	slog.Info("setup page: signed in with Home Assistant", "at", si.base)
	home.Get().Poke() // the forecast, now that there is access to ask it with
	haBackPage(w, http.StatusOK, "Connected", "This device is connected to Home Assistant and has its own access now.")
}

// haBackPage is what the browser sees back from Home Assistant, with the way on to the settings. A
// link, not a redirect: the browser came from another site, and on a redirect from here it would still
// not send the page's cookie; a link followed on this page does.
func haBackPage(w http.ResponseWriter, code int, title, said string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer") // the address bar holds the code until the page moves on
	w.WriteHeader(code)
	head(w)
	fmt.Fprintf(w, `<div class="wrap"><h1>%s</h1><p class="sub">Setup</p><p>%s</p>
	 <p><a href="/setup?tab=network#sec-haaccess">Back to the setup page</a></p></div>`,
		html.EscapeString(title), html.EscapeString(said))
}

// haDisconnect forgets the access, taking a sign-in back at Home Assistant.
func (f *Feature) haDisconnect(w http.ResponseWriter, r *http.Request) {
	token, in := f.session(r)
	if !in {
		notLetIn(w)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "post to disconnect", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	if err := r.ParseForm(); err != nil || !sameToken(r.PostFormValue("token"), token) {
		http.Error(w, "that form did not come from this page", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	if err := hass.Get().Disconnect(ctx); err != nil {
		slog.Warn("setup page: disconnecting from Home Assistant failed", "err", err)
	} else {
		slog.Info("setup page: disconnected from Home Assistant")
	}
	http.Redirect(w, r, back("haaccess", "", ""), http.StatusSeeOther)
}

// pageOrigin is the address the browser opened this page at, as Home Assistant is to send it back to:
// http and the Host it asked for, when that is a plain host and port.
func pageOrigin(r *http.Request) (string, bool) {
	host := r.Host
	h, port, err := net.SplitHostPort(host)
	if err != nil {
		h, port = host, ""
	}
	if h == "" || strings.ContainsAny(h, "/?#@\\ ") {
		return "", false
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if port != "" {
		return scheme + "://" + net.JoinHostPort(h, port), true
	}
	return scheme + "://" + host, true
}

// newState is a sign-in's state: 32 random bytes in hex.
func newState() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
