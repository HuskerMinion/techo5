package setup

import (
	"context"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/announce"
	"github.com/HuskerMinion/techo5/echod/internal/feature/share"
)

// Shared settings, on the General tab (feature/share): whether this device takes part, and for each
// setting that can be shared, the devices it is kept in step with and whether this device keeps its own.
// Below it, a copy of chosen settings to chosen devices, once. Both have routes of their own, for a form
// with a box for every setting and every device.

func init() {
	routes["/setup/share"] = (*Feature).saveShare
	routes["/setup/share-copy"] = (*Feature).copyShare
	routes["/setup/share-request"] = (*Feature).answerShare
}

// shareBody is the most a sharing form may say: a box for each setting and each device.
const shareBody = 128 << 10

// shareDevices is the other devices to offer: those on the network now, and any a setting is linked with
// that is not, so a link to a device switched off can still be undone.
func shareDevices() []string {
	me := config.Get().Device.Name
	var out []string
	add := func(n string) {
		if n != "" && !strings.EqualFold(n, me) && !slices.ContainsFunc(out, func(o string) bool { return strings.EqualFold(o, n) }) {
			out = append(out, n)
		}
	}
	for _, p := range announce.Peers() {
		add(p.Name)
	}
	for _, devices := range config.Get().Share.Links {
		for _, d := range devices {
			add(d)
		}
	}
	sort.Strings(out)
	return out
}

func shareSection(w http.ResponseWriter, token string) {
	c := config.Get()
	s := c.Share
	fieldsetOpen(w, "share", "Shared settings")
	devices := shareDevices()
	// Requests first, each a form of its own, ahead of the settings' form.
	if s.On && len(s.Requests) > 0 {
		fmt.Fprint(w, `<h4>Asked to link</h4>`)
		for _, rq := range s.Requests {
			fmt.Fprintf(w, `<form method="post" action="/setup/share-request" class="btns">
			 <input type="hidden" name="token" value="%s"><input type="hidden" name="id" value="%s">
			 <input type="hidden" name="device" value="%s">
			 <span>%s asks to link <strong>%s</strong></span>
			 <button type="submit" name="op" value="accept">Accept</button>
			 <button type="submit" name="op" value="decline">Decline</button></form>`,
				html.EscapeString(token), html.EscapeString(rq.ID), html.EscapeString(rq.Device),
				html.EscapeString(rq.Device), html.EscapeString(shareName(rq.ID)))
		}
	}
	fmt.Fprintf(w, `<form method="post" action="/setup/share"><input type="hidden" name="token" value="%s">
	 <label><input type="checkbox" name="on" value="1"%s> Share settings with the other devices</label>
	 <p class="note">Off on every device until it is turned on here or with the Settings sharing switch in Home
	  Assistant; a device with it off takes nothing. With it on, any device of the house can copy the settings
	  listed here to this one, except those kept. Tick a device for a setting to ask it to link: once accepted
	  there, a change to that setting on either reaches the other, the latest change winning; another device can
	  ask, never link this one by itself. Keep mine: nothing another device sends changes it. What opens the
	  device to the network, uses or shows the use of its microphones or camera, holds a password or says where
	  its voice goes is never shared. Every device needs the same house word (Connections).</p>`,
		html.EscapeString(token), checked(s.On))
	if c.Home.HouseWord == "" {
		fmt.Fprint(w, `<p class="note"><strong>This device has no house word set</strong>, so it can neither send nor take
		 shared settings.</p>`)
	}
	if s.On {
		if len(devices) == 0 {
			fmt.Fprint(w, `<p class="note">No other device is on the network now.</p>`)
		}
		for _, sec := range share.Sections() {
			fmt.Fprintf(w, `<details><summary>%s</summary><div style="overflow-x:auto"><table class="share"><tr><th>Setting</th><th>Keep mine</th>`,
				html.EscapeString(sec.Name))
			for _, d := range devices {
				fmt.Fprintf(w, `<th>%s</th>`, html.EscapeString(d))
			}
			fmt.Fprint(w, `<th>Last from</th></tr>`)
			for _, st := range sec.Settings {
				fmt.Fprintf(w, `<tr><td>%s<input type="hidden" name="id" value="%s"></td>
				 <td><input type="checkbox" name="keep_%s" value="1"%s aria-label="Keep mine: %s"></td>`,
					html.EscapeString(st.Name), html.EscapeString(st.ID), html.EscapeString(st.ID),
					checked(s.Kept(st.ID)), html.EscapeString(st.Name))
				for _, d := range devices {
					asked := ""
					switch {
					case share.NoAnswer(s, st.ID, d, time.Now()):
						asked = ` <small>no answer</small>`
					case s.WaitingFor(st.ID, d):
						asked = ` <small>asked</small>`
					}
					fmt.Fprintf(w, `<td><input type="checkbox" name="link_%s" value="%s"%s aria-label="%s with %s">%s</td>`,
						html.EscapeString(st.ID), html.EscapeString(d), checked(s.LinkedWith(st.ID, d)),
						html.EscapeString(st.Name), html.EscapeString(d), asked)
				}
				from := ""
				if f, ok := s.From[st.ID]; ok {
					from = f.Device + ", " + f.At.Local().Format("Jan 2 15:04")
				}
				fmt.Fprintf(w, `<td>%s</td></tr>`, html.EscapeString(from))
			}
			fmt.Fprint(w, `</table></div></details>`)
		}
	}
	fmt.Fprint(w, `<p><button type="submit">Save</button></p></form>`)

	if s.On && len(devices) > 0 {
		fmt.Fprintf(w, `<h4>Copy once</h4><form method="post" action="/setup/share-copy">
		 <input type="hidden" name="token" value="%s">
		 <p class="note">This device's value for each setting ticked goes to each device ticked, once, with no
		  link. A device that keeps a setting its own, or has sharing off, says so.</p><p>To:`,
			html.EscapeString(token))
		for _, d := range devices {
			fmt.Fprintf(w, ` <label><input type="checkbox" name="to" value="%s"> %s</label>`, html.EscapeString(d), html.EscapeString(d))
		}
		fmt.Fprint(w, `</p>`)
		for _, sec := range share.Sections() {
			fmt.Fprintf(w, `<details><summary>%s</summary>`, html.EscapeString(sec.Name))
			for _, st := range sec.Settings {
				fmt.Fprintf(w, `<label><input type="checkbox" name="copy" value="%s"> %s</label><br>`,
					html.EscapeString(st.ID), html.EscapeString(st.Name))
			}
			fmt.Fprint(w, `</details>`)
		}
		fmt.Fprint(w, `<p><button type="submit">Copy</button></p></form>`)
	}
	fmt.Fprint(w, `</fieldset>`)
}

