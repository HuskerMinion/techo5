package secure

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/flynn/noise"
	"github.com/gorilla/websocket"
)

// Suite is the cipher suite this device picks. A server supports both; ChaCha20-Poly1305 is the one
// that is fast in software on every Echo's CPU, whether or not it has the ARMv8 crypto extensions.
const Suite = "25519_ChaChaPoly_SHA256"

var suite = noise.NewCipherSuite(noise.DH25519, noise.CipherChaChaPoly, noise.HashSHA256)

// handshakeTimeout bounds each wait for the server's next handshake message, as the specification
// recommends.
const handshakeTimeout = 30 * time.Second

// initTimeout is the wait for server/init, which a server sends at once: a server still silent after
// this is one from before encryption, waiting for a client/hello.
const initTimeout = 10 * time.Second

// Transport message types, the first byte of a decrypted message.
const (
	typeJSON     = 0
	typeFragment = 1

	fragLast  = 0x01
	fragFirst = 0x02
)

// maxPlain is the most one Noise transport message carries: 65535 bytes less the 16-byte tag.
const maxPlain = 65535 - 16

// maxMessage bounds a message reassembled from fragments. The largest the server sends is a picture,
// and the device asks for 800 pixels a side.
const maxMessage = 8 << 20

// Wire is the WebSocket the handshake and transport run over: a *websocket.Conn.
type Wire interface {
	ReadMessage() (int, []byte, error)
	WriteMessage(int, []byte) error
	SetReadDeadline(time.Time) error
}

// ErrNotEncrypted is a server that answered client/init with something other than server/init or
// server/error, or hung up on it: a server from before encryption, which knows only client/hello.
var ErrNotEncrypted = errors.New("the server does not speak encrypted Sendspin")

// ErrHungUp is an ErrNotEncrypted from a server that hung up on client/init or said nothing until
// initTimeout. A server from before encryption does that, but so does a Music Assistant that is
// restarting or busy, and a network that dropped the connection; a server that answered with
// something else said in so many words that it is not one. The caller, which knows whether this
// server has spoken encrypted before, tells the two apart (#721).
var ErrHungUp = fmt.Errorf("%w: it hung up on client/init or said nothing", ErrNotEncrypted)

// hungUpOrSilent is a wait for server/init that ended as a server from before encryption ends it:
// the server closed the connection, or said nothing until the deadline.
func hungUpOrSilent(err error) bool {
	var ce *websocket.CloseError
	var ne net.Error
	return errors.As(err, &ce) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		(errors.As(err, &ne) && ne.Timeout())
}

// RefusedError is a server/error: the server could not take this client/init.
type RefusedError struct{ Reason string }

func (e *RefusedError) Error() string { return "the server refused the handshake: " + e.Reason }

// Conn is a connection in Noise transport mode.
type Conn struct {
	w     Wire
	store *Store

	// ServerID is the server's static key, from server/init.
	ServerID string

	// Category is how the matched PSK is used: long-term on a paired connection, the pairing PSK while
	// pairing by token, and the Sentinel on an unpaired one. Mismatch is a Sentinel standing in for a
	// credential the device could not use.
	Category Category
	Mismatch bool

	// hash is the handshake hash h: a re-handshake's prologue.
	hash []byte

	// recv is the read side's, used only by the reader. send is under mu, which also keeps a fragmented
	// message from being interleaved with another.
	recv *noise.CipherState
	mu   sync.Mutex
	send *noise.CipherState

	// reassembly of a fragmented message
	frag     []byte
	fragType byte
	fragging bool
}

type envelope struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

type msg1Payload struct {
	PSKID       string   `json:"psk_id"`
	PSKCategory Category `json:"psk_category"`
}

