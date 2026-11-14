package setup

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/firmware"
	"github.com/HuskerMinion/techo5/echod/internal/feature/firstrun"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
	"github.com/HuskerMinion/techo5/echod/internal/lib/wifi"
)

// The Status tab is where the page opens: whether the device is on its network, whether Home Assistant
// is connected to it, what software it runs and whether newer is waiting, and when this page closes.
// A device with no screen has nowhere else to say any of it, and on every device these were spread over
// three tabs or not said at all (nothing told whether Home Assistant was connected).
//
// It shows and links; everything it names is changed on its own tab.

// statusHome is whether Home Assistant is connected to the device now; a variable for the tests.
var statusHome = func() bool { return firstrun.HomeConnected() }

// statusLine writes one line of the list: a dot (ok, bad, or plain for something that is neither), what
// it is about, and what is so. said is trusted HTML, made here from escaped parts.
func statusLine(w http.ResponseWriter, dot, about, said string) {
	fmt.Fprintf(w, `<p class="st"><span class="dot %s"></span><span><strong>%s</strong> %s</span></p>`,
		dot, html.EscapeString(about), said)
}

func statusSection(w http.ResponseWriter) {
	fmt.Fprint(w, `<fieldset><legend>Now</legend>`)

	if statusHome() {
		statusLine(w, "ok", "Home Assistant", "is connected.")
	} else {
		statusLine(w, "bad", "Home Assistant", `is not connected. Alarms, timers and what is set here keep
		 working without it; voice, the dashboard and its controls wait for it.
		 <a href="/setup?tab=general">General</a> has how it is added.`)
	}

	if wifi.Available() {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		st := wifi.Current(ctx)
		cancel()
		if st.Connected {
			statusLine(w, "ok", "Wi-Fi", fmt.Sprintf(`on <strong>%s</strong>, at %s.`,
				html.EscapeString(st.SSID), html.EscapeString(st.Address)))
		} else {
			statusLine(w, "bad", "Wi-Fi", fmt.Sprintf(`is not on a network (%s).
			 <a href="/setup?tab=connections">Connections</a> has the networks.`, html.EscapeString(st.State)))
		}
	}

	fw := firmware.Get()
	running := fmt.Sprintf(`<strong>%s</strong>.`, html.EscapeString(layout.Version))
	switch on, _ := fw.Installing(); {
	case on:
		statusLine(w, "", "Software", running+` An update is installing; the device restarts when it is done.`)
	case fw.Failed() != "":
		statusLine(w, "bad", "Software", running+` The last update did not install.
		 <a href="/setup?tab=general">General</a> says why.`)
	case fw.Offered() != "":
		statusLine(w, "", "Software", running+fmt.Sprintf(` <strong>%s</strong> is ready to install, on
		 <a href="/setup?tab=general">General</a>.`, html.EscapeString(fw.Offered())))
	default:
		statusLine(w, "ok", "Software", running+` Nothing newer is waiting.`)
	}

	longest := fmt.Sprintf("%d minutes", int(life/time.Minute))
	if life == time.Hour {
		longest = "an hour"
	}
	statusLine(w, "", "This page", fmt.Sprintf(`closes by itself when it is left alone for %d minutes, and
	 after %s at most. To come back in, ask again and %s.`,
		int(idle/time.Minute), longest, letInBy))
	fmt.Fprint(w, `</fieldset>`)
}