func checked(b bool) string {
	if b {
		return " checked"
	}
	return ""
}

// shareForm checks a sharing form came from this page, behind the press on the device.
func (f *Feature) shareForm(w http.ResponseWriter, r *http.Request) bool {
	token, in := f.session(r)
	if !in {
		notLetIn(w)
		return false
	}
	if r.Method != http.MethodPost {
		http.Error(w, "post the form", http.StatusMethodNotAllowed)
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, shareBody)
	if err := r.ParseForm(); err != nil || !sameToken(r.PostFormValue("token"), token) {
		http.Error(w, "that did not come from this page", http.StatusForbidden)
		return false
	}
	f.used()
	return true
}

// saveShare saves whether this device shares, what it keeps, and its links: each setting whose devices
// changed is linked anew, which tells the devices in its group and any taken out of it.
func (f *Feature) saveShare(w http.ResponseWriter, r *http.Request) {
	if !f.shareForm(w, r) {
		return
	}
	on := r.PostFormValue("on") == "1"
	if err := config.Set().Share().On(on); err != nil {
		http.Redirect(w, r, back("share", "problem", "could not be saved: "+err.Error()), http.StatusSeeOther)
		return
	}
	share.Get().Restore(config.Get())
	var problems []string
	if on {
		// Each message has its own time to arrive (share's deliver), however many settings changed.
		ctx := context.WithoutCancel(r.Context())
		// What is kept, in one write, and only when it changed.
		keep := map[string]bool{}
		for _, id := range r.PostForm["id"] {
			if share.Shareable(id) {
				keep[id] = r.PostFormValue("keep_"+id) == "1"
			}
		}
		cur := config.Get().Share
		changed := false
		for id, k := range keep {
			if cur.Kept(id) != k {
				changed = true
			}
		}
		if changed {
			if err := config.Set().Share().Update(func(s *config.Share) {
				for id, k := range keep {
					setKept(s, id, k)
				}
			}); err != nil {
				problems = append(problems, err.Error())
			}
		}
		// Every setting at once: a device that has gone and is still listed takes sendWithin to give up on,
		// and one after another the page would wait that long for each setting linked with it.
		var mu sync.Mutex
		var wg sync.WaitGroup
		for _, id := range slices.Compact(slices.Sorted(slices.Values(r.PostForm["id"]))) {
			if !share.Shareable(id) {
				continue
			}
			want := r.PostForm["link_"+id]
			// Unchanged, and nothing still asked: nothing to send. A device still asked is asked again.
			if sameDevices(want, cur.Linked(id)) && len(cur.Waiting[id]) == 0 {
				continue
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				results, err := share.Get().Link(ctx, id, want)
				mu.Lock()
				defer mu.Unlock()
				if err != nil {
					problems = append(problems, err.Error())
					return
				}
				for d, outcome := range results {
					switch outcome {
					case share.Applied, share.Linked, share.Asked:
					default:
						problems = append(problems, fmt.Sprintf("%s: %s (%s)", d, shareName(id), orUnreachable(outcome)))
					}
				}
			}()
		}
		wg.Wait()
		sort.Strings(problems)
	}
	slog.Info("setup page: shared settings saved", "on", on)
	to := back("share", "saved", "1")
	if len(problems) > 0 {
		to = back("share", "problem", strings.Join(problems, "; "))
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
}

func orUnreachable(outcome string) string {
	if outcome == "" {
		return share.Unreachable
	}
	return outcome
}

// sameDevices is whether two lists name the same devices, however cased and ordered.
func sameDevices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for _, x := range a {
		if !slices.ContainsFunc(b, func(y string) bool { return strings.EqualFold(x, y) }) {
			return false
		}
	}
	return true
}