// Handshake runs the initial exchange as the client: client/init, server/init, then the two Noise
// messages. The device is the Noise responder whichever side dialed.
func Handshake(w Wire, st *Store) (*Conn, error) {
	init, err := json.Marshal(map[string]any{
		"type": "client/init",
		"payload": map[string]any{
			"client_id": st.ClientID(),
			"version":   1,
			"suite":     Suite,
		},
	})
	if err != nil {
		return nil, err
	}
	if err := w.WriteMessage(websocket.TextMessage, init); err != nil {
		return nil, err
	}

	// A server from before encryption reads client/init as a broken client/hello: it hangs up, or
	// waits for a hello that never comes. Either is taken as that, and if it was only the network, the
	// unencrypted connection that follows fails to start and the next one is encrypted again. Any
	// other failure is the network's. A device paired with a server takes neither as a server from
	// before encryption: one dropped frame is not to turn its encryption off. An unpaired one is told
	// which it was (ErrHungUp), as a server it has spoken encrypted with is not taken for an old one
	// on a hang-up either.
	if err := w.SetReadDeadline(time.Now().Add(initTimeout)); err != nil {
		return nil, err
	}
	kind, reply, err := w.ReadMessage()
	_ = w.SetReadDeadline(time.Time{})
	if err != nil {
		if hungUpOrSilent(err) && !st.Paired() {
			return nil, fmt.Errorf("%w: %v", ErrHungUp, err)
		}
		return nil, fmt.Errorf("waiting for server/init: %w", err)
	}
	// A paired device never takes a reply for a server from before encryption: whoever answered with
	// something else, junk included, could otherwise turn its encryption off for a day.
	notEncrypted := func(what string) error {
		if st.Paired() {
			return fmt.Errorf("waiting for server/init: %s, and this device is paired", what)
		}
		return ErrNotEncrypted
	}
	var env envelope
	if kind != websocket.TextMessage || json.Unmarshal(reply, &env) != nil {
		return nil, notEncrypted("a reply that is not JSON")
	}
	switch env.Type {
	case "server/init":
	case "server/error":
		var p struct {
			Reason string `json:"reason"`
		}
		_ = json.Unmarshal(env.Payload, &p)
		return nil, &RefusedError{Reason: p.Reason}
	default:
		return nil, notEncrypted(fmt.Sprintf("%q", env.Type))
	}
	var si struct {
		ServerID string `json:"server_id"`
		Version  int    `json:"version"`
	}
	if err := json.Unmarshal(env.Payload, &si); err != nil {
		return nil, fmt.Errorf("server/init: %w", err)
	}
	if si.Version != 1 {
		return nil, fmt.Errorf("server/init: version %d", si.Version)
	}
	serverKey, err := peerKey(si.ServerID)
	if err != nil {
		return nil, fmt.Errorf("server/init: server_id: %w", err)
	}

	// The prologue is the two init messages exactly as they crossed the wire.
	prologue := append(append([]byte(nil), init...), reply...)
	c := &Conn{w: w, store: st, ServerID: si.ServerID}

	_, data, err := readHandshake(w)
	if err != nil {
		return nil, err
	}
	if err := c.respond(serverKey, prologue, data, true, func(b []byte) error {
		return w.WriteMessage(websocket.TextMessage, b)
	}); err != nil {
		return nil, err
	}
	return c, nil
}

// respond takes the server's Noise message 1 and answers it with message 2 through send, keying this
// connection. fallback is the initial handshake's: a PSK the device does not hold is answered under
// the Sentinel, so the session can carry a re-pairing rather than end here. A re-handshake has none.
func (c *Conn) respond(serverKey, prologue, msg1 []byte, fallback bool, send func([]byte) error) error {
	priv := c.store.private()
	pub, err := publicKey(priv)
	if err != nil {
		return err
	}
	hs, err := noise.NewHandshakeState(noise.Config{
		CipherSuite:           suite,
		Pattern:               noise.HandshakeKK,
		Initiator:             false,
		Prologue:              prologue,
		PresharedKeyPlacement: 2,
		StaticKeypair:         noise.DHKey{Private: priv, Public: pub},
		PeerStatic:            serverKey,
	})
	if err != nil {
		return err
	}

	pt, _, _, err := hs.ReadMessage(nil, msg1)
	if err != nil {
		return fmt.Errorf("noise message 1: %w", err)
	}
	var p msg1Payload
	if err := json.Unmarshal(pt, &p); err != nil || !p.PSKCategory.valid() || len(p.PSKID) != 43 {
		return errors.New("noise message 1: a malformed payload")
	}

	got, ok := c.store.resolve(p.PSKID, p.PSKCategory)
	mismatch := false
	if !ok {
		if !fallback {
			return errors.New("noise message 1: a PSK this device does not hold")
		}
		got, mismatch = resolved{psk: sentinelPSK, category: Sentinel}, true
	}
	// A long-term PSK is bound to the server it was agreed with. A match for another server is a
	// misbinding, not a miss, and never falls back.
	if got.category == LongTerm && got.serverID != c.ServerID {
		return errors.New("noise message 1: a PSK agreed with another server")
	}
	if err := hs.SetPresharedKey(got.psk); err != nil {
		return err
	}

	// Message 2's payload is the two bytes {}, not an empty payload.
	msg2, toServer, toClient, err := hs.WriteMessage(nil, []byte("{}"))
	if err != nil {
		return err
	}
	b, err := json.Marshal(map[string]any{
		"type":    "noise/handshake",
		"payload": map[string]string{"data": b64.EncodeToString(msg2)},
	})
	if err != nil {
		return err
	}
	// Message 2 and the new send keys go together under the lock: a message written between them
	// would go out under the old keys, which the server has already left.
	// The responder sends with the second cipher state and reads with the first.
	c.mu.Lock()
	err = send(b)
	if err == nil {
		c.send = toClient
	}
	c.mu.Unlock()
	if err != nil {
		return err
	}
	c.recv = toServer
	c.hash = hs.ChannelBinding()
	c.Category, c.Mismatch = got.category, mismatch
	if got.category == LongTerm {
		c.store.used(c.ServerID)
	}
	return nil
}

