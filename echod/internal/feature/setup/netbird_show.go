//go:build !dot

package setup

import (
	"context"
	"errors"
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
	case st.Joining:
		// The key is held in memory: the page follows the download and the join by reloading.
		if st.Downloading {
			where = fmt.Sprintf("Downloading NetBird (%d%%). It will join by itself when the download is done.", int(st.Got*100))
		} else {
			where = "Joining NetBird."
		}
		where += joiningReload
	case st.JoinProblem != "":
		where = "Could not join: " + html.EscapeString(st.JoinProblem) + "."
	case st.On && st.Downloading:
		where = fmt.Sprintf("Downloading NetBird (%d%%).", int(st.Got*100))
	case st.On && st.Problem != "":
		where = html.EscapeString(st.Problem)
	case st.On:
		where = "Starting."
	}
	netbirdForm(w, token, where, netbirdSummary(st)...)
}

// netbirdSummary is the line on the shut card: where the device stands on NetBird, in a word or two and
// its name there. No setup key is kept to say.
func netbirdSummary(st netbird.Status) []string {
	switch {
	case st.State == "Connected" && st.Name != "":
		return []string{"On as " + st.Name}
	case st.Joining && st.Downloading:
		return []string{"Downloading", fmt.Sprintf("%d%%", int(st.Got*100))}
	case st.Joining:
		return []string{"Joining"}
	case st.JoinProblem != "":
		return []string{"Could not join"}
	case st.On && st.Downloading:
		return []string{"On", fmt.Sprintf("downloading %d%%", int(st.Got*100))}
	case st.On && st.Problem != "":
		return []string{"On", "not connected"}
	case st.On:
		return []string{"Starting"}
	}
	return []string{"Off"}
}

// joiningReload reloads the page every 3 s while a join is under way, but not once anything on the page
// has been typed or changed: a reload would lose it.
const joiningReload = `<script>var nbDirty=false;addEventListener('input',()=>{nbDirty=true});addEventListener('change',()=>{nbDirty=true});setInterval(()=>{if(!nbDirty)location.reload()},3000)</script>`

// netbirdForm is the section's form, with where the device stands said above it (as HTML) and on its
// shut card (summary). Joining holds the request for up to two minutes, so the form says it is at work
// (script.go).
func netbirdForm(w http.ResponseWriter, token, where string, summary ...string) {
	sectionOpen(w, "netbird", "NetBird", summary...)
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
	 <p><button type="submit">Join</button></p></form>`,
		where, onceField("nbkey", "Setup key", "key", "required"),
		html.EscapeString(config.Get().Netbird.Management), html.EscapeString(netbird.DefaultManagement))
	sectionClose(w)
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
		var parked *netbird.Parked
		if errors.As(err, &parked) {
			return "" // held; the section says where the download stands and joins by itself
		}
		return "could not join: " + err.Error()
	}
	return ""
}
