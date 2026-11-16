package setup

import (
	"fmt"
	"html"
	"net/http"
	"net/url"

	"github.com/HuskerMinion/techo5/echod/internal/layout"
)

// The page's tabs. They follow the screen's own settings sections where they can, so what is learned on
// one is near where it is on the other, and split where a page with every setting on it needs more
// tabs than a screen does. Each is a plain link (?tab=), and only the tab asked for
// is drawn: no script, and a save comes back to the tab it was made on.
//
// There is no Display tab. What the screen shows is set on the screen, which is where it can be seen
// changing - except the clock's look, what a tap on it does, the themes, the dashboard and the slideshow,
// which a device far from its owner or on a wall still needs set from a phone, and which are on Screen.
//
// The tabs were seven, and two of them held half the page: Sound & Voice had sixteen sections, from the
// volume to the wake word scores, and Connections had the Wi-Fi, Bluetooth, eight sections of cameras
// and the tailnets. Voice and Cameras are tabs of their own now, Connections is Network, and General,
// with the place and the calendars' links from what was Weather & Calendar, is System.

type tab struct{ id, title, blurb string }

var tabs = []tab{
	{"status", "Status", "What is connected and running"},
	{"alarms", "Alarms & Timers", "Alarms, timers and reminders"},
	{"sound", "Sound", "Speaker, music, radio, Bluetooth"},
	{"voice", "Voice", "Voice assistant, wake words, calls"},
	{"screen", "Screen", "Clock, themes, dashboard, photos"},
	{"cameras", "Cameras", "Cameras, doorbell, streaming"},
	{"network", "Network", "Wi-Fi, Home Assistant, away from home"},
	{"privacy", "Privacy", "Lock, baby monitor, remote access"},
	{"system", "System", "Name, place, time, updates, backup"},
}

// shownTabs are the tabs this device has: Screen only where there is a screen, and Voice only where
// there are microphones (layout.HasMicrophone).
func shownTabs() []tab {
	out := make([]tab, 0, len(tabs))
	for _, t := range tabs {
		if t.id == "screen" && !hasScreen || t.id == "voice" && !layout.HasMicrophone() {
			continue
		}
		out = append(out, t)
	}
	return out
}

// movedTabs are the tabs before they were regrouped, and where what was on each is now, for a link or
// a bookmark made before: it opens where most of that tab went rather than on Status.
var movedTabs = map[string]string{
	"connections": "network", "general": "system", "photos": "screen", "weather": "system",
}

// defaultTab is where the page opens: what the device is connected to and running (status.go), with
// every other tab one tap from it. It was Alarms & Timers; a page that opens on a form says nothing of
// whether the device it belongs to is well.
const defaultTab = "status"

// tabOf is the tab a request names, or the default for none or one that is not a tab here.
func tabOf(id string) string {
	if to, ok := movedTabs[id]; ok {
		id = to
	}
	for _, t := range shownTabs() {
		if t.id == id {
			return id
		}
	}
	return defaultTab
}

