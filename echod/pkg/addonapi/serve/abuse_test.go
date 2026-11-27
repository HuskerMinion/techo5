package serve

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/pkg/addonapi"
)

// logged is the daemon's log, captured for a test.
type logged struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *logged) Write(p []byte) (int, error) { l.mu.Lock(); defer l.mu.Unlock(); return l.b.Write(p) }
func (l *logged) count(sub string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Count(l.b.String(), sub)
}

func captureLog(t *testing.T) *logged {
	l := &logged{}
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(l, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })
	return l
}

// The log is on the flash: an add-on connecting as fast as it can, past its connections or banned,
// is refused without a word, and what is said is said once a minute at most.
func TestRefusalsDoNotFloodTheLog(t *testing.T) {
	l := captureLog(t)
	r := newRig(t, everything)
	for range addonapi.MaxConns {
		r.dial()
	}
	for range 500 {
		if nc, err := net.Dial("unix", r.path); err == nil {
			nc.Close()
		}
	}
	// The wrong user, again and again: one line.
	r.setPeer(func(net.Conn) (uint32, error) { return 4242, nil })
	for range 500 {
		if nc, err := net.Dial("unix", r.path); err == nil {
			_, _ = nc.Read(make([]byte, 1))
			nc.Close()
		}
	}
	time.Sleep(50 * time.Millisecond)
	if n := l.count("connection was refused"); n > 1 {
		t.Errorf("%d lines for 1000 refused connections", n)
	}
}

// A header claiming far more than a message may be hangs up, says so at most once a minute, and is a
// strike: an add-on reconnecting to send it again is soon banned.
func TestHugeHeadersAreStrikes(t *testing.T) {
	l := captureLog(t)
	r := newRig(t, everything)
	r.freeze()
	for range strikeLimit.Load() + 5 {
		nc, err := net.Dial("unix", r.path)
		if err != nil {
			t.Fatal(err)
		}
		var h [4]byte
		binary.BigEndian.PutUint32(h[:], 64<<20)
		_, _ = nc.Write(h[:])
		_, _ = nc.Read(make([]byte, 1))
		nc.Close()
	}
	eventually(t, "the ban", func() bool { _, b := r.s.Banned("meals"); return b })
	if n := l.count("far past the limit"); n != 1 {
		t.Errorf("%d lines for the huge headers", n)
	}
}

// The log line kinds left out are counted and said with the next.
func TestTheLogSaysHowManyWereLeftOut(t *testing.T) {
	l := captureLog(t)
	r := newRig(t, everything)
	r.freeze()
	for range 5 {
		r.s.warn("meals", "x", "addonapi: a test line")
	}
	r.advance(logEvery)
	r.s.warn("meals", "x", "addonapi: a test line")
	if n := l.count("a test line"); n != 2 || l.count("left_out_since_last=4") != 1 {
		t.Errorf("logged:\n%s", l.b.String())
	}
}

// Thousands of keys in one object are refused at once, not compared with each other.
func TestManyKeysAreRefusedQuickly(t *testing.T) {
	var sb strings.Builder
	sb.WriteString(`{"id":1,"op":"hello","args":{`)
	for i := 0; sb.Len() < addonapi.MaxMessage-20; i++ {
		if i > 0 {
			sb.WriteByte(',')
		}
		fmt.Fprintf(&sb, `"k%x":0`, i)
	}
	sb.WriteString(`}}`)
	start := time.Now()
	var req addonapi.Request
	err := addonapi.Unmarshal([]byte(sb.String()), &req)
	if err == nil || !strings.Contains(err.Error(), "more than") {
		t.Errorf("a message of thousands of keys: %v", err)
	}
	if d := time.Since(start); d > 50*time.Millisecond {
		t.Errorf("took %v", d)
	}
}

// A request refused for the rate is refused before its message is parsed, and answered by its id.
func TestTheRateIsTakenBeforeParsing(t *testing.T) {
	was := strikeLimit.Swap(1000) // every bad message is a strike; this one is about the rate alone
	t.Cleanup(func() { strikeLimit.Store(was) })
	r := newRig(t, everything)
	r.freeze()
	w := r.raw()
	w.send(`{"id":1,"op":"hello","args":{"api":1}}`)
	w.read()
	for range addonapi.Burst {
		w.send(`not json`)
		w.read()
	}
	w.send(`{"id":77,"op":"timer.list"}`)
	if got := w.read(); !strings.Contains(got, `"rate_limited"`) || !strings.Contains(got, `"id":77`) {
		t.Errorf("past the rate: %s", got)
	}
}

// A device error's words stay in the daemon's log: the add-on is told only that it failed.
func TestInternalErrorsSayNothingOfTheDevice(t *testing.T) {
	r := newRig(t, everything)
	r.d.mu.Lock()
	r.d.failEntity = errors.New("open /data/techo5-linux/secret.json: permission denied")
	r.d.mu.Unlock()
	c := r.dial()
	err := c.SetEntity(ctx(t), "step", "x")
	if code(err) != addonapi.CodeInternal || strings.Contains(err.Error(), "/data") {
		t.Errorf("an internal error: %v", err)
	}
}

