package voice

import (
	"context"
	"encoding/binary"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/lib/llm"
	"github.com/HuskerMinion/techo5/echod/internal/lib/mcp"
	"github.com/HuskerMinion/techo5/echod/internal/lib/realtime"
)

// fakeSession is a provider's session: what it is sent, and events to give.
type fakeSession struct {
	mu      sync.Mutex
	audio   int
	ended   int
	goOns   int
	results []string
	closed  bool
	events  chan realtime.Event
}

func newFakeSession() *fakeSession { return &fakeSession{events: make(chan realtime.Event, 64)} }

func (s *fakeSession) Audio(p []int16) error {
	s.mu.Lock()
	s.audio += len(p)
	s.mu.Unlock()
	return nil
}
func (s *fakeSession) EndOfSpeech() error { s.mu.Lock(); s.ended++; s.mu.Unlock(); return nil }
func (s *fakeSession) ToolResult(id, name, out string) error {
	s.mu.Lock()
	s.results = append(s.results, name+"="+out)
	s.mu.Unlock()
	return nil
}
func (s *fakeSession) Continue() error               { s.mu.Lock(); s.goOns++; s.mu.Unlock(); return nil }
func (s *fakeSession) Events() <-chan realtime.Event { return s.events }
func (s *fakeSession) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.closed = true
		close(s.events)
	}
	return nil
}

// fakeGate is the assistant's lockout, as far as these tests need it.
type fakeGate struct {
	from, read bool
	ran        []string
}

func (g *fakeGate) Tools() []llm.Tool {
	return []llm.Tool{{Name: "set_timer", Description: "a timer"}, {Name: "web_search", Description: "the web"}}
}
func (g *fakeGate) Run(c llm.ToolCall) string {
	g.ran = append(g.ran, c.Function.Name)
	if c.Function.Name == "web_search" {
		g.read = true
	}
	return "ran " + c.Function.Name
}
func (g *fakeGate) Allow(readOnly bool) (bool, string) {
	if g.from && !readOnly {
		return false, "refused"
	}
	return true, ""
}
func (g *fakeGate) Saw(outside bool) { g.read = g.read || outside }
func (g *fakeGate) EndRound()        { g.from = g.from || g.read }

type fakeMCP struct {
	mu    sync.Mutex
	calls []string
}

func (m *fakeMCP) Start(context.Context) error { return nil }
func (m *fakeMCP) Tools(context.Context) ([]mcp.Tool, error) {
	return []mcp.Tool{{Name: "HassTurnOn"}, {Name: "GetLiveContext"}, {Name: "calendar_get_events"}}, nil
}
func (m *fakeMCP) Call(_ context.Context, name string, _ map[string]any) (string, bool, error) {
	m.mu.Lock()
	m.calls = append(m.calls, name)
	m.mu.Unlock()
	return "done " + name, false, nil
}

type rtRig struct {
	r      *rtBackend
	mu     sync.Mutex
	posted []event
	sess   []*fakeSession
	gate   *fakeGate
	mcp    *fakeMCP
	dialed chan struct{}
}

func newRTRig(t *testing.T) *rtRig {
	t.Helper()
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	_ = config.Set().Realtime().Set("openai", "", "", "", 0)
	_ = config.Set().Realtime().SetKey("sk-test")
	_ = config.Set().Realtime().SetMCPToken("ha-token")
	g := &rtRig{gate: &fakeGate{}, mcp: &fakeMCP{}, dialed: make(chan struct{}, 8)}
	wasGate := newGate
	newGate = func() ToolGate { return g.gate }
	t.Cleanup(func() { newGate = wasGate })
	g.r = newRealtime(func(e event) { g.mu.Lock(); g.posted = append(g.posted, e); g.mu.Unlock() })
	g.r.mcpFor = func(token string) mcpClient {
		if token != "ha-token" {
			t.Errorf("token %q", token)
		}
		return g.mcp
	}
	g.r.dial = func(ctx context.Context, c realtime.Config) (rtSession, error) {
		if c.Key != "sk-test" || c.Provider != "openai" || len(c.Functions) != 5 {
			t.Errorf("dialed with %+v", c)
		}
		s := newFakeSession()
		g.mu.Lock()
		g.sess = append(g.sess, s)
		g.mu.Unlock()
		g.dialed <- struct{}{}
		return s, nil
	}
	t.Cleanup(func() { _ = g.r.Stop(); g.r.counting.Wait() })
	return g
}

