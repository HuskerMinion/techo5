package setup

import (
	"fmt"
	"html"
	"log/slog"
	"net/http"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/babymon"
	"github.com/HuskerMinion/techo5/echod/internal/feature/security"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
)

func init() {
	saves["babylink"] = saveBabyLink
	saves["babylight"] = saveBabyLight
}

// babySection is the baby monitor (feature/babymon), on the Privacy & Security tab of a device with
// microphones: whether it is on, the link to listen with, and a new link. The switch itself is on the
// device's screen and in Home Assistant, not here: this page never turns a microphone on.
func babySection(w http.ResponseWriter, token string) {
	if !layout.HasMicrophone() {
		return
	}
	fmt.Fprint(w, `<fieldset id="baby"><legend>Baby monitor</legend>`)
	s := config.Get().Security
	if !s.Baby || s.BabyKey == "" {
		fmt.Fprint(w, `<p style="margin:0">The baby monitor is off. Turn on <strong>Baby monitor</strong> under
		 Privacy &amp; Security, on the device's screen or in Home Assistant, and its link shows here.</p>`)
	} else {
		n := babymon.Get().Count()
		fmt.Fprintf(w, `<p style="margin:0">The baby monitor is <strong>on</strong>: %d listening now (up to %d).</p>
		 <p>Open this link in a browser on your Wi-Fi, and tap Listen:</p>
		 <p><a id="babylink" href="/baby?k=%s" style="word-break:break-all">/baby?k=%s</a></p>
		 <script>var a=document.getElementById('babylink');a.textContent=location.origin+a.getAttribute('href');</script>
		 <form method="post" action="/setup/save">`, n, babymon.Max, html.EscapeString(s.BabyKey), html.EscapeString(s.BabyKey))
		hidden(w, token, "babylink", "privacy")
		fmt.Fprint(w, `<p><button type="submit">New link</button></p></form>`)
	}
	fmt.Fprint(w, `<p class="note">With it on, anyone on your home network who has the link can hear this room, live, in
	 a browser: the same sound the wake word hears, after echo cancellation. Keep the link private, as you
	 would a password. It is not encrypted on the way, as this page is not. Up to three can listen at once. A Show
	 or a Spot draws an amber dot on its screen and a Dot pulses its ring in amber while anyone listens,
	 unless <strong>Baby monitor light</strong> below says otherwise. Home Assistant has a Listening now
	 sensor, and the mute button stops the sound at once. A new link ends every listener and stops the old
	 link working. Voice keeps working while someone listens.</p>`)
	babyLightForm(w, token, s.BabyLight)
	fmt.Fprint(w, `</fieldset>`)
}

// babyLightForm is the Baby monitor light choice, with what Dimmest and Off give up.
func babyLightForm(w http.ResponseWriter, token, now string) {
	fmt.Fprint(w, `<form method="post" action="/setup/save">`)
	hidden(w, token, "babylight", "privacy")
	fmt.Fprint(w, `<label for="babylight">Baby monitor light</label><select id="babylight" name="babylight">`)
	for _, name := range babymon.LightNames() {
		fmt.Fprintf(w, `<option%s>%s</option>`, selected(name == babymon.LightName(now)), html.EscapeString(name))
	}
	fmt.Fprint(w, `</select>
	 <p class="note"><strong>Warning:</strong> this is how the room can tell that someone is listening.
	 <strong>Dimmest</strong> keeps the mark at its least light: a dark amber dot on a Show's or a Spot's
	 screen, which stays at its dimmest backlight, or one dim segment of a Dot's ring, steady.
	 <strong>Off</strong> shows nothing at all, and a dark screen stays dark, so nobody in the room can see
	 that the microphones are being heard. Use them only in a room where everyone knows the monitor is
	 there, such as a sleeping baby's. It changes only the baby monitor's light: the camera's and every
	 other light stay as they are. Home Assistant's Listening now sensor still says when someone listens.</p>
	 <p><button type="submit">Save</button></p></form>`)
}

func saveBabyLight(r *http.Request) string {
	if err := babymon.Get().SetLight(r.PostFormValue("babylight")); err != nil {
		return err.Error()
	}
	slog.Info("setup page: baby monitor light", "value", babymon.LightName(config.Get().Security.BabyLight))
	return ""
}

func saveBabyLink(r *http.Request) string {
	if !config.Get().Security.Baby {
		return "The baby monitor is off"
	}
	if err := security.Get().ReplaceBabyKey(); err != nil {
		return err.Error()
	}
	slog.Info("setup page: baby monitor link replaced")
	return ""
}
