package setup

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/alarm"
	"github.com/HuskerMinion/techo5/echod/internal/feature/firmware"
	"github.com/HuskerMinion/techo5/echod/internal/feature/firstrun"
	"github.com/HuskerMinion/techo5/echod/internal/feature/timer"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
	"github.com/HuskerMinion/techo5/echod/internal/lib/wifi"
)

// The Status tab is where the page opens: whether the device is on its network, whether Home Assistant
// is connected to it, what goes off next, whether it is on a tailnet, what software it runs and whether
// newer is waiting, and when this page closes.
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
	sectionOpen(w, "now", "Now")

	if statusHome() {
		statusLine(w, "ok", "Home Assistant", "is connected.")
	} else {
		statusLine(w, "bad", "Home Assistant", `is not connected. Alarms, timers and what is set here keep
		 working without it; voice, the dashboard and its controls wait for it.
		 <a href="/setup?tab=network&amp;open=adopt#sec-adopt">Network</a> has how it is added.`)
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
			 <a href="/setup?tab=network&amp;open=wifi#sec-wifi">Network</a> has the networks.`, html.EscapeString(st.State)))
		}
	}

	statusAlarms(w, time.Now())
	statusTailnet(w)

	fw := firmware.Get()
	running := fmt.Sprintf(`<strong>%s</strong>.`, html.EscapeString(layout.Version))
	switch on, _ := fw.Installing(); {
	case on:
		statusLine(w, "", "Software", running+` An update is installing; the device restarts when it is done.`)
	case fw.Failed() != "":
		statusLine(w, "bad", "Software", running+` The last update did not install.
		 <a href="/setup?tab=system&amp;open=updates#sec-updates">System</a> says why.`)
	case fw.Offered() != "":
		statusLine(w, "", "Software", running+fmt.Sprintf(` <strong>%s</strong> is ready to install, on
		 <a href="/setup?tab=system&amp;open=updates#sec-updates">System</a>.`, html.EscapeString(fw.Offered())))
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
	sectionClose(w)
}

// statusAlarms writes what goes off next: the next alarm and the timers running, or that every alarm is
// off, or that nothing is set.
func statusAlarms(w http.ResponseWriter, now time.Time) {
	running := 0
	for _, t := range timer.Get().List(now) {
		if t.Active {
			running++
		}
	}
	timers := ""
	switch {
	case running == 1:
		timers = " A timer is running."
	case running > 1:
		timers = fmt.Sprintf(" %d timers are running.", running)
	}
	link := ` <a href="/setup?tab=alarms">Alarms &amp; Timers</a> has them.`
	v := alarm.Get().View(now)
	switch {
	case v.Ringing != nil:
		statusLine(w, "", "Alarm", "is ringing now."+timers)
	case config.Get().Alarms.AllOff:
		statusLine(w, "", "Alarms", "are all turned off."+timers+link)
	case v.Next != nil:
		statusLine(w, "", "Next alarm", fmt.Sprintf("%s at %s%s.%s",
			dayWord(v.Next.At, now), clockText(v.Next.At), labelSuffix(v.Next.Label), timers))
	case running > 0:
		statusLine(w, "", "No alarm", "is set."+timers)
	default:
		statusLine(w, "", "No alarm", "is set."+link)
	}
}

// dayWord is the day of at as seen from now: today, tomorrow, the weekday within the week, or the date.
func dayWord(at, now time.Time) string {
	day := func(t time.Time) time.Time { y, m, d := t.Date(); return time.Date(y, m, d, 0, 0, 0, 0, t.Location()) }
	days := int(day(at).Sub(day(now)).Hours()/24 + 0.5)
	switch {
	case days <= 0:
		return "Today"
	case days == 1:
		return "Tomorrow"
	case days < 7:
		return at.Weekday().String()
	}
	return at.Format("Monday, January 2")
}
