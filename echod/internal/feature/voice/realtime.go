package voice

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
	"github.com/HuskerMinion/techo5/echod/internal/lib/llm"
	"github.com/HuskerMinion/techo5/echod/internal/lib/mcp"
	"github.com/HuskerMinion/techo5/echod/internal/lib/realtime"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
)

// Realtime voice: a slot chosen for it (config.WakeWord.Realtime) is answered by a speech-to-speech model
// at OpenAI, xAI or Google (lib/realtime), the voice going up as it is heard and the answer coming back
// as speech while it is made. It is a backend like the others, so everything that stops or holds a turn
// (the microphones taken, a call, a ring, the stop word, another wake) stops it, and its speech plays
// through the reply, under the same claim on the speaker, so whatever outranks a reply cuts it off.
//
// Every turn has a session of its own, opened at its start and closed at its end: nothing a turn read is
// in the next one's session, and the next turn's lockout starts clean because its session does.
//
// The model may call the device's own tools and, with a token for it, Home Assistant's (its MCP server),
// every call under the same lockout as the assistant's own (feature/assistant's TurnGate): once a turn
// has read outside words, nothing that changes anything runs in it. A Home Assistant tool changes
// something unless it is known to only look things up, and brings outside words in unless it is one of
// Home Assistant's own intents that do not (bringsOutsideWords).
//
// Every session is held to a turn's length (realtimeTurn) and the day's minutes (config.Realtime), which
// the provider bills by.

// ToolGate is one turn's lockout, as feature/assistant makes it (SetRealtime).
type ToolGate interface {
	Tools() []llm.Tool
	Run(call llm.ToolCall) string
	Allow(readOnly bool) (ok bool, refused string)
	Saw(outside bool)
	EndRound()
}

var (
	newGate      func() ToolGate
	instructions = func() string { return "You are a voice assistant on a smart speaker. Answer briefly." }
)

// SetRealtime is feature/assistant handing realtime voice its lockout and its instructions, as SetThink
// hands the direct pipeline its thinking: it imports this package, so this one cannot import it.
func SetRealtime(gate func() ToolGate, instr func() string) {
	newGate, instructions = gate, instr
}

const (
	// realtimeTurn is the longest one turn's session may run, whatever happens in it.
	realtimeTurn = 5 * time.Minute
	// realtimePreroll is the most speech kept while the session opens: what is said meanwhile is sent once
	// it is open.
	realtimePreroll = 16000 * 15
	// mcpWithin is how long Home Assistant has to answer a tool call.
	mcpWithin = 15 * time.Second

	// What a Home Assistant tool may bring to the provider: its description, its parameters (as JSON),
	// and what a call answers; past these a description and an answer are cut, a tool is left out.
	maxToolDescription = 1 << 10
	maxToolSchema      = 4 << 10
	maxToolResult      = 32 << 10

	// Home Assistant's tool list is kept, so a turn does not wait on it: asked again in the background
	// once it is haStale, and waited for only on the first turn, haFirst at most.
	haStale = 2 * time.Minute
)

// haFirst is how long the first turn waits for Home Assistant's tools; a variable for the tests.
var haFirst = 3 * time.Second

// readOnlyTools are Home Assistant's tools known to only look things up.
var readOnlyTools = map[string]bool{"GetLiveContext": true, "GetDateTime": true}

// outsideWords is part of a tool's name that says it brings outside words into a turn, whatever else is
// true of it.
var outsideWords = []string{"calendar", "todo", "search", "web", "news", "feed", "mail", "message", "weather"}

type rtBackend struct {
	post func(event)

	// dial opens a session; mcpFor is Home Assistant's tools, or nil; both replaced in tests.
	dial   func(ctx context.Context, c realtime.Config) (rtSession, error)
	mcpFor func(token string) mcpClient
	now    func() time.Time

	// ha is Home Assistant's tools as last listed, for the token they were listed with.
	haMu      sync.Mutex
	haList    []mcp.Tool
	haToken   string
	haAt      time.Time
	haBusy    bool
	haUpdated chan struct{} // closed when a listing under way is done

	mu      sync.Mutex
	turn    *rtTurn // the open turn, or none
	gen     int     // which turn's events are wanted
	preroll []int16
	ended   bool // the speaker finished before the session was open

	// counting is the turns' minutes being counted (closeLocked), off the conversation's goroutine.
	counting sync.WaitGroup
}

