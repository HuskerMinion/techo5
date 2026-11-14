//go:build !dot && !spot

package display

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/alarm"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/feature/phone"
	"github.com/HuskerMinion/techo5/echod/internal/feature/tessera"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/touch"
)

// pressCase is a page and one control on it: where is asked of the renderer that has just drawn the
// page plain, and says where the control is.
type pressCase struct {
	name  string
	scene scene
	where func(r *renderer) image.Rectangle
}

func pressCases() []pressCase {
	at := time.Date(2026, 9, 16, 14, 7, 0, 0, time.Local)
	song := home.Radio{Now: "KXYZ 101.1", Title: "Take It Easy", Artist: "Eagles"}
	firstZone := func(kind zoneKind) func(r *renderer) image.Rectangle {
		return func(r *renderer) image.Rectangle {
			r.zmu.Lock()
			defer r.zmu.Unlock()
			for _, z := range r.zones {
				if z.kind == kind && z.r.Dy() <= r.h/2 {
					return z.r
				}
			}
			return image.Rectangle{}
		}
	}
	firstTile := func(r *renderer) image.Rectangle {
		tiles, _ := r.dash()
		for _, t := range tiles {
			if t.page == 0 {
				return t.r
			}
		}
		return image.Rectangle{}
	}
	return []pressCase{
		{"ringing-stop", scene{now: at, phase: "idle", ring: ringState{timer: "Pasta"}}, func(r *renderer) image.Rectangle {
			left, right := r.actionHalves()
			return left.Union(right)
		}},
		{"ringing-snooze", scene{now: at, phase: "idle", ring: ringState{alarm: &alarm.Ring{Label: "Wake up"}, snoozable: true}, snooze: 9},
			func(r *renderer) image.Rectangle { _, right := r.actionHalves(); return right }},
		{"call-answer", scene{now: at, phase: "idle", call: phone.State{Phase: phone.Ringing, Peer: "104"}},
			func(r *renderer) image.Rectangle { _, right := r.actionHalves(); return right }},
		{"call-decline", scene{now: at, phase: "idle", call: phone.State{Phase: phone.Ringing, Peer: "104"}},
			func(r *renderer) image.Rectangle { left, _ := r.actionHalves(); return left }},
		{"call-hang-up", scene{now: at, phase: "idle", call: phone.State{Phase: phone.Talking, Peer: "104", Since: at.Add(-90 * time.Second)}},
			func(r *renderer) image.Rectangle {
				left, right := r.actionHalves()
				return left.Union(right)
			}},
		{"nowplaying-stop", scene{now: at, phase: "idle", nowPlaying: true, playing: true, radio: song},
			func(r *renderer) image.Rectangle { return r.doneButton() }},
		{"nowplaying-star", scene{now: at, phase: "idle", nowPlaying: true, playing: true, radio: song},
			func(r *renderer) image.Rectangle { return r.favButton() }},
		{"nowplaying-back", scene{now: at, phase: "idle", nowPlaying: true, playing: true, radio: song},
			func(r *renderer) image.Rectangle { back, _, _ := r.musicButtons(false); return back }},
		{"nowplaying-play", scene{now: at, phase: "idle", nowPlaying: true, playing: true, radio: song},
			func(r *renderer) image.Rectangle { _, play, _ := r.musicButtons(false); return play }},
		{"nowplaying-modern-next", scene{now: at, phase: "idle", nowPlaying: true, modernMusic: true, playing: true, radio: song},
			func(r *renderer) image.Rectangle { _, _, next := r.musicButtons(true); return next }},
		{"strip-play", scene{now: at, phase: "idle", strip: true, playing: true, radio: song},
			func(r *renderer) image.Rectangle { _, play, _, _ := r.stripButtons(); return play }},
		{"strip-close", scene{now: at, phase: "idle", strip: true, playing: true, radio: song},
			func(r *renderer) image.Rectangle { _, _, _, x := r.stripButtons(); return x }},
		{"calendar-done", calendarScenes()["calendar-month"], func(r *renderer) image.Rectangle {
			r.calMu.Lock()
			defer r.calMu.Unlock()
			for _, h := range r.calHits {
				if h.kind == calDone {
					return h.r
				}
			}
			return image.Rectangle{}
		}},
		{"settings-row", scene{now: at, phase: "idle", showSheet: true, sheet: settings{cat: catSound, volume: 15, wakeWord: "Okay Nabu"}},
			firstZone(zoneRow)},
		{"settings-rail", scene{now: at, phase: "idle", showSheet: true, sheet: settings{cat: catSound, volume: 15, wakeWord: "Okay Nabu"}},
			firstZone(zoneCat)},
		{"drawer-row", scene{now: at, phase: "idle", showDrawer: true, drawerTab: drawerCall, demo: true,
			callees: []phone.Callee{{Name: "a", Device: true}, {Name: "b", Device: true}}}, firstZone(zoneRow)},
		{"drawer-tab", scene{now: at, phase: "idle", showDrawer: true, drawerTab: drawerCall, demo: true,
			callees: []phone.Callee{{Name: "a", Device: true}}}, firstZone(zoneTab)},
		{"dashboard-tile", scene{now: at, phase: "idle", showDash: true, dashMode: config.DashboardDrawn, drawn: fourControls()}, firstTile},
		{"tessera-tile", scene{now: at, phase: "idle", showDash: true, dashMode: config.DashboardTessera,
			tessera: tessera.Place(tessLayout(), 4, 2), tessOn: true}, firstTile},
		{"camera-talk", scene{now: at, phase: "idle", showCamera: true, talkOffered: true, camera: home.CameraView{Entity: "camera.front_door",
			Name: "Front door", Until: at.Add(time.Hour), Frame: image.NewRGBA(image.Rect(0, 0, 853, 480))}},
			func(r *renderer) image.Rectangle {
				r.weatherMu.Lock()
				defer r.weatherMu.Unlock()
				return r.cameraTalkAt
			}},
	}
}

