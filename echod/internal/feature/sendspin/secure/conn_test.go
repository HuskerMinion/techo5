package secure

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/flynn/noise"
	"github.com/gorilla/websocket"
)

// pipe is one end of an in-memory WebSocket: whole messages, in order.
type pipe struct {
	in  chan frame
	out chan frame
}

type frame struct {
	kind int
	b    []byte
}

func pipes() (*pipe, *pipe) {
	a, b := make(chan frame, 64), make(chan frame, 64)
	return &pipe{in: a, out: b}, &pipe{in: b, out: a}
}

func (p *pipe) ReadMessage() (int, []byte, error) {
	select {
	case f, ok := <-p.in:
		if !ok {
			return 0, nil, io.EOF
		}
		return f.kind, f.b, nil
	case <-time.After(5 * time.Second):
		return 0, nil, errors.New("test: read timed out")
	}
}

func (p *pipe) WriteMessage(k int, b []byte) error {
	p.out <- frame{k, append([]byte(nil), b...)}
	return nil
}

func (p *pipe) SetReadDeadline(time.Time) error { return nil }

// server is the initiator's side, written from the specification rather than from Conn.
type server struct {
	t    *testing.T
	w    Wire
	priv []byte
	pub  []byte
	id   string

	send, recv *noise.CipherState
	hash       []byte
}