// rtTurn is one turn's session and what goes with it, for as long as it is open.
//
// What goes to the session is sent under send, never under the backend's mu: a write can wait on the
// network, and Stop, which the conversation calls on its own goroutine, needs only mu, and closes the
// session, which ends any write waiting.
type rtTurn struct {
	gen    int
	send   sync.Mutex
	sess   rtSession
	at     time.Time // when the session opened, for the day's count
	gate   ToolGate  // this turn's lockout, never swapped
	tools  map[string]mcp.Tool
	token  string // Home Assistant's, for its tools' calls
	cap    *time.Timer
	saying strings.Builder
	spoke  bool // audio of the answer has gone to the reply
}

type rtSession interface {
	Audio([]int16) error
	EndOfSpeech() error
	ToolResult(callID, name, output string) error
	Continue() error
	Events() <-chan realtime.Event
	Close() error
}

type mcpClient interface {
	Start(ctx context.Context) error
	Tools(ctx context.Context) ([]mcp.Tool, error)
	Call(ctx context.Context, name string, args map[string]any) (string, bool, error)
}

func newRealtime(post func(event)) *rtBackend {
	return &rtBackend{
		post: post,
		dial: func(ctx context.Context, c realtime.Config) (rtSession, error) { return realtime.Dial(ctx, c) },
		mcpFor: func(token string) mcpClient {
			addr := hass.Get().Address()
			if token == "" || addr == "" {
				return nil
			}
			return mcp.New(addr+"/api/mcp", token)
		},
		now: time.Now,
	}
}

func (r *rtBackend) Name() string { return "realtime" }

// Ready is whether realtime voice is set up, and has minutes left today.
func (r *rtBackend) Ready() bool {
	rc := config.Get().Realtime
	return rc.Ready() && rc.SecondsLeft(r.now()) > 0
}

// Start opens a turn: a session of its own, opened meanwhile, with what is said until it is open kept to
// send then.
func (r *rtBackend) Start(string) error { return r.startFrom(-1) }

// generation is the turn now, for startFrom.
func (r *rtBackend) generation() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.gen
}

// startFrom is Start for a turn opened at generation gen (any, for less than none): the conversation
// queues the start while it stops a turn at once (Stop), and a turn stopped before its start ran opens
// nothing, where it would hold a session open, its minutes counted, until the turn's time ran out.
func (r *rtBackend) startFrom(gen int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if gen >= 0 && gen != r.gen {
		slog.Debug("realtime: a start for a turn already over, not opened")
		return nil
	}
	r.closeLocked()
	r.gen++
	r.preroll, r.ended = r.preroll[:0], false
	opening := r.gen
	safe.Go("realtime open", func() { r.open(opening) })
	return nil
}