// A control with a finger on it is drawn differently from the same control without one, the difference
// stays on the control, and the frame after the press is the plain one again: nothing is left looking
// pressed. With SHOW_PREVIEW set, each pressed page is written there to look at.
func TestAControlUnderAFingerShowsIt(t *testing.T) {
	previewFont(t)
	dir := os.Getenv("SHOW_PREVIEW")
	for _, panel := range []struct {
		name       string
		wide, high int
	}{{"", showWide, showHigh}, {"-show8", show8Wide, show8High}} {
		for _, c := range pressCases() {
			frame := func() *renderer { return newRenderer(image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high))) }
			plain := frame()
			plain.draw(c.scene)
			b := c.where(plain)
			if b.Empty() {
				t.Errorf("%s%s: no control to press", c.name, panel.name)
				continue
			}
			pressed := frame()
			s := c.scene
			s.press = pressView{on: true, at: image.Pt((b.Min.X+b.Max.X)/2, (b.Min.Y+b.Max.Y)/2)}
			pressed.draw(s)
			box, differs := diffBox(pressed, plain)
			if !differs {
				t.Errorf("%s%s: the control under the finger at %v looks as it does without one", c.name, panel.name, s.press.at)
			} else if !box.In(b.Inset(-plain.s(8))) {
				t.Errorf("%s%s: the press shows over %v, outside its control %v", c.name, panel.name, box, b)
			}
			if dir != "" {
				f, err := os.Create(filepath.Join(dir, "pressed-"+c.name+panel.name+".png"))
				if err != nil {
					t.Fatal(err)
				}
				if err := png.Encode(f, pressed.dst); err != nil {
					t.Fatal(err)
				}
				if err := f.Close(); err != nil {
					t.Fatal(err)
				}
			}
			// The same renderer, the finger gone: the page as it was.
			pressed.draw(c.scene)
			if box, differs := diffBox(pressed, plain); differs {
				t.Errorf("%s%s: after the press the page still differs over %v", c.name, panel.name, box)
			}
		}
	}
}

