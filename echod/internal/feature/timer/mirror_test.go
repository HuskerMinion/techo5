package timer

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"
	"github.com/ygelfand/go-esphome-device/api"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// asked is the timer list actions the device asked Home Assistant for, as a test sees them.
type asked struct {
	mu    sync.Mutex
	calls []component.Call
}

func (a *asked) all() []component.Call {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]component.Call(nil), a.calls...)
}

func (a *asked) last(t *testing.T) component.Call {
	t.Helper()
	calls := a.all()
	if len(calls) == 0 {
		t.Fatal("nothing was asked of Home Assistant")
	}
	return calls[len(calls)-1]
}

// inList is a set of timers with "Timers set here in Home Assistant" on, and what they ask Home
// Assistant for.
func inList(t *testing.T, on bool) (*Timers, *asked) {
	t.Helper()
	ts := controlled(t)
	if err := config.Set().Timers().InHAList(on); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = config.Set().Timers().InHAList(false) })
	a := &asked{}
	t.Cleanup(component.CallService.Listen(func(c component.Call) {
		a.mu.Lock()
		a.calls = append(a.calls, c)
		a.mu.Unlock()
	}))
	return ts, a
}

// answer is Home Assistant answering a call with the id it created.
func answer(c component.Call, id string) {
	resp, _ := json.Marshal(id)
	c.Reply(resp, nil)
}

func event(typ api.VoiceAssistantTimerEvent, id, name string, left, total int, active bool) esphome.TimerEvent {
	return esphome.TimerEvent{Type: typ, TimerID: id, Name: name, SecondsLeft: uint32(left), TotalSeconds: uint32(total), IsActive: active}
}

func (ts *Timers) heldFor(t *testing.T, id string) timer {
	t.Helper()
	ts.mu.Lock()
	defer ts.mu.Unlock()
	c, ok := ts.held[id]
	if !ok {
		t.Fatalf("timer %s is not held", id)
	}
	return *c
}

func (ts *Timers) heldCount() int {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return len(ts.held)
}

// A timer set here gets a stand-in in Home Assistant's list, asked of a Home Assistant that has lists,
// and the id it answers with is remembered.
func TestATimerSetHereGetsAStandIn(t *testing.T) {
	ts, a := inList(t, true)

	id := ts.Start("Pasta", 5*time.Minute)

	c := a.last(t)
	if c.Service != "timer_list.create_timer" || c.MinVersion != listSince || c.Reply == nil {
		t.Fatalf("asked %+v, want timer_list.create_timer with a reply from %s on", c, listSince)
	}
	if c.Data["name"] != "Pasta" || c.Data["duration"] != "300" {
		t.Errorf("created with %v, want Pasta for 300 seconds", c.Data)
	}
	if !strings.Contains(c.Templates["entity_id"], "timer_list") {
		t.Errorf("the timer list is not looked up: %q", c.Templates["entity_id"])
	}

	answer(c, "01HA")
	if got := ts.heldFor(t, id).ha; got != "01HA" {
		t.Fatalf("stand-in %q, want 01HA", got)
	}
	if got := config.Get().Timers.Local[0].HA; got != "01HA" {
		t.Errorf("saved stand-in %q, want 01HA: a restart would lose it", got)
	}

	// Home Assistant says the stand-in started: that is this timer, not a second one.
	ts.Event(event(api.VoiceAssistantTimerEvent_VOICE_ASSISTANT_TIMER_STARTED, "01HA", "Pasta", 300, 300, true))
	if n := ts.heldCount(); n != 1 {
		t.Errorf("%d timers held, want the one", n)
	}
}

// Home Assistant says a timer started before it answers with its id. The started event of a stand-in
// being added is taken for it rather than counted as one of Home Assistant's.
func TestAStandInThatStartsBeforeTheAnswerIsTakenForIt(t *testing.T) {
	ts, a := inList(t, true)
	id := ts.Start("Pasta", 5*time.Minute)

	ts.Event(event(api.VoiceAssistantTimerEvent_VOICE_ASSISTANT_TIMER_STARTED, "01HA", "Pasta", 300, 300, true))
	if n := ts.heldCount(); n != 1 {
		t.Fatalf("%d timers held, want the one", n)
	}
	answer(a.last(t), "01HA")
	if got := ts.heldFor(t, id).ha; got != "01HA" {
		t.Errorf("stand-in %q, want 01HA", got)
	}
}