func (g *rtRig) waitDial(t *testing.T) *fakeSession {
	t.Helper()
	select {
	case <-g.dialed:
	case <-time.After(5 * time.Second):
		t.Fatal("never dialed")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.sess[len(g.sess)-1]
}

func (g *rtRig) waitPosted(t *testing.T, kind eventKind) []event {
	t.Helper()
	for range 500 {
		g.mu.Lock()
		for _, e := range g.posted {
			if e.kind == kind {
				out := append([]event(nil), g.posted...)
				g.mu.Unlock()
				return out
			}
		}
		g.mu.Unlock()
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("no event %d posted", kind)
	return nil
}

func frame(n int) []byte { return make([]byte, 2*n) }

func TestARealtimeTurn(t *testing.T) {
	g := newRTRig(t)
	if !g.r.Ready() {
		t.Fatal("not ready")
	}
	_ = g.r.Start("")
	_ = g.r.Audio(frame(320)) // said while the session opens: kept, and sent once it is open
	s := g.waitDial(t)
	_ = g.r.End()
	waitUntilT(t, func() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.audio == 320 && s.ended == 1 })
	_ = g.r.Audio(frame(160))

	pcm := []int16{1, 2, 3, 4, 5, 6}
	s.events <- realtime.Event{Kind: realtime.Heard, Text: " what time is it "}
	s.events <- realtime.Event{Kind: realtime.Audio, PCM: pcm, Rate: 24000}
	s.events <- realtime.Event{Kind: realtime.Said, Text: "It is noon. Anything else?"}
	s.events <- realtime.Event{Kind: realtime.Done}
	evs := g.waitPosted(t, evRunEnd)
	var kinds []eventKind
	for _, e := range evs {
		kinds = append(kinds, e.kind)
	}
	// A question at the end opens no listening: the next turn would not know what it was.
	want := []eventKind{evHeard, evStreamAudio, evReplyText, evStreamEnd, evRunEnd}
	if len(kinds) != len(want) {
		t.Fatalf("posted %v, want %v", kinds, want)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("posted %v, want %v", kinds, want)
		}
	}
	if evs[0].text != "what time is it" || evs[2].text != "It is noon. Anything else?" {
		t.Errorf("%+v", evs)
	}
	if got := binary.LittleEndian.Uint16(evs[1].audio); len(evs[1].audio) == 0 || got > 10 {
		t.Errorf("audio not brought to the voice rate: %v", evs[1].audio)
	}

	// Stopped: closed at once, and its time counted.
	_ = g.r.Stop()
	g.r.counting.Wait()
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if !closed || config.Get().Realtime.UsedSeconds < 1 {
		t.Fatalf("closed %v, used %d", closed, config.Get().Realtime.UsedSeconds)
	}

	// The next turn, a follow-up too, has a session of its own: nothing the last one read is in it.
	_ = g.r.Start("")
	if s2 := g.waitDial(t); s2 == s {
		t.Fatal("the same session")
	}
}

