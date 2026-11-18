package setup

import (
	"context"
	"fmt"
	"html"
	"io"
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

// Also on: a card holding settings that can be shared (feature/share's list) offers, above its Save, the
// other devices of the house, and a save sends the card's shared settings to the ones ticked, once, the
// way a copy from Shared settings does; ticking "Keep these in step" also asks them to link each of
// them. What each device made of it is said on the card after the save. Only the settings on the list
// travel, never the rest of the card (its volume, its ring volume, its slideshow): the row names them.
//
// Shown only with sharing on here, and another device on the network: sharing is off on every device
// until it is turned on, and the row would offer nothing otherwise.

// alsoOnCards are the cards with settings that can be shared, by the form's what, each with the settings
// of the list it holds. The calendar's look is its switches (alsoOnIDs).
var alsoOnCards = map[string][]string{
	"speaker":     {"night_volume", "louder_speech"},
	"alarm_prefs": {"alarm_sound", "alarm_snooze_length"},
	"screen": {"screen_clock_style", "screen_font", "screen_clock_style_swipe", "screen_volume_swipe",
		"screen_clock_tap", "screen_ignores_room_lights", "slideshow_weather_art",
		"slideshow_screensaver_overlay", "slideshow_screensaver_position"},
	"dashpanel":     {"screen_dashboard_idle", "screen_dashboard_handle", "screen_dashboard_return", "screen_dashboard_tiles", "screen_dashboard_layout"},
	"calendar-look": nil,
}

// alsoOnFor is how long what a save sent is said on its card.
const alsoOnFor = 10 * time.Minute

// alsoOnWithin is the most a save waits for the devices' answers: each is sent in parallel, and one that
// does not answer is said to be out of reach.
const alsoOnWithin = 15 * time.Second

// alsoOnIDs is the settings of the list a card holds, as this device has them.
func alsoOnIDs(what string) []string {
	ids, ok := alsoOnCards[what]
	if !ok {
		return nil
	}
	if what == "calendar-look" {
		if s := screen.Load(); s != nil {
			for _, c := range s.Calendar {
				ids = append(ids, "calendar_"+c.Key)
			}
		}
	}
	var out []string
	for _, id := range ids {
		if share.Shareable(id) {
			out = append(out, id)
		}
	}
	return out
}

// alsoOnDevices is the other devices on the network now, by name.
var alsoOnDevices = func() []string {
	me := config.Get().Device.Name
	var out []string
	for _, p := range announce.Peers() {
		if p.Name != "" && !strings.EqualFold(p.Name, me) && share.ValidName(p.Name) &&
			!slices.ContainsFunc(out, func(o string) bool { return strings.EqualFold(o, p.Name) }) {
			out = append(out, p.Name)
		}
	}
	sort.Strings(out)
	return out
}

// alsoOnRow is the row above a card's Save: the devices to save on too, and what the last save here did
// on them. Nothing on a card with no setting of the list, with sharing off, or with no other device.
func alsoOnRow(w http.ResponseWriter, token, what string) {
	ids := alsoOnIDs(what)
	if len(ids) == 0 || !config.Get().Share.On {
		return
	}
	devices := alsoOnDevices()
	said := alsoOnSaid.get(token, what)
	if len(devices) == 0 && said == nil {
		return
	}
	names := make([]string, len(ids))
	for i, id := range ids {
		names[i] = shareName(id)
	}
	fmt.Fprint(w, `<fieldset class="also"><legend>Also on</legend>`)
	if len(devices) > 0 {
		here, now := alsoOnHere(ids), alsoOnNow.drawn(w, devices)
		fmt.Fprint(w, `<div class="also-devs">`)
		for _, d := range devices {
			fmt.Fprintf(w, `<label><input type="checkbox" name="also_on" value="%s" style="width:auto"> %s%s%s</label>`,
				html.EscapeString(d), html.EscapeString(d), alsoOnNowNote(ids, here, now[d]), alsoOnLinkNote(config.Get().Share, ids, d))
		}
		fmt.Fprintf(w, `</div>
	 <p><label><input type="checkbox" name="also_link" value="yes" style="width:auto"> Keep these in step from now on:
	  a change on any of them reaches the others, once each device accepts on its own page</label></p>
	 <p class="note">A save here also sends these to the devices ticked: %s. The rest of this card stays on this
	  device. A device with sharing off, or one that keeps its own, says so below.</p>`,
			html.EscapeString(strings.Join(names, ", ")))
	}
	if said != nil {
		fmt.Fprintf(w, `<div class="also-said"><strong>After the save at %s</strong><ul>`, said.at.Format("3:04 PM"))
		for _, l := range said.lines {
			fmt.Fprintf(w, `<li>%s</li>`, html.EscapeString(l))
		}
		fmt.Fprint(w, `</ul></div>`)
	}
	fmt.Fprint(w, `</fieldset>`)
}

// alsoOnNowNote is what a device has of the card's settings, beside its name: same as here, or the
// settings it has otherwise, and what it has. Nothing for a device that did not say (sharing off there,
// a release from before it could be asked, or no answer in time).
func alsoOnNowNote(ids []string, here, there map[string]string) string {
	if there == nil {
		return ""
	}
	var differ []string
	asked := 0
	for _, id := range ids {
		v, ok := there[id]
		if !ok {
			continue
		}
		asked++
		if v != here[id] {
			differ = append(differ, strings.ToLower(shareName(id))+" "+v)
		}
	}
	switch {
	case asked == 0:
		return ""
	case len(differ) == 0:
		return ` <small>same as here</small>`
	case len(differ) > alsoOnShown:
		more := len(differ) - alsoOnShown
		differ = append(differ[:alsoOnShown], fmt.Sprintf("%d more", more))
	}
	return ` <small>now: ` + html.EscapeString(strings.Join(differ, ", ")) + `</small>`
}

// alsoOnShown is the most settings said beside a device that differ from here.
const alsoOnShown = 3

// alsoOnAskFor is how long what the devices have is kept: a tab drawing several cards asks them once, and
// a page opened again a minute later asks again.
const alsoOnAskFor = 20 * time.Second

// alsoOnAskWithin is the most the page waits for the devices to say: a device switched off or slow does
// not hold the page up, it only goes without its note.
const alsoOnAskWithin = 1500 * time.Millisecond

// alsoOnHere and alsoOnValues are feature/share's, replaced in tests.
var (
	alsoOnHere   = share.Here
	alsoOnValues = func(ctx context.Context, ids, devices []string) map[string]map[string]string {
		return share.Get().Values(ctx, ids, devices)
	}
)

// alsoOnNow is what the other devices have of every setting on the list, as they last said.
var alsoOnNow = alsoOnAsked{}

type alsoOnAsked struct {
	mu    sync.Mutex
	at    time.Time
	asked []string
	by    map[string]map[string]string
}

// drawn is what the devices have, for a card being drawn: as get, except for a tab drawn to be searched
// (find.go), which never waits on the network and takes what was kept, nil when nothing was.
func (a *alsoOnAsked) drawn(w io.Writer, devices []string) map[string]map[string]string {
	if s, ok := w.(*sectionWriter); ok && s.finding {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.by
	}
	return a.get(devices)
}

// get is what the devices have, asked again when what was kept is old or was not asked of all of them.
// It holds the lock while it asks, so cards drawn at once ask once.
func (a *alsoOnAsked) get(devices []string) map[string]map[string]string {
	a.mu.Lock()
	defer a.mu.Unlock()
	fresh := a.by != nil && time.Since(a.at) < alsoOnAskFor
	for _, d := range devices {
		fresh = fresh && slices.Contains(a.asked, d)
	}
	if !fresh {
		ctx, cancel := context.WithTimeout(context.Background(), alsoOnAskWithin)
		a.by = alsoOnValues(ctx, share.IDs(), devices)
		cancel()
		a.at, a.asked = time.Now(), slices.Clone(devices)
	}
	return a.by
}

// forget drops what was kept: a save has just changed what the devices have.
func (a *alsoOnAsked) forget() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.by = nil
}

