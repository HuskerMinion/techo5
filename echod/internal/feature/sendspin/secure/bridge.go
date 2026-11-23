package secure

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// The player is written against the Sendspin library, which speaks to a plain WebSocket. The bridge is
// that WebSocket: it runs on this side of an encrypted connection and hands the library each message
// as it would have crossed unencrypted, and the library's own back the same way. The parts of the
// session the library predates are kept here, out of its sight: the hello order, server/activate,
// pairing and the re-handshake.

// heldMost bounds what the player may queue before the server's first server/activate admits it. The
// player sends its state and starts the clock once it has the hello; anything past this is a clock
// round, which is worth nothing late.
const heldMost = 64

// pairingTimeout is the attempt timeout the specification recommends, from client/pair-finalize.
const pairingTimeout = 2 * time.Minute

// Keepalive on the encrypted side: the library pings only the plain side, which is never lost.
const (
	pingEvery = 20 * time.Second
	pongWait  = 60 * time.Second
)

// Options is what the bridge needs to know about the device.
type Options struct {
	// Unpaired says whether a server with no pairing record may play: unpaired_access.enabled.
	Unpaired bool

	// MAC is the device's MAC address, lowercase and colon-separated, for device_info.mac_address:
	// a server that knew the device by it before encryption keeps the player it had. It goes to a
	// server on unpaired access too, which has proved nothing: on purpose. The unencrypted wire, still
	// spoken to older servers, has always sent it as client_id to any server, anything on the network
	// sees it anyway, and without it an unpaired Music Assistant would make a second player for the
	// device in place of the one it had.
	MAC string
}

type bridge struct {
	c     *Conn
	ws    *websocket.Conn // the encrypted side
	plain *websocket.Conn // the bridge's end of the library's WebSocket
	opts  Options

	done chan struct{}
	once sync.Once

	// gate orders the player's messages: held until server/activate admits them, then written in the
	// order they were sent. A re-handshake closes the gate until the next server/activate.
	gate   sync.Mutex
	active bool
	held   [][]byte

	// helloed is set when server/hello has arrived. The library's client/hello, sent first, waits in
	// hello until then.
	helloed bool
	hello   []byte

	// roles is the active_roles last declared, which an activation that omits them keeps.
	roles []string

	// state is the player's newest client/state, under gate. An activation that turns roles on
	// asks for the state of each, which the player only sends when something changes: the bridge
	// sends this one again.
	state []byte

	// pairing state, read and written on the encrypted side's reader only, except for the timer.
	pairingIndex int
	pendingMu    sync.Mutex
	pending      []byte
	timer        *time.Timer
}

// Bridge starts the bridge over c, the encrypted connection on ws, and returns the WebSocket the
// library is to be given. done closes once either side has ended, and both are closed by then.
func Bridge(ctx context.Context, c *Conn, ws *websocket.Conn, opts Options) (*websocket.Conn, <-chan struct{}, error) {
	lib, plain, err := pair(ctx)
	if err != nil {
		return nil, nil, err
	}
	b := &bridge{c: c, ws: ws, plain: plain, opts: opts, done: make(chan struct{})}

	ws.SetPongHandler(func(string) error { return ws.SetReadDeadline(time.Now().Add(pongWait)) })
	_ = ws.SetReadDeadline(time.Now().Add(pongWait))

	go b.fromServer()
	go b.fromPlayer()
	go b.keepalive()
	go func() {
		select {
		case <-ctx.Done():
			b.end(nil)
		case <-b.done:
		}
	}()
	return lib, b.done, nil
}

func (b *bridge) end(err error) {
	b.once.Do(func() {
		if err != nil {
			slog.Warn("sendspin encrypted session ended", "err", err)
		}
		close(b.done)
		_ = b.ws.Close()
		_ = b.plain.Close()
		b.pendingMu.Lock()
		if b.timer != nil {
			b.timer.Stop()
		}
		b.pendingMu.Unlock()
	})
}

func (b *bridge) keepalive() {
	t := time.NewTicker(pingEvery)
	defer t.Stop()
	for {
		select {
		case <-b.done:
			return
		case <-t.C:
			if err := b.ws.WriteControl(websocket.PingMessage, nil, time.Now().Add(10*time.Second)); err != nil {
				b.end(err)
				return
			}
		}
	}
}

// fromServer reads the encrypted side and hands the library what is its.
func (b *bridge) fromServer() {
	for {
		kind, data, err := b.c.Read()
		if err != nil {
			b.end(err)
			return
		}
		if kind == websocket.BinaryMessage {
			if err := b.plain.WriteMessage(websocket.BinaryMessage, forLibrary(data)); err != nil {
				b.end(err)
				return
			}
			continue
		}
		var env envelope
		if err := json.Unmarshal(data, &env); err != nil {
			b.end(fmt.Errorf("a malformed message: %w", err))
			return
		}
		if err := b.server(env, data); err != nil {
			b.end(err)
			return
		}
	}
}