// A realtime follow-up keeps the session open (#487): resume reuses the open one for its generation,
// dialing nothing; a resume whose turn was stopped before it ran (the generation moved on) opens nothing,
// rather than dialing a session with no turn behind it.
func TestRealtimeFollowUpKeepsTheSession(t *testing.T) {
	g := newRTRig(t)
	_ = g.r.Start("")
	s := g.waitDial(t)
	waitUntilT(t, func() bool { g.r.mu.Lock(); defer g.r.mu.Unlock(); return g.r.turn != nil })

	// The follow-up reuses the open session: nothing new is dialed, and the same session is kept.
	gen := g.r.generation()
	if err := g.r.resume(gen); err != nil {
		t.Fatal(err)
	}
	select {
	case <-g.dialed:
		t.Fatal("a fresh session was dialed for a follow-up with one open")
	case <-time.After(100 * time.Millisecond):
	}
	g.r.mu.Lock()
	kept := g.r.turn != nil && g.r.turn.sess == s
	g.r.mu.Unlock()
	if !kept {
		t.Fatal("the open session was not kept for the follow-up")
	}

	// A resume for a turn stopped before it ran (a cancel, another wake, the cap or a failure moved the
	// generation on) opens nothing: no phantom session billed with no turn behind it.
	_ = g.r.Stop()
	g.r.counting.Wait()
	if err := g.r.resume(gen); err != nil {
		t.Fatal(err)
	}
	select {
	case <-g.dialed:
		t.Fatal("a phantom session was dialed for a stopped follow-up turn")
	case <-time.After(100 * time.Millisecond):
	}
}

// Calls made together are each answered, and the model told once to go on.
func TestRealtimeCallsTogether(t *testing.T) {
	g := newRTRig(t)
	_ = g.r.Start("")
	s := g.waitDial(t)
	s.events <- realtime.Event{Kind: realtime.Call, CallID: "a", Name: "HassTurnOn", Args: `{}`}
	s.events <- realtime.Event{Kind: realtime.Call, CallID: "b", Name: "HassTurnOn", Args: `{}`}
	s.events <- realtime.Event{Kind: realtime.Calls}
	waitUntilT(t, func() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.goOns == 1 })
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.results) != 2 {
		t.Fatalf("%v", s.results)
	}
}

func TestOutsideWords(t *testing.T) {
	for name, want := range map[string]bool{
		"HassTurnOn": false, "HassLightSet": false, "GetLiveContext": false, "GetDateTime": false,
		"HassGetWeather": true, "get_headlines": true, "read_inbox_summary": true, "calendar_get_events": true,
		"script_morning": true,
	} {
		if got := bringsOutsideWords(name); got != want {
			t.Errorf("%s: %v, want %v", name, got, want)
		}
	}
}

// A Home Assistant tool with the name of one of the device's own is left out, not offered twice.
func TestAToolsNameIsTakenOnce(t *testing.T) {
	g := newRTRig(t)
	clash := &clashMCP{}
	g.r.mcpFor = func(string) mcpClient { return clash }
	var fns []realtime.Function
	g.r.dial = func(ctx context.Context, c realtime.Config) (rtSession, error) {
		fns = c.Functions
		g.dialed <- struct{}{}
		return newFakeSession(), nil
	}
	_ = g.r.Start("")
	<-g.dialed
	names := map[string]int{}
	for _, f := range fns {
		names[f.Name]++
	}
	if names["set_timer"] != 1 || names["HassTurnOn"] != 1 || len(fns) != 3 {
		t.Fatalf("%v", names)
	}
}

type clashMCP struct{ fakeMCP }

func (m *clashMCP) Tools(context.Context) ([]mcp.Tool, error) {
	return []mcp.Tool{{Name: "set_timer"}, {Name: "HassTurnOn"}}, nil
}

// Audio after the reply's stream has ended is dropped, not sent into a closed queue.
func TestAStreamAfterItsEnd(t *testing.T) {
	st := newStream(0, 0, 0)
	st.done()
	st.send([]byte{1, 2})
	st.done()
}

// The model's calls go through the lockout: the device's own through the gate, Home Assistant's through
// its MCP server while the gate allows; after outside words, only what looks things up.
func TestRealtimeCallsAreHeldToTheLockout(t *testing.T) {
	g := newRTRig(t)
	_ = g.r.Start("")
	s := g.waitDial(t)
	call := func(name string) {
		s.events <- realtime.Event{Kind: realtime.Call, CallID: "c", Name: name, Args: `{}`}
	}
	call("set_timer")
	call("HassTurnOn")
	call("calendar_get_events") // outside words: the lockout holds from the next call on
	call("HassTurnOn")
	call("GetLiveContext")
	waitUntilT(t, func() bool { s.mu.Lock(); defer s.mu.Unlock(); return len(s.results) == 5 })
	s.mu.Lock()
	results := append([]string(nil), s.results...)
	s.mu.Unlock()
	want := []string{"set_timer=ran set_timer", "HassTurnOn=done HassTurnOn", "calendar_get_events=done calendar_get_events",
		"HassTurnOn=refused", "GetLiveContext=done GetLiveContext"}
	for i := range want {
		if results[i] != want[i] {
			t.Fatalf("%v, want %v", results, want)
		}
	}
	g.mcp.mu.Lock()
	defer g.mcp.mu.Unlock()
	if len(g.mcp.calls) != 3 {
		t.Fatalf("Home Assistant was called %v", g.mcp.calls)
	}
}

