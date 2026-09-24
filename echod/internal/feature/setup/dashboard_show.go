//go:build !dot

package setup

import (
	"fmt"
	"html"
	"net/http"
	"strings"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/dashboard"
)

// dashboardSection is where a streamed dashboard comes from: the dashcast server's address and the key
// it asks for. Here as well as in the dashboard_server action, since a key is miserable to get into
// an action by hand. The key once saved is never shown again, only whether there is one.
func dashboardSection(w http.ResponseWriter, token string) {
	d := config.Get().Dashboard
	keyNote := "No key saved yet."
	if d.Key != "" {
		keyNote = "A key is saved. Leave this empty to keep it."
	}
	fmt.Fprint(w, `<fieldset><legend>Dashboard server</legend><form method="post" action="/setup/save">`)
	hidden(w, token, "dashboard", "connections")
	fmt.Fprintf(w, `<label for="dashaddr">Address</label>
	 <input id="dashaddr" name="address" value="%s" placeholder="192.168.1.20:9555" autocomplete="off">
	 <label for="dashkey">Key</label>
	 <input id="dashkey" name="key" type="password" autocomplete="off">
	 <p class="note">%s</p>
	 <p class="note">Only for a <strong>streamed</strong> dashboard, which a dashcast server draws: set
	  <strong>Dashboard</strong> to <strong>Streamed</strong> in Home Assistant. A dashboard drawn on the
	  device needs no server.</p>
	 <p><button type="submit">Save</button></p></form></fieldset>`,
		html.EscapeString(d.Server), html.EscapeString(keyNote))
}

// saveDashboard keeps the server, and the key when a new one was typed.
func saveDashboard(r *http.Request) string {
	addr := strings.TrimSpace(r.PostFormValue("address"))
	key := strings.TrimSpace(r.PostFormValue("key"))
	if key == "" {
		key = config.Get().Dashboard.Key
	}
	if addr != "" && !strings.Contains(addr, ":") {
		addr += ":9555"
	}
	if err := dashboard.Get().SetServer(addr, key); err != nil {
		return "could not save it: " + err.Error()
	}
	return ""
}
