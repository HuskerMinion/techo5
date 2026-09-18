//go:build !dot && !spot

package display

import (
	"fmt"
	"image"
	"strings"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/touch"
)

func testRenderer() *renderer { return newRenderer(image.NewRGBA(image.Rect(0, 0, 960, 480))) }

// A tap takes the topmost thing under it; a near miss takes what it nearly hit; and with a list open,
// nothing under the list can be reached, only its choices or the tap that puts it away.
func TestZoneAt(t *testing.T) {
	r := testRenderer()
	r.zones = []zone{
		{r: image.Rect(0, 0, 100, 100), kind: zoneRow, id: "under"},
		{r: image.Rect(40, 40, 60, 60), kind: zoneRow, id: "over"},
		{r: image.Rect(300, 300, 340, 340), kind: zoneRow, id: "small"},
	}
	for _, c := range []struct {
		x, y int
		want string
	}{{10, 10, "under"}, {50, 50, "over"}, {320, 320, "small"}, {345, 320, "small"}, {400, 400, ""}} {
		z, ok := r.zoneAt(c.x, c.y)
		if got := map[bool]string{true: z.id, false: ""}[ok]; got != c.want {
			t.Errorf("tap at %d,%d = %q, want %q", c.x, c.y, got, c.want)
		}
	}

	r.zones = append(r.zones,
		zone{r: r.dst.Rect, kind: zoneDismiss},
		zone{r: image.Rect(500, 100, 700, 150), kind: zoneOption, opt: 3})
	if z, _ := r.zoneAt(50, 50); z.kind != zoneDismiss {
		t.Errorf("with a list open, a tap on the row under it reached %v", z.kind)
	}
	if z, _ := r.zoneAt(600, 155); z.kind != zoneOption || z.opt != 3 {
		t.Errorf("a near miss on a choice = %+v, want choice 3", z)
	}
}

// sheetScene is a settings screen on a category, with enough in it to draw every kind of row.
func sheetScene(cat category) scene {
	now := time.Date(2026, 9, 18, 15, 0, 0, 0, time.Local)
	s := scene{now: now, snooze: 9}
	s.sheet = settings{cat: cat, now: now, brightness: 75, volume: 5, name: "Kitchen", version: "v0.0.0",
		slot: "a", address: "192.168.1.50", wifiName: "HomeWiFi", wifiOK: true, weather: "Home"}
	s.bt.Available, s.bt.Remembered = true, "Speaker"
	s.security.SSHAvailable, s.security.Encrypted = true, true
	s.alarms.Local = []config.Alarm{{ID: "a", Hour: 6, Minute: 30, Days: config.DaysWeekdays, On: true}}
	return s
}

// Every category, and each editor, draws its rows with somewhere to tap for every row that does
// something, and all of it on the screen.
func TestEveryCardDraws(t *testing.T) {
	cases := map[string]scene{}
	for c := category(0); c < categories; c++ {
		cases[categoryNames[c]] = sheetScene(c)
	}
	editor := sheetScene(catAlarms)
	editor.draft = &alarmDraft{alarm: editor.alarms.Local[0]}
	cases["alarm editor"] = editor
	colours := sheetScene(catDisplay)
	colours.sheet.colours = true
	cases["custom colours"] = colours

	for name, s := range cases {
		r := testRenderer()
		r.settingsScreen(s)
		v := categoryCard(s.view())
		if len(v.rows) == 0 {
			t.Errorf("%s: no rows", name)
			continue
		}
		ids := map[string]bool{}
		cats, done := 0, 0
		for _, z := range r.zones {
			if !z.r.In(r.dst.Rect) || z.r.Empty() {
				t.Errorf("%s: zone %+v is off the screen or empty", name, z)
			}
			switch z.kind {
			case zoneRow:
				ids[z.id] = true
			case zoneCat:
				cats++
			case zoneDone:
				done++
			}
		}
		if cats != int(categories) || done != 1 {
			t.Errorf("%s: %d categories and %d Done on the rail, want %d and 1", name, cats, done, categories)
		}
		// The rows in view: every one with an id can be tapped.
		shown := (r.h - 2*cardIn - headerH - 8) / rowH
		for i, row := range v.rows {
			if i < shown && row.id != "" && !ids[row.id] {
				t.Errorf("%s: row %q has nowhere to tap", name, row.label)
			}
		}
	}
}

// A card with more rows than fit scrolls, as far as its last row and no further, and nothing it
// draws while scrolled can be tapped outside the list.
func TestCardScrolls(t *testing.T) {
	s := sheetScene(catAlarms)
	s.alarms.Local = nil
	for i := range 10 {
		s.alarms.Local = append(s.alarms.Local, config.Alarm{ID: fmt.Sprint(i), Hour: i + 5, On: true})
	}
	r := testRenderer()
	r.settingsScreen(s)
	cardMax, _ := r.scrollLimits()
	if want := 12*rowH - (r.h - 2*cardIn - headerH - 8); cardMax != want {
		t.Fatalf("12 rows scroll %d, want %d", cardMax, want)
	}

	s.sheet.cardScroll = cardMax + 500 // held to the end
	r.settingsScreen(s)
	list := image.Rect(railW, cardIn+headerH, r.w-cardIn, r.h-cardIn-8)
	var last bool
	for _, z := range r.zones {
		if z.kind == zoneRow && !z.r.In(list) {
			t.Errorf("row zone %+v outside the list %v", z, list)
		}
		last = last || z.id == "alarmsound"
	}
	if !last {
		t.Error("scrolled to the end, the last row cannot be tapped")
	}
}