// A command's reply cannot answer a settings test, which is held to 200 characters, nor the other way
// round.
func TestRepliesAnswerOnlyTheirOwnKind(t *testing.T) {
	r := newRig(t, everything)
	c := r.dial()
	go func() {
		for ev := range c.Events() {
			var st addonapi.SettingsTest
			if ev.Event == addonapi.EventSettingsTest && ev.Decode(&st) == nil {
				err := c.ReplyCommand(context.Background(), st.Call, false, strings.Repeat("x", 900))
				if code(err) != addonapi.CodeBadRequest {
					t.Errorf("a command reply to a test: %v", err)
				}
				_ = c.ReplyTest(context.Background(), st.Call, true, "")
			}
		}
	}()
	// Dial returns once the add-on has said hello; the server files the connection just after.
	eventually(t, "the connection to be filed", func() bool { return r.s.Connected("meals") == 1 })
	if ok, _, err := r.s.Test(ctx(t), "meals", nil); !ok || err != nil {
		t.Errorf("the test: %v %v", ok, err)
	}
}

// Connections starting timers at once cannot pass MaxTimers together.
func TestMaxTimersHoldsAcrossConnections(t *testing.T) {
	r := newRig(t, everything)
	r.d.mu.Lock()
	r.d.timerDelay = 5 * time.Millisecond
	r.d.mu.Unlock()
	var cs []*addonapi.Client
	for range addonapi.MaxConns {
		cs = append(cs, r.dial())
	}
	var wg sync.WaitGroup
	for _, c := range cs {
		for range 2 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, _ = c.StartTimer(context.Background(), "x", time.Minute)
				_, _ = c.StartTimer(context.Background(), "x", time.Minute)
			}()
		}
	}
	wg.Wait()
	if n := len(r.d.Timers("meals")); n > addonapi.MaxTimers {
		t.Errorf("%d timers, more than %d", n, addonapi.MaxTimers)
	}
}

// A hold asked for on a connection that has closed is refused, so nothing is left holding the screen.
func TestAHoldOnAClosedConnectionIsRefused(t *testing.T) {
	r := newRig(t, everything)
	r.dial()
	r.s.mu.Lock()
	var c *conn
	for x := range r.s.addons["meals"].conns {
		c = x
	}
	r.s.mu.Unlock()
	c.close()
	if _, err := r.s.hold(c, false, addonapi.AwakeHold{Seconds: 60}); code(err) != addonapi.CodeUnavailable {
		t.Errorf("a hold on a closed connection: %v", err)
	}
	if r.s.Awake() {
		t.Error("the screen is held")
	}
}

// media.play is held to 3 at once and then one every 30 seconds, and carrying on is refused in quiet
// hours.
func TestMediaLimits(t *testing.T) {
	r := newRig(t, everything)
	r.freeze()
	c := r.dial()
	for range addonapi.MediaPlayBurst {
		if err := c.PlayMedia(ctx(t), "audio", "http://x/a.mp3", ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.PlayMedia(ctx(t), "audio", "http://x/b.mp3", ""); code(err) != addonapi.CodeRateLimited {
		t.Errorf("a fourth play at once: %v", err)
	}
	r.advance(addonapi.MediaPlayEvery / 2)
	if err := c.PlayMedia(ctx(t), "audio", "http://x/b.mp3", ""); code(err) != addonapi.CodeRateLimited {
		t.Errorf("after 15 seconds: %v", err)
	}
	r.advance(addonapi.MediaPlayEvery / 2)
	if err := c.PlayMedia(ctx(t), "audio", "http://x/b.mp3", ""); err != nil {
		t.Errorf("after 30 seconds: %v", err)
	}
	r.d.mu.Lock()
	r.d.quiet = true
	r.d.mu.Unlock()
	if err := c.ResumeMedia(ctx(t)); code(err) != addonapi.CodeBusy {
		t.Errorf("resume in quiet hours: %v", err)
	}
	if err := c.PauseMedia(ctx(t)); err != nil {
		t.Errorf("pause in quiet hours: %v", err)
	}
}

// All of an add-on's entities together are held to StatesPerSecond.
func TestEntityStatesTogether(t *testing.T) {
	r := newRig(t, func(dir string) Grant {
		g := everything(dir)
		for i := range 6 {
			g.Entities = append(g.Entities, addonapi.Entity{Domain: addonapi.DomainSensor, ID: fmt.Sprint("s", i), Name: "S", Text: true})
		}
		return g
	})
	r.freeze()
	c := r.dial()
	limited := 0
	for i := range 6 {
		if code(c.SetEntity(ctx(t), fmt.Sprint("s", i), "x")) == addonapi.CodeRateLimited {
			limited++
		}
	}
	if limited != 6-addonapi.StatesPerSecond {
		t.Errorf("%d of 6 entities limited", limited)
	}
}
