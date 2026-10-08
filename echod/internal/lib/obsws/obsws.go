// Package obsws is a client for OBS Studio's built-in WebSocket server (protocol v5, OBS 28 and
// later): it logs in, keeps what the deck shows (the live scene, streaming, recording, which inputs
// are muted, which sources are shown) up to date from OBS's own events, and sends the few requests a
// deck button makes. It reconnects on its own for as long as it runs, so OBS can be closed and opened
// again without anybody touching the device.
//
// OBS's WebSocket is not encrypted. The login is a challenge, so the password itself never crosses the
// network, but scene names and requests do, readable by anything on the same network.
package obsws

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// DefaultPort is the port OBS's WebSocket server listens on unless it was changed.
const DefaultPort = 4455

const (
	opHello        = 0
	opIdentify     = 1
	opIdentified   = 2
	opEvent        = 5
	opRequest      = 6
	opRequestReply = 7

	// subscriptions are the event groups the deck mirrors: General, Scenes, Inputs, Outputs and
	// SceneItems. The high-volume groups (volume meters and the like) are left out.
	subscriptions = 1<<0 | 1<<2 | 1<<3 | 1<<6 | 1<<7

	// readLimit bounds one message from OBS. A scene list of a few hundred names is a few tens of
	// kilobytes; anything far past this is not OBS.
	readLimit = 1 << 20

	// maxNames bounds the scene and input lists kept, and the length of each name, so a hostile or
	// broken server cannot make the device hold more than a deck can show.
	maxNames   = 500
	maxNameLen = 256

	dialTimeout    = 10 * time.Second
	requestTimeout = 10 * time.Second
	retryFirst     = 2 * time.Second
	retryMost      = 30 * time.Second
	unsetEvery     = 5 * time.Second
)

// Config is where OBS is and its WebSocket password (empty when authentication is off in OBS).
type Config struct {
	Addr     string // host or host:port
	Password string
}

// Item is a source in a scene, the thing a show or hide button acts on.
type Item struct{ Scene, Source string }

// State is what the deck shows. Connected is false while OBS is away, and the rest is then as it was
// last seen.
type State struct {
	Connected bool
	// Problem says why there is no connection, for the setup page: a wrong password reads differently
	// from OBS not running.
	Problem   string
	Scene     string
	Scenes    []string
	Inputs    []string
	Streaming bool
	Recording bool
	Muted     map[string]bool // by input name, for every input OBS listed
	Shown     map[Item]bool   // for the items asked for with Track
}

// ErrNotConnected is a request made while OBS is away.
var ErrNotConnected = errors.New("obs: not connected")

// ErrAuth is a login OBS refused: the password is wrong, or OBS asks for one and none is set.
var ErrAuth = errors.New("obs: the password was refused")

// Client keeps one connection to OBS. The zero value is not usable; use New.
type Client struct {
	onChange func()

	mu      sync.Mutex
	st      State
	conn    *websocket.Conn
	pending map[string]chan reply
	tracked []Item
	itemIDs map[Item]int

	writeMu sync.Mutex
	nextID  atomic.Uint64

	// reload asks the session's one reloader to read OBS's lists again; restart wakes Run from a wait
	// and stop ends the session that is up or still logging in, after the settings change.
	reload  chan struct{}
	restart chan struct{}
	stop    context.CancelFunc
}

type reply struct {
	ok      bool
	code    int
	comment string
	data    json.RawMessage
}

// New makes a client. onChange, when set, is called (from the client's own goroutine) after anything
// in State changes; it must not block.
func New(onChange func()) *Client {
	return &Client{onChange: onChange, pending: map[string]chan reply{}, itemIDs: map[Item]int{},
		reload: make(chan struct{}, 1), restart: make(chan struct{}, 1)}
}

// State is a copy of what is known now.
func (c *Client) State() State {
	c.mu.Lock()
	defer c.mu.Unlock()
	st := c.st
	st.Scenes = append([]string(nil), c.st.Scenes...)
	st.Inputs = append([]string(nil), c.st.Inputs...)
	st.Muted = make(map[string]bool, len(c.st.Muted))
	for k, v := range c.st.Muted {
		st.Muted[k] = v
	}
	st.Shown = make(map[Item]bool, len(c.st.Shown))
	for k, v := range c.st.Shown {
		st.Shown[k] = v
	}
	return st
}

