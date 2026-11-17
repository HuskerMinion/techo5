package setup

import (
	"context"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/detect"
)

// Send wake clips to a trainer on the home network (feature/detect, clipsend.go): where, the address,
// the token the receiver asks for, and which kinds of clip. The token is a secret: this page only ever
// says one is kept. Clips are only ever sent to an address on the home network; the address is checked
// here and again before each send.

// wakeSendAsk is the most saving an address, or testing it, waits for the trainer's name to be found.
const wakeSendAsk = 15 * time.Second

// What the page asks of the sender; a test replaces them, since there is no recorder to run.
var (
	setWakeSend = func(ctx context.Context, w config.WakeSend) error { return detect.Get().SetSend(ctx, w) }
	pingTrainer = func(ctx context.Context, w config.WakeSend) (string, error) {
		return detect.Get().Sender().Ping(ctx, w)
	}
	wakeClipsKept   = func() bool { return detect.Get().KeepOn() }
	setKeepClips    = func(on bool) error { return detect.Get().SetKeepClips(on) }
	wakeClipsUnsent = func() (int, string) { return detect.Get().Sender().Unsent() }
	wakeCertSeen    = func() string { return detect.Get().Sender().Untrusted() }
	trustCert       = func(ctx context.Context, fp string) error { return detect.Get().Trust(ctx, fp) }
)

func init() {
	saves["wakeclips"] = saveWakeSend
	saves["wakeclips-test"] = testWakeSend
	saves["wakeclips-trust"] = trustWakeCert
	saves["wakeclips-keep"] = saveKeepWake
}

// wakeSendPlaceholder is what the address field shows before one is typed.
const wakeSendPlaceholder = "http://192.168.1.20:8765"

func wakeClipsSendSection(w http.ResponseWriter, token string) {
	cur := config.Get().Wake.Send
	fieldsetOpen(w, "wakeclips", "Send wake clips to")
	keeping := wakeClipsKept()
	on, off := "", ` selected`
	if keeping {
		on, off = ` selected`, ""
	}
	fmt.Fprint(w, `<p style="margin:0">Keep short clips of the wake word on the device, and upload each one to a wake word
	 trainer on your home network, to train on what this room really says and hears.</p>
	 <form method="post" action="/setup/save">`)
	hidden(w, token, "wakeclips-keep")
	fmt.Fprintf(w, `<label for="wckeep">Keep wake clips</label>
	 <select id="wckeep" name="keep"><option value="on"%s>On</option><option value="off"%s>Off</option></select>
	 <p class="note">While it is on the device keeps the four seconds around each wake, each near miss and each
	  wake that looks like a mistake, the newest 100 of each kind. <strong>Turning it off deletes every kept clip on this device</strong>, the ones not yet sent with them.
	  Takes from Record wake word waiting to be sent go with them, unless Send wake clips to is wakesmith: then
	  they stay until they reach it.</p>
	 <p><label><input type="checkbox" name="confirm" value="yes" style="width:auto"> Delete the wake clips on this device (tick this to turn it off)</label></p>
	 <p><button type="submit">Save</button></p></form>`, on, off)
	fmt.Fprint(w, `<p style="margin-bottom:0">Nothing is sent unless you choose where below, and only clips kept after
	 you do. It works while Keep wake clips is on, and only to an address on your home network or your
	 Tailscale network. Takes from Record wake word, under Record takes of a word, go to wakesmith alone,
	 with Keep wake clips on or off.</p>
	 <form method="post" action="/setup/save"`+busy(busyTrainer)+`>`)
	hidden(w, token, "wakeclips")
	fmt.Fprint(w, `<label for="wcto">Send to</label><select id="wcto" name="target">`)
	for _, t := range config.SendTargets {
		sel := ""
		if t == cur.Target {
			sel = " selected"
		}
		fmt.Fprintf(w, `<option value="%s"%s>%s</option>`, html.EscapeString(t), sel, html.EscapeString(config.SendLabel(t)))
	}
	keyNote := `Used for wakesmith and Custom: they ask for a bearer token. Tater Trainer asks for none.`
	if cur.Token != "" {
		keyNote = `Paste a new one to replace it.`
	}
	fmt.Fprintf(w, `</select>
	 <label for="wcurl">Address</label>
	 <input id="wcurl" name="url" value="%s" placeholder="%s" autocomplete="off">
	 <p class="note">For wakesmith, <code>http://&lt;host&gt;:8765</code> (or <code>https://</code>); for Tater Trainer,
	  <code>http://&lt;host&gt;:8789</code>; for a custom receiver, the address its <code>/api/clips</code> is under.</p>
	 %s
	 <p class="note">%s The token stays on this device and this page never shows it again.</p>
	 <p style="margin-bottom:.2em">Kinds of clip to send</p>`,
		html.EscapeString(cur.URL), wakeSendPlaceholder,
		secretField("wctoken", "Token", "bearer", cur.Token != "", "forgettoken"), keyNote)
	for _, k := range config.SendKinds {
		on := ""
		if cur.Sends(k) {
			on = " checked"
		}
		fmt.Fprintf(w, `<label style="display:inline-block;margin-right:1em"><input type="checkbox" name="kind" value="%s" style="width:auto"%s> %s</label>`,
			k, on, html.EscapeString(sendKindName(k)))
	}
	fmt.Fprint(w, `<p><button type="submit">Save</button></p></form>`)

	if cur.On() {
		fmt.Fprint(w, `<form method="post" action="/setup/save"`+busy(busyTrainer)+`>`)
		hidden(w, token, "wakeclips-test")
		fmt.Fprint(w, `<p><button type="submit" class="quiet">Test</button>
		 <span class="note">asks the saved address whether it is there</span></p></form>`)
		if fp := wakeCertSeen(); fp != "" && strings.HasPrefix(cur.URL, "https://") {
			fmt.Fprint(w, `<form method="post" action="/setup/save">`)
			hidden(w, token, "wakeclips-trust")
			fmt.Fprintf(w, `<input type="hidden" name="fingerprint" value="%s">
			 <p class="note">The trainer's certificate is not one this device trusts, as a self-signed one is not.
			  Its SHA-256 fingerprint is <code>%s</code>. If that is the one your trainer shows, trust it: clips are then sent
			  to a trainer showing exactly this certificate, and refused if it ever changes.</p>
			 <p><button type="submit">Trust this certificate</button></p></form>`, html.EscapeString(fp), html.EscapeString(fp))
		}
		wakeSendState(w)
	}
	fmt.Fprint(w, `</fieldset>`)
}