func newServer(t *testing.T, w Wire) *server {
	k, err := suite.GenerateKeypair(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &server{t: t, w: w, priv: k.Private, pub: k.Public, id: b64.EncodeToString(k.Public)}
}

func (s *server) text() envelope {
	s.t.Helper()
	k, b, err := s.w.ReadMessage()
	if err != nil || k != websocket.TextMessage {
		s.t.Fatalf("server read: %v %d", err, k)
	}
	var e envelope
	if err := json.Unmarshal(b, &e); err != nil {
		s.t.Fatal(err)
	}
	return e
}

// handshake runs the server's side of the initial exchange keyed by psk, declared as category.
func (s *server) handshake(psk []byte, category Category) (clientInit []byte) {
	s.t.Helper()
	_, ci, err := s.w.ReadMessage()
	if err != nil {
		s.t.Fatal(err)
	}
	var e envelope
	_ = json.Unmarshal(ci, &e)
	var p struct {
		ClientID string `json:"client_id"`
		Suite    string `json:"suite"`
		Version  int    `json:"version"`
	}
	_ = json.Unmarshal(e.Payload, &p)
	if e.Type != "client/init" || p.Suite != Suite || p.Version != 1 {
		s.t.Fatalf("client/init: %s", ci)
	}
	clientKey, err := peerKey(p.ClientID)
	if err != nil {
		s.t.Fatal(err)
	}
	si := []byte(`{"type":"server/init","payload":{"server_id":"` + s.id + `","version":1}}`)
	_ = s.w.WriteMessage(websocket.TextMessage, si)

	hs := s.initiator(clientKey, append(append([]byte(nil), ci...), si...), psk)
	m1, _, _, err := hs.WriteMessage(nil, []byte(`{"psk_id":"`+pskID(psk)+`","psk_category":"`+string(category)+`"}`))
	if err != nil {
		s.t.Fatal(err)
	}
	_ = s.w.WriteMessage(websocket.TextMessage, handshakeMsg(m1))

	m2 := s.text()
	s.finish(hs, m2)
	return ci
}

func (s *server) initiator(clientKey, prologue, psk []byte) *noise.HandshakeState {
	hs, err := noise.NewHandshakeState(noise.Config{
		CipherSuite: suite, Pattern: noise.HandshakeKK, Initiator: true, Prologue: prologue,
		PresharedKey: psk, PresharedKeyPlacement: 2,
		StaticKeypair: noise.DHKey{Private: s.priv, Public: s.pub}, PeerStatic: clientKey,
	})
	if err != nil {
		s.t.Fatal(err)
	}
	return hs
}

func (s *server) finish(hs *noise.HandshakeState, m2 envelope) {
	s.t.Helper()
	if m2.Type != "noise/handshake" {
		s.t.Fatalf("message 2: %s", m2.Type)
	}
	var d struct {
		Data string `json:"data"`
	}
	_ = json.Unmarshal(m2.Payload, &d)
	raw, _ := b64.DecodeString(d.Data)
	pt, toClient, toServer, err := hs.ReadMessage(nil, raw)
	if err != nil {
		s.t.Fatalf("message 2: %v", err)
	}
	if string(pt) != "{}" {
		s.t.Fatalf("message 2 payload %q", pt)
	}
	s.send, s.recv, s.hash = toClient, toServer, hs.ChannelBinding()
}

func handshakeMsg(m []byte) []byte {
	b, _ := json.Marshal(map[string]any{"type": "noise/handshake", "payload": map[string]string{"data": b64.EncodeToString(m)}})
	return b
}

func (s *server) writeJSON(b []byte) {
	ct, err := s.send.Encrypt(nil, nil, append([]byte{typeJSON}, b...))
	if err != nil {
		s.t.Fatal(err)
	}
	_ = s.w.WriteMessage(websocket.BinaryMessage, ct)
}

func (s *server) readPlain() []byte {
	s.t.Helper()
	k, ct, err := s.w.ReadMessage()
	if err != nil || k != websocket.BinaryMessage {
		s.t.Fatalf("server read: %v %d", err, k)
	}
	pt, err := s.recv.Decrypt(nil, nil, ct)
	if err != nil {
		s.t.Fatal(err)
	}
	return pt
}

func store(t *testing.T) *Store {
	st, err := Open(filepath.Join(t.TempDir(), "keys.json"))
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func TestAnUnpairedServerIsAnsweredUnderTheSentinel(t *testing.T) {
	st := store(t)
	dev, srv := pipes()
	s := newServer(t, srv)
	done := make(chan *Conn)
	go func() {
		c, err := Handshake(dev, st)
		if err != nil {
			t.Error(err)
		}
		done <- c
	}()
	s.handshake(sentinelPSK, Sentinel)
	c := <-done
	if c == nil {
		t.FailNow()
	}
	if c.Category != Sentinel || c.Mismatch || c.ServerID != s.id {
		t.Fatalf("got %+v", c)
	}

	s.writeJSON([]byte(`{"type":"server/hello"}`))
	k, b, err := c.Read()
	if err != nil || k != websocket.TextMessage || string(b) != `{"type":"server/hello"}` {
		t.Fatalf("read %d %q %v", k, b, err)
	}
	if err := c.WriteText([]byte(`{"type":"client/hello"}`)); err != nil {
		t.Fatal(err)
	}
	if got := s.readPlain(); !bytes.Equal(got, append([]byte{0}, `{"type":"client/hello"}`...)) {
		t.Fatalf("server got %q", got)
	}
}

func TestAPSKTheDeviceLostFallsBackToTheSentinel(t *testing.T) {
	st := store(t)
	dev, srv := pipes()
	s := newServer(t, srv)
	done := make(chan *Conn)
	go func() {
		c, err := Handshake(dev, st)
		if err != nil {
			t.Error(err)
		}
		done <- c
	}()
	// The server names a long-term PSK the device does not hold; the device answers under the
	// Sentinel, and a server checking message 2 against its own PSK fails, as it should.
	lost := bytes.Repeat([]byte{7}, 32)
	_, ci, _ := srv.ReadMessage()
	var e envelope
	_ = json.Unmarshal(ci, &e)
	var p struct {
		ClientID string `json:"client_id"`
	}
	_ = json.Unmarshal(e.Payload, &p)
	ck, _ := peerKey(p.ClientID)
	si := []byte(`{"type":"server/init","payload":{"server_id":"` + s.id + `","version":1}}`)
	_ = srv.WriteMessage(websocket.TextMessage, si)
	prologue := append(append([]byte(nil), ci...), si...)
	hs := s.initiator(ck, prologue, lost)
	m1, _, _, _ := hs.WriteMessage(nil, []byte(`{"psk_id":"`+pskID(lost)+`","psk_category":"lt"}`))
	_ = srv.WriteMessage(websocket.TextMessage, handshakeMsg(m1))
	m2 := s.text()
	c := <-done
	if c == nil || c.Category != Sentinel || !c.Mismatch {
		t.Fatalf("got %+v", c)
	}
	var d struct {
		Data string `json:"data"`
	}
	_ = json.Unmarshal(m2.Payload, &d)
	raw, _ := b64.DecodeString(d.Data)
	if _, _, _, err := hs.ReadMessage(nil, raw); err == nil {
		t.Fatal("message 2 verified under the lost PSK")
	}
}

func TestAPairedServerIsRecognizedAndAnotherIsNot(t *testing.T) {
	st := store(t)
	dev, srv := pipes()
	s := newServer(t, srv)
	lt := bytes.Repeat([]byte{9}, 32)
	if err := st.remember(s.id, lt); err != nil {
		t.Fatal(err)
	}
	done := make(chan *Conn)
	go func() {
		c, _ := Handshake(dev, st)
		done <- c
	}()
	s.handshake(lt, LongTerm)
	if c := <-done; c == nil || c.Category != LongTerm {
		t.Fatalf("got %+v", c)
	}

	// Another server naming the same PSK is a misbinding: the handshake fails.
	dev2, srv2 := pipes()
	other := newServer(t, srv2)
	errc := make(chan error, 1)
	go func() {
		_, err := Handshake(dev2, st)
		errc <- err
		close(dev2.out)
	}()
	_, ci, _ := srv2.ReadMessage()
	var e envelope
	_ = json.Unmarshal(ci, &e)
	var p struct {
		ClientID string `json:"client_id"`
	}
	_ = json.Unmarshal(e.Payload, &p)
	ck, _ := peerKey(p.ClientID)
	si := []byte(`{"type":"server/init","payload":{"server_id":"` + other.id + `","version":1}}`)
	_ = srv2.WriteMessage(websocket.TextMessage, si)
	hs := other.initiator(ck, append(append([]byte(nil), ci...), si...), lt)
	m1, _, _, _ := hs.WriteMessage(nil, []byte(`{"psk_id":"`+pskID(lt)+`","psk_category":"lt"}`))
	_ = srv2.WriteMessage(websocket.TextMessage, handshakeMsg(m1))
	if err := <-errc; err == nil || !strings.Contains(err.Error(), "another server") {
		t.Fatalf("err = %v", err)
	}
}

func TestAServerFromBeforeEncryptionIsToldApart(t *testing.T) {
	st := store(t)
	dev, srv := pipes()
	errc := make(chan error, 1)
	go func() {
		_, err := Handshake(dev, st)
		errc <- err
	}()
	_, _, _ = srv.ReadMessage()
	_ = srv.WriteMessage(websocket.TextMessage, []byte(`{"type":"server/hello","payload":{}}`))
	if err := <-errc; !errors.Is(err, ErrNotEncrypted) {
		t.Fatalf("err = %v", err)
	}
}

func TestALongMessageCrossesInFragments(t *testing.T) {
	st := store(t)
	dev, srv := pipes()
	s := newServer(t, srv)
	done := make(chan *Conn)
	go func() {
		c, _ := Handshake(dev, st)
		done <- c
	}()
	s.handshake(sentinelPSK, Sentinel)
	c := <-done

	big := make([]byte, 3*maxPlain+5)
	big[0] = 8 // an artwork message
	for i := 1; i < len(big); i++ {
		big[i] = byte(i)
	}
	go func() { _ = c.WriteBinary(big) }()
	var got []byte
	for {
		f := s.readPlain()
		if f[0] != typeFragment {
			t.Fatalf("type %d", f[0])
		}
		if f[1]&fragFirst != 0 {
			got = append(got, f[2])
			got = append(got, f[3:]...)
		} else {
			got = append(got, f[2:]...)
		}
		if f[1]&fragLast != 0 {
			break
		}
	}
	if !bytes.Equal(got, big) {
		t.Fatal("reassembled message differs")
	}

	// And the other way.
	for _, fr := range [][]byte{
		append([]byte{typeFragment, fragFirst, 8}, big[1:40000]...),
		append([]byte{typeFragment, 0}, big[40000:70000]...),
		append([]byte{typeFragment, fragLast}, big[70000:]...),
	} {
		ct, _ := s.send.Encrypt(nil, nil, fr)
		_ = srv.WriteMessage(websocket.BinaryMessage, ct)
	}
	k, b, err := c.Read()
	if err != nil || k != websocket.BinaryMessage || !bytes.Equal(b, big) {
		t.Fatalf("read %d %d %v", k, len(b), err)
	}
}

func TestTheTokenMatchesTheSpecificationsVector(t *testing.T) {
	st := &Store{pub: make([]byte, 32)}
	st.file.PairingPSK = make([]byte, 32)
	for i := range 32 {
		st.pub[i] = byte(i)
		st.file.PairingPSK[i] = byte(0xe0 + i)
	}
	const want = "SP:0AAAQEAYEAUDAOCAJBIFQYDIOB4IBCEQTCQKRMFYYDENBWHA5DYP6BYPC4PSOLZXH5DU6V97M5XXO74HR6LZ7J5PW674PT6X37T6757Y"
	if got := st.Token(); got != want {
		t.Fatalf("token\n got %s\nwant %s", got, want)
	}
}

func TestTheSentinelIsThePublishedOne(t *testing.T) {
	if got := pskID(sentinelPSK); got != "GFsV9tLaSQm9HcFWpKsgYQOr7wFTvNUtkmFwuVz3zoo" {
		t.Fatal(got)
	}
}

func TestTheStoreKeepsItsIdentityAndEvictsTheOldest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys.json")
	a, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := range capacity + 1 {
		if err := a.remember(strings.Repeat(string(rune('a'+i)), 43), bytes.Repeat([]byte{byte(i)}, 32)); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Millisecond)
	}
	b, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if b.ClientID() != a.ClientID() || b.Token() != a.Token() {
		t.Fatal("the identity changed on reopening")
	}
	if len(b.file.Records) != capacity || b.file.Records[0].ServerID[0] != 'b' {
		t.Fatalf("records %d, first %q", len(b.file.Records), b.file.Records[0].ServerID)
	}
}