// The day's minutes: none left, not ready; a few left, the turn ends when they run out.
func TestRealtimeDailyCap(t *testing.T) {
	g := newRTRig(t)
	now := g.r.now()
	daily := config.Get().Realtime.Daily() * 60
	_ = config.Set().Realtime().Used(daily, now)
	if g.r.Ready() {
		t.Fatal("ready with no minutes left")
	}
	config.Use(filepath.Join(t.TempDir(), "fresh.json"))
	_ = config.Set().Realtime().Set("openai", "", "", "", 0)
	_ = config.Set().Realtime().SetKey("sk-test")
	_ = config.Set().Realtime().SetMCPToken("ha-token")
	_ = config.Set().Realtime().Used(daily-1, now)
	_ = g.r.Start("")
	s := g.waitDial(t)
	g.waitPosted(t, evError)
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		t.Fatal("the session went on past the day's minutes")
	}
}

func TestARealtimeFailureIsSaid(t *testing.T) {
	g := newRTRig(t)
	g.r.dial = func(context.Context, realtime.Config) (rtSession, error) { return nil, errors.New("401") }
	_ = g.r.Start("")
	evs := g.waitPosted(t, evError)
	if evs[len(evs)-1].code != "realtime-failed" {
		t.Fatalf("%+v", evs)
	}
}

func TestASlotChosenForRealtime(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	c := &conversation{direct: newDirect(func(event) {}), realtime: newRealtime(func(event) {})}
	if _, rt := c.backendFor(0).(*rtBackend); rt {
		t.Fatal("realtime with no slot chosen for it")
	}
	_ = config.Set().Wake(0).Realtime(true)
	be := c.backendFor(0)
	if _, rt := be.(*rtBackend); !rt {
		t.Fatalf("%T", be)
	}
	c.be = be
	if !c.stopsAtOnce() || be.Ready() {
		t.Fatal("realtime with nothing set up is ready, or does not stop at once")
	}
	if _, rt := c.backendFor(1).(*rtBackend); rt {
		t.Fatal("another slot answered by realtime")
	}
}