// head starts every page: the document and its style, in the device's own colors. The five colors
// are the theme's; everything between them is mixed from them, so a Custom theme works as well.
func head(w http.ResponseWriter) {
	p := pageColors()
	c := p.Hex()
	scheme, ok, bad := "dark", "#8bc34a", "#ff8a65"
	if p.Light() {
		scheme, ok, bad = "light", "#3f7a1c", "#b3401e"
	}
	fmt.Fprintf(w, `<!doctype html><html lang="en"><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>TECHO5 setup</title>
<style>
 :root{--bg:%s;--accent:%s;--text:%s;--dim:%s;--line:%s;--ok:%s;--bad:%s;color-scheme:%s;
  --field:color-mix(in srgb,var(--text) 6%%,var(--bg));--drawer:color-mix(in srgb,var(--text) 3%%,var(--bg));
  --dimtext:color-mix(in srgb,var(--dim) 70%%,var(--text));
  --edge:color-mix(in srgb,var(--text) 55%%,var(--bg));--tap:2.75rem}
 *{box-sizing:border-box}
 body{font:16px/1.5 system-ui,sans-serif;margin:0;background:var(--bg);color:var(--text)}
 .wrap{max-width:60rem;margin:0 auto;padding:1.5rem 1rem 3rem}
 h1{font-size:1.4rem;margin:0 0 .2rem} h2{font-size:1.2rem;margin:0 0 .8rem} p.sub{color:var(--dimtext);margin:0 0 1.2rem}
 fieldset{border:1px solid var(--line);border-radius:10px;margin:0 0 1rem;padding:1rem;min-width:0}
 fieldset,details{scroll-margin-top:4rem}
 fieldset:target,details:target{border-color:color-mix(in srgb,var(--accent) 60%%,var(--line))}
 legend{padding:0 .4rem;color:var(--accent)}
 h3{font-size:.85rem;margin:1.6rem 0 .6rem;color:var(--dimtext);text-transform:uppercase;letter-spacing:.06em} h2+h3{margin-top:.4rem}
 label{display:block;margin:.6rem 0 .2rem;color:var(--dimtext)}
 select,input,textarea{font:inherit;width:100%%;min-height:var(--tap);padding:.6rem .7rem;border-radius:8px;border:1px solid var(--edge);background:var(--field);color:inherit}
 input[type=checkbox],input[type=radio]{width:1.25rem;height:1.25rem;min-height:0;padding:0;margin-right:.4rem;accent-color:var(--accent);vertical-align:-.2rem}
 input[type=range],input[type=color]{min-height:0;padding:0}
 button{font:inherit;min-height:var(--tap);padding:.6rem 1.2rem;border:0;border-radius:999px;background:var(--accent);color:var(--bg);font-weight:600;cursor:pointer}
 :focus-visible{outline:2px solid var(--accent);outline-offset:2px}
 h4{font-size:1rem;margin:1rem 0 .4rem} code{overflow-wrap:anywhere}
 a{color:var(--accent)}
 .note{color:var(--dimtext);font-size:.9rem;max-width:65ch} .ok{color:var(--ok)} .bad{color:var(--bad)}
 .deckrow{display:grid;grid-template-columns:repeat(auto-fit,minmax(8.5rem,1fr));gap:.4rem;margin-bottom:.4rem} .deckrow>p{grid-column:1/-1}
 button.quiet{background:var(--field);color:var(--text);border:1px solid var(--edge);font-weight:500;padding:.5rem 1rem}
 button.danger{background:transparent;color:var(--bad);border:1px solid var(--bad)}
 .playing{color:var(--ok);display:flex;align-items:center;gap:.7rem;flex-wrap:wrap}
 .banner{border:1px solid color-mix(in srgb,var(--ok) 50%%,var(--bg));background:color-mix(in srgb,var(--ok) 10%%,var(--bg));border-radius:10px;padding:.5rem .8rem;margin:0 0 1rem}
 .body>.banner{margin-top:.9rem}
 .banner.bad{border-color:color-mix(in srgb,var(--bad) 50%%,var(--bg));background:color-mix(in srgb,var(--bad) 10%%,var(--bg))}
 .layout{display:grid;grid-template-columns:14rem minmax(0,1fr);gap:1.5rem;align-items:start}
 .layout>*{min-width:0}
 nav.rail{display:flex;flex-direction:column;gap:.25rem;position:sticky;top:1rem}
 nav.rail a{display:block;padding:.6rem .8rem;border-radius:10px;color:var(--text);text-decoration:none;border:1px solid transparent}
 nav.rail a small{display:block;color:var(--dimtext);font-size:.8rem;line-height:1.3}
 nav.rail a:hover{background:var(--field)}
 nav.rail a.on{background:var(--field);border-color:var(--line);color:var(--accent)}
 @media (max-width:44rem){
  .layout{grid-template-columns:minmax(0,1fr);gap:1rem}
  nav.rail{flex-direction:row;overflow-x:auto;position:sticky;top:0;z-index:1;background:var(--bg);padding:.3rem 0;border-bottom:1px solid var(--line)}
  nav.rail a{white-space:nowrap;padding:.65rem .9rem}
  nav.rail a small{display:none}
 }
 details{border:1px solid var(--line);border-radius:10px;margin:0 0 .6rem;background:var(--drawer)}
 details>summary{list-style:none;cursor:pointer;padding:.7rem .9rem;display:flex;gap:.7rem;align-items:center;flex-wrap:wrap}
 details>summary::-webkit-details-marker{display:none}
 details>summary::after{content:"›";margin-left:auto;color:var(--dimtext)}
 details[open]>summary::after{transform:rotate(90deg)}
 details .body{padding:0 .9rem .9rem;border-top:1px solid var(--line)}
 details>:not(summary):not(.body){margin:0 .9rem .9rem}
 .when{font-weight:600;font-variant-numeric:tabular-nums;min-width:4.6rem}
 .what{flex:1;min-width:8rem}
 .where{color:var(--dimtext);font-size:.85rem}
 .chip{font-size:.72rem;letter-spacing:.04em;text-transform:uppercase;padding:.1rem .5rem;border-radius:999px;border:1px solid var(--line);color:var(--dimtext)}
 .chip.rem{color:var(--accent);border-color:color-mix(in srgb,var(--accent) 45%%,var(--bg))}
 .chip.tim{color:var(--ok);border-color:color-mix(in srgb,var(--ok) 45%%,var(--bg))}
 .off{opacity:.75}
 .row{display:flex;gap:.6rem;flex-wrap:wrap;align-items:flex-end}
 .row>*{flex:1;min-width:7rem}
 .days{display:flex;gap:.3rem;flex-wrap:wrap;margin:.2rem 0}
 .days label,.kinds label{margin:0;display:flex;align-items:center;min-height:var(--tap);padding:.55rem .8rem;border:1px solid var(--edge);border-radius:999px;color:var(--text);font-size:.9rem}
 .kinds{display:flex;gap:.4rem;flex-wrap:wrap}
 form:has(input[name=kind]:not([value=alarm]):checked) .alarmonly{display:none}
 form:has(select[name=mode] option[value=""]:checked) .directonly{display:none}
 form:has(input[name=spotify]:not(:checked)) .spotifyonly{display:none}
 form:has(select[name=slideshow] option:not([value=screensaver]):checked) .saveronly{display:none}
 form:has(select[name=clockstyle] option:not(.world):checked) .worldonly{display:none}
 form:has(input[name=mode][value=dhcp]:checked) .fixedonly{display:none}
 form:has(input[name=mode][value=none]:checked) .theseonly,form:has(input[name=mode][value=all]:checked) .theseonly{display:none}
 .btns{display:flex;gap:.5rem;flex-wrap:wrap;margin-top:.8rem;align-items:center}
 .btns form{display:inline}
 .st{display:flex;gap:.7rem;align-items:baseline;margin:.5rem 0} .st:first-of-type{margin-top:0} .st:last-of-type{margin-bottom:0}
 .dot{flex:none;width:.65rem;height:.65rem;border-radius:50%%;background:var(--dim);transform:translateY(.05rem)}
 .dot.ok{background:var(--ok)} .dot.bad{background:var(--bad)}
 .top{display:flex;justify-content:space-between;align-items:flex-start;gap:1rem} .top form{flex:none}
 span.busy{margin-left:.6rem} button:disabled{opacity:.6;cursor:default}
 label.show{margin:.3rem 0 0} label.show[hidden]{display:none} p.secret{margin:.2rem 0 .6rem} p.secret label{margin:0}
</style>`, c[0], c[1], c[2], c[3], c[4], ok, bad, scheme)
}

