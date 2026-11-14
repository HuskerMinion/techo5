package setup

import (
	"context"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/firmware"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
)

// updatesSection is the device's own software: what runs, what is offered, a button to install it, and
// installing by itself overnight - for a device with no Home Assistant to offer updates on.
func updatesSection(w http.ResponseWriter, token string) {
	fw := firmware.Get()
	fieldsetOpen(w, "updates", "Updates")
	fmt.Fprintf(w, `<p style="margin:0">Running <strong>%s</strong>.`,
		html.EscapeString(layout.Version))
	if on, at := fw.Installing(); on {
		fmt.Fprintf(w, ` Installing <strong>%s</strong>: <span id="upat">%d%%</span> downloaded.</p>
		 <p class="note">The device restarts into it when the download is done, and is back in about a
		  minute. This page checks by itself; after the restart, ask to be let in again.</p>
		 <script>setTimeout(()=>location.reload(),3000)</script></fieldset>`, html.EscapeString(fw.Offered()), int(at*100))
		return
	}
	if why := fw.Failed(); why != "" {
		fmt.Fprintf(w, `</p><div class="banner bad">The last install did not work: %s</div><p style="margin:0">`, html.EscapeString(why))
	}
	if v := fw.Offered(); v != "" {
		fmt.Fprintf(w, ` <strong>%s</strong> is ready.</p>`, html.EscapeString(v))
		fmt.Fprintf(w, `<form method="post" action="/setup/save"%s>`, busy(busyInstall))
		hidden(w, token, "update-install")
		fmt.Fprint(w, `<p><button type="submit">Install now</button></p>
		 <p class="note">The device restarts into it, back in about a minute, and this page with it. If it
		  does not start properly it goes back to the version it had.</p></form>`)
	} else {
		fmt.Fprint(w, ` Nothing newer is waiting.</p>`)
		fmt.Fprintf(w, `<form method="post" action="/setup/save"%s>`, busy(busyCheck))
		hidden(w, token, "update-check")
		fmt.Fprint(w, `<p><button type="submit">Check now</button></p></form>`)
	}
	fmt.Fprint(w, `<form method="post" action="/setup/save">`)
	hidden(w, token, "update-auto")
	checked := ""
	if fw.AutoInstall() {
		checked = " checked"
	}
	fmt.Fprintf(w, `<p><label><input type="checkbox" name="auto" value="yes"%s style="width:auto"> Install updates
	 automatically, overnight between 2 and 5 in the morning while nothing is playing</label></p>
	 <p><button type="submit">Save</button></p></form></fieldset>`, checked)
}

func saveUpdates(what string, auto bool) string {
	fw := firmware.Get()
	switch what {
	case "update-install":
		if fw.Offered() == "" {
			return "there is no update to install"
		}
		slog.Info("setup page: installing the offered update", "version", fw.Offered())
		safe.Go("update install from the setup page", func() { fw.Install(context.Background()) })
	case "update-check":
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		fw.Check(ctx)
	case "update-auto":
		fw.SetAutoInstall(auto)
	}
	return ""
}
