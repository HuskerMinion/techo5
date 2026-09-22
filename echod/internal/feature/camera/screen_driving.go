//go:build !dot

package camera

import "net/http"

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