// alsoOnLinkNote is how the card's settings stand with a device, beside its name: kept in step when every
// one is linked both ways, asked while a link waits to be accepted there, nothing when none is linked.
func alsoOnLinkNote(s config.Share, ids []string, d string) string {
	linked, waiting := 0, 0
	for _, id := range ids {
		switch {
		case s.WaitingFor(id, d):
			waiting++
		case s.LinkedWith(id, d):
			linked++
		}
	}
	switch {
	case waiting > 0:
		return ` <small>asked to keep in step, waiting for it to accept</small>`
	case linked == len(ids):
		return ` <small>kept in step</small>`
	case linked > 0:
		return fmt.Sprintf(` <small>%d of %d kept in step</small>`, linked, len(ids))
	}
	return ""
}

// alsoOnSave sends a card's shared settings to the devices ticked on it, after the card was saved here,
// and keeps what each made of it for the card. The settings are read as this device has them now, so
// the save just made is what goes.
func alsoOnSave(r *http.Request, token, what string) {
	ids := alsoOnIDs(what)
	if len(ids) == 0 || !config.Get().Share.On {
		return
	}
	var devices []string
	for _, d := range r.PostForm["also_on"] {
		d = strings.TrimSpace(d)
		if share.ValidName(d) && !strings.EqualFold(d, config.Get().Device.Name) &&
			!slices.ContainsFunc(devices, func(o string) bool { return strings.EqualFold(o, d) }) {
			devices = append(devices, d)
		}
	}
	if len(devices) == 0 {
		alsoOnSaid.keep(token, what, nil) // a save here only: what an earlier one sent is no longer the news
		return
	}
	// Not the browser's: a tab closed while the devices answer does not stop it half way.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), alsoOnWithin)
	defer cancel()
	copied := alsoOnCopy(ctx, ids, devices)
	var linked map[string]map[string]string
	var notLinked []string // a link refused here, before any device was asked: said under the devices
	if r.PostFormValue("also_link") == "yes" {
		linked = map[string]map[string]string{}
		for _, id := range ids {
			want := config.Get().Share.Linked(id)
			for _, d := range devices {
				if !slices.ContainsFunc(want, func(o string) bool { return strings.EqualFold(o, d) }) {
					want = append(want, d)
				}
			}
			got, err := alsoOnLink(ctx, id, want)
			if err != nil {
				slog.Warn("setup page: also on: a link was not made", "setting", id, "err", err)
				notLinked = append(notLinked, fmt.Sprintf("%s not kept in step: %v", shareName(id), err))
				continue
			}
			for d, outcome := range got {
				if linked[d] == nil {
					linked[d] = map[string]string{}
				}
				linked[d][id] = outcome
			}
		}
	}
	alsoOnNow.forget()
	var lines []string
	for _, d := range devices {
		lines = append(lines, alsoOnLine(d, copied[d], linked[d]))
	}
	lines = append(lines, notLinked...)
	slog.Info("setup page: also on", "card", what, "settings", len(ids), "devices", len(devices), "linked", linked != nil)
	alsoOnSaid.keep(token, what, lines)
}

