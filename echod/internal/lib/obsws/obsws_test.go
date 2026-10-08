package obsws

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// fakeOBS is enough of OBS's WebSocket server for the client: Hello with or without a login, the
// requests the client makes, and events pushed on demand.
type fakeOBS struct {
	t        *testing.T
	password string

	mu        sync.Mutex
	scene     string
	scenes    []string
	muted     map[string]bool
	streaming bool
	enabled   map[int]bool
	requests  []string
	conn      *websocket.Conn
}

func newFake(t *testing.T, password string) (*fakeOBS, string) {
	f := &fakeOBS{t: t, password: password, scene: "Main", scenes: []string{"Main", "BRB", "Ending"},
		muted: map[string]bool{"Mic": false, "Desktop": true}, enabled: map[int]bool{7: true}}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	return f, strings.TrimPrefix(srv.URL, "http://")
}

const (
	fakeSalt      = "lM1GncleQOaCu9lT1yeUZhFYnqhsLLP1G5lAGo3ixaI="
	fakeChallenge = "+IxH4CnCiqpX1rM9scsNynZzbOe4KhDeYcTNS3PDaeY="
)

func (f *fakeOBS) serve(w http.ResponseWriter, r *http.Request) {
	up := websocket.Upgrader{Subprotocols: []string{"obswebsocket.json"}}
	conn, err := up.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	hello := map[string]any{"obsWebSocketVersion": "5.5.0", "rpcVersion": 1}
	if f.password != "" {
		hello["authentication"] = map[string]any{"challenge": fakeChallenge, "salt": fakeSalt}
	}
	_ = conn.WriteJSON(map[string]any{"op": 0, "d": hello})
	var id struct {
		Op int `json:"op"`
		D  struct {
			Auth string `json:"authentication"`
			Subs int    `json:"eventSubscriptions"`
		} `json:"d"`
	}
	if err := conn.ReadJSON(&id); err != nil || id.Op != 1 {
		return
	}
	if f.password != "" && id.D.Auth != Auth(f.password, fakeSalt, fakeChallenge) {
		_ = conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(4009, "Authentication failed."))
		return
	}
	_ = conn.WriteJSON(map[string]any{"op": 2, "d": map[string]any{"negotiatedRpcVersion": 1}})
	f.mu.Lock()
	f.conn = conn
	f.mu.Unlock()
	for {
		var m struct {
			Op int `json:"op"`
			D  struct {
				Type string          `json:"requestType"`
				ID   string          `json:"requestId"`
				Data json.RawMessage `json:"requestData"`
			} `json:"d"`
		}
		if err := conn.ReadJSON(&m); err != nil {
			return
		}
		if m.Op != 6 {
			continue
		}
		data, ok := f.answer(m.D.Type, m.D.Data)
		f.mu.Lock()
		f.requests = append(f.requests, m.D.Type)
		_ = conn.WriteJSON(map[string]any{"op": 7, "d": map[string]any{
			"requestType": m.D.Type, "requestId": m.D.ID,
			"requestStatus": map[string]any{"result": ok, "code": map[bool]int{true: 100, false: 600}[ok]},
			"responseData":  data}})
		f.mu.Unlock()
	}
}

func (f *fakeOBS) answer(typ string, raw json.RawMessage) (any, bool) {
	var in struct {
		Scene   string `json:"sceneName"`
		Input   string `json:"inputName"`
		Source  string `json:"sourceName"`
		ItemID  int    `json:"sceneItemId"`
		Enabled bool   `json:"sceneItemEnabled"`
	}
	_ = json.Unmarshal(raw, &in)
	f.mu.Lock()
	defer f.mu.Unlock()
	switch typ {
	case "GetSceneList":
		var scenes []map[string]any
		for i, s := range f.scenes {
			// OBS's own order: the bottom scene has index 0.
			scenes = append(scenes, map[string]any{"sceneName": s, "sceneIndex": len(f.scenes) - 1 - i})
		}
		return map[string]any{"currentProgramSceneName": f.scene, "scenes": scenes}, true
	case "GetInputList":
		return map[string]any{"inputs": []map[string]any{{"inputName": "Mic"}, {"inputName": "Desktop"}}}, true
	case "GetInputMute":
		return map[string]any{"inputMuted": f.muted[in.Input]}, true
	case "GetStreamStatus":
		return map[string]any{"outputActive": f.streaming}, true
	case "GetRecordStatus":
		return map[string]any{"outputActive": false}, true
	case "SetCurrentProgramScene":
		f.scene = in.Scene
		return nil, true
	case "ToggleStream":
		f.streaming = !f.streaming
		return map[string]any{"outputActive": f.streaming}, true
	case "ToggleInputMute":
		f.muted[in.Input] = !f.muted[in.Input]
		return map[string]any{"inputMuted": f.muted[in.Input]}, true
	case "GetSceneItemId":
		if in.Scene == "Main" && in.Source == "Webcam" {
			return map[string]any{"sceneItemId": 7}, true
		}
		return nil, false
	case "GetSceneItemEnabled":
		return map[string]any{"sceneItemEnabled": f.enabled[in.ItemID]}, true
	case "SetSceneItemEnabled":
		f.enabled[in.ItemID] = in.Enabled
		return nil, true
	}
	return nil, false
}

