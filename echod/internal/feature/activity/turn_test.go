package activity

import (
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// caught collects the events a turn fires under name, so a test can read what Home Assistant would
// have.
func caught(t *testing.T, name ...string) chan component.Event {
	t.Helper()

	want := TurnEvent
	if len(name) > 0 {
		want = name[0]
	}
	got := make(chan component.Event, 8)
	cancel := component.Fire.Listen(func(e component.Event) {
		if e.Name == want {
			got <- e
		}
	})
	t.Cleanup(cancel)

	return got
}

// one waits for the turn's event, which goes out from a goroutine of its own, and then makes sure no
// second one follows.
func one(t *testing.T, got chan component.Event) map[string]string {
	t.Helper()

	var e component.Event
	select {
	case e = <-got:
	case <-time.After(2 * time.Second):
		t.Fatal("no event fired")
	}
	select {
	case extra := <-got:
		t.Errorf("a second event fired: %v", extra.Data)
	case <-time.After(50 * time.Millisecond):
	}
	return e.Data
}

// A phase is only reported if the turn reached it, which is the difference between "spent no time
// speaking" and "never got as far as speaking".
func TestAFailedTurnReportsNoPhaseItNeverReached(t *testing.T) {
	got := caught(t)

	turn := Get().Begin(1, "hey jarvis", WakeWord)
	time.Sleep(2 * time.Millisecond)
	turn.Listening()
	time.Sleep(2 * time.Millisecond)
	turn.Heard("what time is it")
	time.Sleep(2 * time.Millisecond)
	turn.Fails("stt-stream-failed")
	turn.Ends(Failed)

	data := one(t, got)
	for _, key := range []string{"listen_ms", "think_ms"} {
		if data[key] == "" {
			t.Errorf("%s missing, and the turn reached that phase", key)
		}
	}
	if data["reply_ms"] != "" {
		t.Errorf("reply_ms is %q, and the turn never replied", data["reply_ms"])
	}
	if data["reply"] != "" {
		t.Errorf("reply is %q, and there was none", data["reply"])
	}
	if data["ended"] != "error" || data["error"] != "stt-stream-failed" {
		t.Errorf("ended %q with error %q, want error and stt-stream-failed", data["ended"], data["error"])
	}
	if data["trigger"] != "wake_word" || data["wake_word"] != "hey jarvis" {
		t.Errorf("trigger %q, wake word %q", data["trigger"], data["wake_word"])
	}
	if data["heard"] != "what time is it" {
		t.Errorf("heard is %q", data["heard"])
	}
}

func TestAWholeTurnReportsEveryPhase(t *testing.T) {
	got := caught(t)

	turn := Get().Begin(2, "alexa", Button)
	for _, step := range []func(){
		turn.Listening,
		func() { turn.Heard("play something") },
		func() { turn.Replying("playing") },
	} {
		time.Sleep(2 * time.Millisecond)
		step()
	}
	time.Sleep(2 * time.Millisecond)
	turn.Ends(Answered)

	data := one(t, got)
	for _, key := range []string{"listen_ms", "think_ms", "reply_ms"} {
		if data[key] == "" {
			t.Errorf("%s missing", key)
		}
	}
	if data["slot"] != "2" {
		t.Errorf("slot is %q, want 2", data["slot"])
	}
	if data["id"] == "" {
		t.Error("no id, so nothing could ask for the recording")
	}
	if data["version"] != Version {
		t.Errorf("version is %q, want %q", data["version"], Version)
	}
	if data["ended"] != "answered" || data["trigger"] != "button" || data["reply"] != "playing" {
		t.Errorf("ended %q, trigger %q, reply %q", data["ended"], data["trigger"], data["reply"])
	}
	if _, ok := data["error"]; ok {
		t.Errorf("an answered turn carries an error: %q", data["error"])
	}
}

// A transcript or a reply is kept short enough for the recorder, and a mark says it was cut.
func TestLongTextIsBounded(t *testing.T) {
	got := caught(t)

	long := strings.Repeat("ab", textLimit)
	turn := Get().Begin(1, "hey jarvis", FollowUp)
	turn.Heard(long)
	turn.Replying(long)
	turn.Ends(Answered)

	data := one(t, got)
	for _, key := range []string{"heard", "reply"} {
		if n := utf8.RuneCountInString(data[key]); n != textLimit || !strings.HasSuffix(data[key], "…") {
			t.Errorf("%s is %d characters, want %d ending in a mark", key, n, textLimit)
		}
	}
	if data["trigger"] != "follow_up" {
		t.Errorf("trigger is %q, want follow_up", data["trigger"])
	}
}

// Several things notice a turn is over — the run closing, the reply finishing, a timeout — and the
// event has to be the one, not one each.
func TestEndingTwiceFiresOnce(t *testing.T) {
	got := caught(t)

	turn := Get().Begin(1, "hey jarvis", WakeWord)
	turn.Ends(Answered)
	turn.Ends(Failed)

	if data := one(t, got); data["ended"] != string(Answered) {
		t.Errorf("ended is %q, want the first one", data["ended"])
	}
}

// A turn nobody started is what a cancel arriving while idle has, so every method takes a nil.
func TestNoTurnIsSafeToMark(t *testing.T) {
	got := caught(t)

	var turn *Turn
	turn.Listening()
	turn.Heard("nothing")
	turn.Replying("nothing")
	turn.Fails("timeout")
	turn.Cancels(Stopped)
	turn.AwaitsScore()
	turn.Scored(0.9)
	turn.Ends(Canceled)

	if turn.ID() != "" {
		t.Errorf("id is %q, want empty", turn.ID())
	}
	select {
	case e := <-got:
		t.Errorf("fired %v, want none", e.Data)
	case <-time.After(50 * time.Millisecond):
	}
}

// EchoLocal's integration reads the old event for its logbook and activity view, so it goes out as it
// always did beside the new one: its own name, outcome rather than ended, speak_ms, and no trigger.
func TestTheEchoLocalEventGoesOutAsBefore(t *testing.T) {
	for _, tc := range []struct {
		how     Ending
		timeout bool
		failure string
		outcome string
	}{
		{how: Answered, outcome: "completed"},
		{how: NoSpeech, outcome: "cancelled"},
		{how: Canceled, outcome: "cancelled"},
		{how: Failed, failure: "intent-failed", outcome: "failed"},
		{how: Failed, timeout: true, outcome: "timeout"},
	} {
		newer, older := caught(t), caught(t, LegacyTurnEvent)

		turn := Get().Begin(1, "hey jarvis", WakeWord)
		turn.Listening()
		turn.Heard("what time is it")
		time.Sleep(2 * time.Millisecond)
		turn.Replying("It's noon.")
		time.Sleep(2 * time.Millisecond)
		if tc.failure != "" {
			turn.Fails(tc.failure)
		}
		if tc.timeout {
			turn.TimesOut()
		}
		turn.Ends(tc.how)

		data, legacy := one(t, newer), one(t, older)
		if legacy["outcome"] != tc.outcome || data["ended"] != string(tc.how) {
			t.Errorf("%s: outcome %q, want %q; ended %q", tc.how, legacy["outcome"], tc.outcome, data["ended"])
		}
		if legacy["id"] != data["id"] || legacy["version"] != "1" || legacy["slot"] != "1" ||
			legacy["wake_word"] != "hey jarvis" || legacy["heard"] != "what time is it" || legacy["reply"] != "It's noon." {
			t.Errorf("%s: the old event's fields are %v", tc.how, legacy)
		}
		if legacy["speak_ms"] == "" || data["reply_ms"] == "" {
			t.Errorf("%s: speak_ms %q, reply_ms %q", tc.how, legacy["speak_ms"], data["reply_ms"])
		}
		for _, key := range []string{"ended", "trigger", "error", "device", "reply_ms"} {
			if _, ok := legacy[key]; ok {
				t.Errorf("%s: the old event carries the new field %s", tc.how, key)
			}
		}
		for _, key := range []string{"outcome", "speak_ms"} {
			if _, ok := data[key]; ok {
				t.Errorf("%s: the new event carries the old field %s", tc.how, key)
			}
		}
	}
}

// ownSender gives the test a sender of its own with this send and room for n events, and puts the
// device's back after.
func ownSender(t *testing.T, n int, send func(component.Event)) *sender {
	t.Helper()
	s := &sender{outbox: make(chan component.Event, n), send: send}
	was := events
	events = s
	t.Cleanup(func() { events = was })
	return s
}

// Ending a turn never waits on Home Assistant. With the sender stuck on a dead connection, Ends still
// returns at once, the queue fills to its room, and what comes after that is dropped: the newest, so
// what is already waiting goes out in order once the connection is back.
func TestEndsNeverWaitsForASenderThatIsStuck(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var mu sync.Mutex
	var sent []string
	s := ownSender(t, 2, func(e component.Event) {
		mu.Lock()
		first := len(sent) == 0
		sent = append(sent, e.Name+" "+e.Data["id"])
		mu.Unlock()
		if first {
			close(started)
			<-release // the write to a connection that has died
		}
	})

	one := Get().Begin(1, "hey jarvis", WakeWord)
	begun := time.Now()
	one.Ends(Answered)
	<-started // the first event is being sent, and stuck; the second waits in the queue
	two := Get().Begin(1, "hey jarvis", WakeWord)
	two.Ends(Answered) // its first event fills the queue, its second is dropped
	if took := time.Since(begun); took > time.Second {
		t.Errorf("ending two turns took %v with the sender stuck", took)
	}
	if n := len(s.outbox); n != cap(s.outbox) {
		t.Errorf("%d events wait, want the queue full at %d", n, cap(s.outbox))
	}

	close(release)
	deadline := time.After(2 * time.Second)
	for {
		mu.Lock()
		n := len(sent)
		mu.Unlock()
		if n == 3 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("%d events went out, want 3", n)
		case <-time.After(5 * time.Millisecond):
		}
	}
	time.Sleep(20 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	want := []string{TurnEvent + " " + one.ID(), LegacyTurnEvent + " " + one.ID(), TurnEvent + " " + two.ID()}
	if strings.Join(sent, ",") != strings.Join(want, ",") {
		t.Errorf("sent %v, want %v", sent, want)
	}
}

// One event that panics on its way out is that event lost, not the sender: the turns after it are
// still reported.
func TestTheSenderOutlivesAPanic(t *testing.T) {
	got := make(chan component.Event, 4)
	panicked := false
	ownSender(t, 4, func(e component.Event) {
		if !panicked {
			panicked = true
			panic("the connection went away mid-write")
		}
		got <- e
	})

	Get().Begin(1, "hey jarvis", WakeWord).Ends(Answered)
	select {
	case e := <-got:
		if e.Name != LegacyTurnEvent {
			t.Errorf("sent %s after the panic, want the turn's other event", e.Name)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("nothing went out after the panic")
	}
	Get().Begin(1, "hey jarvis", WakeWord).Ends(Answered)
	select {
	case <-got:
	case <-time.After(2 * time.Second):
		t.Fatal("the next turn was not reported")
	}
}

// The device field says which device a turn was on, and is left out when the device has no name.
func TestTheDeviceFieldIsLeftOutWithoutAName(t *testing.T) {
	was := config.Get().Device
	t.Cleanup(func() { config.Started(was) })

	for _, name := range []string{"", "Office"} {
		got := caught(t)
		config.Started(config.Device{Name: name})
		Get().Begin(1, "hey jarvis", WakeWord).Ends(Answered)
		device, ok := one(t, got)["device"]
		if name == "" && ok {
			t.Errorf("an unnamed device sent device %q", device)
		}
		if name != "" && device != name {
			t.Errorf("device is %q, want %q", device, name)
		}
	}
}

// A turn owed its wake word's score holds its events for it, and sends them as soon as it comes.
func TestAFinishedTurnWaitsForItsScore(t *testing.T) {
	got := caught(t)

	turn := Get().Begin(1, "okay nabu", WakeWord)
	turn.AwaitsScore()
	turn.Cancels(AnsweredElsewhere)
	turn.Ends(Canceled)
	select {
	case e := <-got:
		t.Fatalf("fired %v before the score came", e.Data)
	case <-time.After(50 * time.Millisecond):
	}
	turn.Scored(0.968)

	data := one(t, got)
	if data["wake_score"] != "0.97" || data["reason"] != "answered_elsewhere" {
		t.Errorf("wake_score %q, reason %q; want 0.97, answered_elsewhere", data["wake_score"], data["reason"])
	}
	turn.Scored(0.5) // too late: nothing more goes out
	select {
	case e := <-got:
		t.Errorf("a second event fired: %v", e.Data)
	case <-time.After(50 * time.Millisecond):
	}
}

// A score that never comes holds the events no longer than scoreWait.
func TestAScoreThatNeverComesHoldsNothingLong(t *testing.T) {
	got := caught(t)

	turn := Get().Begin(1, "okay nabu", WakeWord)
	turn.AwaitsScore()
	start := time.Now()
	turn.Ends(NoSpeech)

	data := one(t, got)
	if waited := time.Since(start); waited < scoreWait/2 || waited > scoreWait+time.Second {
		t.Errorf("sent after %v, want about %v", waited, scoreWait)
	}
	if data["wake_score"] != "" {
		t.Errorf("wake_score is %q, and none came", data["wake_score"])
	}
}
