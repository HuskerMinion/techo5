package setup

import (
	"context"
	"fmt"
	"html"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/announce"
	"github.com/HuskerMinion/techo5/echod/internal/feature/share"
)

// Your devices, on the System tab below Shared settings: the shared settings of the devices of the house side by
// side, those that differ from this device only, each with a button that makes every device like this one.
// What the devices have is asked as for Also on (alsoOnNow), so the tab asks them once. A device with
// sharing off says so and has a link to its own page, where it is turned on.

func init() {
	routes["/setup/devices"] = (*Feature).devicesSame
}

// devicesRow is a setting that differs somewhere, with this device's value.
type devicesRow struct {
	id, name, here string
}

// devicesRows is the settings, in the page's order, for which a device that answered has a value other
// than this device's. same is how many the devices that answered all have as this one does.
func devicesRows(here map[string]string, by map[string]map[string]string, answered []string) (rows []devicesRow, same int) {
	for _, sec := range share.Sections() {
		for _, st := range sec.Settings {
			v, ok := here[st.ID]
			if !ok {
				continue
			}
			differs, asked := false, false
			for _, d := range answered {
				if w, ok := by[d][st.ID]; ok {
					asked = true
					differs = differs || w != v
				}
			}
			switch {
			case differs:
				rows = append(rows, devicesRow{st.ID, st.Name, v})
			case asked:
				same++
			}
		}
	}
	return rows, same
}

// devicePage is a device's own setup page, for a device with sharing off; "" when it cannot be told.
var devicePage = func(d string) string {
	for _, p := range announce.Peers() {
		if strings.EqualFold(p.Name, d) && p.Address != "" && p.Port > 0 {
			return "http://" + net.JoinHostPort(p.Address, strconv.Itoa(p.Port)) + "/setup?tab=system"
		}
	}
	return ""
}

func devicesSection(w http.ResponseWriter, token string) {
	if !config.Get().Share.On {
		return
	}
	devices := alsoOnDevices()
	if len(devices) == 0 {
		return
	}
	by := alsoOnNow.drawn(w, devices)
	if by == nil {
		// Only for a find: nothing kept to show, and a find does not ask.
		sectionOpen(w, "devices", "Your devices")
		fmt.Fprint(w, `<p class="note">Your devices side by side: open the System tab to see them.</p>`)
		sectionClose(w)
		return
	}
	var answered, off, silent []string
	for _, d := range devices {
		v, ok := by[d]
		switch {
		case ok && v == nil:
			off = append(off, d)
		case ok:
			answered = append(answered, d)
		default:
			silent = append(silent, d)
		}
	}
	rows, same := devicesRows(alsoOnHere(share.IDs()), by, answered)
	var summary []string
	switch {
	case len(answered) == 0:
		summary = append(summary, "No device answered")
	case len(rows) == 0:
		summary = append(summary, "All the same")
	default:
		summary = append(summary, count(len(rows), "setting differs", "settings differ"))
	}
	if len(off) > 0 {
		summary = append(summary, count(len(off), "device", "devices")+" with sharing off")
	}
	sectionOpen(w, "devices", "Your devices", summary...)
	fmt.Fprint(w, `<p class="note">The devices of the house side by side: only the settings where one differs from this
	 device are listed, and the values that differ are marked. Make all like this sends this device's value to every
	 device, once; a device that keeps its own says so.</p>`)
	for _, d := range off {
		if page := devicePage(d); page != "" {
			fmt.Fprintf(w, `<p class="note"><strong>%s</strong> has sharing off. <a href="%s">Open its page</a> to turn it on, under System, Shared settings.</p>`,
				html.EscapeString(d), html.EscapeString(page))
		} else {
			fmt.Fprintf(w, `<p class="note"><strong>%s</strong> has sharing off: turn it on on its own page, under System, Shared settings.</p>`,
				html.EscapeString(d))
		}
	}
	if len(silent) > 0 {
		fmt.Fprintf(w, `<p class="note">Did not answer: %s. A device on a release from before this card is not asked.</p>`,
			html.EscapeString(strings.Join(silent, ", ")))
	}
	if said := alsoOnSaid.get(token, "devices"); said != nil && len(said.lines) > 1 {
		fmt.Fprintf(w, `<div class="also-said"><strong>%s, made like this device at %s</strong><ul>`,
			html.EscapeString(said.lines[0]), said.at.Format("3:04 PM"))
		for _, l := range said.lines[1:] {
			fmt.Fprintf(w, `<li>%s</li>`, html.EscapeString(l))
		}
		fmt.Fprint(w, `</ul></div>`)
	}
	if len(rows) > 0 {
		fmt.Fprintf(w, `<form method="post" action="/setup/devices"><input type="hidden" name="token" value="%s">
		 <div style="overflow-x:auto"><table class="devices"><tr><th>Setting</th><th>%s<small>this device</small></th>`,
			html.EscapeString(token), html.EscapeString(deviceName()))
		for _, d := range answered {
			fmt.Fprintf(w, `<th>%s</th>`, html.EscapeString(d))
		}
		fmt.Fprint(w, `<th></th></tr>`)
		for _, row := range rows {
			fmt.Fprintf(w, `<tr><td>%s</td><td>%s</td>`, html.EscapeString(row.name), html.EscapeString(row.here))
			for _, d := range answered {
				v, ok := by[d][row.id]
				switch {
				case !ok:
					fmt.Fprint(w, `<td class="na">-</td>`)
				case v != row.here:
					fmt.Fprintf(w, `<td class="diff">%s</td>`, html.EscapeString(v))
				default:
					fmt.Fprintf(w, `<td>%s</td>`, html.EscapeString(v))
				}
			}
			fmt.Fprintf(w, `<td><button type="submit" name="id" value="%s" aria-label="Make all like this: %s">Make all like this</button></td></tr>`,
				html.EscapeString(row.id), html.EscapeString(row.name))
		}
		fmt.Fprint(w, `</table></div></form>`)
		fmt.Fprint(w, `<p class="note">A dash: that device keeps its own, or does not have the setting.</p>`)
	}
	if same > 0 {
		fmt.Fprintf(w, `<p class="note">%s the same on every device that answered.</p>`,
			html.EscapeString(count(same, "more setting is", "more settings are")))
	}
	sectionClose(w)
}

// devicesSame sends this device's value of one setting to every other device, once, and keeps what each
// made of it for the card, the setting's name first.
func (f *Feature) devicesSame(w http.ResponseWriter, r *http.Request) {
	if !f.shareForm(w, r) {
		return
	}
	token, _ := f.session(r)
	id := r.PostFormValue("id")
	switch {
	case !config.Get().Share.On:
		http.Redirect(w, r, back("devices", "problem", "turn settings sharing on first"), http.StatusSeeOther)
		return
	case !share.Shareable(id):
		http.Redirect(w, r, back("devices", "problem", "that setting is not shared"), http.StatusSeeOther)
		return
	}
	devices := alsoOnDevices()
	if len(devices) == 0 {
		http.Redirect(w, r, back("devices", "problem", "no other device is on the network now"), http.StatusSeeOther)
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), alsoOnWithin)
	defer cancel()
	copied := alsoOnCopy(ctx, []string{id}, devices)
	alsoOnNow.forget()
	lines := []string{shareName(id)}
	for _, d := range devices {
		lines = append(lines, alsoOnLine(d, copied[d], nil))
	}
	alsoOnSaid.keep(token, "devices", lines)
	slog.Info("setup page: one setting made the same on every device", "setting", id, "devices", len(devices))
	http.Redirect(w, r, back("devices", "saved", "1"), http.StatusSeeOther)
}