// Short lists sit in columns with every choice in view; a long one is a single column that scrolls.
func TestPickerLayout(t *testing.T) {
	for _, n := range []int{4, 13, 30} {
		p := pickerView{title: "List", cur: 0}
		for i := range n {
			p.opts = append(p.opts, fmt.Sprintf("Choice %d", i+1))
		}
		r := testRenderer()
		r.pending = r.pending[:0]
		pickMax := r.picker(p, 0)
		var opts []image.Rectangle
		for _, z := range r.pending {
			if z.kind == zoneOption {
				opts = append(opts, z.r)
			}
		}
		switch {
		case n <= pickScrollOver && (pickMax != 0 || len(opts) != n):
			t.Errorf("%d choices: scroll %d, %d in view; want 0 and all", n, pickMax, len(opts))
		case n > pickScrollOver && (pickMax <= 0 || len(opts) > pickRows+1):
			t.Errorf("%d choices: scroll %d, %d in view; want a scroll and at most %d", n, pickMax, len(opts), pickRows+1)
		}
		for i := range opts {
			for j := i + 1; j < len(opts); j++ {
				if opts[i].Inset(8).Overlaps(opts[j].Inset(8)) {
					t.Errorf("%d choices: %d and %d overlap", n, i, j)
				}
			}
		}
	}
}

// Screenshots for publishing hide station names: call letters say where the owner lives.
func TestDemoHidesStations(t *testing.T) {
	s := scene{drawerTab: drawerRadio, demo: true}
	s.radio = home.Radio{Configured: true, Stations: []string{"101.1 WXYZ", "88.5 KABC"}}
	rows, _ := drawerRows(s)
	for _, row := range rows {
		if strings.Contains(row.label, "WXYZ") || strings.Contains(row.label, "KABC") {
			t.Errorf("demo shows %q", row.label)
		}
	}
	s.demo = false
	rows, _ = drawerRows(s)
	if rows[1].label != "101.1 WXYZ" {
		t.Errorf("without demo, first station = %q", rows[1].label)
	}
}

// The swipe that opened the settings keeps reporting notches until its finger lifts; those do not
// scroll. Another swipe does, a notch at a time, and stops at the ends.
func TestSheetSwipe(t *testing.T) {
	d := &Display{r: testRenderer()}
	d.r.cardMax = 100
	d.openedBy = image.Pt(480, 30)
	d.sheetSwipe(touch.Gesture{Kind: touch.SwipeUp, X: 480, Y: 30})
	if d.cardScroll != 0 {
		t.Fatalf("the opening swipe scrolled to %d", d.cardScroll)
	}
	for range 5 {
		d.sheetSwipe(touch.Gesture{Kind: touch.SwipeUp, X: 600, Y: 300})
	}
	if d.cardScroll != 100 {
		t.Errorf("five notches up = %d, want held at 100", d.cardScroll)
	}
	d.sheetSwipe(touch.Gesture{Kind: touch.SwipeDown, X: 600, Y: 300})
	if d.cardScroll != 100-notchPx {
		t.Errorf("a notch back = %d, want %d", d.cardScroll, 100-notchPx)
	}
}

func TestSheetWords(t *testing.T) {
	defer clock24.Store(clock24.Load())
	clock24.Store(false)
	if got := nightText("22-6"); got != "10 PM – 6 AM" {
		t.Errorf("nightText = %q", got)
	}
	if got := nightText(""); got != "Never" {
		t.Errorf("nightText(off) = %q", got)
	}
	clock24.Store(true)
	if got := nightText("22-6"); got != "22:00 – 06:00" {
		t.Errorf("24-hour nightText = %q", got)
	}

	if repeatName(config.DaysWeekdays) != "Weekdays" || repeatName(0x05) != "Custom" {
		t.Error("repeatName")
	}
	now := time.Date(2026, 9, 18, 15, 0, 0, 0, time.Local)
	for at, want := range map[time.Time]string{
		now.Add(time.Hour): "Today", now.Add(20 * time.Hour): "Tomorrow", now.Add(72 * time.Hour): "Monday",
	} {
		if got := dayWord(at, now); got != want {
			t.Errorf("dayWord(%v) = %q, want %q", at, got, want)
		}
	}
}

// Changing category repaints the parts of the screen that depend on it; the rest is kept. On the
// Show this is what a tap on the rail waits for.
func BenchmarkCategorySwitch(b *testing.B) {
	r := testRenderer()
	scenes := make([]scene, categories)
	for c := range scenes {
		scenes[c] = sheetScene(category(c))
	}
	r.settingsScreen(scenes[0])
	b.ResetTimer()
	for i := range b.N {
		r.settingsScreen(scenes[(i+1)%len(scenes)])
	}
}

// Opening a list over the card: the long Theme list, in columns.
func BenchmarkPickerOpen(b *testing.B) {
	r := testRenderer()
	s := sheetScene(catDisplay)
	s.sheet.picker = "theme"
	b.ResetTimer()
	for range b.N {
		r.settingsScreen(s)
	}
}