// open dials turn gen's session, gathering its tools first, and holds it to the turn's time.
func (r *rtBackend) open(gen int) {
	rc := config.Get().Realtime
	ctx := context.Background()
	var gate ToolGate
	if newGate != nil {
		gate = newGate()
	}
	t := &rtTurn{gen: gen, gate: gate, tools: map[string]mcp.Tool{}, token: rc.MCPToken}

	var fns []realtime.Function
	seen := map[string]bool{}
	if gate != nil {
		for _, d := range gate.Tools() {
			seen[d.Name] = true
			fns = append(fns, realtime.Function{Name: d.Name, Description: d.Description, Parameters: d.Parameters})
		}
	}
	for _, ht := range r.haTools(rc.MCPToken) {
		// A name the device's own tools or another of Home Assistant's has is left out: two of a
		// name could not be told apart, and a provider refuses a session with both.
		if seen[ht.Name] {
			slog.Info("realtime: a Home Assistant tool left out: its name is taken", "name", ht.Name)
			continue
		}
		if b, _ := json.Marshal(ht.InputSchema); len(b) > maxToolSchema {
			slog.Info("realtime: a Home Assistant tool left out: its parameters are too long", "name", ht.Name)
			continue
		}
		seen[ht.Name] = true
		t.tools[ht.Name] = ht
		fns = append(fns, realtime.Function{Name: ht.Name, Description: clipText(ht.Description, maxToolDescription),
			Parameters: ht.InputSchema})
	}

	sess, err := r.dial(ctx, realtime.Config{
		Provider: rc.Provider, Key: rc.Key, Endpoint: rc.Endpoint, Model: rc.Model, Voice: rc.Voice,
		Instructions: instructions(), Functions: fns,
	})
	r.mu.Lock()
	if r.gen != gen {
		r.mu.Unlock()
		if sess != nil {
			_ = sess.Close()
		}
		return
	}
	if err != nil {
		r.mu.Unlock()
		slog.Warn("realtime: the session could not be opened", "provider", rc.Provider, "err", err)
		r.post(event{kind: evError, code: "realtime-failed", msg: err.Error()})
		return
	}
	t.sess, t.at = sess, r.now()
	limit := min(realtimeTurn, time.Duration(rc.SecondsLeft(t.at))*time.Second)
	t.cap = time.AfterFunc(limit, func() {
		r.mu.Lock()
		stale := r.gen != gen
		r.mu.Unlock()
		if stale {
			return
		}
		slog.Warn("realtime: the turn ran past its time, or the day's minutes", "limit", limit.Round(time.Second))
		_ = r.Stop()
		r.post(event{kind: evError, code: "realtime-limit", msg: "realtime voice reached its time limit"})
	})
	// What was said while it opened goes first, and the end of speech after it, before anything sent from
	// now on: the turn is published with its send lock held, so a frame arriving meanwhile waits behind
	// them, and the lock that Stop needs is let go before anything is written.
	pre, ended := r.preroll, r.ended
	r.preroll, r.ended = nil, false
	t.send.Lock()
	r.turn = t
	r.mu.Unlock()
	if len(pre) > 0 {
		_ = sess.Audio(pre)
	}
	if ended {
		_ = sess.EndOfSpeech()
	}
	t.send.Unlock()
	slog.Info("realtime: session open", "provider", rc.Provider, "functions", len(fns))
	safe.Go("realtime events", func() { r.pump(t) })
}

// sending is the open turn, to send to under its send lock, or nil, the frame kept, while none is open.
func (r *rtBackend) sending(keep func()) *rtTurn {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.turn == nil {
		keep()
		return nil
	}
	return r.turn
}

// Audio sends a frame of the microphone, or keeps it while the session opens.
func (r *rtBackend) Audio(frame []byte) error {
	pcm := make([]int16, len(frame)/2)
	for i := range pcm {
		pcm[i] = int16(binary.LittleEndian.Uint16(frame[2*i:]))
	}
	t := r.sending(func() {
		if len(r.preroll)+len(pcm) <= realtimePreroll {
			r.preroll = append(r.preroll, pcm...)
		}
	})
	if t == nil {
		return nil
	}
	t.send.Lock()
	defer t.send.Unlock()
	return t.sess.Audio(pcm)
}

// End says the speaker has finished: the answer is asked for.
func (r *rtBackend) End() error {
	t := r.sending(func() { r.ended = true })
	if t == nil {
		return nil
	}
	t.send.Lock()
	defer t.send.Unlock()
	return t.sess.EndOfSpeech()
}

// Stop ends the turn's session at once, and what it was saying with it. Safe from any goroutine.
func (r *rtBackend) Stop() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.gen++
	r.closeLocked()
	return nil
}

// closeLocked closes the turn's session and counts its minutes, with r.mu held.
func (r *rtBackend) closeLocked() {
	t := r.turn
	r.turn = nil
	if t == nil {
		return
	}
	if t.cap != nil {
		t.cap.Stop()
	}
	_ = t.sess.Close()
	now := r.now()
	secs := int(now.Sub(t.at).Round(time.Second) / time.Second)
	// Written on a goroutine of its own: the file is written and synced, and this runs on the
	// conversation's goroutine. Each count adds to the day's as it stands, whichever is written first.
	r.counting.Add(1)
	safe.Go("realtime minutes", func() {
		defer r.counting.Done()
		if err := config.Set().Realtime().Used(max(secs, 1), now); err != nil {
			slog.Warn("realtime: the day's minutes could not be counted", "err", err)
		}
	})
	slog.Info("realtime: session closed", "seconds", secs)
}