func (f *fakeOBS) push(typ string, data map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.conn != nil {
		_ = f.conn.WriteJSON(map[string]any{"op": 5, "d": map[string]any{"eventType": typ, "eventData": data}})
	}
}

// waitFor polls the client's state until ok, failing the test after a while.
func waitFor(t *testing.T, c *Client, what string, ok func(State) bool) State {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		st := c.State()
		if ok(st) {
			return st
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s; state %+v", what, st)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func run(t *testing.T, addr, password string) *Client {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	c := New(nil)
	go c.Run(ctx, func() Config { return Config{Addr: addr, Password: password} })
	return c
}

func TestAuthMatchesOBSDocs(t *testing.T) {
	// The worked example in obs-websocket's protocol docs.
	got := Auth("supersecretpassword", fakeSalt, fakeChallenge)
	if want := "1Ct943GAT+6YQUUX47Ia/ncufilbe6+oD6lY+5kaCu4="; got != want {
		t.Fatalf("Auth = %q, want %q", got, want)
	}
}

func TestConnectLoadsWhatTheDeckShows(t *testing.T) {
	f, addr := newFake(t, "pw")
	c := run(t, addr, "pw")
	st := waitFor(t, c, "connected", func(s State) bool { return s.Connected })
	if st.Scene != "Main" {
		t.Errorf("scene %q", st.Scene)
	}
	if got := strings.Join(st.Scenes, ","); got != "Main,BRB,Ending" {
		t.Errorf("scenes %q, want OBS's own top-first order", got)
	}
	if !st.Muted["Desktop"] || st.Muted["Mic"] {
		t.Errorf("muted %v", st.Muted)
	}
	_ = f
}

func TestWrongPasswordSaysSo(t *testing.T) {
	_, addr := newFake(t, "right")
	c := run(t, addr, "wrong")
	st := waitFor(t, c, "a problem", func(s State) bool { return s.Problem != "" })
	if st.Connected || !strings.Contains(st.Problem, "password") {
		t.Fatalf("state %+v", st)
	}
}

func TestNoPasswordWhenOBSAsksForOne(t *testing.T) {
	_, addr := newFake(t, "right")
	c := run(t, addr, "")
	st := waitFor(t, c, "a problem", func(s State) bool { return s.Problem != "" })
	if !strings.Contains(st.Problem, "password") {
		t.Fatalf("problem %q", st.Problem)
	}
}

func TestButtonsAndEvents(t *testing.T) {
	f, addr := newFake(t, "")
	c := run(t, addr, "")
	waitFor(t, c, "connected", func(s State) bool { return s.Connected })
	ctx := context.Background()

	if err := c.SetScene(ctx, "BRB"); err != nil {
		t.Fatal(err)
	}
	f.push("CurrentProgramSceneChanged", map[string]any{"sceneName": "BRB"})
	waitFor(t, c, "scene BRB", func(s State) bool { return s.Scene == "BRB" })

	if err := c.ToggleStream(ctx); err != nil {
		t.Fatal(err)
	}
	f.push("StreamStateChanged", map[string]any{"outputActive": true, "outputState": "OBS_WEBSOCKET_OUTPUT_STARTED"})
	waitFor(t, c, "streaming", func(s State) bool { return s.Streaming })

	if err := c.ToggleMute(ctx, "Mic"); err != nil {
		t.Fatal(err)
	}
	f.push("InputMuteStateChanged", map[string]any{"inputName": "Mic", "inputMuted": true})
	waitFor(t, c, "mic muted", func(s State) bool { return s.Muted["Mic"] })

	// An input OBS never listed is not added by an event.
	f.push("InputMuteStateChanged", map[string]any{"inputName": "Ghost", "inputMuted": true})
	f.push("StreamStateChanged", map[string]any{"outputActive": false})
	st := waitFor(t, c, "stream stopped", func(s State) bool { return !s.Streaming })
	if _, ok := st.Muted["Ghost"]; ok {
		t.Error("an unlisted input appeared from an event")
	}
}

func TestShownSources(t *testing.T) {
	f, addr := newFake(t, "")
	c := run(t, addr, "")
	waitFor(t, c, "connected", func(s State) bool { return s.Connected })
	cam := Item{Scene: "Main", Source: "Webcam"}
	c.Track([]Item{cam})
	waitFor(t, c, "webcam shown", func(s State) bool { return s.Shown[cam] })

	if err := c.ToggleShown(context.Background(), cam); err != nil {
		t.Fatal(err)
	}
	f.push("SceneItemEnableStateChanged", map[string]any{"sceneName": "Main", "sceneItemId": 7, "sceneItemEnabled": false})
	waitFor(t, c, "webcam hidden", func(s State) bool { v, ok := s.Shown[cam]; return ok && !v })

	if err := c.ToggleShown(context.Background(), Item{Scene: "Main", Source: "Nope"}); err == nil {
		t.Error("a source OBS doesn't have toggled without an error")
	}
}

func TestRequestWhileAwayFailsFast(t *testing.T) {
	c := New(nil)
	if err := c.SetScene(context.Background(), "x"); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("err %v", err)
	}
}

