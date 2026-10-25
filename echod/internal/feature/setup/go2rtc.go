package setup

import (
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/lib/go2rtc"
)

func init() { saves["go2rtc"] = saveGo2RTC }

// go2rtcSection is a go2rtc server whose streams are cameras (feature/home/go2rtc.go). A password in its
// address is never shown again: the address is shown without it.
func go2rtcSection(w http.ResponseWriter, token string) {
	g := config.Get().Home.Go2RTC
	fmt.Fprint(w, `<fieldset><legend>go2rtc cameras</legend><form method="post" action="/setup/save">`)
	hidden(w, token, "go2rtc", "connections")
	shown := ""
	if g.Base != "" {
		var names []string
		for _, c := range g.Cameras {
			names = append(names, html.EscapeString(c.Name))
		}
		fmt.Fprintf(w, `<p style="margin:0">Set up: %d stream(s): %s. Read the list again by saving the same
		 address.</p>`, len(g.Cameras), strings.Join(names, ", "))
		if u, err := url.Parse(g.Base); err == nil {
			u.User = nil
			shown = u.String()
		}
	}
	for i, c := range g.Cameras {
		stream := strings.TrimPrefix(c.Entity, "go2rtc:")
		fmt.Fprintf(w, `<label for="g2n%d">Name for %s</label>
		 <input id="g2n%d" name="name" value="%s" placeholder="%s" maxlength="60" autocomplete="off">
		 <input type="hidden" name="stream" value="%s">`, i, html.EscapeString(stream), i,
			html.EscapeString(c.Name), html.EscapeString(stream), html.EscapeString(c.Entity))
	}
	fmt.Fprintf(w, `<label for="g2r">go2rtc address</label>
	 <input id="g2r" name="address" value="%s" placeholder="192.168.x.y:1984" autocomplete="off">
	 <p class="note">Every stream go2rtc has becomes a camera here, by its name in go2rtc: its picture
	  from go2rtc, and live video from go2rtc's RTSP port (8554) with Live camera video on. A go2rtc with
	  a login takes it in the address, as http://user:password@host:1984; saved, it is not shown again.
	  A stream can have a name of its own to show and say ("show the doorbell"); it changes nothing in
	  go2rtc. Left empty, the stream is called by its name in go2rtc. Clear the address and save to take
	  the cameras away.</p>
	 <p><button type="submit">Save</button></p></form></fieldset>`, html.EscapeString(shown))
}

func saveGo2RTC(r *http.Request) string {
	address := strings.TrimSpace(r.PostFormValue("address"))
	if strings.ContainsAny(address, "\r\n") {
		return "the address is one line"
	}
	// The names first: reading the streams again keeps the names they have.
	named := false
	if streams, names := r.PostForm["stream"], r.PostForm["name"]; len(streams) > 0 {
		if len(streams) != len(names) {
			return "the stream names did not come through; reload the page and try again"
		}
		given := map[string]string{}
		changed := false
		for _, c := range config.Get().Home.Go2RTC.Cameras {
			for i, e := range streams {
				if e != c.Entity {
					continue
				}
				n := strings.TrimSpace(names[i])
				if strings.ContainsAny(n, "\r\n") {
					return "a stream's name is one line"
				}
				if n == "" {
					n = strings.TrimPrefix(e, "go2rtc:")
				}
				given[e] = n
				changed = changed || n != c.Name
			}
		}
		if changed {
			if err := home.SetGo2RTCNames(given); err != nil {
				return "could not save the stream names: " + err.Error()
			}
			slog.Info("setup page: go2rtc stream names set")
			named = true
		}
	}
	// The address shown has no password: saved back unchanged, the one kept is kept. Only for the same
	// server by the same way in: a different scheme (https to http, say) or host:port gets no login, so
	// the password is never sent somewhere it was not given for, or in the clear where it was not.
	if kept := config.Get().Home.Go2RTC.Base; address != "" && kept != "" {
		u, err1 := go2rtc.Base(kept)
		v, err2 := go2rtc.Base(address)
		if err1 == nil && err2 == nil && u.User != nil && v.User == nil && v.Scheme == u.Scheme && v.Host == u.Host {
			v.User = u.User
			address = v.String()
		}
	}
	n, err := home.SetGo2RTC(address)
	if err != nil {
		if named {
			return "the names were saved, but go2rtc's streams could not be read again: " + err.Error()
		}
		return "could not read go2rtc's streams: " + err.Error()
	}
	slog.Info("setup page: go2rtc cameras set", "cameras", n)
	return ""
}
