//go:build !dot

package setup

import (
	"fmt"
	"html"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/dashboard"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// dashboardSection is where a streamed dashboard comes from: the dashcast server's address and the key
// it asks for. Here as well as in the dashboard_server action, since a key is miserable to get into
// an action by hand. The key once saved is never shown again, only whether there is one.
func dashboardSection(w http.ResponseWriter, token string) {
	d := config.Get().Dashboard
	sectionOpen(w, "dashboard", "Dashboard server", dashboardServerSummary(d, lastCheck.Load())...)
	fmt.Fprint(w, `<form method="post" action="/setup/save">`)
	hidden(w, token, "dashboard")
	fmt.Fprintf(w, `<label for="dashaddr">Address</label>
	 <input id="dashaddr" name="address" value="%s" placeholder="192.168.1.20:9555" autocomplete="off">
	 %s%s
	 <p class="note">Only for a <strong>streamed</strong> dashboard, which a dashcast server draws: set
	  <strong>Dashboard</strong> to <strong>Streamed</strong> above, or in Home Assistant.
	  A dashboard drawn on the device needs no server.</p>
	 <p><button type="submit">Save</button></p></form>`,
		html.EscapeString(d.Server), secretField("dashkey", "Key", "key", d.Key != "", ""), checkNote(d.Server))
	sectionClose(w)
}

// dashboardServerSummary is the line on the shut card: whether a server is set, whether it has a key,
// and what the last save's check found of it. The key is never said.
func dashboardServerSummary(d config.Dashboard, c *serverCheck) []string {
	if d.Server == "" {
		return []string{"Not set up"}
	}
	out := []string{"Set up"}
	if d.Key == "" {
		out = append(out, "no key")
	}
	if c != nil && strings.Contains(c.said, d.Server) {
		if c.ok {
			out = append(out, "working")
		} else {
			out = append(out, "not working")
		}
	}
	return out
}

// serverCheck is the dashboard server as the last save found it (dashboard.Check).
type serverCheck struct {
	said string
	ok   bool
	at   time.Time
}

// lastCheck is the last save's check, nil before one; it is shown while the address it was for is the
// one saved.
var lastCheck atomic.Pointer[serverCheck]

// checkNote is what the last check found, said beside the server it was for; nothing before one.
func checkNote(server string) string {
	c := lastCheck.Load()
	if c == nil || server == "" || !strings.Contains(c.said, server) {
		return ""
	}
	mark := "Not working"
	if c.ok {
		mark = "Working"
	}
	return fmt.Sprintf(`
	 <p class="note"><strong>%s</strong> (checked at %s, on Save): %s</p>`, mark, c.at.Format("3:04 PM"), html.EscapeString(c.said))
}

// dashboardPanelSection is the dashboard as a panel by a door wants it, on the Screen tab:
// whether there is one and how it is shown, whether it stands in for the clock, how big its tiles are
// and how they are laid out on the Show, and how long one opened by hand stays up. The same settings are in Home Assistant;
// which dashboard to show is there too, where the list of them is.
func dashboardPanelSection(w http.ResponseWriter, token string) {
	d := config.Get().Dashboard
	sectionOpen(w, "dashpanel", "Dashboard", dashboardPanelSummary(dashboard.Get().Mode(), d.Idle)...)
	fmt.Fprint(w, `<form method="post" action="/setup/save">`)
	hidden(w, token, "dashpanel")
	fmt.Fprint(w, `<label for="dashmode">Dashboard</label><select id="dashmode" name="mode">`)
	for _, m := range dashboard.Modes() {
		// The mode in force, as the dashboard reads it: one this device does not have is Off here too.
		fmt.Fprintf(w, `<option value="%s"%s>%s</option>`, m, selected(m == dashboard.Get().Mode()), html.EscapeString(m.Label()))
	}
	fmt.Fprint(w, `</select>`)
	if dashboard.HasTiles() {
		fmt.Fprint(w, `<label for="dashtiles">Tiles</label><select id="dashtiles" name="tiles">`)
		now := dashboard.Tiles()
		for _, l := range dashboard.TilesChoices() {
			fmt.Fprintf(w, `<option%s>%s</option>`, selected(l == now), html.EscapeString(l))
		}
		fmt.Fprint(w, `</select>`)
		fmt.Fprint(w, `<label for="dashlayout">Tile layout</label><select id="dashlayout" name="layout">`)
		layout := dashboard.Layout()
		for _, l := range dashboard.LayoutChoices() {
			fmt.Fprintf(w, `<option%s>%s</option>`, selected(l == layout), html.EscapeString(l))
		}
		fmt.Fprint(w, `</select>`)
	}
	fmt.Fprint(w, `<label for="dashback">Back to the clock after</label><select id="dashback" name="back">`)
	now := dashboard.Back()
	for _, l := range dashboard.BackChoices() {
		fmt.Fprintf(w, `<option%s>%s</option>`, selected(l == now), html.EscapeString(l))
	}
	tesseraNote := ""
	if slices.Contains(dashboard.Modes(), config.DashboardTessera) {
		tesseraNote = ` <strong>Tessera</strong> shows the layout the Tessera add-on sends, with
	  <strong>Tessera screen</strong> on in Home Assistant.`
	}
	idle := ""
	if d.Idle {
		idle = " checked"
	}
	handle := ""
	if dashboard.HasTiles() { // the Show draws the handle; the Spot's dashboard has none
		handle = `<p><label><input type="checkbox" name="handle" value="yes" style="width:auto"` + checked(dashboard.Handle()) +
			`> Show the handle on the dashboard's left edge</label> <span class="note">Hidden, a swipe from the edge still leaves it.</span></p>`
	}
	fmt.Fprintf(w, `</select>
	 <p><label><input type="checkbox" name="idle" value="yes" style="width:auto"%s> Show it in place of the clock when idle</label></p>
	 %s
	 <p class="note"><strong>Drawn on the device</strong> needs no server: it reads your dashboards with this
	  device's Home Assistant token. <strong>Streamed</strong> needs a
	  dashcast server, set below.%s <strong>Back to the clock after</strong> is for a dashboard
	  opened by hand; one Home Assistant shows stays until it is hidden.</p>
	 %s`, idle, handle, tesseraNote, tokenNote())
	alsoOnRow(w, token, "dashpanel")
	fmt.Fprint(w, `<p><button type="submit">Save</button></p></form>`)
	sectionClose(w)
}

// tokenNote says whether the device has the Home Assistant access a drawn dashboard is read with, and
// if not, where it gets one: Connect to Home Assistant on the Network tab (ha_signin.go). People who
// went looking for a token field here found none (#551).
func tokenNote() string {
	if hass.Get().Ready() {
		return `<p class="note">This device has access to Home Assistant. It is on the
	  <a href="/setup?tab=network&amp;open=haaccess#sec-haaccess">Network</a> tab.</p>`
	}
	return `<p class="bad"><strong>This device has no access to Home Assistant yet</strong>, so a drawn
	  dashboard can't be shown.</p>
	 <p class="note">Choose <strong>Connect to Home Assistant</strong> on the
	  <a href="/setup?tab=network&amp;open=haaccess#sec-haaccess">Network</a> tab and sign in.</p>`
}

// saveDashboardPanel keeps what dashboardPanelSection asked, refusing a choice the page did not offer.
func saveDashboardPanel(r *http.Request) string {
	f := dashboard.Get()
	mode := config.DashboardMode(r.PostFormValue("mode"))
	if !slices.Contains(dashboard.Modes(), mode) {
		return "that is not a way this device shows a dashboard"
	}
	back := r.PostFormValue("back")
	if !slices.Contains(dashboard.BackChoices(), back) {
		return "that is not a time the dashboard can stay up"
	}
	tiles, layout := "", ""
	if dashboard.HasTiles() {
		tiles = r.PostFormValue("tiles")
		if !slices.Contains(dashboard.TilesChoices(), tiles) {
			return "that is not a tile size this device has"
		}
		// A form from before the setting, or one that leaves it out, leaves it as it is.
		if posted, ok := r.PostForm["layout"]; ok && len(posted) > 0 {
			layout = posted[0]
			if !slices.Contains(dashboard.LayoutChoices(), layout) {
				return "that is not a tile layout this device has"
			}
		}
	}
	if mode != f.Mode() {
		if err := f.ChooseMode(mode); err != nil {
			return "could not save it: " + err.Error()
		}
	}
	if on := r.PostFormValue("idle") == "yes"; on != f.Idle() {
		if err := f.ChooseIdle(on); err != nil {
			return "could not save it: " + err.Error()
		}
	}
	if dashboard.HasTiles() {
		if on := r.PostFormValue("handle") == "yes"; on != dashboard.Handle() {
			if err := f.ChooseHandle(on); err != nil {
				return "could not save it: " + err.Error()
			}
		}
	}
	if back != dashboard.Back() {
		f.SetBack(back)
	}
	if tiles != "" && tiles != dashboard.Tiles() {
		f.SetTiles(tiles)
	}
	if layout != "" && layout != dashboard.Layout() {
		f.SetLayout(layout)
	}
	return ""
}

// saveDashboard keeps the server, and the key when a new one was typed. The address is cleaned up
// by SetServer (a pasted https://…/ becomes host:port), and one it cannot make sense of is said
// here rather than saved.
func saveDashboard(r *http.Request) string {
	addr := r.PostFormValue("address")
	key := strings.TrimSpace(r.PostFormValue("key"))
	if key == "" {
		key = config.Get().Dashboard.Key
	}
	if err := dashboard.Get().SetServer(addr, key); err != nil {
		return "could not save it: " + err.Error()
	}
	// Tried at once, so a wrong address or key is said here rather than found as an empty screen.
	d := config.Get().Dashboard
	if d.Server == "" {
		lastCheck.Store(nil)
		return ""
	}
	said, ok := dashboard.Check(d.Server, d.Key)
	lastCheck.Store(&serverCheck{said: said, ok: ok, at: time.Now()})
	if !ok {
		return "Saved, but " + strings.ToLower(said[:1]) + said[1:]
	}
	return ""
}

// dashboardPanelSummary is the line on the shut card: how the dashboard is shown, or Off, and whether
// it stands in for the clock.
func dashboardPanelSummary(mode config.DashboardMode, idle bool) []string {
	if mode == config.DashboardOff {
		return []string{"Off"}
	}
	out := []string{mode.Label()}
	if idle {
		out = append(out, "in place of the clock")
	}
	return out
}