// A ringing alarm stops on a tap anywhere but Snooze, so a finger anywhere but Snooze shows on Stop; a
// call's buttons answer only in their band, across it, so a finger between the two shows on the one its
// tap would be.
func TestAPressShowsOnTheButtonItsTapWouldBe(t *testing.T) {
	at := time.Date(2026, 9, 16, 14, 7, 0, 0, time.Local)
	for name, c := range map[string]struct {
		scene scene
		at    image.Point
		on    func(r *renderer) image.Rectangle
	}{
		"the ringing page's clock": {scene{now: at, phase: "idle", ring: ringState{alarm: &alarm.Ring{Label: "Wake up"}, snoozable: true}, snooze: 9},
			image.Pt(700, 200), func(r *renderer) image.Rectangle { left, _ := r.actionHalves(); return left }},
		"just left of Answer": {scene{now: at, phase: "idle", call: phone.State{Phase: phone.Ringing, Peer: "104"}},
			image.Pt(482, 400), func(r *renderer) image.Rectangle { _, right := r.actionHalves(); return right }},
	} {
		plain, pressed := testRenderer(), testRenderer()
		plain.draw(c.scene)
		s := c.scene
		s.press = pressView{on: true, at: c.at}
		pressed.draw(s)
		b := c.on(plain)
		if box, differs := diffBox(pressed, plain); !differs || !box.In(b.Inset(-plain.s(8))) {
			t.Errorf("%s: the press shows over %v (%v), want on %v", name, box, differs, b)
		}
	}
}

// A finger on nothing shows nothing: the bare clock, the words over a call, the ground beside a
// dashboard's tiles and the dimmed clock beside the drawer are drawn as they are without it.
func TestAFingerOnNothingShowsNothing(t *testing.T) {
	at := time.Date(2026, 9, 16, 14, 7, 0, 0, time.Local)
	for name, c := range map[string]struct {
		scene scene
		at    image.Point
	}{
		"the clock":         {scene{now: at, phase: "idle"}, image.Pt(480, 240)},
		"a call's words":    {scene{now: at, phase: "idle", call: phone.State{Phase: phone.Ringing, Peer: "104"}}, image.Pt(480, 200)},
		"beside the drawer": {scene{now: at, phase: "idle", showDrawer: true, drawerTab: drawerCall}, image.Pt(100, 240)},
		"a strip under the drawer": {scene{now: at, phase: "idle", strip: true, playing: true, showDrawer: true, drawerTab: drawerCall,
			radio: home.Radio{Now: "KXYZ 101.1", Title: "Take It Easy"}}, image.Pt(60, 430)},
		"beside the tiles": {scene{now: at, phase: "idle", showDash: true, dashMode: config.DashboardDrawn, drawn: fourControls()}, image.Pt(940, 470)},
	} {
		plain, pressed := testRenderer(), testRenderer()
		plain.draw(c.scene)
		s := c.scene
		s.press = pressView{on: true, at: c.at}
		pressed.draw(s)
		if box, differs := diffBox(pressed, plain); differs {
			t.Errorf("%s: a finger at %v changed the page over %v", name, c.at, box)
		}
	}
}

// A tile under an open sheet does not show a finger that is on the sheet over it.
func TestATileUnderASheetIsNotPressed(t *testing.T) {
	at := time.Date(2026, 9, 16, 14, 7, 0, 0, time.Local)
	s := scene{now: at, phase: "idle", showDash: true, dashMode: config.DashboardDrawn, drawn: fourControls()}
	r := testRenderer()
	r.draw(s)
	tiles, _ := r.dash()
	if len(tiles) == 0 {
		t.Fatal("no tiles")
	}
	mid := image.Pt((tiles[0].r.Min.X+tiles[0].r.Max.X)/2, (tiles[0].r.Min.Y+tiles[0].r.Max.Y)/2)
	view := mediaView{sheet: mediaSheet{entity: "media_player.den", volume: -1}}
	s.dashMedia = &view
	plain, pressed := testRenderer(), testRenderer()
	plain.draw(s)
	s.press = pressView{on: true, at: mid}
	pressed.draw(s)
	if box, differs := diffBox(pressed, plain); differs && box.Overlaps(tiles[0].r) && !mid.In(plain.mediaCard) {
		t.Errorf("the tile under the sheet shows the press over %v", box)
	}
}