// Track sets the sources whose shown or hidden state the deck needs, the ones its buttons act on.
// They are looked up now when connected, and again on every connection.
func (c *Client) Track(items []Item) {
	c.mu.Lock()
	c.tracked = append([]Item(nil), items...)
	connected := c.conn != nil
	c.mu.Unlock()
	if connected {
		go c.refreshItems(context.Background())
	}
}

// Run keeps a connection to the OBS that cfg names, until ctx ends. cfg is read before every attempt,
// so a change on the setup page is picked up on the next one; an empty address waits.
func (c *Client) Run(ctx context.Context, cfg func() Config) {
	wait := retryFirst
	for ctx.Err() == nil {
		// The session's own context, made before the settings are read, so a Restart from here on ends
		// this session and not the last one.
		sctx, end := context.WithCancel(ctx)
		c.mu.Lock()
		c.stop = end
		c.mu.Unlock()
		conf := cfg()
		if strings.TrimSpace(conf.Addr) == "" {
			end()
			c.setProblem("")
			t := time.NewTimer(unsetEvery)
			select {
			case <-ctx.Done():
				t.Stop()
				return
			case <-c.restart:
				t.Stop()
			case <-t.C:
			}
			continue
		}
		start := time.Now()
		err := c.session(sctx, conf)
		restarted := sctx.Err() != nil
		end()
		if ctx.Err() != nil {
			return
		}
		if restarted {
			// Restarted for new settings: try them now, without a "lost the connection" in between.
			select {
			case <-c.restart:
			default:
			}
			wait = retryFirst
			continue
		}
		c.setProblem(problemText(err))
		slog.Info("obs: connection ended", "err", err)
		if time.Since(start) > time.Minute {
			wait = retryFirst
		}
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-c.restart:
			// New settings: try them now, from the start of the backoff.
			t.Stop()
			wait = retryFirst
			continue
		case <-t.C:
		}
		if wait *= 2; wait > retryMost {
			wait = retryMost
		}
	}
}

// Restart ends the session that is up, so the next one reads the settings again: call it after the
// address or password changes.
func (c *Client) Restart() {
	c.mu.Lock()
	stop := c.stop
	c.mu.Unlock()
	if stop != nil {
		stop()
	}
	select {
	case c.restart <- struct{}{}:
	default:
	}
}

func problemText(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrAuth):
		return "OBS refused the password"
	default:
		var ne net.Error
		if errors.As(err, &ne) || strings.Contains(err.Error(), "connection refused") {
			return "OBS is not answering (is it running, with its WebSocket server on?)"
		}
		return "Lost the connection to OBS"
	}
}

// URL is the WebSocket address for addr: a host gets the default port.
func URL(addr string) (string, error) {
	addr = strings.TrimSpace(addr)
	addr = strings.TrimPrefix(strings.TrimPrefix(addr, "ws://"), "http://")
	addr = strings.TrimSuffix(addr, "/")
	if addr == "" || strings.ContainsAny(addr, "/?#@ ") {
		return "", fmt.Errorf("obs: %q is not an address", addr)
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		host, port = addr, strconv.Itoa(DefaultPort)
	}
	if p, err := strconv.Atoi(port); err != nil || p < 1 || p > 65535 || host == "" {
		return "", fmt.Errorf("obs: %q is not an address", addr)
	}
	return (&url.URL{Scheme: "ws", Host: net.JoinHostPort(host, port)}).String(), nil
}

type message struct {
	Op int             `json:"op"`
	D  json.RawMessage `json:"d"`
}

// session is one connection: dial, log in, load the state, then read until it drops.
func (c *Client) session(ctx context.Context, conf Config) error {
	u, err := URL(conf.Addr)
	if err != nil {
		return err
	}
	dctx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()
	d := websocket.Dialer{Subprotocols: []string{"obswebsocket.json"}, HandshakeTimeout: dialTimeout}
	conn, _, err := d.DialContext(dctx, u, nil)
	if err != nil {
		return err
	}
	defer conn.Close()
	conn.SetReadLimit(readLimit)
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()

	if err := identify(conn, conf.Password); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}

	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()
	defer c.dropped(conn)

	readErr := make(chan error, 1)
	go func() { readErr <- c.read(conn) }()

	if err := c.load(ctx); err != nil {
		_ = conn.Close()
		<-readErr
		return err
	}
	slog.Info("obs: connected", "addr", conf.Addr)
	go c.reloader(ctx)
	return <-readErr
}

