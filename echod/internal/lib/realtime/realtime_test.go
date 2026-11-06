package realtime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// The tests' servers are plain ws:// ones.
func TestMain(m *testing.M) {
	Insecure = true
	os.Exit(m.Run())
}

// fakeServer is a provider: it records what it is sent and its request's header, and answers each message
// with what script says.
type fakeServer struct {
	mu     sync.Mutex
	header http.Header
	query  string
	got    []map[string]any
	script func(m map[string]any) []string
}

func (f *fakeServer) start(t *testing.T) string {
	t.Helper()
	up := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.header, f.query = r.Header.Clone(), r.URL.RawQuery
		f.mu.Unlock()
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		for {
			_, msg, err := c.ReadMessage()
			if err != nil {
				return
			}
			var m map[string]any
			_ = json.Unmarshal(msg, &m)
			f.mu.Lock()
			f.got = append(f.got, m)
			f.mu.Unlock()
			for _, out := range f.script(m) {
				if c.WriteMessage(websocket.TextMessage, []byte(out)) != nil {
					return
				}
			}
		}
	}))
	t.Cleanup(srv.Close)
	return "ws" + strings.TrimPrefix(srv.URL, "http")
}

func (f *fakeServer) sent() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]map[string]any(nil), f.got...)
}

func collect(t *testing.T, s *Session, until string) []Event {
	t.Helper()
	var out []Event
	deadline := time.After(5 * time.Second)
	for {
		select {
		case e, ok := <-s.Events():
			if !ok {
				return out
			}
			out = append(out, e)
			if e.Kind == until {
				return out
			}
		case <-deadline:
			t.Fatalf("no %s; had %+v", until, out)
		}
	}
}

func kinds(evs []Event) string {
	var k []string
	for _, e := range evs {
		k = append(k, e.Kind)
	}
	return strings.Join(k, ",")
}

