package setup

import (
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/timer"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/buttons"
)

// in is a browser that has been let in, and the token its forms carry.
func in(t *testing.T) (*Feature, *http.Cookie) {
	t.Helper()
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	f := build()
	f.Open()
	c := ask(t, f)
	f.button(buttons.Event{Name: buttons.Action, Kind: buttons.Tap})
	return f, c
}

// post sends a form as the page does, and returns where it was sent back to.
func post(t *testing.T, f *Feature, c *http.Cookie, v url.Values) *url.URL {
	t.Helper()
	v.Set("token", c.Value)
	r := httptest.NewRequest(http.MethodPost, "/setup/save", strings.NewReader(v.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(c)
	w := httptest.NewRecorder()
	f.serve(w, r)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("saving %v: %d %s", v, w.Code, w.Body.String())
	}
	to, err := url.Parse(w.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	return to
}

func TestEveryTabDrawsAndMarksItself(t *testing.T) {
	f, c := in(t)
	for _, tb := range shownTabs() {
		body := get(f, "/setup?tab="+tb.id, c).Body.String()
		if !strings.Contains(body, `href="/setup?tab=`+tb.id+`" class="on"`) {
			t.Errorf("tab %s is not marked as the one shown", tb.id)
		}
	}
	if body := get(f, "/setup?tab=nonsense", c).Body.String(); !strings.Contains(body, `tab=status" class="on"`) {
		t.Error("a tab that is not one did not fall back to Status")
	}
	if body := get(f, "/setup?tab=general", c).Body.String(); !strings.Contains(body, "Time zone") ||
		!strings.Contains(body, "Download diagnostics") {
		t.Error("General lost the time zone or the diagnostics")
	}
}

// A save comes back to the tab it was made on, or every save would throw the page back to the first,
// and to the section it was made in, where what happened is said.
func TestASaveComesBackToItsTab(t *testing.T) {
	f, c := in(t)
	to := post(t, f, c, url.Values{"what": {"house"}, "word": {"bluebird"}})
	if to.Query().Get("tab") != "sound" || to.Query().Get("saved") != "1" || to.Query().Get("at") != "house" ||
		to.Fragment != "sec-house" {
		t.Errorf("sent back to %s", to)
	}
	body := get(f, to.RequestURI(), c).Body.String()
	if !strings.Contains(body, `<fieldset id="sec-house"><legend>Announcements</legend><div class="banner ok" role="status">Saved.</div>`) {
		t.Error("Saved is not said in the section the save was made in")
	}
	if strings.Count(body, `class="banner ok"`) != 1 {
		t.Error("Saved is said more than once")
	}

	to = post(t, f, c, url.Values{"what": {"alarm_add"}, "kind": {"alarm"}, "at": {""}})
	if to.Query().Get("tab") != "alarms" || to.Query().Get("problem") == "" || to.Fragment != "sec-alarm_add" {
		t.Errorf("a refused save was sent back to %s", to)
	}
	// What went wrong arrives as words and is shown escaped, in the drawer it was typed in, left open.
	body = get(f, to.RequestURI(), c).Body.String()
	if !strings.Contains(body, `banner bad" role="alert">an alarm needs a time, in the At box`) {
		t.Errorf("the problem is not on the page: %s", first(body))
	}
	drawer := strings.Index(body, `<details id="sec-alarm_add" open>`)
	if said := strings.Index(body, `banner bad`); drawer < 0 || said < drawer {
		t.Error("the problem is not said in the drawer it was made in, or the drawer is shut")
	}

	// A form that shares a section with others comes back to that section.
	to = post(t, f, c, url.Values{"what": {"update-auto"}, "auto": {"yes"}})
	if to.Query().Get("at") != "updates" || to.Fragment != "sec-updates" {
		t.Errorf("a form in a shared section was sent back to %s", to)
	}
}

// What happened is said above the sections, as it used to be, when the section named is not on the
// page or is not a section at all; and a name from the address is never written into the page.
func TestWhatHappenedIsSaidAboveWhenItsSectionIsNotThere(t *testing.T) {
	f, c := in(t)
	for _, at := range []string{"", "house", "nonsense", `%22%3E%3Cb%3Ehello`} {
		body := get(f, "/setup?tab=general&saved=1&at="+at, c).Body.String()
		top := strings.Index(body, `</h2><div class="banner ok" role="status">Saved.</div>`)
		if top < 0 || strings.Count(body, `class="banner ok"`) != 1 {
			t.Errorf("at=%q: Saved is not said once, above the sections", at)
		}
		if strings.Contains(body, "hello") {
			t.Errorf("at=%q was written into the page", at)
		}
	}
	body := get(f, "/setup?tab=general&problem=no&at=timezone", c).Body.String()
	if !strings.Contains(body, `<fieldset id="sec-timezone"><legend>Time zone</legend><div class="banner bad" role="alert">no</div>`) ||
		strings.Count(body, `class="banner bad" role="alert"`) != 1 {
		t.Error("a problem is not said once, in its section")
	}
}

func TestAlarmsRemindersAndTimersFromThePage(t *testing.T) {
	f, c := in(t)
	add := func(v url.Values) {
		t.Helper()
		v.Set("what", "alarm_add")
		v.Set("tab", "alarms")
		if p := post(t, f, c, v).Query().Get("problem"); p != "" {
			t.Fatalf("adding %v: %s", v, p)
		}
	}
	add(url.Values{"kind": {"alarm"}, "at": {"06:45"}, "label": {"Wake up"}, "day": {"1", "2", "3", "4", "5"}, "alarm_sunrise": {"10"}})
	add(url.Values{"kind": {"reminder"}, "in": {"20 minutes"}, "label": {"Pasta"}, "ring_on": {"Kitchen, Office"}})
	add(url.Values{"kind": {"timer"}, "in": {"10 minutes"}, "label": {"Eggs"}})

	list := config.Get().Alarms.List
	if len(list) != 2 {
		t.Fatalf("%d alarms saved, want an alarm and a reminder", len(list))
	}
	var wake, pasta config.Alarm
	for _, a := range list {
		if a.Remind {
			pasta = a
		} else {
			wake = a
		}
	}
	if wake.Hour != 6 || wake.Minute != 45 || wake.Days != config.DaysWeekdays || wake.Sunrise != 10 || wake.Label != "Wake up" {
		t.Errorf("the alarm saved as %+v", wake)
	}
	if pasta.Label != "Pasta" || len(pasta.RingOn) != 2 || pasta.RingOn[1] != "Office" {
		t.Errorf("the reminder saved as %+v", pasta)
	}
	var eggs string
	for _, tm := range timer.Get().List(time.Now()) {
		if tm.Name == "Eggs" {
			eggs = tm.ID
		}
	}
	if eggs == "" {
		t.Fatal("the timer was not started")
	}

	body := get(f, "/setup?tab=alarms", c).Body.String()
	for _, want := range []string{"Wake up", "Pasta", "Eggs", "here and Kitchen, Office", "light 10 min"} {
		if !strings.Contains(body, want) {
			t.Errorf("the tab does not show %q", want)
		}
	}

	edit := url.Values{"what": {"alarm_edit"}, "tab": {"alarms"}, "id": {wake.ID}, "act": {"save"},
		"at": {"07:00"}, "label": {"Up"}, "alarm_sunrise": {"-1"}}
	if p := post(t, f, c, edit).Query().Get("problem"); p != "" {
		t.Fatal(p)
	}
	for _, a := range config.Get().Alarms.List {
		if a.ID == wake.ID && (a.Hour != 7 || a.Label != "Up" || a.Days != config.DaysOnce || a.Sunrise != config.SunriseOff) {
			t.Errorf("the edit saved as %+v", a)
		}
	}
	post(t, f, c, url.Values{"what": {"alarm_edit"}, "tab": {"alarms"}, "id": {wake.ID}, "act": {"toggle"}})
	for _, a := range config.Get().Alarms.List {
		if a.ID == wake.ID && a.On {
			t.Error("turning it off left it on")
		}
	}
	post(t, f, c, url.Values{"what": {"alarm_edit"}, "tab": {"alarms"}, "id": {pasta.ID}, "act": {"delete"}})
	post(t, f, c, url.Values{"what": {"timer_cancel"}, "tab": {"alarms"}, "id": {eggs}})
	if n := len(config.Get().Alarms.List); n != 1 {
		t.Errorf("%d alarms after deleting the reminder", n)
	}
	for _, tm := range timer.Get().List(time.Now()) {
		if tm.ID == eggs {
			t.Error("the timer is still running after it was canceled")
		}
	}

	// A reminder has to say something, from the page as from Home Assistant.
	v := url.Values{"what": {"alarm_add"}, "tab": {"alarms"}, "kind": {"reminder"}, "at": {"14:30"}}
	if post(t, f, c, v).Query().Get("problem") == "" {
		t.Error("a reminder with no words was accepted")
	}
}

// What was done is said as what it was: a form that only plays something, looks for an update or forgets
// a network does not come back as "Saved.", and a code the page does not know is shown as a save, never
// as itself.
func TestWhatWasDoneIsSaidAsItWas(t *testing.T) {
	f, c := in(t)
	to := post(t, f, c, url.Values{"what": {"update-check"}, "tab": {"general"}})
	if got := to.Query().Get("saved"); to.Query().Get("problem") == "" && got != "checked" {
		t.Errorf("checking for an update came back as %q", got)
	}
	body := get(f, "/setup?tab=general&saved=checked", c).Body.String()
	if !strings.Contains(body, "Checked for an update.") || strings.Contains(body, ">Saved.<") {
		t.Errorf("the page does not say an update was checked for: %s", first(body))
	}
	body = get(f, "/setup?tab=general&saved=%3Cb%3Ehello", c).Body.String()
	if !strings.Contains(body, ">Saved.<") || strings.Contains(body, "hello") {
		t.Error("a code the page does not know was not shown as a plain save")
	}
	for code, said := range doneSaid {
		if said == "" || doneWords(code) != said {
			t.Errorf("code %q says %q", code, doneWords(code))
		}
	}
}

// The page opens on Status, which says whether Home Assistant is connected, what software runs, and
// when the page closes; and says so plainly when Home Assistant is not there.
func TestThePageOpensOnWhatIsConnected(t *testing.T) {
	f, c := in(t)
	was := statusHome
	t.Cleanup(func() { statusHome = was })

	statusHome = func() bool { return true }
	body := get(f, "/setup", c).Body.String()
	if !strings.Contains(body, `tab=status" class="on"`) {
		t.Fatal("the page did not open on Status")
	}
	for _, want := range []string{"Home Assistant</strong> is connected.", "Software</strong>", "This page</strong>"} {
		if !strings.Contains(body, want) {
			t.Errorf("Status does not say %q", want)
		}
	}
	statusHome = func() bool { return false }
	if body := get(f, "/setup?tab=status", c).Body.String(); !strings.Contains(body, "is not connected") ||
		!strings.Contains(body, `class="dot bad"`) {
		t.Error("with Home Assistant away, Status does not say so")
	}
}

// formsInTheCode reads this package's own source for every name a form goes by: what the save handler
// and the alarms' part of it have a case for, what a section's form is written with (hidden), and what
// a page of its own comes back as (back). Reading the source finds the ones another build leaves out.
func formsInTheCode(t *testing.T) map[string]string {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]string{}
	lit := func(e ast.Expr, where string) {
		if b, ok := e.(*ast.BasicLit); ok && b.Kind == token.STRING {
			if s, err := strconv.Unquote(b.Value); err == nil {
				found[s] = where
			}
		}
	}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.FuncDecl:
				if n.Body == nil || (n.Name.Name != "save" && n.Name.Name != "alarmSave") {
					break
				}
				ast.Inspect(n.Body, func(n ast.Node) bool {
					if c, ok := n.(*ast.CaseClause); ok {
						for _, e := range c.List {
							lit(e, name+": a case in the save handler")
						}
					}
					return true
				})
			case *ast.CallExpr:
				fn, ok := n.Fun.(*ast.Ident)
				switch {
				case ok && fn.Name == "hidden" && len(n.Args) == 3:
					lit(n.Args[2], name+": a form")
				case ok && fn.Name == "back" && len(n.Args) == 3:
					lit(n.Args[0], name+": a page that comes back")
				case ok && (fn.Name == "fieldsetOpen" || fn.Name == "sayHere" || fn.Name == "cameBackTo") && len(n.Args) >= 2:
					lit(n.Args[1], name+": a section")
				}
			}
			return true
		})
	}
	for what := range saves {
		found[what] = "a form added to saves"
	}
	return found
}

