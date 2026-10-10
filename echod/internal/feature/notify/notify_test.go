package notify

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
)

// fakePictures is the camera page: what was shown on it, which notification's view is up, and how
// often it was taken down.
type fakePictures struct {
	mu     sync.Mutex
	srcs   []string
	shown  []home.Caption
	up     string // the notification whose view is up, "" for none
	hides  int
	sounds []string // each picture's sound argument, as passed
	onShow func()   // called inside ShowPicture, once the new view is up, with no lock held
	onHide func()   // called inside HidePicture, once the view is down, with no lock held
}

func (p *fakePictures) ShowPicture(src string, c home.Caption, d time.Duration, sound string) error {
	if err := home.CheckPicture(src); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.srcs, p.shown, p.up = append(p.srcs, src), append(p.shown, c), c.ID
	p.sounds = append(p.sounds, sound)
	if p.onShow != nil {
		p.mu.Unlock()
		p.onShow()
		p.mu.Lock()
	}
	return nil
}

func (p *fakePictures) PictureUp(id string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return id != "" && p.up == id
}

// HidePicture is the camera page's: it hides the view only if it is notification id's.
func (p *fakePictures) HidePicture(id string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if id == "" || p.up != id {
		return false
	}
	p.up, p.hides = "", p.hides+1
	if p.onHide != nil {
		p.mu.Unlock()
		p.onHide()
		p.mu.Lock()
	}
	return true
}

// HideCamera is the view going some other way: "go home", the cameras page.
func (p *fakePictures) HideCamera() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.up, p.hides = "", p.hides+1
}

type rig struct {
	f      *Feature
	pics   *fakePictures
	now    time.Time
	chimes atomic.Int32
	hushed atomic.Bool

	saidMu sync.Mutex
	said   []string // what was asked to be said aloud, in order

	mu     sync.Mutex
	events []component.Event
}

// fired is the events' kinds in order: "shown", "expired", ...
func (r *rig) fired() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, e := range r.events {
		out = append(out, e.Data["event"])
	}
	return out
}

func (r *rig) last() component.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.events[len(r.events)-1]
}

// newRig is a notify feature on a device with a screen (pics) or without one (no pics), on a clock
// that only moves when the test moves it.
func newRig(t *testing.T, screen bool) *rig {
	t.Helper()
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	r := &rig{now: time.Date(2026, 10, 9, 14, 0, 0, 0, time.Local)}
	var pics pictures
	if screen {
		r.pics = &fakePictures{}
		pics = r.pics
	}
	r.f = newFeature(pics)
	r.f.now = func() time.Time { return r.now }
	r.f.chime = func() { r.chimes.Add(1) }
	r.f.say = func(words string) {
		r.saidMu.Lock()
		r.said = append(r.said, words)
		r.saidMu.Unlock()
	}
	r.f.hushed = r.hushed.Load
	r.f.fire = func(e component.Event) {
		r.mu.Lock()
		r.events = append(r.events, e)
		r.mu.Unlock()
	}
	return r
}

func same(a, b []string) bool { return strings.Join(a, ",") == strings.Join(b, ",") }

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for end := time.Now().Add(2 * time.Second); time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
		if ok() {
			return
		}
	}
	t.Fatalf("waited for %s", what)
}

func TestNotifyNeedsAMessage(t *testing.T) {
	r := newRig(t, true)
	for _, m := range []string{"", "   ", "\x07\n"} {
		if err := r.f.Notify(m, "Washer", "", "", "", "", 0); err == nil {
			t.Errorf("message %q: taken, want an error", m)
		}
	}
	if _, up := r.f.Showing(); up || len(r.fired()) != 0 {
		t.Error("a refused notification was shown or reported")
	}
}