func waitUntilT(t *testing.T, ok func() bool) {
	t.Helper()
	for range 500 {
		if ok() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("not in time")
}

// stuckSession is a session whose writes wait on a network that has stopped, until it is closed.
type stuckSession struct {
	*fakeSession
	stuck chan struct{}
	once  sync.Once
}

func (s *stuckSession) Audio([]int16) error {
	<-s.stuck
	return errors.New("closed")
}

func (s *stuckSession) Close() error {
	s.once.Do(func() { close(s.stuck) })
	return s.fakeSession.Close()
}

// A write waiting on the network does not hold Stop up: the conversation calls it, and the stop word and
// a ring wait on it.
func TestStopDoesNotWaitOnAWrite(t *testing.T) {
	g := newRTRig(t)
	stuck := &stuckSession{fakeSession: newFakeSession(), stuck: make(chan struct{})}
	g.r.dial = func(context.Context, realtime.Config) (rtSession, error) {
		g.dialed <- struct{}{}
		return stuck, nil
	}
	_ = g.r.Start("")
	<-g.dialed
	waitUntilT(t, func() bool { g.r.mu.Lock(); defer g.r.mu.Unlock(); return g.r.turn != nil })
	writing := make(chan struct{})
	go func() { close(writing); _ = g.r.Audio(frame(160)) }()
	<-writing
	time.Sleep(20 * time.Millisecond) // the write is waiting now
	start := time.Now()
	_ = g.r.Stop()
	if d := time.Since(start); d > time.Second {
		t.Fatalf("Stop waited %v on a stuck write", d)
	}
}

// Nothing heard: the turn ends as one that heard nothing.
func TestRealtimeHeardNothing(t *testing.T) {
	g := newRTRig(t)
	_ = g.r.Start("")
	s := g.waitDial(t)
	s.events <- realtime.Event{Kind: realtime.Empty}
	evs := g.waitPosted(t, evRunEnd)
	if len(evs) != 2 || evs[0].kind != evHeard || evs[0].text != "" {
		t.Fatalf("%+v", evs)
	}
}

// countingMCP lists its tools as it is told to, counting the listings, and can be slow.
type countingMCP struct {
	fakeMCP
	mu    sync.Mutex
	lists int
	delay time.Duration
	tools []mcp.Tool
}

func (m *countingMCP) Tools(context.Context) ([]mcp.Tool, error) {
	time.Sleep(m.delay)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lists++
	return append([]mcp.Tool(nil), m.tools...), nil
}

func (m *countingMCP) count() int { m.mu.Lock(); defer m.mu.Unlock(); return m.lists }

// Home Assistant's tools are kept: listed once, asked again in the background when stale, and listed anew
// for another token; a slow first listing is waited for haFirst at most.
func TestHomeAssistantToolsAreKept(t *testing.T) {
	g := newRTRig(t)
	m := &countingMCP{tools: []mcp.Tool{{Name: "HassTurnOn"}}}
	g.r.mcpFor = func(string) mcpClient { return m }
	now := time.Unix(1_000_000, 0)
	var clock sync.Mutex
	g.r.now = func() time.Time { clock.Lock(); defer clock.Unlock(); return now }
	advance := func(d time.Duration) { clock.Lock(); now = now.Add(d); clock.Unlock() }

	if ts := g.r.haTools("a"); len(ts) != 1 || m.count() != 1 {
		t.Fatalf("%v, %d listings", ts, m.count())
	}
	if g.r.haTools("a"); m.count() != 1 {
		t.Fatal("listed again while fresh")
	}
	// Stale: the kept list now, a new one in the background.
	m.mu.Lock()
	m.tools = append(m.tools, mcp.Tool{Name: "HassTurnOff"})
	m.mu.Unlock()
	advance(haStale + time.Second)
	if ts := g.r.haTools("a"); len(ts) != 1 {
		t.Fatalf("a stale list was not used while the new one came: %v", ts)
	}
	waitUntilT(t, func() bool { return m.count() == 2 })
	waitUntilT(t, func() bool { return len(g.r.haTools("a")) == 2 })

	// Another token: listed for it, not the other's list.
	if ts := g.r.haTools("b"); len(ts) != 2 || m.count() != 3 {
		t.Fatalf("%v, %d listings", ts, m.count())
	}

	// A slow first listing: the turn goes on without it after haFirst.
	slow := &countingMCP{delay: 300 * time.Millisecond, tools: []mcp.Tool{{Name: "HassTurnOn"}}}
	g.r.mcpFor = func(string) mcpClient { return slow }
	was := haFirst
	haFirst = 50 * time.Millisecond
	defer func() { haFirst = was }()
	start := time.Now()
	if ts := g.r.haTools("c"); len(ts) != 0 || time.Since(start) > 250*time.Millisecond {
		t.Fatalf("%v after %v", ts, time.Since(start))
	}
	waitUntilT(t, func() bool { return len(g.r.haTools("c")) == 1 })
}

// A start queued for a turn that was stopped before it ran opens nothing.
func TestAStartForAStoppedTurnOpensNothing(t *testing.T) {
	g := newRTRig(t)
	gen := g.r.generation()
	_ = g.r.Stop() // the turn ended, its start still queued
	_ = g.r.startFrom(gen)
	select {
	case <-g.dialed:
		t.Fatal("a session was opened for a turn already over")
	case <-time.After(100 * time.Millisecond):
	}
	// The next turn's start opens one.
	_ = g.r.startFrom(g.r.generation())
	g.waitDial(t)
}