// pressDisplay is a display with its panel lit and a page drawn, as a finger finds it.
func pressDisplay(s scene) *Display {
	d := &Display{r: testRenderer(), poke: make(chan struct{}, 1), on: true}
	d.pressScene(&s, s.now)
	d.r.draw(s)
	return d
}

// pressShown is whether the frame of s at now shows a press.
func pressShown(d *Display, s scene, now time.Time) bool {
	d.pressScene(&s, now)
	return s.press.on
}

// drained empties the frame loop's poke and says whether there was one.
func drained(d *Display) bool {
	select {
	case <-d.poke:
		return true
	default:
		return false
	}
}

// A finger coming down is a press from the next frame; it shows while the finger is down and for a
// moment after the lift, and then not. Coming down and lifting each ask for a frame.
func TestAPressSetsAndClears(t *testing.T) {
	now := time.Now()
	ringing := scene{now: now, phase: "idle", ring: ringState{timer: "Pasta"}}
	d := pressDisplay(ringing)
	left, _ := d.r.actionHalves()
	at := touch.Point{X: left.Min.X + 40, Y: left.Min.Y + 40}

	d.fingerDown(true)
	if pressShown(d, ringing, now) {
		t.Error("a press before any finger landed")
	}
	drained(d)
	d.fingerLanded(at)
	if !drained(d) {
		t.Error("a finger landing asked for no frame")
	}
	s := ringing
	d.pressScene(&s, now)
	if !s.press.on || s.press.at != image.Pt(at.X, at.Y) {
		t.Fatalf("the frame after a finger landed shows %+v, want a press at %v", s.press, at)
	}
	// Still down a second later: still shown.
	s = ringing
	if d.pressScene(&s, now.Add(time.Second)); !s.press.on {
		t.Error("the press went while the finger was still down")
	}
	d.fingerDown(false)
	if !drained(d) {
		t.Error("the lift asked for no frame")
	}
	lifted := time.Now()
	s = ringing
	if d.pressScene(&s, lifted); !s.press.on {
		t.Error("the press went the moment the finger lifted: a quick tap would never show")
	}
	s = ringing
	if d.pressScene(&s, lifted.Add(pressLinger+time.Millisecond)); s.press.on {
		t.Error("the press is still shown after its moment")
	}
	if d.press.set {
		t.Error("the press was not let go")
	}
	// A second lift (the touchscreen says it once, but nothing depends on that) asks for nothing.
	drained(d)
	d.fingerDown(false)
	if drained(d) {
		t.Error("a lift with no press asked for a frame")
	}
}

// A finger the touchscreen never says lifted does not leave its control pressed: the press ends by
// itself.
func TestAPressEndsByItself(t *testing.T) {
	now := time.Now()
	ringing := scene{now: now, phase: "idle", ring: ringState{timer: "Pasta"}}
	d := pressDisplay(ringing)
	d.fingerLanded(touch.Point{X: 200, Y: 400})
	s := ringing
	if d.pressScene(&s, now.Add(pressMax-time.Second)); !s.press.on {
		t.Fatal("no press under a finger that is down")
	}
	s = ringing
	if d.pressScene(&s, time.Now().Add(pressMax)); s.press.on || d.press.set {
		t.Error("a finger down past the longest press is still shown")
	}
}