// alsoOnCopy and alsoOnLink are feature/share's, replaced in tests.
var (
	alsoOnCopy = func(ctx context.Context, ids, devices []string) map[string]map[string]string {
		return share.Get().Copy(ctx, ids, devices)
	}
	alsoOnLink = func(ctx context.Context, id string, devices []string) (map[string]string, error) {
		return share.Get().Link(ctx, id, devices)
	}
)

// alsoOnLine is one device's line after a save: what it made of the settings, by setting, in a few words.
func alsoOnLine(d string, copied, linked map[string]string) string {
	var took, same, kept, missing int
	var keptNames []string
	reach, off := false, false
	for id, outcome := range copied {
		switch outcome {
		case share.Applied:
			took++
		case share.Same:
			same++
		case share.Kept:
			kept++
			keptNames = append(keptNames, shareName(id))
		case share.NotHere, share.NotShared:
			missing++
		case share.NotTaking:
			off = true
		case share.Unreachable:
			reach = true
		}
	}
	switch {
	case len(copied) == 0 || reach && took+same+kept == 0:
		return d + ": could not be reached"
	case off && took+same+kept == 0:
		return d + ": sharing is off there (System, Shared settings, on its own page)"
	}
	var parts []string
	// Taken is said apart from already so: a device that had them already changed nothing, and "took it"
	// would hide a save that sent what it had.
	switch {
	case took > 0 && kept == 0:
		parts = append(parts, "took it")
	case took > 0:
		parts = append(parts, "took the rest")
	case same > 0:
		parts = append(parts, "already had these")
	}
	if kept > 0 {
		sort.Strings(keptNames)
		parts = append([]string{"kept its own " + strings.ToLower(strings.Join(keptNames, ", ")) + " (Keep mine is on there)"}, parts...)
	}
	if missing > 0 {
		parts = append(parts, count(missing, "setting", "settings")+" it does not have")
	}
	if len(linked) > 0 {
		asked, inStep := 0, 0
		for _, outcome := range linked {
			switch outcome {
			case share.Asked:
				asked++
			case share.Linked, "":
				inStep++
			}
		}
		switch {
		case asked > 0:
			parts = append(parts, "asked to keep them in step: accept on its own page, under System, Shared settings")
		case inStep > 0:
			parts = append(parts, "kept in step")
		}
	}
	if len(parts) == 0 {
		parts = append(parts, "took none of them")
	}
	return d + ": " + strings.Join(parts, "; ")
}

// alsoOnSaid is what the last save of each card sent, for the session that saved it, a short while.
var alsoOnSaid = alsoOnStore{}

type alsoOnResult struct {
	what  string
	lines []string
	at    time.Time
}

type alsoOnStore struct {
	mu   sync.Mutex
	byID map[string]alsoOnResult
}

func (s *alsoOnStore) keep(token, what string, lines []string) {
	if token == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.byID == nil {
		s.byID = map[string]alsoOnResult{}
	}
	now := time.Now()
	for t, r := range s.byID {
		if now.Sub(r.at) > alsoOnFor {
			delete(s.byID, t)
		}
	}
	s.byID[token] = alsoOnResult{what: what, lines: lines, at: now}
}

func (s *alsoOnStore) get(token, what string) *alsoOnResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.byID[token]
	if !ok || r.what != what || len(r.lines) == 0 || time.Since(r.at) > alsoOnFor {
		return nil
	}
	return &r
}

func (s *alsoOnStore) drop(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if token == "" {
		s.byID = nil
		return
	}
	delete(s.byID, token)
}