// Changes made in Home Assistant's list change the timer here.
func TestChangesInTheListChangeTheTimerHere(t *testing.T) {
	ts, a := inList(t, true)
	id := ts.Start("Pasta", 5*time.Minute)
	answer(a.last(t), "01HA")

	ts.Event(event(api.VoiceAssistantTimerEvent_VOICE_ASSISTANT_TIMER_UPDATED, "01HA", "Pasta", 200, 300, false))
	c := ts.heldFor(t, id)
	if c.active || c.left != 200*time.Second {
		t.Errorf("after a pause there: running %v with %v left, want paused with 200s", c.active, c.left)
	}

	ts.Event(event(api.VoiceAssistantTimerEvent_VOICE_ASSISTANT_TIMER_UPDATED, "01HA", "Pasta", 260, 360, true))
	c = ts.heldFor(t, id)
	if !c.active || c.left != 260*time.Second || c.total != 360*time.Second {
		t.Errorf("after resuming with time added there: %+v, want running, 260s of 360s", c)
	}

	ts.Event(event(api.VoiceAssistantTimerEvent_VOICE_ASSISTANT_TIMER_CANCELLED, "01HA", "Pasta", 0, 360, false))
	if n := ts.heldCount(); n != 0 {
		t.Errorf("%d timers held after it was canceled there, want none", n)
	}
	if ts.Ringing() {
		t.Error("a timer canceled there rang here")
	}
}

// What is done to a timer here is done to its stand-in.
func TestChangesHereFollowInTheList(t *testing.T) {
	ts, a := inList(t, true)
	id := ts.Start("Pasta", 5*time.Minute)
	answer(a.last(t), "01HA")

	for _, step := range []struct {
		do      func() error
		service string
		data    map[string]string
	}{
		{func() error { return ts.Pause(id) }, "timer_list.pause_timer", map[string]string{"timer_id": "01HA"}},
		{func() error { return ts.Resume(id) }, "timer_list.unpause_timer", map[string]string{"timer_id": "01HA"}},
		{func() error { return ts.Adjust(id, time.Minute) }, "timer_list.add_time", map[string]string{"timer_id": "01HA", "duration": "60"}},
		{func() error { return ts.Adjust(id, -2*time.Minute) }, "timer_list.subtract_time", map[string]string{"timer_id": "01HA", "duration": "120"}},
	} {
		if err := step.do(); err != nil {
			t.Fatal(err)
		}
		c := a.last(t)
		if c.Service != step.service || !sameData(c.Data, step.data) {
			t.Errorf("asked %s %v, want %s %v", c.Service, c.Data, step.service, step.data)
		}
	}

	if !ts.Cancel(id) {
		t.Fatal("not canceled")
	}
	if c := a.last(t); c.Service != "timer_list.cancel_timer" || c.Data["timer_id"] != "01HA" {
		t.Errorf("asked %s %v, want the stand-in canceled", c.Service, c.Data)
	}
	// Home Assistant's word that its stand-in finished, late, does not ring a timer canceled here.
	ts.Event(event(api.VoiceAssistantTimerEvent_VOICE_ASSISTANT_TIMER_FINISHED, "01HA", "Pasta", 0, 300, false))
	if ts.Ringing() {
		t.Error("the stand-in of a timer canceled here rang")
	}
}

