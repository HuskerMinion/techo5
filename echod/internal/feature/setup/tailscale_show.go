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
	case st.State == "Running" && st.Name != "" && st.Control != "":
		where = "On the tailnet as <strong>" + html.EscapeString(st.Name) + "</strong> (" + html.EscapeString(st.IP) +
			"), through the Headscale at " + html.EscapeString(st.Control) + "."
	case st.State == "Running" && st.Name != "":
		where = "On the tailnet as <strong>" + html.EscapeString(st.Name) + "</strong> (" + html.EscapeString(st.IP) + ")."
	case st.On && st.Problem != "":
		where = html.EscapeString(st.Problem)
	case st.On:
		where = "Starting."
	}
	tailscaleForm(w, token, where)
}

// tailscaleForm is the section's form, with where the device stands said above it (as HTML). Joining
// holds the request until the tailnet has answered, so the form says it is at work (script.go).
func tailscaleForm(w http.ResponseWriter, token, where string) {
	fieldsetOpen(w, "tailscale", "Tailscale")
	fmt.Fprintf(w, `<form method="post" action="/setup/save"%s>`, busy(busyTailscale))
	hidden(w, token, "tailscale")
	fmt.Fprintf(w, `<p class="note">%s</p>
	 %s
	 <label for="tscontrol">Headscale server</label>
	 <input id="tscontrol" name="control" type="url" inputmode="url" autocomplete="off" maxlength="253" placeholder="https://headscale.example.com">
	 <p class="note">An auth key from the Tailscale admin console, tagged <strong>tag:techo5</strong>. It joins
	  the device to your tailnet once and is not kept; after that the device rejoins by itself. Over the
	  tailnet, Home Assistant reaches the device on its encrypted link, and nothing else answers but the
	  setup page while Remote setup is on.</p>
	 <p class="note">With your own Headscale, give its address and a pre-auth key from it
	  (<code>headscale preauthkeys create</code>); leave the server empty for Tailscale's.</p>
	 <p><button type="submit">Join</button></p></form></fieldset>`, where,
		onceField("tskey", "Auth key", "key", `required placeholder="tskey-auth-..."`))
}

// saveTailscale joins the tailnet with the key typed, at the Headscale typed if one is.
func saveTailscale(r *http.Request) string {
	key := strings.TrimSpace(r.PostFormValue("key"))
	if key == "" {
		return "type the auth key first"
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if err := tailscale.Get().Login(ctx, key, r.PostFormValue("control")); err != nil {
		return "could not join: " + err.Error()
	}
	return ""
}