// Every form has a tab to come back to, and it is one of the page's tabs: a form the map does not
// know would come back to Status, far from where it was sent.
func TestEveryFormHasATab(t *testing.T) {
	real := map[string]bool{}
	for _, tb := range tabs {
		real[tb.id] = true
	}
	// A device with no screen has no Screen & Photos tab, and none of its forms either; the map still
	// names them, for the builds that do.
	real["photos"] = true
	forms := formsInTheCode(t)
	for _, must := range []string{"wifi", "forget", "house", "alarm_add", "timer_cancel", "camstream", "sounds", "share"} {
		if forms[must] == "" {
			t.Errorf("the source was read without finding the form %q", must)
		}
	}
	for what, where := range forms {
		if tab, ok := tabFor[what]; !ok {
			t.Errorf("%q (%s) has no tab in tabFor", what, where)
		} else if !real[tab] {
			t.Errorf("%q is on %q, which is not a tab", what, tab)
		}
	}
	for what, tab := range tabFor {
		if !real[tab] {
			t.Errorf("tabFor puts %q on %q, which is not a tab", what, tab)
		}
	}
	// A form drawn in another's section is on that section's tab, or its save would come back to a tab
	// the section is not on.
	for what, section := range sectionFor {
		if tabFor[what] == "" || tabFor[what] != tabFor[section] {
			t.Errorf("%q is on %q and its section %q on %q", what, tabFor[what], section, tabFor[section])
		}
	}
}