func (b *bridge) server(env envelope, data []byte) error {
	switch env.Type {
	case "server/hello":
		b.gate.Lock()
		if b.helloed {
			b.gate.Unlock()
			return errors.New("a second server/hello")
		}
		b.helloed = true
		hello := b.hello
		b.hello = nil
		var err error
		if hello != nil {
			err = b.c.WriteText(hello)
		}
		b.gate.Unlock()
		if err != nil {
			return err
		}
		return b.plain.WriteMessage(websocket.TextMessage, data)

	case "server/activate":
		return b.activated(env.Payload)

	case "noise/handshake":
		// A re-handshake. Nothing new goes out from the player until the server/activate under the
		// new keys, and the pairing count starts again.
		b.gate.Lock()
		b.active = false
		b.gate.Unlock()
		if err := b.c.Rehandshake(env.Payload); err != nil {
			return fmt.Errorf("re-handshake: %w", err)
		}
		b.pairingIndex = 0
		slog.Info("sendspin re-handshake", "psk", b.c.Category)
		return nil

	case "server/pair-finalize":
		b.pendingMu.Lock()
		psk := b.pending
		b.pending = nil
		if b.timer != nil {
			b.timer.Stop()
		}
		b.pendingMu.Unlock()
		if psk == nil {
			return nil // an attempt already ended: discarded
		}
		if err := b.c.store.remember(b.c.ServerID, psk); err != nil {
			return fmt.Errorf("keeping the pairing: %w", err)
		}
		slog.Info("sendspin paired")
		return nil

	case "pair/abort":
		b.pendingMu.Lock()
		b.pending = nil
		b.pendingMu.Unlock()
		slog.Info("sendspin pairing aborted by the server", "payload", string(env.Payload))
		return nil

	case "server/unpair":
		if b.c.Category != LongTerm {
			return nil // an unpaired session ignores it
		}
		if err := b.c.store.forget(b.c.ServerID); err != nil {
			slog.Warn("sendspin unpair", "err", err)
		}
		slog.Info("sendspin unpaired by the server")
		b.goodbye("unpaired")
		return errors.New("unpaired")
	}

	if isPairing(env.Type) {
		// Only the pairing PSK is offered, and it has no PAKE round: anything else is out of sequence.
		return fmt.Errorf("pairing message %s out of sequence", env.Type)
	}
	return b.plain.WriteMessage(websocket.TextMessage, data)
}

func isPairing(t string) bool {
	switch t {
	case "server/pair-init", "server/pair-auth", "server/pair-confirm":
		return true
	}
	return false
}

type activation struct {
	Activities  []string  `json:"activities"`
	ActiveRoles *[]string `json:"active_roles"`
	Pairing     *struct {
		Method string `json:"method"`
	} `json:"pairing"`
}

// allowed is whether activities are an allowed set for the matched PSK.
func allowed(c Category, activities []string, unpaired bool) bool {
	ok := []string{"pairing"}
	switch {
	case c == LongTerm:
		ok = []string{"playback"}
	case unpaired:
		ok = []string{"playback", "pairing"}
	}
	for _, a := range activities {
		if !slices.Contains(ok, a) {
			return false
		}
	}
	return true
}

func admissible(c Category, activities []string, roles bool, unpaired bool) bool {
	return allowed(c, activities, unpaired) &&
		(!roles || allowed(c, append(slices.Clone(activities), "playback"), unpaired))
}

func (b *bridge) activated(payload json.RawMessage) error {
	var a activation
	if err := json.Unmarshal(payload, &a); err != nil {
		return fmt.Errorf("server/activate: %w", err)
	}
	for _, act := range a.Activities {
		if act != "playback" && act != "pairing" {
			b.goodbye("unauthorized")
			return fmt.Errorf("server/activate: activity %q", act)
		}
	}
	cat, unpaired := b.c.Category, b.opts.Unpaired

	// active_roles persists when omitted, unless the connection is no longer playback-capable.
	roles := b.roles
	switch {
	case a.ActiveRoles != nil:
		roles = *a.ActiveRoles
	case !allowed(cat, append(slices.Clone(a.Activities), "playback"), unpaired):
		roles = nil
	}
	if !admissible(cat, a.Activities, len(roles) > 0, unpaired) {
		reason := "unauthorized"
		if cat != LongTerm && !unpaired && admissible(cat, a.Activities, len(roles) > 0, true) {
			reason = "pairing_required"
		}
		b.goodbye(reason)
		return fmt.Errorf("server/activate refused: %s", reason)
	}
	turnedOn := len(roles) > 0 && !slices.Equal(roles, b.roles)
	b.roles = roles
	slog.Info("sendspin activated", "activities", a.Activities, "roles", roles, "psk", cat)

	// The gate opens before any pairing message, which is the player's like any other.
	b.open(turnedOn)

	if slices.Contains(a.Activities, "pairing") {
		b.pairingIndex++
		if a.Pairing == nil || a.Pairing.Method != "pairing_psk" || cat != Pairing {
			return b.send(map[string]any{"type": "pair/abort", "payload": map[string]string{"reason": "method_not_supported"}})
		}
		return b.pairByPSK()
	}
	return nil
}

