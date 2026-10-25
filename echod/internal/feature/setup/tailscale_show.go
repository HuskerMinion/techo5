//go:build !dot

package setup

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"strings"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/tailscale"
)

// hasTailnet is whether this device can be on the tailnet at all: whether its image carries Tailscale.
func hasTailnet() bool { return tailscale.Available() }

// tailscaleSection is where the device is given its key for the tailnet, and says where it stands on
// it. The key is used once, to join, and never kept or shown: what stays is the device's own identity
// on the tailnet, which Tailscale keeps.
func tailscaleSection(w http.ResponseWriter, token string) {
	if !hasTailnet() {
		return
	}
	st := tailscale.Get().State()
	where := "Not on a tailnet."
	switch {
	case st.State == "Running" && st.Name != "":
		where = "On the tailnet as <strong>" + html.EscapeString(st.Name) + "</strong> (" + html.EscapeString(st.IP) + ")."
	case st.On && st.Problem != "":
		where = html.EscapeString(st.Problem)
	case st.On:
		where = "Starting."
	}
	fmt.Fprint(w, `<fieldset><legend>Tailscale</legend><form method="post" action="/setup/save">`)
	hidden(w, token, "tailscale", "connections")
	fmt.Fprintf(w, `<p class="note">%s</p>
	 <label for="tskey">Auth key</label>
	 <input id="tskey" name="key" type="password" autocomplete="off" placeholder="tskey-auth-...">
	 <p class="note">An auth key from the Tailscale admin console, tagged <strong>tag:techo5</strong>. It joins
	  the device to your tailnet once and is not kept; after that the device rejoins by itself. Over the
	  tailnet, Home Assistant reaches the device on its encrypted link, and nothing else answers but the
	  setup page while Remote setup is on.</p>
	 <p><button type="submit">Join</button></p></form></fieldset>`, where)
}

// saveTailscale joins the tailnet with the key typed.
func saveTailscale(r *http.Request) string {
	key := strings.TrimSpace(r.PostFormValue("key"))
	if key == "" {
		return "type the auth key first"
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if err := tailscale.Get().Login(ctx, key); err != nil {
		return "could not join: " + err.Error()
	}
	return ""
}