// Nothing is pressed after a page change: a press made on one page is not shown on the page that
// takes its place, where something else is under the same point.
func TestNothingIsPressedAfterAPageChange(t *testing.T) {
	now := time.Now()
	clock := scene{now: now, phase: "idle"}
	sheet := scene{now: now, phase: "idle", showSheet: true, sheet: settings{cat: catSound}}
	for name, next := range map[string]scene{
		"the settings":       sheet,
		"the drawer":         {now: now, phase: "idle", showDrawer: true},
		"a ringing alarm":    {now: now, phase: "idle", ring: ringState{timer: "Pasta"}},
		"a call":             {now: now, phase: "idle", call: phone.State{Phase: phone.Ringing}},
		"the dashboard":      {now: now, phase: "idle", showDash: true},
		"now playing":        {now: now, phase: "idle", nowPlaying: true},
		"a voice turn":       {now: now, phase: "listening"},
		"the calendar":       {now: now, phase: "idle", showCalendar: true},
		"a camera":           {now: now, phase: "idle", showCamera: true},
		"the music's strip":  {now: now, phase: "idle", strip: true},
		"the deck":           {now: now, phase: "idle", showDeck: true},
		"the forecast":       {now: now, phase: "idle", showWeather: true},
		"the PIN pad":        {now: now, phase: "idle", pin: pinView{open: true}},
		"the setup question": {now: now, phase: "idle", setupAsking: true},
	} {
		d := pressDisplay(clock)
		d.fingerLanded(touch.Point{X: 480, Y: 300})
		s := clock
		if d.pressScene(&s, now); !s.press.on {
			t.Fatalf("%s: no press on the clock to begin with", name)
		}
		if d.pressScene(&next, now); next.press.on || d.press.set {
			t.Errorf("%s came up with the clock's press still shown", name)
		}
	}
	// Another category of the settings, a list opened over them, another page of a Tessera layout and a
	// sheet over a dashboard are other pages too.
	for name, pair := range map[string][2]scene{
		"another category": {sheet, {now: now, phase: "idle", showSheet: true, sheet: settings{cat: catDisplay}}},
		"an open list":     {sheet, {now: now, phase: "idle", showSheet: true, sheet: settings{cat: catSound, picker: "sound"}}},
		"another Tessera page": {{now: now, phase: "idle", showDash: true, dashMode: config.DashboardTessera},
			{now: now, phase: "idle", showDash: true, dashMode: config.DashboardTessera, tessPage: 1}},
		"a sheet over the dashboard": {{now: now, phase: "idle", showDash: true},
			{now: now, phase: "idle", showDash: true, dashMedia: &mediaView{}}},
	} {
		d := pressDisplay(pair[0])
		d.fingerLanded(touch.Point{X: 480, Y: 300})
		if pressShown(d, pair[1], now) || d.press.set {
			t.Errorf("%s came up with the press still shown", name)
		}
	}
	// A finger that lands while one page is drawn and whose first frame is already another's (a quick
	// tap that opened something) shows nothing there either.
	d := pressDisplay(clock)
	d.fingerLanded(touch.Point{X: 480, Y: 300})
	if pressShown(d, sheet, now) {
		t.Error("a tap that opened the settings shows a press on them")
	}
}

// A finger that goes on to swipe, or to drag further than a tap wanders, is not pressing what it came
// down on; one that only rolls a little still is. A dark panel shows no press at all.
func TestASwipeOrADragIsNotAPress(t *testing.T) {
	now := time.Now()
	clock := scene{now: now, phase: "idle"}
	d := pressDisplay(clock)
	d.fingerLanded(touch.Point{X: 480, Y: 300})
	d.pressMoved(480+d.r.s(tapWander), 300)
	if !d.press.set {
		t.Error("a finger that rolled as far as a tap may lost its press")
	}
	d.pressMoved(480+d.r.s(tapWander)+1, 300)
	if d.press.set {
		t.Error("a finger dragged further than a tap wanders is still pressing")
	}
	d.fingerLanded(touch.Point{X: 480, Y: 300})
	drained(d)
	if !d.pressClear() || d.press.set {
		t.Error("a swipe left the press")
	}
	if d.pressClear() {
		t.Error("a second swipe found a press to let go")
	}

	dark := &Display{r: testRenderer(), poke: make(chan struct{}, 1)}
	dark.fingerLanded(touch.Point{X: 480, Y: 300})
	if dark.press.set || drained(dark) {
		t.Error("a finger on a dark panel is a press")
	}
}