// pairByPSK runs the Pairing PSK flow: the connection is keyed by the pairing PSK, so the server holds
// the token and both sides are authenticated. The device makes the long-term PSK and sends it.
func (b *bridge) pairByPSK() error {
	psk, err := random()
	if err != nil {
		return err
	}
	b.pendingMu.Lock()
	b.pending = psk
	if b.timer != nil {
		b.timer.Stop()
	}
	b.timer = time.AfterFunc(pairingTimeout, func() {
		b.pendingMu.Lock()
		waiting := b.pending != nil
		b.pending = nil
		b.pendingMu.Unlock()
		if waiting {
			_ = b.send(map[string]any{"type": "pair/abort", "payload": map[string]string{"reason": "attempt_timeout"}})
		}
	})
	b.pendingMu.Unlock()

	if err := b.send(map[string]any{"type": "client/pair-init", "payload": map[string]int{"pairing_index": b.pairingIndex}}); err != nil {
		return err
	}
	return b.send(map[string]any{"type": "client/pair-finalize", "payload": map[string]string{"long_term_psk": b64.EncodeToString(psk)}})
}

func (b *bridge) send(m any) error {
	j, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return b.c.WriteText(j)
}

// goodbye says why before hanging up. It is allowed before activation.
func (b *bridge) goodbye(reason string) {
	_ = b.send(map[string]any{"type": "client/goodbye", "payload": map[string]string{"reason": reason}})
}

// open admits the player's messages, the held ones first. restate sends the player's newest state
// again when nothing held already carries it.
func (b *bridge) open(restate bool) {
	b.gate.Lock()
	defer b.gate.Unlock()
	for _, m := range b.held {
		if isState(m) {
			restate = false
		}
		if err := b.c.WriteText(m); err != nil {
			go b.end(err)
			return
		}
	}
	b.held, b.active = nil, true
	if restate && b.state != nil {
		if err := b.c.WriteText(b.state); err != nil {
			go b.end(err)
		}
	}
}

func isState(m []byte) bool {
	var env envelope
	return json.Unmarshal(m, &env) == nil && env.Type == "client/state"
}

// fromPlayer reads what the library sends and passes it on encrypted.
func (b *bridge) fromPlayer() {
	for {
		kind, data, err := b.plain.ReadMessage()
		if err != nil {
			b.end(err)
			return
		}
		if kind == websocket.BinaryMessage {
			err = b.forward(data, true)
		} else {
			err = b.player(data)
		}
		if err != nil {
			b.end(err)
			return
		}
	}
}

func (b *bridge) player(data []byte) error {
	var env envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return err
	}
	switch env.Type {
	case "client/hello":
		// It goes out once the server's has arrived, which is the order the specification has. The
		// library sends it first, so it waits here rather than holding up the library's writes.
		hello, err := b.rewriteHello(env.Payload)
		if err != nil {
			return err
		}
		b.gate.Lock()
		defer b.gate.Unlock()
		if !b.helloed {
			b.hello = hello
			return nil
		}
		return b.c.WriteText(hello)
	case "client/goodbye":
		return b.c.WriteText(data)
	case "client/state":
		// The library sends a state of its own as it starts, in the fields from before 1.0 and
		// without available; the player's own follows it, in this wire's.
		var p map[string]json.RawMessage
		if json.Unmarshal(env.Payload, &p) == nil {
			if _, ok := p["available"]; !ok {
				return nil
			}
		}
		b.gate.Lock()
		b.state = data
		b.gate.Unlock()
	}
	return b.forward(data, false)
}