func TestNotifyCleansWhatItShows(t *testing.T) {
	r := newRig(t, true)
	if err := r.f.Notify("  The washer\n\tis\x07 done  ", strings.Repeat("é", 30), " mdi:washing-machine ", "", "", "", 0); err != nil {
		t.Fatal(err)
	}
	n, up := r.f.Card()
	if !up {
		t.Fatal("no card")
	}
	if n.Message != "The washer is done" {
		t.Errorf("message %q", n.Message)
	}
	// 30 two-byte characters, clipped to 40 bytes: 20 whole characters, none cut.
	if n.Title != strings.Repeat("é", 20) {
		t.Errorf("title %q (%d bytes)", n.Title, len(n.Title))
	}
	if n.Icon != "mdi:washing-machine" {
		t.Errorf("icon %q", n.Icon)
	}
	if err := r.f.Notify(strings.Repeat("a", 500), "", "", "", "", "", 0); err != nil {
		t.Fatal(err)
	}
	if n, _ := r.f.Card(); len(n.Message) != maxMessage {
		t.Errorf("message is %d bytes, want %d", len(n.Message), maxMessage)
	}
}

func TestHowLongANotificationLasts(t *testing.T) {
	cases := []struct {
		seconds int
		text    time.Duration
		picture time.Duration
	}{
		{0, 12 * time.Hour, 12 * time.Hour},
		{-5, 12 * time.Hour, 12 * time.Hour},
		{45, 45 * time.Second, 45 * time.Second},
		{1_000_000_000, 24 * time.Hour, 24 * time.Hour},
	}
	for _, c := range cases {
		r := newRig(t, true)
		_ = r.f.Notify("m", "", "", "", "", "", c.seconds)
		if n, _ := r.f.Showing(); n.Until.Sub(r.now) != c.text {
			t.Errorf("notify seconds=%d: lasts %v, want %v", c.seconds, n.Until.Sub(r.now), c.text)
		}
		_ = r.f.NotifyPicture("m", "", "/local/door.png", "", "", "", "", c.seconds)
		if n, _ := r.f.Showing(); n.Until.Sub(r.now) != c.picture {
			t.Errorf("notify_picture still, seconds=%d: lasts %v, want %v", c.seconds, n.Until.Sub(r.now), c.picture)
		}
		// A live camera with no time is 30 seconds, as home_show_camera takes 0.
		live := c.picture
		if c.seconds <= 0 {
			live = 30 * time.Second
		}
		_ = r.f.NotifyPicture("m", "", "camera.front_door", "", "", "", "", c.seconds)
		if n, _ := r.f.Showing(); n.Until.Sub(r.now) != live {
			t.Errorf("notify_picture camera, seconds=%d: lasts %v, want %v", c.seconds, n.Until.Sub(r.now), live)
		}
	}
}

func TestANewNotificationReplacesTheOld(t *testing.T) {
	r := newRig(t, true)
	_ = r.f.Notify("first", "", "", "", "", "", 0)
	_ = r.f.Notify("second", "", "", "", "", "", 0)
	if n, _ := r.f.Card(); n.Message != "second" {
		t.Errorf("card says %q, want the second", n.Message)
	}
	if got := r.fired(); !same(got, []string{"shown", "replaced", "shown"}) {
		t.Errorf("events %v", got)
	}
}

func TestTheEventSaysWhatItWas(t *testing.T) {
	r := newRig(t, true)
	_ = r.f.Notify("The washer is done", "Washer", "", "", "", "", 0)
	e := r.last()
	if e.Name != Event || e.Data["event"] != "shown" || e.Data["title"] != "Washer" ||
		e.Data["message"] != "The washer is done" || e.Data["kind"] != "text" {
		t.Errorf("event %+v", e)
	}
	if _, ok := e.Data["device"]; !ok {
		t.Error("the event does not name the device")
	}
}

func TestDismissIsReportedOnce(t *testing.T) {
	r := newRig(t, true)
	_ = r.f.Notify("m", "", "", "", "", "", 0)
	if !r.f.Dismiss() {
		t.Fatal("Dismiss with one up said there was none")
	}
	if r.f.Dismiss() {
		t.Error("a second Dismiss found one")
	}
	r.now = r.now.Add(time.Hour)
	r.f.tick(r.now)
	if got := r.fired(); !same(got, []string{"shown", "dismissed"}) {
		t.Errorf("events %v", got)
	}
}

func TestExpiryIsReportedOnce(t *testing.T) {
	r := newRig(t, true)
	_ = r.f.Notify("m", "", "", "", "", "", 60)
	r.f.tick(r.now.Add(59 * time.Second))
	if _, up := r.f.Card(); !up {
		t.Fatal("gone before its time")
	}
	r.now = r.now.Add(60 * time.Second)
	r.f.tick(r.now)
	r.f.tick(r.now.Add(time.Second))
	if _, up := r.f.Card(); up {
		t.Error("still up after its time")
	}
	if r.f.Dismiss() {
		t.Error("an expired notification was dismissed")
	}
	if got := r.fired(); !same(got, []string{"shown", "expired"}) {
		t.Errorf("events %v", got)
	}
}

