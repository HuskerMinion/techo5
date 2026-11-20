//go:build !dot

package camera

import (
	"crypto/subtle"
	"net/http"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/web"
)

// The parameters that do something to the device rather than only read it: they open sheets, switch
// the palette, put the Wi-Fi keyboard up and start and stop a radio station. They are here so that
// the screen can be driven from a PC to photograph it, which is worth having; but the switch in
// front of this page is "Screen web access", and letting the network read the screen is not the same
// permission as letting it work the screen. So anything in this list needs the setup page's session
// too — the press on the device, made by somebody standing at it — while a plain screenshot needs
// only the switch, which is what the page is for.
var driven = []string{"demo", "theme", "wifi", "radio", "weather", "sheet", "alarm", "list", "ring"}

// driving reports whether this request asks for any of them.
func driving(r *http.Request) bool {
	q := r.URL.Query()
	for _, name := range driven {
		if q.Get(name) != "" {
			return true
		}
	}
	return false
}

// screenRefused is why /screen.png turns this request away, or "" to answer it. A browser the setup page
// has let in may read and drive the screen. Anything else needs the page's key (?key=, config
// ScreenKey, shown on the setup page) and may only read: the page has no login otherwise, and the
// screen can show the camera's picture.
func screenRefused(r *http.Request) string {
	if web.LetIn(r) {
		return ""
	}
	if driving(r) {
		return "those options need a browser the setup page has let in"
	}
	if !screenKeyOK(r.URL.Query().Get("key")) {
		return "this page needs its key: copy the address from the setup page"
	}
	return ""
}

// screenKeyOK is whether a key is /screen.png's, compared in constant time.
func screenKeyOK(got string) bool {
	want := config.Get().Security.ScreenKey
	return want != "" && subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}
