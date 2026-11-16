//go:build !dot

package setup

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"strings"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/netbird"
)

// netbirdSection is where the device is given a setup key for a NetBird network, and the management
// server if it is the owner's own, and says where it stands there. The key is used once, to join, and
// never kept or shown.
func netbirdSection(w http.ResponseWriter, token string) {
	if !netbird.Available() {
		return
	}
	st := netbird.Get().State()
	where := "Not on a NetBird network. The first join downloads NetBird (about 14 MB)."
	switch {
	case st.State == "Connected" && st.Name != "":
		where = "On NetBird as <strong>" + html.EscapeString(st.Name) + "</strong> (" + html.EscapeString(st.IP) + ")."
	case st.On && st.Downloading:
		where = fmt.Sprintf("Downloading NetBird (%d%%).", int(st.Got*100))
	case st.On && st.Problem != "":
		where = html.EscapeString(st.Problem)
	case st.On:
		where = "Starting."
	}
	netbirdForm(w, token, where)
	netbirdRoutesForm(w, token, st)
}

// netbirdForm is the section's form, with where the device stands said above it (as HTML). Joining
// holds the request for up to two minutes, so the form says it is at work (script.go).
func netbirdForm(w http.ResponseWriter, token, where string) {
	fieldsetOpen(w, "netbird", "NetBird")
	fmt.Fprintf(w, `<form method="post" action="/setup/save"%s>`, busy(busyNetbird))
	hidden(w, token, "netbird")
	fmt.Fprintf(w, `<p class="note">%s</p>
	 %s
	 <label for="nbmgmt">Management server</label>
	 <input id="nbmgmt" name="management" type="url" inputmode="url" value="%s" placeholder="%s">
	 <p class="note">A setup key from your NetBird dashboard. Leave the management server empty for NetBird's
	  own; put in your own if you run one. The key joins the device once and is not kept; after that the
	  device rejoins by itself. Over NetBird, as over Tailscale, Home Assistant reaches the device on its
	  encrypted link, and nothing else answers but the setup page while Remote setup is on.</p>
	 <p><button type="submit">Join</button></p></form></fieldset>`,
		where, onceField("nbkey", "Setup key", "key", "required"),
		html.EscapeString(config.Get().Netbird.Management), html.EscapeString(netbird.DefaultManagement))
}

// saveNetbird joins with the key and server typed.
func saveNetbird(r *http.Request) string {
	key := strings.TrimSpace(r.PostFormValue("key"))
	if key == "" {
		return "type the setup key first"
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	if err := netbird.Get().Login(ctx, key, r.PostFormValue("management")); err != nil {
		return "could not join: " + err.Error()
	}
	return ""
}

// netbirdRoutesForm lists the networks the device may reach through the mesh's routes (feature/netbird,
// routes.go), once it has joined: none at first, and then it takes no routes at all.
func netbirdRoutesForm(w http.ResponseWriter, token string, st netbird.Status) {
	c := config.Get().Netbird
	if !c.Joined {
		return
	}
	inUse := "None in use."
	if len(st.Routes) > 0 {
		inUse = "In use: " + html.EscapeString(strings.Join(st.Routes, ", ")) + "."
	}
	text := strings.Join(c.Routes, "\n")
	fieldsetOpen(w, "netbirdroutes", "Routes from NetBird")
	fmt.Fprint(w, `<form method="post" action="/setup/save">`)
	hidden(w, token, "netbirdroutes")
	fmt.Fprintf(w, `<label for="nbroutes">Networks</label>
	 <textarea id="nbroutes" name="routes" rows="3" maxlength="1000" placeholder="192.168.1.0/24">%s</textarea>
	 <p class="note">%s Networks this device may reach through routes your NetBird network offers it, such
	  as the LAN at home behind a routing peer, one a line. Empty, as it starts, takes no routes at all. Only
	  routes to these networks are used, whatever else the network offers; which routes it offers is up to
	  your NetBird dashboard. The device only uses them itself: it routes nothing into NetBird and is no exit
	  node. Turning routes on or off restarts NetBird, which takes a few seconds.</p>
	 <p><button type="submit">Save</button></p></form></fieldset>`,
		html.EscapeString(text), inUse)
}

// saveNetbirdRoutes keeps the networks typed.
func saveNetbirdRoutes(r *http.Request) string {
	routes, err := netbird.ParseRoutes(r.PostFormValue("routes"))
	if err != nil {
		return err.Error()
	}
	if strings.Join(routes, " ") == strings.Join(config.Get().Netbird.Routes, " ") {
		return "" // nothing changed
	}
	if err := netbird.Get().SetRoutes(routes); err != nil {
		return err.Error()
	}
	return ""
}
