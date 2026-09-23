package setup

import (
	"fmt"
	"html"
	"net/http"
	"net/url"
)

// The page's tabs: the device's own settings sections, in the order its screen lists them, so what is
// learned on one is where it is on the other. Each is a plain link (?tab=), and only the tab asked for
// is drawn: no script, and a save comes back to the tab it was made on.
//
// There is no Display tab. What the screen shows is set on the screen, which is where it can be seen
// changing.

type tab struct{ id, title, blurb string }

var tabs = []tab{
	{"sound", "Sound & Voice", "Announcements, radio"},
	{"alarms", "Alarms & Timers", "Alarms, timers and reminders"},
	{"connections", "Connections", "Wi-Fi"},
	{"privacy", "Privacy & Security", "What this device shares"},
	{"general", "General", "Name, time zone, help"},
}

// defaultTab is where the page opens: the section people come to this page for most.
const defaultTab = "alarms"

// tabOf is the tab a request names, or the default for none or one that is not a tab.
func tabOf(id string) string {
	for _, t := range tabs {
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
  --dimtext:color-mix(in srgb,var(--dim) 70%%,var(--text))}
 *{box-sizing:border-box}
 body{font:16px/1.5 system-ui,sans-serif;margin:0;background:var(--bg);color:var(--text)}
 .wrap{max-width:60rem;margin:0 auto;padding:1.5rem 1rem 3rem}
 h1{font-size:1.4rem;margin:0 0 .2rem} h2{font-size:1.2rem;margin:0 0 .8rem} p.sub{color:var(--dimtext);margin:0 0 1.2rem}
 fieldset{border:1px solid var(--line);border-radius:10px;margin:0 0 1rem;padding:1rem;min-width:0}
 legend{padding:0 .4rem;color:var(--accent)}
 label{display:block;margin:.6rem 0 .2rem;color:var(--dimtext)}
 select,input{font:inherit;width:100%%;padding:.5rem;border-radius:8px;border:1px solid var(--line);background:var(--field);color:inherit}
 input[type=checkbox],input[type=radio]{width:auto;margin-right:.4rem}
 button{font:inherit;padding:.55rem 1.1rem;border:0;border-radius:999px;background:var(--accent);color:var(--bg);font-weight:600;cursor:pointer}
 a{color:var(--accent)}
 .note{color:var(--dimtext);font-size:.9rem} .ok{color:var(--ok)} .bad{color:var(--bad)}
 button.quiet{background:var(--field);color:var(--text);border:1px solid var(--line);font-weight:500;padding:.35rem .9rem}
 .playing{color:var(--ok);display:flex;align-items:center;gap:.7rem;flex-wrap:wrap}
 .banner{border:1px solid color-mix(in srgb,var(--ok) 50%%,var(--bg));background:color-mix(in srgb,var(--ok) 10%%,var(--bg));border-radius:10px;padding:.5rem .8rem;margin:0 0 1rem}
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
  nav.rail{flex-direction:row;overflow-x:auto;position:static;padding-bottom:.3rem;border-bottom:1px solid var(--line)}
  nav.rail a{white-space:nowrap;padding:.45rem .8rem}
  nav.rail a small{display:none}
 }
 details{border:1px solid var(--line);border-radius:10px;margin:0 0 .6rem;background:var(--drawer)}
 details>summary{list-style:none;cursor:pointer;padding:.7rem .9rem;display:flex;gap:.7rem;align-items:center;flex-wrap:wrap}
 details>summary::-webkit-details-marker{display:none}
 details>summary::after{content:"›";margin-left:auto;color:var(--dimtext)}
 details[open]>summary::after{transform:rotate(90deg)}
 details .body{padding:0 .9rem .9rem;border-top:1px solid var(--line)}
 .when{font-weight:600;font-variant-numeric:tabular-nums;min-width:4.6rem}
 .what{flex:1;min-width:8rem}
 .where{color:var(--dimtext);font-size:.85rem}
 .chip{font-size:.72rem;letter-spacing:.04em;text-transform:uppercase;padding:.1rem .5rem;border-radius:999px;border:1px solid var(--line);color:var(--dimtext)}
 .chip.rem{color:var(--accent);border-color:color-mix(in srgb,var(--accent) 45%%,var(--bg))}
 .chip.tim{color:var(--ok);border-color:color-mix(in srgb,var(--ok) 45%%,var(--bg))}
 .off{opacity:.55}
 .row{display:flex;gap:.6rem;flex-wrap:wrap;align-items:flex-end}
 .row>*{flex:1;min-width:7rem}
 .days{display:flex;gap:.3rem;flex-wrap:wrap;margin:.2rem 0}
 .days label,.kinds label{margin:0;display:flex;align-items:center;padding:.3rem .7rem;border:1px solid var(--line);border-radius:999px;color:var(--text);font-size:.9rem}
 .kinds{display:flex;gap:.4rem;flex-wrap:wrap}
 .btns{display:flex;gap:.5rem;flex-wrap:wrap;margin-top:.8rem;align-items:center}
 .btns form{display:inline}
</style>`, c[0], c[1], c[2], c[3], c[4], ok, bad, scheme)
}

// nav is the tabs, the one being shown marked.
func nav(w http.ResponseWriter, on string) {
	fmt.Fprint(w, `<nav class="rail">`)
	for _, t := range tabs {
		cls := ""
		if t.id == on {
			cls = ` class="on" aria-current="page"`
		}
		fmt.Fprintf(w, `<a href="/setup?tab=%s"%s>%s<small>%s</small></a>`,
			t.id, cls, html.EscapeString(t.title), html.EscapeString(t.blurb))
	}
	fmt.Fprint(w, `</nav>`)
}

// hidden is the fields every form carries: the session it came from, what it saves, and the tab to
// come back to.
func hidden(w http.ResponseWriter, token, what, tab string) {
	fmt.Fprintf(w, `<input type="hidden" name="token" value="%s"><input type="hidden" name="what" value="%s"><input type="hidden" name="tab" value="%s">`,
		html.EscapeString(token), html.EscapeString(what), html.EscapeString(tab))
}

// back is where a save sends the browser: the tab it came from, with what happened.
func back(tab, key, value string) string {
	q := url.Values{"tab": {tabOf(tab)}}
	if key != "" {
		q.Set(key, value)
	}
	return "/setup?" + q.Encode()
}