// reloader reads OBS's lists again when they change, once for a burst of changes: deleting several
// sources, or switching scene collections, sends dozens of events, and each would otherwise start its
// own full reload, the last to finish winning whatever it saw.
func (c *Client) reloader(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.reload:
		}
		// Let the burst finish.
		t := time.NewTimer(300 * time.Millisecond)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
		select {
		case <-c.reload:
		default:
		}
		c.mu.Lock()
		c.itemIDs = map[Item]int{}
		c.mu.Unlock()
		lctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		if err := c.load(lctx); err != nil && ctx.Err() == nil {
			slog.Info("obs: reloading after a change", "err", err)
		}
		cancel()
	}
}

// identify answers OBS's Hello, with the login when OBS asks for one.
func identify(conn *websocket.Conn, password string) error {
	_ = conn.SetReadDeadline(time.Now().Add(dialTimeout))
	defer conn.SetReadDeadline(time.Time{})
	var hello message
	if err := conn.ReadJSON(&hello); err != nil {
		return err
	}
	if hello.Op != opHello {
		return fmt.Errorf("obs: expected Hello, got op %d", hello.Op)
	}
	var h struct {
		RPCVersion int `json:"rpcVersion"`
		Auth       *struct {
			Challenge string `json:"challenge"`
			Salt      string `json:"salt"`
		} `json:"authentication"`
	}
	if err := json.Unmarshal(hello.D, &h); err != nil {
		return err
	}
	id := map[string]any{"rpcVersion": 1, "eventSubscriptions": subscriptions}
	if h.Auth != nil {
		if password == "" {
			return ErrAuth
		}
		id["authentication"] = Auth(password, h.Auth.Salt, h.Auth.Challenge)
	}
	if err := conn.WriteJSON(map[string]any{"op": opIdentify, "d": id}); err != nil {
		return err
	}
	var m message
	if err := conn.ReadJSON(&m); err != nil {
		// OBS closes the connection with 4009 on a wrong password.
		var ce *websocket.CloseError
		if errors.As(err, &ce) && ce.Code == 4009 {
			return ErrAuth
		}
		return err
	}
	if m.Op != opIdentified {
		return fmt.Errorf("obs: expected Identified, got op %d", m.Op)
	}
	return nil
}

// Auth is the login string OBS expects: base64(sha256(base64(sha256(password+salt)) + challenge)).
func Auth(password, salt, challenge string) string {
	s := sha256.Sum256([]byte(password + salt))
	secret := base64.StdEncoding.EncodeToString(s[:])
	a := sha256.Sum256([]byte(secret + challenge))
	return base64.StdEncoding.EncodeToString(a[:])
}

func (c *Client) dropped(conn *websocket.Conn) {
	c.mu.Lock()
	if c.conn == conn {
		c.conn = nil
	}
	c.st.Connected = false
	for id, ch := range c.pending {
		close(ch)
		delete(c.pending, id)
	}
	c.itemIDs = map[Item]int{}
	c.mu.Unlock()
	c.changed()
}

func (c *Client) setProblem(p string) {
	c.mu.Lock()
	same := c.st.Problem == p
	c.st.Problem = p
	c.mu.Unlock()
	if !same {
		c.changed()
	}
}

func (c *Client) changed() {
	if c.onChange != nil {
		c.onChange()
	}
}

// read handles everything OBS sends until the connection drops.
func (c *Client) read(conn *websocket.Conn) error {
	for {
		var m message
		if err := conn.ReadJSON(&m); err != nil {
			return err
		}
		switch m.Op {
		case opRequestReply:
			var r struct {
				ID     string `json:"requestId"`
				Status struct {
					Result  bool   `json:"result"`
					Code    int    `json:"code"`
					Comment string `json:"comment"`
				} `json:"requestStatus"`
				Data json.RawMessage `json:"responseData"`
			}
			if json.Unmarshal(m.D, &r) != nil {
				continue
			}
			c.mu.Lock()
			ch := c.pending[r.ID]
			delete(c.pending, r.ID)
			c.mu.Unlock()
			if ch != nil {
				ch <- reply{ok: r.Status.Result, code: r.Status.Code, comment: r.Status.Comment, data: r.Data}
			}
		case opEvent:
			c.event(m.D)
		}
	}
}