func TestAPictureGoesOnTheCameraPage(t *testing.T) {
	r := newRig(t, true)
	if err := r.f.NotifyPicture("Someone is here", "Doorbell", " camera.front_door ", "", "", "", "", 0); err != nil {
		t.Fatal(err)
	}
	if len(r.pics.srcs) != 1 || r.pics.srcs[0] != "camera.front_door" {
		t.Fatalf("shown %v", r.pics.srcs)
	}
	n, up := r.f.Showing()
	c := r.pics.shown[0]
	if !up || n.Kind != Picture || c.ID != n.ID || c.Title != "Doorbell" || c.Message != "Someone is here" {
		t.Errorf("notification %+v, caption %+v", n, c)
	}
	if _, card := r.f.Card(); card {
		t.Error("a picture notification is drawn as a card too")
	}
	if e := r.last(); e.Data["kind"] != "picture" || e.Data["event"] != "shown" {
		t.Errorf("event %+v", e)
	}
}

// Only a still given no time is until tapped, and so only it loses the page's countdown: a still with
// 60 seconds and a live camera with none (30 seconds) count down.
func TestOnlyAStillWithNoTimeIsUntilTapped(t *testing.T) {
	for _, tc := range []struct {
		pic     string
		seconds int
		want    bool
	}{{"/local/door.jpg", 0, true}, {"/local/door.jpg", -5, true}, {"/local/door.jpg", 60, false}, {"camera.front_door", 0, false}} {
		r := newRig(t, true)
		if err := r.f.NotifyPicture("m", "", tc.pic, "", "", "", "", tc.seconds); err != nil {
			t.Fatal(err)
		}
		if got := r.pics.shown[0].UntilTapped; got != tc.want {
			t.Errorf("%s with %d seconds: UntilTapped %v, want %v", tc.pic, tc.seconds, got, tc.want)
		}
	}
}

func TestAPictureNeedsAPicture(t *testing.T) {
	r := newRig(t, true)
	for _, p := range []string{"", "  ", "front door", "sensor.washer"} {
		if err := r.f.NotifyPicture("m", "", p, "", "", "", "", 0); err == nil {
			t.Errorf("picture %q: taken", p)
		}
	}
	if len(r.pics.srcs) != 0 || len(r.fired()) != 0 {
		t.Error("a refused picture was shown or reported")
	}
}

// A picture needs no words: with neither a title nor a message it is the picture alone, and the
// event says so with both empty.
func TestAPictureNeedsNoWords(t *testing.T) {
	r := newRig(t, true)
	if err := r.f.NotifyPicture("", "  ", "camera.front_door", "", "", "", "", 0); err != nil {
		t.Fatalf("a picture with no words was refused: %v", err)
	}
	if c := r.pics.shown[0]; c.Title != "" || c.Message != "" {
		t.Errorf("caption %+v, want no words", c)
	}
	if e := r.last(); e.Data["event"] != "shown" || e.Data["title"] != "" || e.Data["message"] != "" {
		t.Errorf("event %+v", e)
	}
	// Words alone still need words: a card with nothing on it says nothing.
	if err := r.f.Notify("", "Washer", "", "", "", "", 0); err == nil {
		t.Error("a text notification with no message was taken")
	}
}

// chime "off" puts a notification up without its chime, by day and out of quiet hours; anything else
// leaves the chime to the hour.
func TestChimeOffIsSilent(t *testing.T) {
	r := newRig(t, true)
	_ = r.f.Notify("m", "", "", "off", "", "", 0)
	_ = r.f.NotifyPicture("m", "", "camera.front_door", " OFF ", "", "", "", 0)
	time.Sleep(50 * time.Millisecond)
	if n := r.chimes.Load(); n != 0 {
		t.Errorf("chimed %d times with the chime off", n)
	}
	if got := r.fired(); !same(got, []string{"shown", "replaced", "shown"}) {
		t.Errorf("events %v, want a silent notification reported as any other", got)
	}
	_ = r.f.Notify("m", "", "", "on", "", "", 0)
	waitFor(t, "the chime", func() bool { return r.chimes.Load() == 1 })
}