func sameData(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// A stand-in that finishes there rings the timer here, once.
func TestAStandInFinishingRingsOnce(t *testing.T) {
	ts, a := inList(t, true)
	id := ts.Start("Pasta", 5*time.Minute)
	answer(a.last(t), "01HA")

	ts.Event(event(api.VoiceAssistantTimerEvent_VOICE_ASSISTANT_TIMER_FINISHED, "01HA", "Pasta", 0, 300, false))
	if n := ts.heldCount(); n != 0 {
		t.Fatalf("%d timers held after it finished, want none", n)
	}
	if !ts.Ringing() {
		t.Fatal("a timer that finished there did not ring here")
	}

	// Home Assistant says so again: taken as the stand-in of a timer ended here, not one of its own
	// to ring for.
	if !ts.listEvent(event(api.VoiceAssistantTimerEvent_VOICE_ASSISTANT_TIMER_FINISHED, "01HA", "Pasta", 0, 300, false)) {
		t.Errorf("a second finish of %s's stand-in would ring again", id)
	}
}

// With the setting off, and for a silent timer, nothing is asked of Home Assistant.
func TestNothingIsAddedWhenOffOrSilent(t *testing.T) {
	ts, a := inList(t, false)
	ts.Start("Pasta", 5*time.Minute)
	if calls := a.all(); len(calls) != 0 {
		t.Errorf("asked %+v with the setting off", calls)
	}

	ts, a = inList(t, true)
	ts.StartSilent("Waiting", 5*time.Minute, countdownColor)
	if calls := a.all(); len(calls) != 0 {
		t.Errorf("asked %+v for a silent timer", calls)
	}
}

// A stand-in that could not be added is tried again when Home Assistant is back, as is one Home
// Assistant no longer has; one paused here meanwhile is paused there, and one canceled here meanwhile
// is canceled there.
func TestTheListIsBroughtInLine(t *testing.T) {
	ts, a := inList(t, true)
	lost := ts.Start("Lost", 5*time.Minute)
	a.last(t).Reply(nil, errFake)
	kept := ts.Start("Kept", 10*time.Minute)
	answer(a.last(t), "01KEPT")
	gone := ts.Start("Gone", 10*time.Minute)
	answer(a.last(t), "01GONE")

	// Home Assistant away: paused and canceled here, and it could not be told.
	if err := ts.Pause(kept); err != nil {
		t.Fatal(err)
	}
	ts.Cancel(gone)
	for _, c := range a.all() {
		if c.Service == "timer_list.cancel_timer" {
			c.Reply(nil, errFake)
		}
	}

	before := len(a.all())
	ts.listReconcile(map[string]listed{
		"01KEPT": {ID: "01KEPT", Status: "active", Left: 600},
		"01GONE": {ID: "01GONE", Status: "active", Left: 600},
	}, time.Now())

	var services []string
	for _, c := range a.all()[before:] {
		services = append(services, c.Service+" "+c.Data["timer_id"]+c.Data["name"])
	}
	want := []string{"timer_list.cancel_timer 01GONE", "timer_list.pause_timer 01KEPT", "timer_list.create_timer Lost"}
	if strings.Join(services, ", ") != strings.Join(want, ", ") {
		t.Errorf("asked %v, want %v", services, want)
	}
	if got := ts.heldFor(t, lost).ha; got != "" {
		t.Errorf("a timer without a stand-in has %q", got)
	}
}

type fakeErr string

func (e fakeErr) Error() string { return string(e) }

const errFake = fakeErr("Home Assistant is away")

func TestParseListed(t *testing.T) {
	resp, _ := json.Marshal(`["01A", "01B"]|["active", "paused"]|[12.5, 300.0]`)
	got, err := parseListed(resp)
	if err != nil {
		t.Fatal(err)
	}
	if got["01A"] != (listed{ID: "01A", Status: "active", Left: 12.5}) || got["01B"].Status != "paused" {
		t.Errorf("read %+v", got)
	}
	for _, bad := range []string{`[]|[]`, `["01A"]|[]|[]`, `x|y|z`} {
		resp, _ := json.Marshal(bad)
		if _, err := parseListed(resp); err == nil {
			t.Errorf("%q read without an error", bad)
		}
	}
}

func TestHAMAC(t *testing.T) {
	for raw, want := range map[string]string{
		"02005E10A0B1":      "02:00:5e:10:a0:b1",
		"02:00:5e:10:a0:b1": "02:00:5e:10:a0:b1",
		"":                  "",
	} {
		if got := haMAC(raw); got != want {
			t.Errorf("haMAC(%q) = %q, want %q", raw, got, want)
		}
	}
}

// Back after a while away, Home Assistant says what it had of a stand-in before it went. A change made
// here meanwhile is newer, so the device's timer is left as it is, to be brought in line there.
func TestWhatHomeAssistantHadBeforeItWentIsPassedOver(t *testing.T) {
	ts, a := inList(t, true)
	id := ts.Start("Pasta", 5*time.Minute)
	answer(a.last(t), "01HA")
	if err := ts.Adjust(id, -time.Minute); err != nil {
		t.Fatal(err)
	}
	held := ts.heldFor(t, id)
	before := held.remaining(time.Now())

	ts.Keep()
	ts.Event(event(api.VoiceAssistantTimerEvent_VOICE_ASSISTANT_TIMER_UPDATED, "01HA", "Pasta", 300, 300, true))
	held = ts.heldFor(t, id)
	if after := held.remaining(time.Now()); absDuration(after-before) > time.Second {
		t.Errorf("%v left after Home Assistant's old word, want the %v set here", after, before)
	}

	// A cancel from there is taken: somebody canceled it while the device was away.
	ts.Event(event(api.VoiceAssistantTimerEvent_VOICE_ASSISTANT_TIMER_CANCELLED, "01HA", "Pasta", 0, 300, false))
	if n := ts.heldCount(); n != 0 {
		t.Errorf("%d timers held after a cancel from there, want none", n)
	}
}