func TestOpenAITurn(t *testing.T) {
	answer := pcmBase64([]int16{100, -100, 200})
	// Asked for the answer, the model calls a function first; after its result, it speaks.
	f := &fakeServer{}
	calls := 0
	f.script = func(m map[string]any) []string {
		if m["type"] != "response.create" {
			return nil
		}
		calls++
		if calls == 1 {
			return []string{
				`{"type":"conversation.item.input_audio_transcription.completed","transcript":"turn on the kitchen light"}`,
				`{"type":"response.output_item.done","item":{"type":"function_call","call_id":"c1","name":"HassTurnOn","arguments":"{\"name\":\"kitchen\"}"}}`,
				`{"type":"response.output_item.done","item":{"type":"function_call","call_id":"c2","name":"HassTurnOn","arguments":"{\"name\":\"hall\"}"}}`,
				`{"type":"response.done","response":{"status":"completed","output":[{"type":"function_call"},{"type":"function_call"}]}}`,
			}
		}
		return []string{
			`{"type":"response.output_audio.delta","delta":"` + answer + `"}`,
			`{"type":"response.audio_transcript.done","transcript":"Done."}`,
			`{"type":"response.done","response":{"status":"completed","output":[{"type":"message"}]}}`,
		}
	}
	addr := f.start(t)
	s, err := Dial(context.Background(), Config{Provider: OpenAI, Key: "sk-test", Endpoint: addr, Model: "m1",
		Instructions: "be brief", Functions: []Function{{Name: "HassTurnOn", Description: "turns on"}}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Audio(make([]int16, 320)); err != nil {
		t.Fatal(err)
	}
	if err := s.EndOfSpeech(); err != nil {
		t.Fatal(err)
	}
	evs := collect(t, s, Calls)
	if kinds(evs) != "heard,call,call,calls" || evs[0].Text != "turn on the kitchen light" || evs[1].Name != "HassTurnOn" ||
		evs[1].CallID != "c1" || evs[1].Args != `{"name":"kitchen"}` || evs[2].CallID != "c2" {
		t.Fatalf("%+v", evs)
	}
	// Both answered, then one request to go on: a second while one is going would be refused.
	_ = s.ToolResult("c1", "HassTurnOn", "ok")
	_ = s.ToolResult("c2", "HassTurnOn", "ok")
	if n := countType(f.sent(), "response.create"); n != 1 {
		t.Fatalf("%d responses asked for before the calls were answered", n)
	}
	if err := s.Continue(); err != nil {
		t.Fatal(err)
	}
	evs = collect(t, s, Done)
	if kinds(evs) != "audio,said,done" || len(evs[0].PCM) != 3 || evs[0].Rate != 24000 || evs[1].Text != "Done." {
		t.Fatalf("%+v", evs)
	}

	got := f.sent()
	if got[0]["type"] != "session.update" || !strings.Contains(fmtJSON(got[0]), `"turn_detection":null`) ||
		!strings.Contains(fmtJSON(got[0]), `"name":"HassTurnOn"`) {
		t.Errorf("setup %s", fmtJSON(got[0]))
	}
	// Speech went up at 24 kHz: 320 samples at 16 kHz are 480.
	if a := got[1]; a["type"] != "input_audio_buffer.append" || len(pcmFrom(a["audio"].(string))) != 480 {
		t.Errorf("audio %v", a["type"])
	}
	if f.query != "model=m1" {
		t.Errorf("query %q", f.query)
	}
	// A ws:// endpoint gets no key: only a secure one, or the provider's own address, does.
	if h := f.header.Get("Authorization"); h != "" {
		t.Errorf("the key went to a ws:// endpoint: %q", h)
	}
}

func TestGeminiTurn(t *testing.T) {
	answer := pcmBase64([]int16{1, 2, 3, 4})
	f := &fakeServer{}
	f.script = func(m map[string]any) []string {
		if _, ok := m["setup"]; ok {
			return []string{`{"setupComplete":{}}`}
		}
		if ri, ok := m["realtimeInput"].(map[string]any); ok {
			if _, end := ri["activityEnd"]; end {
				return []string{
					`{"serverContent":{"inputTranscription":{"text":"what time is it"}}}`,
					`{"toolCall":{"functionCalls":[{"id":"f1","name":"get_time","args":{"zone":"local"}}]}}`,
				}
			}
		}
		if _, ok := m["toolResponse"]; ok {
			return []string{
				`{"serverContent":{"modelTurn":{"parts":[{"inlineData":{"mimeType":"audio/pcm;rate=24000","data":"` + answer + `"}}]}}}`,
				`{"serverContent":{"outputTranscription":{"text":"It is noon."}}}`,
				`{"serverContent":{"turnComplete":true}}`,
			}
		}
		return nil
	}
	addr := f.start(t)
	s, err := Dial(context.Background(), Config{Provider: Gemini, Key: "g-key", Endpoint: addr,
		Functions: []Function{{Name: "get_time", Description: "the time"}}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_ = s.Audio(make([]int16, 160))
	_ = s.EndOfSpeech()
	evs := collect(t, s, Call)
	if kinds(evs) != "call" || evs[0].Args != `{"zone":"local"}` {
		t.Fatalf("%+v", evs)
	}
	_ = s.ToolResult("f1", "get_time", "12:00")
	evs = collect(t, s, Done)
	// What was heard is said once, at the end of the turn.
	if kinds(evs) != "audio,saying,heard,done" || len(evs[0].PCM) != 4 || evs[1].Text != "It is noon." ||
		evs[2].Text != "what time is it" {
		t.Fatalf("%+v", evs)
	}
	got := f.sent()
	setup, _ := got[0]["setup"].(map[string]any)
	if setup == nil || !strings.Contains(fmtJSON(setup), `"disabled":true`) || !strings.HasPrefix(setup["model"].(string), "models/") {
		t.Errorf("setup %s", fmtJSON(got[0]))
	}
	if _, ok := got[1]["realtimeInput"].(map[string]any)["activityStart"]; !ok {
		t.Errorf("the turn was not started after the setup: %s", fmtJSON(got[1]))
	}
	if f.header.Get("x-goog-api-key") != "" {
		t.Error("the key went to a ws:// endpoint")
	}
}

func TestAddresses(t *testing.T) {
	if _, _, err := (openAI{base: "wss://api.openai.com/v1/realtime"}).address(Config{Provider: OpenAI}); err != ErrNoKey {
		t.Errorf("no key: %v", err)
	}
	addr, h, err := (openAI{base: "wss://api.openai.com/v1/realtime", defaultModel: "gpt-realtime"}).address(Config{Key: "k"})
	if err != nil || addr != "wss://api.openai.com/v1/realtime?model=gpt-realtime" || h.Get("Authorization") != "Bearer k" {
		t.Errorf("%q %v %v", addr, h, err)
	}
	if _, h, _ := (openAI{base: "x"}).address(Config{Key: "k", Endpoint: "wss://relay.home.arpa/rt"}); h.Get("Authorization") != "Bearer k" {
		t.Error("a wss:// endpoint given with a key did not get it")
	}
	if _, _, err := (&gemini{}).address(Config{Key: "k", Endpoint: "http://relay"}); err == nil {
		t.Error("an endpoint that is not ws took")
	}
	if _, err := Dial(context.Background(), Config{Provider: "nobody"}); err == nil {
		t.Error("a provider that is not one")
	}
}

func TestGeminiTurnCompleteAfterToolCallWaitsForSpokenAnswer(t *testing.T) {
	answer := pcmBase64([]int16{10, 20, 30, 40})
	f := &fakeServer{}
	f.script = func(m map[string]any) []string {
		if _, ok := m["setup"]; ok {
			return []string{`{"setupComplete":{}}`}
		}
		if ri, ok := m["realtimeInput"].(map[string]any); ok {
			if _, end := ri["activityEnd"]; end {
				return []string{
					`{"serverContent":{"inputTranscription":{"text":"weather tomorrow"}}}`,
					`{"toolCall":{"functionCalls":[{"id":"c1","name":"weather","args":{}}]}}`,
					`{"serverContent":{"turnComplete":true}}`,
				}
			}
		}
		if _, ok := m["toolResponse"]; ok {
			return []string{
				`{"serverContent":{"modelTurn":{"parts":[{"inlineData":{"mimeType":"audio/pcm;rate=24000","data":"` + answer + `"}}]}}}`,
				`{"serverContent":{"outputTranscription":{"text":"Cloudy, 14 degrees."}}}`,
				`{"serverContent":{"turnComplete":true}}`,
			}
		}
		return nil
	}
	addr := f.start(t)
	s, err := Dial(context.Background(), Config{Provider: Gemini, Key: "g-key", Endpoint: addr,
		Functions: []Function{{Name: "weather", Description: "the forecast"}}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_ = s.Audio(make([]int16, 160))
	_ = s.EndOfSpeech()
	evs := collect(t, s, Calls)
	if kinds(evs) != "call,calls" || evs[0].Name != "weather" {
		t.Fatalf("%+v", evs)
	}
	_ = s.ToolResult("c1", "weather", "Cloudy, 14°")
	evs = collect(t, s, Done)
	if kinds(evs) != "audio,saying,heard,done" || evs[1].Text != "Cloudy, 14 degrees." || evs[2].Text != "weather tomorrow" {
		t.Fatalf("%+v", evs)
	}
}

func TestUpTo24k(t *testing.T) {
	got := upTo24k([]int16{0, 300, 600, 900})
	want := []int16{0, 200, 400, 600, 800, 900}
	if len(got) != len(want) {
		t.Fatalf("%v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%v, want %v", got, want)
		}
	}
	if rateOf("audio/pcm") != 24000 || rateOf("audio/pcm;rate=16000") != 16000 || rateOf("audio/pcm;rate=999999") != 24000 {
		t.Error("rateOf")
	}
}

func TestAFailureIsSaid(t *testing.T) {
	f := &fakeServer{script: func(m map[string]any) []string {
		return []string{`{"type":"error","error":{"message":"invalid key"}}`}
	}}
	addr := f.start(t)
	s, err := Dial(context.Background(), Config{Provider: OpenAI, Endpoint: addr})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	evs := collect(t, s, Failed)
	if last := evs[len(evs)-1]; !strings.Contains(last.Err.Error(), "invalid key") {
		t.Fatalf("%+v", evs)
	}
}

func fmtJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

func countType(ms []map[string]any, typ string) int {
	n := 0
	for _, m := range ms {
		if m["type"] == typ {
			n++
		}
	}
	return n
}

// An answer that failed is said to have, not ended quietly; an error that refuses one request leaves the
// session as it was.
func TestOpenAIFailuresAndErrors(t *testing.T) {
	f := &fakeServer{script: func(m map[string]any) []string {
		if m["type"] != "response.create" {
			return nil
		}
		return []string{
			`{"type":"error","error":{"code":"conversation_already_has_active_response","message":"busy"}}`,
			`{"type":"response.done","response":{"status":"failed","status_details":{"error":{"message":"quota exceeded"}},"output":[]}}`,
		}
	}}
	addr := f.start(t)
	s, err := Dial(context.Background(), Config{Provider: OpenAI, Endpoint: addr})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_ = s.EndOfSpeech()
	evs := collect(t, s, Failed)
	if len(evs) != 1 || !strings.Contains(evs[0].Err.Error(), "quota exceeded") {
		t.Fatalf("%+v", evs)
	}
}

func TestARelayMustBeSecure(t *testing.T) {
	Insecure = false
	defer func() { Insecure = true }()
	for _, e := range []string{"ws://relay.home.arpa/rt", "wss://user:pass@relay.home.arpa/rt", "https://relay"} {
		if _, _, err := endpoint(Config{Endpoint: e}, "x"); err == nil {
			t.Errorf("%s was taken", e)
		}
	}
	if _, keyOK, err := endpoint(Config{Endpoint: "wss://relay.home.arpa/rt"}, "x"); err != nil || !keyOK {
		t.Errorf("%v %v", keyOK, err)
	}
}

// Gemini that never says it is set up is not used.
func TestGeminiNeverSetUp(t *testing.T) {
	f := &fakeServer{script: func(m map[string]any) []string {
		return []string{`{"error":{"message":"bad model"}}`}
	}}
	addr := f.start(t)
	if _, err := Dial(context.Background(), Config{Provider: Gemini, Endpoint: addr}); err == nil ||
		!strings.Contains(err.Error(), "bad model") {
		t.Fatalf("%v", err)
	}
}

// An empty commit is said as nothing heard, not as a failure, and not answered.
func TestOpenAINothingHeard(t *testing.T) {
	f := &fakeServer{script: func(m map[string]any) []string {
		if m["type"] != "input_audio_buffer.commit" {
			return nil
		}
		return []string{`{"type":"error","error":{"code":"input_audio_buffer_commit_empty","message":"buffer too small"}}`}
	}}
	addr := f.start(t)
	s, err := Dial(context.Background(), Config{Provider: OpenAI, Endpoint: addr})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_ = s.EndOfSpeech()
	if evs := collect(t, s, Empty); len(evs) != 1 {
		t.Fatalf("%+v", evs)
	}
}

// Gemini's words come a few letters at a time, some after the answer has begun: they are said as one
// Heard at the end of the turn.
func TestGeminiHeardInPieces(t *testing.T) {
	cd, _ := codecFor(Gemini)
	var evs []Event
	emit := func(e Event) { evs = append(evs, e) }
	for _, p := range []string{"Ho", "w", " are", " you?"} {
		cd.read([]byte(`{"serverContent":{"inputTranscription":{"text":"`+p+`"}}}`), emit)
	}
	if len(evs) != 0 {
		t.Fatalf("said before the answer: %+v", evs)
	}
	cd.read([]byte(`{"serverContent":{"modelTurn":{"parts":[{"inlineData":{"mimeType":"audio/pcm;rate=24000","data":"AAAAAA=="}}]}}}`), emit)
	cd.read([]byte(`{"serverContent":{"inputTranscription":{"text":" Today"}}}`), emit)
	cd.read([]byte(`{"serverContent":{"turnComplete":true}}`), emit)
	if kinds(evs) != "audio,heard,done" || evs[1].Text != "How are you? Today" {
		t.Fatalf("%s %+v", kinds(evs), evs)
	}
	// Words heard and no answer: said at the end of the turn.
	evs = nil
	cd.read([]byte(`{"serverContent":{"inputTranscription":{"text":"Hi"}}}`), emit)
	cd.read([]byte(`{"serverContent":{"turnComplete":true}}`), emit)
	if kinds(evs) != "heard,done" || evs[0].Text != "Hi" {
		t.Fatalf("%s %+v", kinds(evs), evs)
	}
}

// OpenAI is asked to write down what was heard with its transcription model; xAI is not sent OpenAI's.
func TestTranscriptionModelIsOpenAIsOnly(t *testing.T) {
	for _, c := range []struct {
		provider string
		want     bool
	}{{OpenAI, true}, {XAI, false}} {
		cd, _ := codecFor(c.provider)
		raw, _ := json.Marshal(cd.setup(Config{}))
		if got := strings.Contains(string(raw), "gpt-4o-mini-transcribe"); got != c.want {
			t.Errorf("%s: %s", c.provider, raw)
		}
	}
}