func TestReconnectsAfterOBSRestarts(t *testing.T) {
	f, addr := newFake(t, "")
	c := run(t, addr, "")
	waitFor(t, c, "connected", func(s State) bool { return s.Connected })
	f.mu.Lock()
	_ = f.conn.Close()
	f.mu.Unlock()
	waitFor(t, c, "dropped", func(s State) bool { return !s.Connected })
	waitFor(t, c, "back", func(s State) bool { return s.Connected })
}

func TestURL(t *testing.T) {
	for in, want := range map[string]string{
		"192.168.1.20":       "ws://192.168.1.20:4455",
		"pc.local:4456":      "ws://pc.local:4456",
		"ws://192.168.1.20/": "ws://192.168.1.20:4455",
		"[fd00::5]:4455":     "ws://[fd00::5]:4455",
	} {
		if got, err := URL(in); err != nil || got != want {
			t.Errorf("URL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "a b", "host:0", "host:99999", "user@host", "host/path"} {
		if _, err := URL(bad); err == nil {
			t.Errorf("URL(%q) accepted", bad)
		}
	}
}

// A burst of list changes reloads OBS's lists once or twice, not once an event.
func TestListChangesCoalesce(t *testing.T) {
	f, addr := newFake(t, "")
	c := run(t, addr, "")
	waitFor(t, c, "connected", func(s State) bool { return s.Connected })
	f.mu.Lock()
	before := 0
	for _, r := range f.requests {
		if r == "GetSceneList" {
			before++
		}
	}
	f.mu.Unlock()
	for range 40 {
		f.push("SceneItemRemoved", map[string]any{"sceneName": "Main"})
	}
	time.Sleep(1500 * time.Millisecond)
	f.mu.Lock()
	after := 0
	for _, r := range f.requests {
		if r == "GetSceneList" {
			after++
		}
	}
	f.mu.Unlock()
	if n := after - before; n < 1 || n > 2 {
		t.Errorf("40 changes reloaded the lists %d times", n)
	}
}

// New settings are tried at once, not after the backoff.
func TestRestartWakesTheWait(t *testing.T) {
	_, addr := newFake(t, "right")
	var mu sync.Mutex
	pw := "wrong"
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	c := New(nil)
	go c.Run(ctx, func() Config {
		mu.Lock()
		defer mu.Unlock()
		return Config{Addr: addr, Password: pw}
	})
	waitFor(t, c, "refused", func(s State) bool { return s.Problem != "" })
	time.Sleep(3 * time.Second) // into a longer wait
	mu.Lock()
	pw = "right"
	mu.Unlock()
	c.Restart()
	start := time.Now()
	waitFor(t, c, "connected", func(s State) bool { return s.Connected })
	if time.Since(start) > 2*time.Second {
		t.Errorf("took %v after the restart", time.Since(start))
	}
}