// current is whether t is still the open turn.
func (r *rtBackend) current(t *rtTurn) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.turn == t && r.gen == t.gen
}

// pump turns the turn's session's events into the conversation's, until it closes.
func (r *rtBackend) pump(t *rtTurn) {
	for e := range t.sess.Events() {
		if !r.current(t) {
			return
		}
		switch e.Kind {
		case realtime.Heard:
			slog.Info("realtime: heard", "text", e.Text)
			r.postFor(t, event{kind: evHeard, text: strings.TrimSpace(e.Text)})
		case realtime.Saying:
			r.mu.Lock()
			t.saying.WriteString(e.Text)
			r.mu.Unlock()
		case realtime.Said:
			r.mu.Lock()
			t.saying.Reset()
			t.saying.WriteString(e.Text)
			r.mu.Unlock()
		case realtime.Audio:
			pcm := media.ToVoiceRate(e.PCM, e.Rate)
			out := make([]byte, 2*len(pcm))
			for i, s := range pcm {
				binary.LittleEndian.PutUint16(out[2*i:], uint16(s))
			}
			r.mu.Lock()
			t.spoke = true
			r.mu.Unlock()
			r.postFor(t, event{kind: evStreamAudio, audio: out})
		case realtime.Call:
			out := r.call(t, e.Name, e.Args)
			t.send.Lock()
			_ = t.sess.ToolResult(e.CallID, e.Name, out)
			t.send.Unlock()
		case realtime.Calls:
			// Every call of the response is answered (each was, as it came): one request to go on.
			t.send.Lock()
			_ = t.sess.Continue()
			t.send.Unlock()
		case realtime.Empty:
			// Nothing was heard: the turn ends as any turn that heard nothing does.
			r.postFor(t, event{kind: evHeard, text: ""})
			r.postFor(t, event{kind: evRunEnd})
		case realtime.Done, realtime.Interrupt:
			r.finish(t)
		case realtime.Failed:
			slog.Warn("realtime: the session failed", "err", e.Err)
			if !r.current(t) {
				return
			}
			_ = r.Stop()
			r.post(event{kind: evError, code: "realtime-failed", msg: errorText(e.Err)})
			return
		}
	}
}

// finish is an answer done: its words, and its end. The conversation ends the turn, and the session with
// it.
func (r *rtBackend) finish(t *rtTurn) {
	r.mu.Lock()
	said := strings.TrimSpace(t.saying.String())
	t.saying.Reset()
	spoke := t.spoke
	t.spoke = false
	r.mu.Unlock()

	// No listening after it, even for a question: the next turn is a session of its own, which would not
	// know what the question was.
	if said != "" {
		slog.Info("realtime: replying", "text", said)
		r.postFor(t, event{kind: evReplyText, text: said, spoken: spoke})
	}
	if spoke {
		r.postFor(t, event{kind: evStreamEnd})
	}
	r.postFor(t, event{kind: evRunEnd})
}

// postFor posts a turn's event, unless another turn has taken its place. The lock is not held across the
// post, which can wait for room: the conversation, which empties the queue, also stops turns.
func (r *rtBackend) postFor(t *rtTurn, e event) {
	if r.current(t) {
		r.post(e)
	}
}

// haTools is Home Assistant's tools for a token: as last listed, asking again in the background once that
// is haStale; listed now, haFirst at most, the first time.
func (r *rtBackend) haTools(token string) []mcp.Tool {
	if token == "" || r.mcpFor(token) == nil {
		return nil
	}
	r.haMu.Lock()
	have := r.haToken == token && !r.haAt.IsZero()
	list, stale := r.haList, r.now().Sub(r.haAt) > haStale
	wait := r.refreshHA(token, !have || stale)
	r.haMu.Unlock()
	if have {
		return list
	}
	select {
	case <-wait:
	case <-time.After(haFirst):
		slog.Info("realtime: Home Assistant's tools not listed in time; this turn has the device's own")
	}
	r.haMu.Lock()
	defer r.haMu.Unlock()
	if r.haToken == token {
		return r.haList
	}
	return nil
}