// Rehandshake answers the server's in-band re-handshake, whose message 1 arrived as an encrypted
// noise/handshake (payload is its JSON payload). Message 2 goes out under the old keys; everything
// after it is under the new ones.
func (c *Conn) Rehandshake(payload json.RawMessage) error {
	var p struct {
		Data string `json:"data"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return err
	}
	msg1, err := b64.DecodeString(p.Data)
	if err != nil {
		return err
	}
	serverKey, err := peerKey(c.ServerID)
	if err != nil {
		return err
	}
	// respond holds c.mu while it sends message 2.
	return c.respond(serverKey, c.hash, msg1, false, func(b []byte) error {
		return c.writeLocked(append([]byte{typeJSON}, b...))
	})
}

// readWithin reads one message, giving up after the handshake timeout.
func readWithin(w Wire) (int, []byte, error) {
	if err := w.SetReadDeadline(time.Now().Add(handshakeTimeout)); err != nil {
		return 0, nil, err
	}
	defer w.SetReadDeadline(time.Time{})
	return w.ReadMessage()
}

// readHandshake reads a cleartext noise/handshake and returns the Noise bytes it carries.
func readHandshake(w Wire) (string, []byte, error) {
	kind, b, err := readWithin(w)
	if err != nil {
		return "", nil, err
	}
	var env envelope
	if kind != websocket.TextMessage || json.Unmarshal(b, &env) != nil || env.Type != "noise/handshake" {
		return "", nil, errors.New("expected noise/handshake")
	}
	var p struct {
		Data string `json:"data"`
	}
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return "", nil, err
	}
	data, err := b64.DecodeString(p.Data)
	return env.Type, data, err
}

// WriteText sends a JSON message.
func (c *Conn) WriteText(b []byte) error {
	return c.write(append([]byte{typeJSON}, b...))
}

// WriteBinary sends a role's binary message, its type byte first, as it would travel unencrypted.
func (c *Conn) WriteBinary(b []byte) error {
	if len(b) == 0 || b[0] == typeFragment || b[0] == 2 || b[0] == 3 {
		return errors.New("a binary message type the transport reserves")
	}
	return c.write(b)
}

func (c *Conn) write(plain []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.writeLocked(plain)
}

// writeLocked is write with c.mu held.
func (c *Conn) writeLocked(plain []byte) error {
	if len(plain) <= maxPlain {
		return c.seal(plain)
	}
	// Too long for one Noise message: fragments of type 1, a flags byte, and on the first the
	// message's own type.
	orig, data := plain[0], plain[1:]
	first := true
	for first || len(data) > 0 {
		var head []byte
		room := maxPlain - 2
		if first {
			head, room = []byte{typeFragment, fragFirst, orig}, maxPlain-3
		} else {
			head = []byte{typeFragment, 0}
		}
		n := min(room, len(data))
		if n == len(data) {
			head[1] |= fragLast
		}
		if err := c.seal(append(head, data[:n]...)); err != nil {
			return err
		}
		data, first = data[n:], false
	}
	return nil
}

func (c *Conn) seal(plain []byte) error {
	ct, err := c.send.Encrypt(nil, nil, plain)
	if err != nil {
		return err
	}
	return c.w.WriteMessage(websocket.BinaryMessage, ct)
}

// Read returns the next message: websocket.TextMessage with the JSON of a JSON message, or
// websocket.BinaryMessage with a role's binary message, type byte first. Anything that fails to
// decrypt ends the connection: there is no recovering a transport out of step.
func (c *Conn) Read() (int, []byte, error) {
	for {
		kind, ct, err := c.w.ReadMessage()
		if err != nil {
			return 0, nil, err
		}
		if kind != websocket.BinaryMessage {
			return 0, nil, errors.New("a cleartext message in transport mode")
		}
		plain, err := c.recv.Decrypt(nil, nil, ct)
		if err != nil {
			return 0, nil, fmt.Errorf("transport: %w", err)
		}
		if len(plain) == 0 {
			return 0, nil, errors.New("transport: an empty message")
		}
		if plain[0] == typeFragment {
			whole, done, err := c.fragment(plain)
			if err != nil || !done {
				if err != nil {
					return 0, nil, err
				}
				continue
			}
			plain = whole
		} else if c.fragging {
			return 0, nil, errors.New("transport: a message inside a fragmented one")
		}
		if plain[0] == typeJSON {
			return websocket.TextMessage, plain[1:], nil
		}
		return websocket.BinaryMessage, plain, nil
	}
}

func (c *Conn) fragment(plain []byte) ([]byte, bool, error) {
	if len(plain) < 2 || plain[1]&^(fragFirst|fragLast) != 0 {
		return nil, false, errors.New("transport: a malformed fragment")
	}
	flags, data := plain[1], plain[2:]
	if flags&fragFirst != 0 {
		if c.fragging || len(data) < 1 || data[0] == typeFragment {
			return nil, false, errors.New("transport: a malformed first fragment")
		}
		c.fragging, c.fragType, c.frag, data = true, data[0], nil, data[1:]
	} else if !c.fragging {
		return nil, false, errors.New("transport: a fragment with nothing to continue")
	}
	if len(c.frag)+len(data) > maxMessage {
		return nil, false, errors.New("transport: a fragmented message too large")
	}
	c.frag = append(c.frag, data...)
	if flags&fragLast == 0 {
		return nil, false, nil
	}
	whole := append([]byte{c.fragType}, c.frag...)
	c.fragging, c.frag = false, nil
	return whole, true, nil
}