// rewriteHello is the library's client/hello as an encrypted session has it: client_id and version are
// in client/init, and the pairing methods and unpaired access are said here.
func (b *bridge) rewriteHello(payload json.RawMessage) ([]byte, error) {
	var p map[string]any
	if err := json.Unmarshal(payload, &p); err != nil {
		return nil, err
	}
	delete(p, "client_id")
	delete(p, "version")
	// The artwork channels and the player's commands are said in client/state on this wire.
	delete(p, "artwork@v1_support")
	if ps, ok := p["player@v1_support"].(map[string]any); ok {
		delete(ps, "supported_commands")
	}
	p["supported_pair_methods"] = map[string]any{"pairing_psk": map[string]any{}}
	p["unpaired_access"] = map[string]bool{"enabled": b.opts.Unpaired}
	if b.opts.MAC != "" {
		info, _ := p["device_info"].(map[string]any)
		if info == nil {
			info = map[string]any{}
		}
		info["mac_address"] = b.opts.MAC
		p["device_info"] = info
	}
	return json.Marshal(map[string]any{"type": "client/hello", "payload": p})
}

func (b *bridge) forward(data []byte, binary bool) error {
	b.gate.Lock()
	defer b.gate.Unlock()
	if !b.active {
		if binary {
			return nil // nothing binary the player sends is worth holding
		}
		if len(b.held) >= heldMost {
			b.held = b.held[1:]
		}
		b.held = append(b.held, data)
		return nil
	}
	if binary {
		return b.c.WriteBinary(data)
	}
	return b.c.WriteText(data)
}

// pair is a connected pair of WebSockets in memory: the library's end and the bridge's.
func pair(ctx context.Context) (lib, plain *websocket.Conn, err error) {
	a, z := net.Pipe()
	got := make(chan *websocket.Conn, 1)
	up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		got <- c
	})}
	l := &oneConn{c: z, closed: make(chan struct{})}
	go func() { _ = srv.Serve(l) }()
	// The server is only the upgrade: once it has handed the connection over, it is shut, and the
	// connection lives on without it.
	defer srv.Close()

	d := websocket.Dialer{NetDialContext: func(context.Context, string, string) (net.Conn, error) { return a, nil }}
	lib, _, err = d.DialContext(ctx, "ws://sendspin/sendspin", nil)
	if err != nil {
		a.Close()
		z.Close()
		return nil, nil, err
	}
	select {
	case plain = <-got:
		return lib, plain, nil
	case <-ctx.Done():
		lib.Close()
		return nil, nil, ctx.Err()
	}
}

// oneConn is a listener that accepts one connection, then waits until it is closed.
type oneConn struct {
	c      net.Conn
	given  sync.Once
	shut   sync.Once
	closed chan struct{}
}

func (l *oneConn) Accept() (net.Conn, error) {
	var c net.Conn
	l.given.Do(func() { c = l.c })
	if c != nil {
		return c, nil
	}
	<-l.closed
	return nil, net.ErrClosed
}

func (l *oneConn) Close() error {
	l.shut.Do(func() { close(l.closed) })
	return nil
}

func (l *oneConn) Addr() net.Addr { return l.c.LocalAddr() }

// The player's audio chunk is the one binary message whose header the 1.0 specification lengthened: its
// type and timestamp are followed by send_ahead, a uint32 of how far ahead of its timestamp the server
// sent it. The library takes the 9-byte header a server before 1.0 sends, so on an encrypted connection
// (always a 1.0 server) those 4 bytes would be read as the start of the audio: every FLAC frame then
// begins 4 bytes early, and the decoder finds no sync code in any of them.
const (
	audioChunkType = 4
	audioHeaderOld = 1 + 8
	audioHeader10  = audioHeaderOld + 4
)

// playerAudio is a binary message from a 1.0 server as the library takes it: a player audio chunk
// without send_ahead, and any other message as it came.
func playerAudio(data []byte) []byte {
	if len(data) < audioHeader10 || data[0] != audioChunkType {
		return data
	}
	return append(data[:audioHeaderOld:audioHeaderOld], data[audioHeader10:]...)
}

// Artwork from a 1.0 server is not one message per picture with a 9-byte header, as the library takes
// it, but a transfer: an announce, then the picture in parts, each with only its type and a flags byte
// before it, and a cancel that is those two bytes alone. The session puts the parts back together
// (sendspin/artwork.go); all the bridge does is put 8 bytes of padding after the type, where the
// library looks for a timestamp, so the library hands on the rest whole. Without it a cancel, and a
// last part of under 7 bytes, are too short for the library and lost, and every other message loses
// its first 7 bytes.
const (
	artworkFirstType = 8
	artworkLastType  = 11
	artworkPadding   = 8
)

// forLibrary is a binary message from a 1.0 server as the library takes it.
func forLibrary(data []byte) []byte {
	if len(data) > 0 && data[0] >= artworkFirstType && data[0] <= artworkLastType {
		padded := make([]byte, 0, len(data)+artworkPadding)
		padded = append(padded, data[0])
		padded = append(padded, make([]byte, artworkPadding)...)
		return append(padded, data[1:]...)
	}
	return playerAudio(data)
}
