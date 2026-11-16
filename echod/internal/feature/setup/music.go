package setup

import (
	"context"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/sendspin"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/metrics"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
	"github.com/HuskerMinion/techo5/echod/internal/lib/musicassistant"
)

// musicSection is the Music Assistant server the voice assistant asks for songs, artists, albums and
// playlists (config.MusicAssistant). The token is never shown: it is written, or left alone.
func musicSection(ctx context.Context, w http.ResponseWriter, token string) {
	m := config.Get().MusicAssistant
	var addrs []string
	for _, ip := range metrics.Addresses() {
		addrs = append(addrs, ip.String())
	}
	fieldsetOpen(w, "music", "Music Assistant")
	fmt.Fprint(w, `<form method="post" action="/setup/save">`)
	hidden(w, token, "music")
	var a musicAnswer
	if m.Set() {
		// Asked through a lookup (lookup.go), so a Music Assistant that is slow or down does not hold
		// the tab up on every draw.
		got, ok := musicLookup.get(ctx, lookupKey(m.URL, m.Token), func(ctx context.Context) musicAnswer { return askMusic(ctx, m) })
		if a = got; !ok {
			a = musicAnswer{status: "still being asked; reload the page in a moment for its answer", perr: errors.New("it has not answered yet")}
		}
		fmt.Fprintf(w, `<p style="margin-top:0"><b>Now:</b> %s</p>`, html.EscapeString(a.status))
	}
	fmt.Fprintf(w, `<p class="note" style="margin-top:0">For "play some Eagles" by voice, when the voice assistant
	  answers directly: it searches this Music Assistant and plays what it finds here. Music Assistant has to
	  know this device as a player first. On the same network it finds it by itself; from farther away, add
	  one of this device's addresses (%s) in Music Assistant's Sendspin settings, under the manual discovery
	  IP addresses. Make the token for a Music Assistant user allowed only this device's player.</p>
	 <label for="maurl">Address</label>
	 <input id="maurl" name="url" type="url" inputmode="url" value="%s" placeholder="http://192.168.1.20:8095" autocomplete="off">
	 %s`,
		html.EscapeString(strings.Join(addrs, ", ")), html.EscapeString(m.URL),
		secretField("matoken", "Token (a long-lived one)", "matoken", m.Token != "", "notoken"))
	if m.Set() {
		sourceField(w, m, a.providers, a.perr)
	}
	fmt.Fprintf(w, `<label for="sdelay">Playback delay, milliseconds (0 to %d)</label>
	 <input id="sdelay" name="sendspin_delay" type="number" inputmode="numeric" min="0" max="%d" step="1" value="%d">
	 <p class="note">Holds this device back against the group, to match a player that is slower than it. 0 plays as
	 Music Assistant schedules it. It applies as soon as it is saved, even in a song that is playing.</p>`,
		config.MaxSendspinDelayMs, config.MaxSendspinDelayMs, sendspin.Get().Delay())
	fmt.Fprint(w, `<p><button type="submit">Save</button></p></form></fieldset>`)
}

// musicAnswer is what Music Assistant said: how it sees this device (musicStatus), and the services it
// has, or why it would not list them.
type musicAnswer struct {
	status    string
	providers []musicassistant.Provider
	perr      error
}

// musicLookup keeps Music Assistant's answer: two questions of up to 4 seconds each.
var musicLookup = &lookup[musicAnswer]{ask: 8 * time.Second}

// askMusic asks Music Assistant both: the services only when it answered at all, since asking one that
// did not would only wait the same time again.
func askMusic(ctx context.Context, m config.MusicAssistant) musicAnswer {
	status, answered := musicStatus(ctx, m)
	a := musicAnswer{status: status, perr: errors.New("it did not answer")}
	if answered {
		ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
		defer cancel()
		a.providers, a.perr = musicassistant.Client{URL: m.URL, Token: m.Token}.MusicProviders(ctx)
	}
	return a
}