// A picture is over when its view is, however the view went: "go home", the cameras page, another
// camera asked for.
func TestAPictureIsOverWhenItsViewIs(t *testing.T) {
	r := newRig(t, true)
	_ = r.f.NotifyPicture("m", "", "camera.front_door", "", "", "", "", 0)
	r.f.tick(r.now.Add(time.Second))
	if _, up := r.f.Showing(); !up {
		t.Fatal("over while its view is still up")
	}
	r.pics.HideCamera()
	r.f.tick(r.now.Add(2 * time.Second))
	if _, up := r.f.Showing(); up {
		t.Error("still up with its view gone")
	}
	if got := r.fired(); !same(got, []string{"shown", "expired"}) {
		t.Errorf("events %v", got)
	}
}

func TestDismissingAPictureTakesItsViewDown(t *testing.T) {
	r := newRig(t, true)
	_ = r.f.NotifyPicture("m", "", "camera.front_door", "", "", "", "", 0)
	r.f.Dismiss()
	if r.pics.hides != 1 || r.pics.up != "" {
		t.Errorf("hides=%d up=%q, want the view down once", r.pics.hides, r.pics.up)
	}
}

func TestTextReplacingAPictureTakesItsViewDown(t *testing.T) {
	r := newRig(t, true)
	_ = r.f.NotifyPicture("m", "", "camera.front_door", "", "", "", "", 0)
	_ = r.f.Notify("words", "", "", "", "", "", 0)
	if r.pics.hides != 1 {
		t.Errorf("hides=%d, want the picture's view taken down", r.pics.hides)
	}
	if got := r.fired(); !same(got, []string{"shown", "replaced", "shown"}) {
		t.Errorf("events %v", got)
	}
}

func TestAPictureReplacingAPictureLeavesTheNewView(t *testing.T) {
	r := newRig(t, true)
	_ = r.f.NotifyPicture("one", "", "camera.front_door", "", "", "", "", 0)
	_ = r.f.NotifyPicture("two", "", "camera.deck", "", "", "", "", 0)
	n, up := r.f.Showing()
	if !up || n.Message != "two" || !r.pics.PictureUp(n.ID) || r.pics.hides != 0 {
		t.Errorf("notification %+v up=%v hides=%d", n, up, r.pics.hides)
	}
}

func TestItChimesUnlessHushed(t *testing.T) {
	r := newRig(t, true)
	r.hushed.Store(true)
	_ = r.f.Notify("m", "", "", "", "", "", 0)
	time.Sleep(50 * time.Millisecond)
	if n := r.chimes.Load(); n != 0 {
		t.Errorf("chimed %d times while hushed", n)
	}
	r.hushed.Store(false)
	_ = r.f.Notify("m", "", "", "", "", "", 0)
	waitFor(t, "the chime", func() bool { return r.chimes.Load() == 1 })
}

// A Dot has no screen: a picture notification is taken (so an automation calling every device does
// not fail on one), chimes, is reported, and ends on its own time.
func TestADeviceWithNoScreen(t *testing.T) {
	r := newRig(t, false)
	if err := r.f.NotifyPicture("m", "", "camera.front_door", "", "", "", "", 30); err != nil {
		t.Fatal(err)
	}
	if err := r.f.NotifyPicture("m", "", "front door", "", "", "", "", 30); err == nil {
		t.Error("a Dot took a picture a Show would refuse")
	}
	r.now = r.now.Add(30 * time.Second)
	r.f.tick(r.now)
	if got := r.fired(); !same(got, []string{"shown", "expired"}) {
		t.Errorf("events %v", got)
	}
	if r.f.Dismiss() {
		t.Error("dismissed one already over")
	}
}

func TestTheActionsAreTheOnesHomeAssistantKnows(t *testing.T) {
	r := newRig(t, true)
	want := map[string]string{
		"notify":         "message,title,icon,chime,speak,tag,seconds",
		"notify_picture": "message,title,picture,chime,speak,sound,tag,seconds",
	}
	for _, a := range r.f.Actions() {
		var args []string
		for _, arg := range a.Args {
			args = append(args, arg.Name)
		}
		if w, ok := want[a.Name]; !ok || strings.Join(args, ",") != w {
			t.Errorf("action %s(%s), want %q", a.Name, strings.Join(args, ","), w)
		}
		delete(want, a.Name)
	}
	if len(want) != 0 {
		t.Errorf("missing actions: %v", want)
	}
}