// Request sends one request and waits for OBS's answer.
func (c *Client) Request(ctx context.Context, typ string, data any) (json.RawMessage, error) {
	c.mu.Lock()
	conn := c.conn
	if conn == nil {
		c.mu.Unlock()
		return nil, ErrNotConnected
	}
	id := strconv.FormatUint(c.nextID.Add(1), 10)
	ch := make(chan reply, 1)
	c.pending[id] = ch
	c.mu.Unlock()

	d := map[string]any{"requestType": typ, "requestId": id}
	if data != nil {
		d["requestData"] = data
	}
	c.writeMu.Lock()
	_ = conn.SetWriteDeadline(time.Now().Add(requestTimeout))
	err := conn.WriteJSON(map[string]any{"op": opRequest, "d": d})
	c.writeMu.Unlock()
	if err != nil {
		c.forget(id)
		return nil, err
	}

	t := time.NewTimer(requestTimeout)
	defer t.Stop()
	select {
	case r, ok := <-ch:
		if !ok {
			return nil, ErrNotConnected
		}
		if !r.ok {
			return nil, fmt.Errorf("obs: %s failed (%d): %s", typ, r.code, r.comment)
		}
		return r.data, nil
	case <-t.C:
		c.forget(id)
		return nil, fmt.Errorf("obs: %s: no answer", typ)
	case <-ctx.Done():
		c.forget(id)
		return nil, ctx.Err()
	}
}

func (c *Client) forget(id string) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

// load reads everything the deck shows, after a connection is made.
func (c *Client) load(ctx context.Context) error {
	raw, err := c.Request(ctx, "GetSceneList", nil)
	if err != nil {
		return err
	}
	var sl struct {
		Current string `json:"currentProgramSceneName"`
		Scenes  []struct {
			Name  string `json:"sceneName"`
			Index int    `json:"sceneIndex"`
		} `json:"scenes"`
	}
	if err := json.Unmarshal(raw, &sl); err != nil {
		return err
	}
	// OBS lists scenes bottom first; the deck shows them as OBS's own list does, top first.
	sort.SliceStable(sl.Scenes, func(i, j int) bool { return sl.Scenes[i].Index > sl.Scenes[j].Index })
	scenes := make([]string, 0, len(sl.Scenes))
	for _, s := range sl.Scenes {
		scenes = appendName(scenes, s.Name)
	}

	raw, err = c.Request(ctx, "GetInputList", nil)
	if err != nil {
		return err
	}
	var il struct {
		Inputs []struct {
			Name string `json:"inputName"`
		} `json:"inputs"`
	}
	if err := json.Unmarshal(raw, &il); err != nil {
		return err
	}
	inputs := make([]string, 0, len(il.Inputs))
	muted := map[string]bool{}
	for _, in := range il.Inputs {
		before := len(inputs)
		if inputs = appendName(inputs, in.Name); len(inputs) == before {
			continue
		}
		if raw, err := c.Request(ctx, "GetInputMute", map[string]any{"inputName": in.Name}); err == nil {
			var m struct {
				Muted bool `json:"inputMuted"`
			}
			if json.Unmarshal(raw, &m) == nil {
				muted[in.Name] = m.Muted
			}
		}
	}

	streaming := c.active(ctx, "GetStreamStatus")
	recording := c.active(ctx, "GetRecordStatus")

	c.mu.Lock()
	c.st.Connected, c.st.Problem = true, ""
	c.st.Scene = clip(sl.Current)
	c.st.Scenes, c.st.Inputs, c.st.Muted = scenes, inputs, muted
	c.st.Streaming, c.st.Recording = streaming, recording
	c.mu.Unlock()
	c.changed()
	c.refreshItems(ctx)
	return nil
}

func (c *Client) active(ctx context.Context, typ string) bool {
	raw, err := c.Request(ctx, typ, nil)
	if err != nil {
		return false
	}
	var s struct {
		Active bool `json:"outputActive"`
	}
	_ = json.Unmarshal(raw, &s)
	return s.Active
}

// refreshItems looks up the tracked sources' ids and whether each is shown.
func (c *Client) refreshItems(ctx context.Context) {
	c.mu.Lock()
	items := append([]Item(nil), c.tracked...)
	c.mu.Unlock()
	shown := map[Item]bool{}
	ids := map[Item]int{}
	for _, it := range items {
		id, err := c.itemID(ctx, it)
		if err != nil {
			continue
		}
		raw, err := c.Request(ctx, "GetSceneItemEnabled", map[string]any{"sceneName": it.Scene, "sceneItemId": id})
		if err != nil {
			continue
		}
		var e struct {
			Enabled bool `json:"sceneItemEnabled"`
		}
		if json.Unmarshal(raw, &e) == nil {
			shown[it], ids[it] = e.Enabled, id
		}
	}
	c.mu.Lock()
	c.st.Shown = shown
	for k, v := range ids {
		c.itemIDs[k] = v
	}
	c.mu.Unlock()
	c.changed()
}