// errWire is a wire whose wait for server/init ends in err.
type errWire struct{ err error }

func (w errWire) ReadMessage() (int, []byte, error) { return 0, nil, w.err }
func (w errWire) WriteMessage(int, []byte) error    { return nil }
func (w errWire) SetReadDeadline(time.Time) error   { return nil }

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

func TestOnlyAHangUpOrSilenceIsAServerFromBeforeEncryption(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		old  bool
	}{
		{"closed", &websocket.CloseError{Code: websocket.CloseProtocolError}, true},
		{"eof", io.EOF, true},
		{"silent", timeoutErr{}, true},
		{"reset", errors.New("connection reset by peer"), false},
	} {
		_, err := Handshake(errWire{tc.err}, store(t))
		if errors.Is(err, ErrNotEncrypted) != tc.old {
			t.Errorf("%s: err = %v", tc.name, err)
		}
	}
}

func TestAPairedDeviceDoesNotTakeAHangUpForAServerFromBeforeEncryption(t *testing.T) {
	st := store(t)
	if err := st.remember(strings.Repeat("a", 43), bytes.Repeat([]byte{1}, 32)); err != nil {
		t.Fatal(err)
	}
	for _, e := range []error{io.EOF, timeoutErr{}} {
		if _, err := Handshake(errWire{e}, st); err == nil || errors.Is(err, ErrNotEncrypted) {
			t.Errorf("%v: err = %v", e, err)
		}
	}
}

func TestAPairedDeviceDoesNotTakeJunkForAServerFromBeforeEncryption(t *testing.T) {
	st := store(t)
	if err := st.remember(strings.Repeat("a", 43), bytes.Repeat([]byte{1}, 32)); err != nil {
		t.Fatal(err)
	}
	for _, reply := range [][]byte{[]byte("not json"), []byte(`{"type":"server/hello","payload":{}}`)} {
		dev, srv := pipes()
		errc := make(chan error, 1)
		go func() {
			_, err := Handshake(dev, st)
			errc <- err
		}()
		_, _, _ = srv.ReadMessage()
		_ = srv.WriteMessage(websocket.TextMessage, reply)
		if err := <-errc; err == nil || errors.Is(err, ErrNotEncrypted) {
			t.Errorf("%s: err = %v", reply, err)
		}
	}
}