// sourceField is the Music source choice: the services Music Assistant has (askMusic). One it will not
// list is said so, and the saved choice is kept.
func sourceField(w http.ResponseWriter, m config.MusicAssistant, providers []musicassistant.Provider, err error) {
	fmt.Fprint(w, `<label for="masource">Music source</label><select id="masource" name="source">`)
	option := func(value, label string) {
		sel := ""
		if value == m.Source {
			sel = " selected"
		}
		fmt.Fprintf(w, `<option value="%s"%s>%s</option>`, html.EscapeString(value), sel, html.EscapeString(label))
	}
	option("", "Any: the whole library")
	listed := m.Source == ""
	for _, p := range providers {
		option(p.InstanceID, p.Name)
		listed = listed || p.InstanceID == m.Source
	}
	if !listed {
		option(m.Source, m.Source+" (saved)")
	}
	fmt.Fprint(w, `</select>`)
	note := `By voice, this service is searched first, and the rest of the library only when it has nothing by
	  that name. Saying the service ("play Taylor Swift on YouTube Music") picks one for that request.
	  Needs Music Assistant 2.10 or later.`
	if err != nil {
		note = "Music Assistant would not list its services (" + err.Error() + "), so only the saved choice is shown. " + note
	}
	fmt.Fprintf(w, `<p class="note">%s</p>`, html.EscapeString(note))
}

// sourceOK is whether a Music source is one that could be a Music Assistant instance id or domain.
func sourceOK(v string) bool {
	if len(v) > 64 {
		return false
	}
	for _, r := range v {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' || r == '.') {
			return false
		}
	}
	return true
}

func saveMusic(r *http.Request) string {
	addr := strings.TrimRight(strings.TrimSpace(r.PostFormValue("url")), "/")
	if addr != "" {
		u, err := url.Parse(addr)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return "the address should be like http://192.168.1.20:8095"
		}
		if u.User != nil {
			return "the address should not have a name or password in it: the token is what signs in"
		}
	}
	// The source is checked before anything is saved, so a refused form saves none of it.
	src, hasSource := "", false
	if _, shown := r.PostForm["source"]; shown { // only a form that showed the choice carries it
		src, hasSource = strings.TrimSpace(r.PostFormValue("source")), true
		if !sourceOK(src) {
			return "that is not a Music Assistant service"
		}
	}
	delay, hasDelay := 0, false
	if v := strings.TrimSpace(r.PostFormValue("sendspin_delay")); v != "" { // only the page's own form carries it
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 || n > config.MaxSendspinDelayMs {
			return fmt.Sprintf("the playback delay is a whole number of milliseconds, 0 to %d", config.MaxSendspinDelayMs)
		}
		delay, hasDelay = n, true
	}
	var tok *string
	// Not "token": that is the page's own, which every form carries (hidden).
	switch t := strings.TrimSpace(r.PostFormValue("matoken")); {
	case r.PostFormValue("notoken") == "yes":
		empty := ""
		tok = &empty
	case t != "":
		if strings.ContainsFunc(t, func(c rune) bool { return unicode.IsSpace(c) || unicode.IsControl(c) }) {
			return "a token is one word"
		}
		tok = &t
	}
	if err := config.Set().MusicAssistant().Server(addr, tok); err != nil {
		return "could not save it: " + err.Error()
	}
	if hasDelay && delay != sendspin.Get().Delay() {
		sendspin.Get().SetDelay(delay)
	}
	if hasSource {
		if err := config.Set().MusicAssistant().SetSource(src); err != nil {
			return "could not save the music source: " + err.Error()
		}
	}
	musicLookup.forget() // the next draw asks the server as it is now saved
	slog.Info("setup page: music assistant set", "url", addr, "token", tok != nil, "source", config.Get().MusicAssistant.Source)
	return ""
}

// musicStatus is how the music library sees this device right now: whether it answers, and whether
// this device is connected to it as a player, which is what everything it plays here needs. answered
// is whether the server was reached at all.
func musicStatus(ctx context.Context, m config.MusicAssistant) (status string, answered bool) {
	player, err := layout.FactoryMAC()
	if err != nil {
		return "this device does not know its own player id", true // the server was not asked
	}
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	st, err := musicassistant.Client{URL: m.URL, Token: m.Token}.Player(ctx, player)
	switch {
	case err != nil && strings.Contains(err.Error(), "refused the token"):
		return "reachable, but it refused the token: make a new one and save it here", true
	case err != nil && strings.Contains(err.Error(), "did not answer"):
		return "not reachable: " + err.Error(), false
	case err != nil:
		return err.Error(), true
	case !st.Available:
		return "reachable, but this device is not connected to it as a player, so it cannot play from it", true
	case st.State == "playing":
		return "connected, and playing on this device", true
	}
	return "connected: this device is a player in it", true
}