// Pictures sent at once leave the page showing the notification that is up, whatever order they ran in.
func TestConcurrentPicturesLeaveTheViewAndTheNotificationTogether(t *testing.T) {
	r := newRig(t, true)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			if err := r.f.NotifyPicture("m", "t", "/local/door.png", "", "", "", "", 0); err != nil {
				t.Error(err)
			}
		}()
		go func() {
			defer wg.Done()
			_ = r.f.Notify("words", "", "", "", "", "", 0)
		}()
	}
	wg.Wait()
	n, up := r.f.Showing()
	if !up {
		t.Fatal("no notification is up")
	}
	if n.Kind == Picture && !r.pics.PictureUp(n.ID) {
		t.Errorf("notification %s is up but its view is not", n.ID)
	}
	r.pics.mu.Lock()
	view := r.pics.up
	r.pics.mu.Unlock()
	if n.Kind == Text && view != "" {
		t.Errorf("words are up and a picture's view %s is too", view)
	}
}

// spoken is what was asked to be said aloud so far.
func (r *rig) spoken() []string {
	r.saidMu.Lock()
	defer r.saidMu.Unlock()
	return append([]string(nil), r.said...)
}

// speak "on" says the title and the message aloud, joined as a sentence would join them; just the one
// there is when there is one; and nothing for a picture with no words.
func TestSpeakOnSaysTheWords(t *testing.T) {
	cases := []struct {
		title, message string
		picture        bool
		want           string // "" for nothing said
	}{
		{"Doorbell", "Someone is at the front door", false, "Doorbell. Someone is at the front door"},
		{"Doorbell!", "Someone is at the front door", false, "Doorbell! Someone is at the front door"},
		{"", "The washer is done", false, "The washer is done"},
		{"Doorbell", "", true, "Doorbell"},
		{"", "", true, ""},
	}
	for _, c := range cases {
		r := newRig(t, true)
		var err error
		if c.picture {
			err = r.f.NotifyPicture(c.message, c.title, "camera.front_door", "", "on", "", "", 0)
		} else {
			err = r.f.Notify(c.message, c.title, "", "", " ON ", "", 0)
		}
		if err != nil {
			t.Fatal(err)
		}
		if c.want == "" {
			time.Sleep(50 * time.Millisecond)
			if got := r.spoken(); len(got) != 0 {
				t.Errorf("%+v: said %q, want nothing", c, got)
			}
			continue
		}
		waitFor(t, "the words", func() bool { return len(r.spoken()) == 1 })
		if got := r.spoken()[0]; got != c.want {
			t.Errorf("%+v: said %q, want %q", c, got, c.want)
		}
	}
}

// Without speak on nothing is said; with it, nothing is said at night or in quiet hours either, and
// the chime being off does not stop the words.
func TestWhenANotificationSpeaks(t *testing.T) {
	r := newRig(t, true)
	_ = r.f.Notify("m", "", "", "", "", "", 0)
	r.hushed.Store(true)
	_ = r.f.Notify("m", "", "", "", "on", "", 0)
	time.Sleep(50 * time.Millisecond)
	if got := r.spoken(); len(got) != 0 {
		t.Errorf("said %q, want nothing", got)
	}
	r.hushed.Store(false)
	waitFor(t, "the first one's chime", func() bool { return r.chimes.Load() == 1 })
	before := r.chimes.Load()
	_ = r.f.Notify("words", "", "", "off", "on", "", 0)
	waitFor(t, "the words with the chime off", func() bool { return len(r.spoken()) == 1 })
	if n := r.chimes.Load() - before; n != 0 {
		t.Errorf("chimed %d times with the chime off", n)
	}
}