func (c *Client) itemID(ctx context.Context, it Item) (int, error) {
	c.mu.Lock()
	id, ok := c.itemIDs[it]
	c.mu.Unlock()
	if ok {
		return id, nil
	}
	raw, err := c.Request(ctx, "GetSceneItemId", map[string]any{"sceneName": it.Scene, "sourceName": it.Source})
	if err != nil {
		return 0, err
	}
	var r struct {
		ID int `json:"sceneItemId"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return 0, err
	}
	c.mu.Lock()
	c.itemIDs[it] = r.ID
	c.mu.Unlock()
	return r.ID, nil
}

// event folds one of OBS's events into the state.
func (c *Client) event(raw json.RawMessage) {
	var e struct {
		Type string          `json:"eventType"`
		Data json.RawMessage `json:"eventData"`
	}
	if json.Unmarshal(raw, &e) != nil {
		return
	}
	var d struct {
		SceneName   string `json:"sceneName"`
		Active      *bool  `json:"outputActive"`
		InputName   string `json:"inputName"`
		Muted       *bool  `json:"inputMuted"`
		ItemID      int    `json:"sceneItemId"`
		ItemEnabled *bool  `json:"sceneItemEnabled"`
	}
	_ = json.Unmarshal(e.Data, &d)

	reload := false
	c.mu.Lock()
	switch e.Type {
	case "CurrentProgramSceneChanged":
		c.st.Scene = clip(d.SceneName)
	case "StreamStateChanged":
		if d.Active != nil {
			c.st.Streaming = *d.Active
		}
	case "RecordStateChanged":
		if d.Active != nil {
			c.st.Recording = *d.Active
		}
	case "InputMuteStateChanged":
		if d.Muted != nil && c.st.Muted != nil {
			if _, known := c.st.Muted[d.InputName]; known {
				c.st.Muted[d.InputName] = *d.Muted
			}
		}
	case "SceneItemEnableStateChanged":
		if d.ItemEnabled != nil {
			for it, id := range c.itemIDs {
				if it.Scene == d.SceneName && id == d.ItemID {
					if c.st.Shown == nil {
						c.st.Shown = map[Item]bool{}
					}
					c.st.Shown[it] = *d.ItemEnabled
				}
			}
		}
	case "SceneListChanged", "SceneCreated", "SceneRemoved", "SceneNameChanged",
		"InputCreated", "InputRemoved", "InputNameChanged", "SceneItemCreated", "SceneItemRemoved":
		reload = true
	default:
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()
	if reload {
		// Lists changed in OBS: the reloader reads them again, off the read loop, which its requests
		// need.
		select {
		case c.reload <- struct{}{}:
		default:
		}
		return
	}
	c.changed()
}

func appendName(list []string, name string) []string {
	if name == "" || len(list) >= maxNames {
		return list
	}
	return append(list, clip(name))
}

func clip(s string) string {
	if len(s) > maxNameLen {
		return s[:maxNameLen]
	}
	return s
}

// The requests a deck button makes.

// SetScene makes scene the live one.
func (c *Client) SetScene(ctx context.Context, scene string) error {
	_, err := c.Request(ctx, "SetCurrentProgramScene", map[string]any{"sceneName": scene})
	return err
}

// ToggleStream starts or stops streaming.
func (c *Client) ToggleStream(ctx context.Context) error {
	_, err := c.Request(ctx, "ToggleStream", nil)
	return err
}

// ToggleRecord starts or stops recording.
func (c *Client) ToggleRecord(ctx context.Context) error {
	_, err := c.Request(ctx, "ToggleRecord", nil)
	return err
}

// ToggleMute mutes or unmutes an input.
func (c *Client) ToggleMute(ctx context.Context, input string) error {
	_, err := c.Request(ctx, "ToggleInputMute", map[string]any{"inputName": input})
	return err
}

// ToggleShown shows a hidden source in a scene, or hides a shown one.
func (c *Client) ToggleShown(ctx context.Context, it Item) error {
	id, err := c.itemID(ctx, it)
	if err != nil {
		return err
	}
	raw, err := c.Request(ctx, "GetSceneItemEnabled", map[string]any{"sceneName": it.Scene, "sceneItemId": id})
	if err != nil {
		return err
	}
	var e struct {
		Enabled bool `json:"sceneItemEnabled"`
	}
	if err := json.Unmarshal(raw, &e); err != nil {
		return err
	}
	_, err = c.Request(ctx, "SetSceneItemEnabled", map[string]any{"sceneName": it.Scene, "sceneItemId": id, "sceneItemEnabled": !e.Enabled})
	return err
}
