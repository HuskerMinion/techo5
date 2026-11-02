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

func init() { saves["babylink"] = saveBabyLink }

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
	 Home Assistant has a Listening now sensor, and the mute button stops the sound at once. A new link ends
	 every listener and stops the old link working. Voice keeps working while someone listens.</p></fieldset>`)
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