// The chime comes first and the words after it, never over it.
func TestTheChimeComesBeforeTheWords(t *testing.T) {
	r := newRig(t, true)
	var order []string
	var mu sync.Mutex
	r.f.chime = func() {
		time.Sleep(20 * time.Millisecond) // a chime takes a moment to play
		mu.Lock()
		order = append(order, "chime")
		mu.Unlock()
	}
	r.f.say = func(string) {
		mu.Lock()
		order = append(order, "words")
		mu.Unlock()
	}
	_ = r.f.Notify("m", "", "", "", "on", "", 0)
	waitFor(t, "both", func() bool { mu.Lock(); defer mu.Unlock(); return len(order) == 2 })
	if order[0] != "chime" || order[1] != "words" {
		t.Errorf("order %v, want the chime then the words", order)
	}
}

// A Dot, with no screen, says it too: there it is the whole of the notification.
func TestADotSpeaks(t *testing.T) {
	r := newRig(t, false)
	_ = r.f.Notify("The washer is done", "", "", "", "on", "", 0)
	waitFor(t, "the words", func() bool { return len(r.spoken()) == 1 })
	waitFor(t, "its end", func() bool { return len(r.fired()) == 2 }) // so nothing outlives the test
}

// A picture's sound argument goes to the camera page as it was given: what "on", "off" and "" mean for
// a camera's audio is the camera page's business, the same as for home_show_camera_sound.
func TestAPicturesSoundGoesToTheCameraPage(t *testing.T) {
	r := newRig(t, true)
	for _, sound := range []string{"on", "off", ""} {
		if err := r.f.NotifyPicture("m", "", "camera.front_door", "", "", sound, "", 0); err != nil {
			t.Fatal(err)
		}
	}
	if got := strings.Join(r.pics.sounds, ","); got != "on,off," {
		t.Errorf("sounds passed %q, want %q", got, "on,off,")
	}
}

// With no time given, a notification stays until somebody taps it, but not for ever: twelve hours on,
// it has expired.
func TestNoTimeIsUntilTappedButNotForEver(t *testing.T) {
	r := newRig(t, true)
	_ = r.f.Notify("m", "", "", "", "", "", 0)
	r.now = r.now.Add(11 * time.Hour)
	r.f.tick(r.now)
	if _, up := r.f.Card(); !up {
		t.Fatal("a notification with no time went by itself")
	}
	if !r.f.Dismiss() {
		t.Error("it could not be tapped away")
	}
	if got := r.fired(); !same(got, []string{"shown", "dismissed"}) {
		t.Errorf("events %v", got)
	}
	_ = r.f.Notify("m", "", "", "", "", "", 0)
	r.now = r.now.Add(12 * time.Hour)
	r.f.tick(r.now)
	if _, up := r.f.Card(); up {
		t.Error("still up after twelve hours")
	}
	if got := r.fired(); !same(got, []string{"shown", "dismissed", "shown", "expired"}) {
		t.Errorf("events %v", got)
	}
}

// chime and speak read on and off as sound does: Home Assistant sends an unquoted YAML off as "False".
func TestChimeAndSpeakReadOnAndOff(t *testing.T) {
	cases := []struct {
		chime, speak string
		wantChime    bool
		wantSpeak    bool
	}{
		{"", "", true, false},
		{"False", "True", false, true},
		{"false", "true", false, true},
		{"no", "yes", false, true},
		{" OFF ", " ON ", false, true},
		{"0", "1", false, true},
		{"on", "off", true, false},
		{"maybe", "maybe", true, false},
	}
	for _, c := range cases {
		r := newRig(t, true)
		_ = r.f.Notify("m", "", "", c.chime, c.speak, "", 0)
		// What should happen is waited for; what should not is given a moment to wrongly happen.
		if c.wantChime {
			waitFor(t, "the chime", func() bool { return r.chimes.Load() == 1 })
		}
		if c.wantSpeak {
			waitFor(t, "the words", func() bool { return len(r.spoken()) == 1 })
		}
		time.Sleep(50 * time.Millisecond)
		if got := r.chimes.Load() == 1; got != c.wantChime {
			t.Errorf("chime %q: chimed=%v, want %v", c.chime, got, c.wantChime)
		}
		if got := len(r.spoken()) == 1; got != c.wantSpeak {
			t.Errorf("speak %q: spoke=%v, want %v", c.speak, got, c.wantSpeak)
		}
	}
}

