package notify

import (
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
}

func (p *fakePictures) ShowPicture(src string, c home.Caption, d time.Duration, sound string) error {
	if err := home.CheckPicture(src); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.srcs, p.shown, p.up = append(p.srcs, src), append(p.shown, c), c.ID
	p.sounds = append(p.sounds, sound)
	return nil
}

func (p *fakePictures) PictureUp(id string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return id != "" && p.up == id
}

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
		if err := r.f.Notify(m, "Washer", "", "", "", 0); err == nil {
			t.Errorf("message %q: taken, want an error", m)
		}
	}
	if _, up := r.f.Showing(); up || len(r.fired()) != 0 {
		t.Error("a refused notification was shown or reported")
	}
}

func TestNotifyCleansWhatItShows(t *testing.T) {
	r := newRig(t, true)
	if err := r.f.Notify("  The washer\n\tis\x07 done  ", strings.Repeat("é", 30), " mdi:washing-machine ", "", "", 0); err != nil {
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
	if err := r.f.Notify(strings.Repeat("a", 500), "", "", "", "", 0); err != nil {
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
		{0, untilTapped, untilTapped},
		{-5, untilTapped, untilTapped},
		{45, 45 * time.Second, 45 * time.Second},
		{1_000_000_000, 24 * time.Hour, 24 * time.Hour},
	}
	for _, c := range cases {
		r := newRig(t, true)
		_ = r.f.Notify("m", "", "", "", "", c.seconds)
		if n, _ := r.f.Showing(); n.Until.Sub(r.now) != c.text {
			t.Errorf("notify seconds=%d: lasts %v, want %v", c.seconds, n.Until.Sub(r.now), c.text)
		}
		_ = r.f.NotifyPicture("m", "", "camera.front_door", "", "", "", c.seconds)
		if n, _ := r.f.Showing(); n.Until.Sub(r.now) != c.picture {
			t.Errorf("notify_picture seconds=%d: lasts %v, want %v", c.seconds, n.Until.Sub(r.now), c.picture)
		}
	}
}

func TestANewNotificationReplacesTheOld(t *testing.T) {
	r := newRig(t, true)
	_ = r.f.Notify("first", "", "", "", "", 0)
	_ = r.f.Notify("second", "", "", "", "", 0)
	if n, _ := r.f.Card(); n.Message != "second" {
		t.Errorf("card says %q, want the second", n.Message)
	}
	if got := r.fired(); !same(got, []string{"shown", "expired", "shown"}) {
		t.Errorf("events %v", got)
	}
}

func TestTheEventSaysWhatItWas(t *testing.T) {
	r := newRig(t, true)
	_ = r.f.Notify("The washer is done", "Washer", "", "", "", 0)
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
	_ = r.f.Notify("m", "", "", "", "", 0)
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
	_ = r.f.Notify("m", "", "", "", "", 60)
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
	if err := r.f.NotifyPicture("Someone is here", "Doorbell", " camera.front_door ", "", "", "", 0); err != nil {
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

func TestAPictureNeedsAPicture(t *testing.T) {
	r := newRig(t, true)
	for _, p := range []string{"", "  ", "front door", "sensor.washer"} {
		if err := r.f.NotifyPicture("m", "", p, "", "", "", 0); err == nil {
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
	if err := r.f.NotifyPicture("", "  ", "camera.front_door", "", "", "", 0); err != nil {
		t.Fatalf("a picture with no words was refused: %v", err)
	}
	if c := r.pics.shown[0]; c.Title != "" || c.Message != "" {
		t.Errorf("caption %+v, want no words", c)
	}
	if e := r.last(); e.Data["event"] != "shown" || e.Data["title"] != "" || e.Data["message"] != "" {
		t.Errorf("event %+v", e)
	}
	// Words alone still need words: a card with nothing on it says nothing.
	if err := r.f.Notify("", "Washer", "", "", "", 0); err == nil {
		t.Error("a text notification with no message was taken")
	}
}

// chime "off" puts a notification up without its chime, by day and out of quiet hours; anything else
// leaves the chime to the hour.
func TestChimeOffIsSilent(t *testing.T) {
	r := newRig(t, true)
	_ = r.f.Notify("m", "", "", "off", "", 0)
	_ = r.f.NotifyPicture("m", "", "camera.front_door", " OFF ", "", "", 0)
	time.Sleep(50 * time.Millisecond)
	if n := r.chimes.Load(); n != 0 {
		t.Errorf("chimed %d times with the chime off", n)
	}
	if got := r.fired(); !same(got, []string{"shown", "expired", "shown"}) {
		t.Errorf("events %v, want a silent notification reported as any other", got)
	}
	_ = r.f.Notify("m", "", "", "on", "", 0)
	waitFor(t, "the chime", func() bool { return r.chimes.Load() == 1 })
}

// A picture is over when its view is, however the view went: "go home", the cameras page, another
// camera asked for.
func TestAPictureIsOverWhenItsViewIs(t *testing.T) {
	r := newRig(t, true)
	_ = r.f.NotifyPicture("m", "", "camera.front_door", "", "", "", 0)
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
	_ = r.f.NotifyPicture("m", "", "camera.front_door", "", "", "", 0)
	r.f.Dismiss()
	if r.pics.hides != 1 || r.pics.up != "" {
		t.Errorf("hides=%d up=%q, want the view down once", r.pics.hides, r.pics.up)
	}
}

func TestTextReplacingAPictureTakesItsViewDown(t *testing.T) {
	r := newRig(t, true)
	_ = r.f.NotifyPicture("m", "", "camera.front_door", "", "", "", 0)
	_ = r.f.Notify("words", "", "", "", "", 0)
	if r.pics.hides != 1 {
		t.Errorf("hides=%d, want the picture's view taken down", r.pics.hides)
	}
	if got := r.fired(); !same(got, []string{"shown", "expired", "shown"}) {
		t.Errorf("events %v", got)
	}
}

func TestAPictureReplacingAPictureLeavesTheNewView(t *testing.T) {
	r := newRig(t, true)
	_ = r.f.NotifyPicture("one", "", "camera.front_door", "", "", "", 0)
	_ = r.f.NotifyPicture("two", "", "camera.deck", "", "", "", 0)
	n, up := r.f.Showing()
	if !up || n.Message != "two" || !r.pics.PictureUp(n.ID) || r.pics.hides != 0 {
		t.Errorf("notification %+v up=%v hides=%d", n, up, r.pics.hides)
	}
}

func TestItChimesUnlessHushed(t *testing.T) {
	r := newRig(t, true)
	r.hushed.Store(true)
	_ = r.f.Notify("m", "", "", "", "", 0)
	time.Sleep(50 * time.Millisecond)
	if n := r.chimes.Load(); n != 0 {
		t.Errorf("chimed %d times while hushed", n)
	}
	r.hushed.Store(false)
	_ = r.f.Notify("m", "", "", "", "", 0)
	waitFor(t, "the chime", func() bool { return r.chimes.Load() == 1 })
}

// A Dot has no screen: a picture notification is taken (so an automation calling every device does
// not fail on one), chimes, is reported, and ends on its own time.
func TestADeviceWithNoScreen(t *testing.T) {
	r := newRig(t, false)
	if err := r.f.NotifyPicture("m", "", "camera.front_door", "", "", "", 30); err != nil {
		t.Fatal(err)
	}
	if err := r.f.NotifyPicture("m", "", "front door", "", "", "", 30); err == nil {
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
		"notify":         "message,title,icon,chime,speak,seconds",
		"notify_picture": "message,title,picture,chime,speak,sound,seconds",
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
			if err := r.f.NotifyPicture("m", "t", "/local/door.png", "", "", "", 0); err != nil {
				t.Error(err)
			}
		}()
		go func() {
			defer wg.Done()
			_ = r.f.Notify("words", "", "", "", "", 0)
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
			err = r.f.NotifyPicture(c.message, c.title, "camera.front_door", "", "on", "", 0)
		} else {
			err = r.f.Notify(c.message, c.title, "", "", " ON ", 0)
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

// Without speak "on" nothing is said; with it, nothing is said at night or in quiet hours either, and
// the chime being off does not stop the words.
func TestWhenANotificationSpeaks(t *testing.T) {
	r := newRig(t, true)
	_ = r.f.Notify("m", "", "", "", "", 0)
	_ = r.f.Notify("m", "", "", "", "yes", 0)
	r.hushed.Store(true)
	_ = r.f.Notify("m", "", "", "", "on", 0)
	time.Sleep(50 * time.Millisecond)
	if got := r.spoken(); len(got) != 0 {
		t.Errorf("said %q, want nothing", got)
	}
	r.hushed.Store(false)
	before := r.chimes.Load() // the first two chimed, as notifications do
	_ = r.f.Notify("words", "", "", "off", "on", 0)
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
	_ = r.f.Notify("m", "", "", "", "on", 0)
	waitFor(t, "both", func() bool { mu.Lock(); defer mu.Unlock(); return len(order) == 2 })
	if order[0] != "chime" || order[1] != "words" {
		t.Errorf("order %v, want the chime then the words", order)
	}
}

// A Dot, with no screen, says it too: there it is the whole of the notification.
func TestADotSpeaks(t *testing.T) {
	r := newRig(t, false)
	_ = r.f.Notify("The washer is done", "", "", "", "on", 0)
	waitFor(t, "the words", func() bool { return len(r.spoken()) == 1 })
}

// A picture's sound argument goes to the camera page as it was given: what "on", "off" and "" mean for
// a camera's audio is the camera page's business, the same as for home_show_camera_sound.
func TestAPicturesSoundGoesToTheCameraPage(t *testing.T) {
	r := newRig(t, true)
	for _, sound := range []string{"on", "off", ""} {
		if err := r.f.NotifyPicture("m", "", "camera.front_door", "", "", sound, 0); err != nil {
			t.Fatal(err)
		}
	}
	if got := strings.Join(r.pics.sounds, ","); got != "on,off," {
		t.Errorf("sounds passed %q, want %q", got, "on,off,")
	}
}

// With no time given, a notification stays until somebody taps it: a day on, it is still up.
func TestNoTimeIsUntilTapped(t *testing.T) {
	r := newRig(t, true)
	_ = r.f.Notify("m", "", "", "", "", 0)
	r.now = r.now.Add(25 * time.Hour)
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
}