// copyShare copies the settings ticked to the devices ticked, once, and shows what each made of them.
func (f *Feature) copyShare(w http.ResponseWriter, r *http.Request) {
	if !f.shareForm(w, r) {
		return
	}
	if !config.Get().Share.On {
		http.Redirect(w, r, back("share", "problem", "turn settings sharing on first"), http.StatusSeeOther)
		return
	}
	ids, devices := r.PostForm["copy"], r.PostForm["to"]
	if len(ids) == 0 || len(devices) == 0 {
		http.Redirect(w, r, back("share", "problem", "tick the settings to copy and the devices to copy them to"), http.StatusSeeOther)
		return
	}
	results := share.Get().Copy(context.WithoutCancel(r.Context()), ids, devices)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	head(w)
	fmt.Fprintf(w, `<div class="wrap"><h1>%s</h1><p class="sub">Settings copied</p><fieldset><legend>What each device took</legend>`,
		html.EscapeString(deviceName()))
	sort.Strings(devices)
	for _, d := range devices {
		r, ok := results[d]
		fmt.Fprintf(w, `<h4>%s</h4><ul>`, html.EscapeString(d))
		if !ok {
			fmt.Fprint(w, `<li>Nothing sent: it is this device.</li>`)
		}
		for _, id := range ids {
			if !share.Shareable(id) {
				continue
			}
			fmt.Fprintf(w, `<li>%s: %s</li>`, html.EscapeString(shareName(id)), html.EscapeString(orUnreachable(r[id])))
		}
		fmt.Fprint(w, `</ul>`)
	}
	fmt.Fprint(w, `<p><a href="/setup?tab=general">Back to the setup page</a></p></fieldset></div>`)
}

// shareName is a setting's name as the page shows it.
func shareName(id string) string {
	for _, sec := range share.Sections() {
		for _, st := range sec.Settings {
			if st.ID == id {
				return st.Name
			}
		}
	}
	return id
}

// answerShare accepts or declines another device's request to link a setting.
func (f *Feature) answerShare(w http.ResponseWriter, r *http.Request) {
	if !f.shareForm(w, r) {
		return
	}
	id, device := r.PostFormValue("id"), r.PostFormValue("device")
	if !slices.ContainsFunc(config.Get().Share.Requests, func(q config.ShareRequest) bool {
		return q.ID == id && strings.EqualFold(q.Device, device)
	}) {
		http.Redirect(w, r, back("share", "problem", "that request is not waiting any more"), http.StatusSeeOther)
		return
	}
	ctx := context.WithoutCancel(r.Context())
	to := back("share", "saved", "1")
	switch r.PostFormValue("op") {
	case "accept":
		if outcome, err := share.Get().Accept(ctx, id, device); err != nil {
			to = back("share", "problem", err.Error())
		} else if outcome != share.Linked {
			to = back("share", "problem", fmt.Sprintf("linked here; %s answered %s", device, orUnreachable(outcome)))
		}
	case "decline":
		share.Get().Decline(ctx, id, device)
	default:
		to = back("share", "problem", "accept or decline")
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// setKept marks a setting kept, or not, in s.
func setKept(s *config.Share, id string, keep bool) {
	i := slices.Index(s.Keep, id)
	switch {
	case keep && i < 0:
		s.Keep = append(s.Keep, id)
		slices.Sort(s.Keep)
	case !keep && i >= 0:
		s.Keep = slices.Delete(s.Keep, i, i+1)
	}
}