// A hushed notification asks the camera page for no sound, whatever the caller or the device setting said.
func TestHushedForcesTheCameraSoundOff(t *testing.T) {
	r := newRig(t, true)
	r.hushed.Store(true)
	_ = r.f.NotifyPicture("m", "", "camera.front_door", "", "", "on", "", 0)
	_ = r.f.NotifyPicture("m", "", "camera.front_door", "", "", "", "", 0)
	r.hushed.Store(false)
	_ = r.f.NotifyPicture("m", "", "camera.front_door", "", "", "on", "", 0)
	if got := strings.Join(r.pics.sounds, ","); got != "off,off,on" {
		t.Errorf("sounds passed %q, want %q", got, "off,off,on")
	}
}

// A Dot has nothing to show, so with no time given a notification ends once its chime and then its
// words are done.
func TestADotEndsAfterTheChimeAndTheWords(t *testing.T) {
	r := newRig(t, false)
	var mu sync.Mutex
	var order []string
	note := func(s string) { mu.Lock(); order = append(order, s); mu.Unlock() }
	r.f.chime = func() { time.Sleep(20 * time.Millisecond); note("chime") }
	r.f.say = func(string) { note("words") }
	r.f.fire = func(e component.Event) { note(e.Data["event"]) }
	_ = r.f.Notify("m", "", "", "", "on", "", 0)
	waitFor(t, "the end", func() bool { mu.Lock(); defer mu.Unlock(); return len(order) == 4 })
	mu.Lock()
	defer mu.Unlock()
	if !same(order, []string{"shown", "chime", "words", "expired"}) {
		t.Errorf("order %v, want shown, chime, words, expired", order)
	}
	if _, up := r.f.Showing(); up {
		t.Error("still up after its chime and words")
	}
}

// With nothing to play (hushed, or chime off) a Dot's notification ends at once; one given a time keeps
// to it.
func TestADotWithNothingToPlayEndsAtOnce(t *testing.T) {
	r := newRig(t, false)
	r.hushed.Store(true)
	_ = r.f.Notify("m", "", "", "", "on", "", 0)
	if got := r.fired(); !same(got, []string{"shown", "expired"}) {
		t.Errorf("events %v", got)
	}
	r = newRig(t, false)
	_ = r.f.Notify("m", "", "", "off", "", "", 0)
	if got := r.fired(); !same(got, []string{"shown", "expired"}) {
		t.Errorf("chime off: events %v", got)
	}
	r = newRig(t, false)
	r.hushed.Store(true)
	_ = r.f.Notify("m", "", "", "", "", "", 60)
	if got := r.fired(); !same(got, []string{"shown"}) {
		t.Errorf("with a time: events %v, want it to last", got)
	}
}

func TestTheEventCarriesTheIDAndTheTag(t *testing.T) {
	r := newRig(t, true)
	_ = r.f.Notify("one", "", "", "", "", " door ", 0)
	e := r.last()
	n, _ := r.f.Showing()
	if e.Data["id"] == "" || e.Data["id"] != n.ID || e.Data["tag"] != "door" || n.Tag != "door" {
		t.Errorf("event %+v, notification %+v", e.Data, n)
	}
	first := n.ID
	_ = r.f.NotifyPicture("two", "", "camera.front_door", "", "", "", "", 0)
	r.mu.Lock()
	evs := append([]component.Event(nil), r.events...)
	r.mu.Unlock()
	// shown(one), replaced(one), shown(two)
	if len(evs) != 3 || evs[1].Data["event"] != "replaced" || evs[1].Data["id"] != first || evs[1].Data["tag"] != "door" {
		t.Fatalf("events %+v", evs)
	}
	if evs[2].Data["tag"] != "" || evs[2].Data["id"] == first || evs[2].Data["id"] == "" {
		t.Errorf("the second one's event %+v", evs[2].Data)
	}
	ids := map[string]bool{}
	for i := 0; i < 20; i++ {
		_ = r.f.Notify("m", "", "", "off", "", "", 0)
		ids[r.last().Data["id"]] = true
	}
	if len(ids) != 20 {
		t.Errorf("%d ids in 20 notifications", len(ids))
	}
}