// refreshHA lists Home Assistant's tools in the background, when asked and none is under way; with
// r.haMu held. It answers what closes when the listing under way is done.
func (r *rtBackend) refreshHA(token string, ask bool) <-chan struct{} {
	if r.haBusy || !ask {
		if r.haUpdated == nil {
			done := make(chan struct{})
			close(done)
			return done
		}
		return r.haUpdated
	}
	r.haBusy = true
	done := make(chan struct{})
	r.haUpdated = done
	safe.Go("realtime home assistant tools", func() {
		defer close(done)
		c := r.mcpFor(token)
		if c == nil {
			// Home Assistant's address went meanwhile: no tools, and asked again next time.
			r.haMu.Lock()
			r.haBusy = false
			r.haMu.Unlock()
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), mcpWithin)
		defer cancel()
		var ts []mcp.Tool
		err := c.Start(ctx)
		if err == nil {
			ts, err = c.Tools(ctx)
		}
		r.haMu.Lock()
		defer r.haMu.Unlock()
		r.haBusy = false
		if err != nil {
			slog.Warn("realtime: Home Assistant's tools could not be listed", "err", err)
			return
		}
		r.haList, r.haToken, r.haAt = ts, token, r.now()
	})
	return done
}

// call runs a function the model called, under the turn's lockout: the device's own through the gate,
// Home Assistant's through its MCP server once the gate allows it.
func (r *rtBackend) call(t *rtTurn, name, args string) string {
	if t.gate == nil {
		return "error: no tools are available"
	}
	defer t.gate.EndRound() // the model asks for one call at a time: each is a round

	ht, fromHA := t.tools[name]
	if !fromHA {
		var tc llm.ToolCall
		tc.Function.Name, tc.Function.Arguments = name, args
		out := t.gate.Run(tc)
		slog.Info("realtime: tool", "name", name, "args", args, "result", out)
		return out
	}
	readOnly := ht.ReadOnly() || readOnlyTools[name]
	if ok, refused := t.gate.Allow(readOnly); !ok {
		slog.Warn("realtime: a Home Assistant tool refused after reading outside words", "name", name)
		return refused
	}
	var a map[string]any
	if strings.TrimSpace(args) != "" {
		if err := json.Unmarshal([]byte(args), &a); err != nil {
			return "error: the arguments were not JSON"
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), mcpWithin)
	defer cancel()
	c := r.mcpFor(t.token)
	if c == nil {
		return "error: Home Assistant is not reachable"
	}
	if err := c.Start(ctx); err != nil {
		return "error: " + err.Error()
	}
	out, failed, err := c.Call(ctx, name, a)
	t.gate.Saw(bringsOutsideWords(name))
	// The answer can be the whole house, named: its length is logged, not the answer.
	slog.Info("realtime: Home Assistant tool", "name", name, "args", args, "result_bytes", len(out), "failed", failed)
	if err != nil {
		return "error: " + err.Error()
	}
	out = clipText(out, maxToolResult)
	if failed {
		return "error: " + out
	}
	return out
}

// bringsOutsideWords is whether a Home Assistant tool can bring outside words into a turn: any that is not
// one of Home Assistant's own intents (Hass..., which act on the house and say what they did), or that
// looks up what the house knows (the date, the house's own state), is taken to; and any whose name says
// it reads something from outside is, whatever it is.
func bringsOutsideWords(name string) bool {
	n := strings.ToLower(name)
	for _, w := range outsideWords {
		if strings.Contains(n, w) {
			return true
		}
	}
	if readOnlyTools[name] {
		return false
	}
	return !strings.HasPrefix(name, "Hass")
}

// clipText is s cut to at most n bytes, at a character's start.
func clipText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

func errorText(err error) string {
	if err == nil {
		return "the session ended"
	}
	return err.Error()
}
