package setup

import (
	"fmt"
	"html"
	"log/slog"
	"net/http"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/security"
)

func init() { saves["screenkey"] = saveScreenKey }

// screenPageSection is /screen.png's address, key and all, on the Privacy tab of a device with a
// screen: the page answers nothing else (feature/camera screenRefused). The switch is on the device's
// screen and in Home Assistant, as Screen web access, not here.
func screenPageSection(w http.ResponseWriter, token string) {
	if !security.ScreenPage() {
		return
	}
	s := config.Get().Security
	// The key is never on the card's line: the address it makes is as good as a password.
	sectionOpen(w, "screenpage", "Screen page", onOff(s.Screen && s.ScreenKey != ""))
	if !s.Screen || s.ScreenKey == "" {
		fmt.Fprint(w, `<p style="margin:0">The screen page is off. Turn on <strong>Screen web access</strong> under
		 Privacy &amp; Security, on the device's screen or in Home Assistant, and its address shows here.</p>`)
	} else {
		k := html.EscapeString(s.ScreenKey)
		fmt.Fprintf(w, `<p style="margin:0">The screen page is <strong>on</strong>. Its address, with its key:</p>
		 <p><a id="screenlink" href="/screen.png?key=%s" style="word-break:break-all">/screen.png?key=%s</a></p>
		 <script>var s=document.getElementById('screenlink');s.textContent=location.origin+s.getAttribute('href');</script>
		 <form method="post" action="/setup/save">`, k, k)
		hidden(w, token, "screenkey")
		fmt.Fprint(w, `<p><button type="submit" class="danger">New key</button></p></form>`)
	}
	fmt.Fprint(w, `<p class="note">In Home Assistant, a Generic Camera with this address as its still image URL shows
	 the screen. Without the key the page answers nothing, so keep the address private, as you would a
	 password: anyone on your home network who has it can see the screen, and the screen can show the camera.
	 It is not encrypted on the way, as this page is not. A new key stops the old address working; give
	 Home Assistant the new one.</p>`)
	sectionClose(w)
}

func saveScreenKey(*http.Request) string {
	if !config.Get().Security.Screen {
		return "Screen web access is off"
	}
	if err := security.Get().ReplaceScreenKey(); err != nil {
		return err.Error()
	}
	slog.Info("setup page: screen page key replaced")
	return ""
}