func TestDismissIDOnlyDismissesThatOne(t *testing.T) {
	r := newRig(t, true)
	_ = r.f.NotifyPicture("m", "", "camera.front_door", "", "", "", "", 0)
	n, _ := r.f.Showing()
	if r.f.DismissID("999") || r.f.DismissID("") {
		t.Fatal("DismissID took down a notification with another id")
	}
	if _, up := r.f.Showing(); !up || r.pics.hides != 0 {
		t.Fatal("the wrong id changed something")
	}
	if !r.f.DismissID(n.ID) {
		t.Fatal("DismissID with the right id said no")
	}
	if r.pics.hides != 1 || r.f.DismissID(n.ID) {
		t.Errorf("hides=%d, or it dismissed twice", r.pics.hides)
	}
	if got := r.fired(); !same(got, []string{"shown", "dismissed"}) {
		t.Errorf("events %v", got)
	}
}

// Showing and tick answer alike for a picture: once its view is gone neither counts it as up.
func TestShowingAgreesWithTickForAPicture(t *testing.T) {
	r := newRig(t, true)
	_ = r.f.NotifyPicture("m", "", "camera.front_door", "", "", "", "", 0)
	r.pics.HideCamera()
	if _, up := r.f.Showing(); up {
		t.Error("Showing says a picture is up with its view gone")
	}
	r.f.tick(r.now)
	if got := r.fired(); !same(got, []string{"shown", "expired"}) {
		t.Errorf("events %v", got)
	}
	// And the other way: a picture whose view outlasts its own Until (the view's time counts from the
	// first frame) is still up.
	_ = r.f.NotifyPicture("m", "", "camera.front_door", "", "", "", "", 5)
	r.now = r.now.Add(6 * time.Second)
	if _, up := r.f.Showing(); !up {
		t.Error("Showing says a picture is over while its view is up")
	}
}

// Right after boot, before the display has said what night is, the night hours in the settings decide.
func TestNightBeforeTheDisplayAnswers(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	// Other tests call SetNight; this one is about the default before anybody has.
	nightMu.Lock()
	saved := nightFn
	nightMu.Unlock()
	t.Cleanup(func() { SetNight(saved) })
	now := time.Now()
	from := now.Add(-time.Hour).Hour()
	to := now.Add(time.Hour).Hour()
	if err := config.Set().Screen().Night(fmt.Sprintf("%d-%d", from, to)); err != nil {
		t.Fatal(err)
	}
	// Quiet hours are not what is under test, and are off in a fresh config.
	if config.Quiet() {
		t.Skip("quiet hours are on")
	}
	SetNight(nightByHours) // what the package starts with
	if !hushedNow() {
		t.Error("a notification at night, before SetNight, is not hushed")
	}
	if err := config.Set().Screen().Night(""); err != nil {
		t.Fatal(err)
	}
	if hushedNow() {
		t.Error("hushed with no night set")
	}
}

// A tick landing between a picture's view going up and its notification being put does not end the
// old picture as expired: it is being replaced.
func TestATickMidwayThroughAPictureReplacingOneReportsReplaced(t *testing.T) {
	r := newRig(t, true)
	_ = r.f.NotifyPicture("A", "", "camera.front_door", "", "", "", "", 0)
	done := make(chan struct{})
	r.pics.onShow = func() {
		go func() { r.f.tick(r.now); close(done) }()
		time.Sleep(50 * time.Millisecond) // long enough for the tick to run, if it can
	}
	_ = r.f.NotifyPicture("B", "", "camera.deck", "", "", "", "", 0)
	<-done
	if got := r.fired(); !same(got, []string{"shown", "replaced", "shown"}) {
		t.Errorf("events %v, want A replaced and never expired", got)
	}
	if n, up := r.f.Showing(); !up || n.Message != "B" {
		t.Errorf("showing %+v up=%v", n, up)
	}
}

// Events reach Home Assistant in the order things happened. A tap on the new card while the picture it
// replaced is being taken down is a dismiss of a notification that has already been shown, so its
// shown goes out first.
func TestATapMidwayThroughAReplaceComesAfterItsShown(t *testing.T) {
	r := newRig(t, true)
	_ = r.f.NotifyPicture("picture", "", "camera.front_door", "", "", "", "", 0)
	r.pics.onHide = func() { r.f.Dismiss() }
	_ = r.f.Notify("words", "", "", "", "", "", 0)
	if got, want := r.fired(), []string{"shown", "replaced", "shown", "dismissed"}; !same(got, want) {
		t.Errorf("events %v, want %v", got, want)
	}
}