// wakeSendState says what sending is doing.
func wakeSendState(w http.ResponseWriter) {
	n, status := wakeClipsUnsent()
	if !wakeClipsKept() {
		if n == 0 {
			fmt.Fprint(w, `<p class="note">Keep wake clips is off, so nothing is kept or sent.</p>`)
			return
		}
		fmt.Fprintf(w, `<p class="note">Keep wake clips is off: only takes from Record wake word are sent. %d waiting to be sent.`, n)
		if status != "" {
			fmt.Fprintf(w, ` Last problem: %s.`, html.EscapeString(status))
		}
		fmt.Fprint(w, `</p>`)
		return
	}
	fmt.Fprintf(w, `<p class="note">%d clips waiting to be sent.`, n)
	if status != "" {
		fmt.Fprintf(w, ` Last problem: %s.`, html.EscapeString(status))
	}
	fmt.Fprint(w, `</p>`)
}

func sendKindName(k string) string {
	switch k {
	case "nearmiss":
		return "Near misses"
	case "mistake":
		return "Mistakes"
	case "noise":
		return "Noise"
	}
	return "Wakes"
}

// saveWakeSend keeps where clips go, once the address is one on the home network.
func saveWakeSend(r *http.Request) string {
	cur := config.Get().Wake.Send
	next := config.WakeSend{Target: r.PostFormValue("target"), Token: cur.Token, Since: cur.Since}
	if next.Target != config.SendOff && !slices.Contains(config.SendTargets, next.Target) {
		return "choose where to send clips from the list"
	}
	next.URL = strings.TrimSpace(r.PostFormValue("url"))
	if next.Target != config.SendOff && next.URL == "" {
		return "enter the trainer's address"
	}
	// The address as it is kept, so it is compared as it will be saved. One that does not pass is left as
	// typed for the save to say why, or, with nothing sent, kept so that it is not typed again.
	if u, err := detect.CheckSendURL(next.URL); err == nil {
		next.URL = u
	}
	for _, k := range config.SendKinds {
		if !slices.Contains(r.PostForm["kind"], k) {
			next.Skip = append(next.Skip, k)
		}
	}
	token := strings.TrimSpace(r.PostFormValue("bearer"))
	if strings.ContainsAny(token, " \r\n\t") || len(token) > 500 {
		return "the token is one word, with no spaces"
	}
	switch {
	case r.PostFormValue("forgettoken") == "yes":
		next.Token = ""
	case token != "":
		next.Token = token
	case sameAddress(cur.URL, next.URL):
		// kept
	case cur.Token != "" && next.Target != config.SendTater && next.Target != config.SendOff:
		// A kept token goes only to the address it was given for: say so, rather than drop it unseen and
		// have the trainer refuse every clip.
		return "the address changed, so paste the token again: a token is only sent to the address it was given for"
	default:
		next.Token = ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), wakeSendAsk)
	defer cancel()
	if err := setWakeSend(ctx, next); err != nil {
		return err.Error()
	}
	host := ""
	if u, err := url.Parse(next.URL); err == nil {
		host = u.Host
	}
	slog.Info("setup page: sending wake clips was set", "target", next.Target, "host", host)
	return ""
}

func sameAddress(a, b string) bool {
	return a != "" && strings.TrimRight(a, "/") == strings.TrimRight(b, "/")
}

// saveKeepWake turns Keep wake clips on or off. Off deletes every clip on the device, so it is refused
// unless the box that says so is ticked, and then nothing changes.
func saveKeepWake(r *http.Request) string {
	on := r.PostFormValue("keep") == "on"
	if !on && wakeClipsKept() && r.PostFormValue("confirm") != "yes" {
		return "to turn Keep wake clips off, tick Delete the wake clips on this device: it deletes every wake clip that was kept, the ones not yet sent too"
	}
	if on == wakeClipsKept() {
		return ""
	}
	if err := setKeepClips(on); err != nil {
		return err.Error()
	}
	slog.Info("setup page: keep wake clips was set", "on", on)
	return ""
}

// trustWakeCert pins the certificate the last Test was shown, once the trainer shows it again.
func trustWakeCert(r *http.Request) string {
	ctx, cancel := context.WithTimeout(context.Background(), wakeSendAsk)
	defer cancel()
	if err := trustCert(ctx, strings.TrimSpace(r.PostFormValue("fingerprint"))); err != nil {
		return err.Error()
	}
	return ""
}

// testWakeSend asks the saved address whether a trainer is there. What it found is the page's answer
// when it is a refusal; a trainer that answered is "The trainer answered."
func testWakeSend(r *http.Request) string {
	cur := config.Get().Wake.Send
	if !cur.On() {
		return "choose where to send clips and save it first"
	}
	ctx, cancel := context.WithTimeout(context.Background(), wakeSendAsk)
	defer cancel()
	if _, err := pingTrainer(ctx, cur); err != nil {
		return err.Error()
	}
	return ""
}