// nav is the tabs, the one being shown marked.
func nav(w http.ResponseWriter, on string) {
	fmt.Fprint(w, `<nav class="rail">`)
	for _, t := range shownTabs() {
		cls := ""
		if t.id == on {
			cls = ` class="on" aria-current="page"`
		}
		fmt.Fprintf(w, `<a href="/setup?tab=%s"%s>%s<small>%s</small></a>`,
			t.id, cls, html.EscapeString(t.title), html.EscapeString(t.blurb))
	}
	fmt.Fprint(w, `</nav>`)
}

// tabFor is the tab each form lives on, by what the form saves. It is the one place that says so: a
// form names only what it saves, and a save comes back to the tab found here, so a section is moved
// to another tab by its line here and its call in settingsPage. The names with no form of their own
// through /setup/save are sections: the ones whose own pages send the browser back (the sounds'
// uploads, the settings file, sharing, the takes, the history, the remote key), and the ones several
// forms share (sectionFor).
var tabFor = map[string]string{
	"speaker": "sound", "stations": "sound", "music": "sound", "streaming": "sound", "bluetooth": "sound",
	"sound-play": "sound", "sound-remove": "sound", "sounds": "sound",

	"brain": "voice", "replyplayer": "voice", "realtime": "voice", "listening": "voice",
	"house": "voice", "phonebook": "voice", "wakecall": "voice", "wakeword-remove": "voice",
	"wakewords": "voice", "takes": "voice", "history": "voice",

	"alarm_add": "alarms", "alarm_edit": "alarms", "alarm_prefs": "alarms",
	"timer_cancel": "alarms", "snoozes_cancel": "alarms",

	"screen": "screen", "themes": "screen", "theme-colors": "screen", "font-remove": "screen", "fonts": "screen",
	"dashpanel": "screen", "dashboard": "screen", "calendar-look": "screen", "todo": "screen",
	"immich": "screen", "immich-album": "screen", "immich-show": "screen", "immich-forget": "screen",
	"photos-remove": "screen", "photos": "screen", "video": "screen",
	"deckobs": "screen", "deckgrid": "screen", "deckpage": "screen", "deckpc": "screen", "deck": "screen",

	"reolink": "cameras", "doorbell": "cameras", "doorbell-chime": "cameras", "hacameras": "cameras",
	"go2rtc": "cameras", "cameralist": "cameras", "camerabuttons": "cameras", "talkback": "cameras",
	"camstream": "cameras",

	"wifi": "network", "forget": "network", "address": "network", "adopt": "network", "haaccess": "network", "timeserver": "network",
	"tailscale": "network", "netbird": "network", "wallclock": "network",

	"lock": "privacy", "baby": "privacy", "babylink": "privacy", "babylight": "privacy", "remote": "privacy",

	"name": "system", "place": "system", "timezone": "system", "calendar": "system", "calendar-remove": "system",
	"updates": "system", "update-install": "system", "update-check": "system", "update-auto": "system",
	"ringwait": "system", "settingsfile": "system", "share": "system", "giftreset": "system",
}

// formTab is the tab a form is on here: its tabFor, except on a device with no microphones, which has no
// Voice tab and keeps the one form of it it has, the house word, on Sound (settingsPage).
func formTab(what string) string {
	t := tabFor[what]
	if t == "voice" && !layout.HasMicrophone() {
		t = "sound"
	}
	return tabOf(t)
}

// hidden is the fields every form carries: the session it came from and what it saves. Where the
// save comes back to is not the form's to say (tabFor).
func hidden(w http.ResponseWriter, token, what string) {
	fmt.Fprintf(w, `<input type="hidden" name="token" value="%s"><input type="hidden" name="what" value="%s">`,
		html.EscapeString(token), html.EscapeString(what))
}

// back is where a save sends the browser: the tab the form it came from lives on, with what happened,
// and down the page at the section it was made in (section.go), where it is said. Something that is
// not a form here comes back to the tab the page opens on. A rename comes back to the top: the device
// is restarting, and that is said above everything.
func back(what, key, value string) string {
	q := url.Values{"tab": {formTab(what)}}
	if key != "" {
		q.Set(key, value)
	}
	section := sectionNamed(sectionOf(what))
	if section == "" || key == "renamed" {
		return "/setup?" + q.Encode()
	}
	if key != "" {
		q.Set("at", section)
	}
	return "/setup?" + q.Encode() + "#sec-" + section
}
