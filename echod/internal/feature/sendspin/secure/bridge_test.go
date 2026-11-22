package secure

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// libRead is what the library would read next, as type and the whole message.
func libRead(t *testing.T, lib *websocket.Conn) (string, []byte) {
	t.Helper()
	_ = lib.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, b, err := lib.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	var e envelope
	_ = json.Unmarshal(b, &e)
	return e.Type, b
}

func (s *server) readJSON() envelope {
	s.t.Helper()
	pt := s.readPlain()
	if pt[0] != typeJSON {
		s.t.Fatalf("type %d", pt[0])
	}
	var e envelope
	if err := json.Unmarshal(pt[1:], &e); err != nil {
		s.t.Fatal(err)
	}
	return e
}

// rehandshake is the server's in-band re-handshake to psk.
func (s *server) rehandshake(clientKey, psk []byte, category Category) {
	s.t.Helper()
	hs := s.initiator(clientKey, s.hash, psk)
	m1, _, _, err := hs.WriteMessage(nil, []byte(`{"psk_id":"`+pskID(psk)+`","psk_category":"`+string(category)+`"}`))
	if err != nil {
		s.t.Fatal(err)
	}
	s.writeJSON(handshakeMsg(m1))
	s.finish(hs, s.readJSON())
}

func setup(t *testing.T, st *Store, opts Options, psk []byte, cat Category) (*server, *websocket.Conn, chan *websocket.Conn) {
	t.Helper()
	libc := make(chan *websocket.Conn, 1)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		c, err := Handshake(ws, st)
		if err != nil {
			t.Error(err)
			return
		}
		lib, done, err := Bridge(ctx, c, ws, opts)
		if err != nil {
			t.Error(err)
			return
		}
		libc <- lib
		<-done
	}))
	t.Cleanup(hs.Close)
	ws, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(hs.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ws.Close() })
	s := newServer(t, ws)
	s.handshake(psk, cat)
	return s, ws, libc
}

func lib(t *testing.T, libc chan *websocket.Conn) *websocket.Conn {
	select {
	case l := <-libc:
		return l
	case <-time.After(5 * time.Second):
		t.Fatal("no library connection")
		return nil
	}
}

func TestTheHelloWaitsForTheServersAndThePlayerWaitsForActivation(t *testing.T) {
	st := store(t)
	s, _, libc := setup(t, st, Options{Unpaired: true, MAC: "aa:bb:cc:dd:ee:ff"}, sentinelPSK, Sentinel)
	l := lib(t, libc)

	// The library sends its hello first, then a state of its own without available, which is
	// dropped, then the player's.
	_ = l.WriteMessage(websocket.TextMessage, []byte(`{"type":"client/hello","payload":{"client_id":"x","version":1,"name":"Kitchen"}}`))
	_ = l.WriteMessage(websocket.TextMessage, []byte(`{"type":"client/state","payload":{"player":{"state":"synchronized"}}}`))
	_ = l.WriteMessage(websocket.TextMessage, []byte(`{"type":"client/state","payload":{"available":false}}`))

	s.writeJSON([]byte(`{"type":"server/hello","payload":{"name":"MA"}}`))
	if typ, _ := libRead(t, l); typ != "server/hello" {
		t.Fatalf("library got %s", typ)
	}
	h := s.readJSON()
	if h.Type != "client/hello" {
		t.Fatalf("server got %s", h.Type)
	}
	var p map[string]any
	_ = json.Unmarshal(h.Payload, &p)
	if _, ok := p["client_id"]; ok {
		t.Error("client/hello still carries client_id")
	}
	if _, ok := p["version"]; ok {
		t.Error("client/hello still carries version")
	}
	if ua, _ := p["unpaired_access"].(map[string]any); ua["enabled"] != true {
		t.Errorf("unpaired_access = %v", p["unpaired_access"])
	}
	if m, _ := p["supported_pair_methods"].(map[string]any); m["pairing_psk"] == nil {
		t.Errorf("supported_pair_methods = %v", p["supported_pair_methods"])
	}
	if di, _ := p["device_info"].(map[string]any); di["mac_address"] != "aa:bb:cc:dd:ee:ff" {
		t.Errorf("device_info = %v", p["device_info"])
	}

	// Nothing more until server/activate; then the held state, then the rest as it comes.
	s.writeJSON([]byte(`{"type":"server/activate","payload":{"activities":["playback"],"active_roles":["player@v1"]}}`))
	if e := s.readJSON(); e.Type != "client/state" {
		t.Fatalf("server got %s", e.Type)
	}
	_ = l.WriteMessage(websocket.TextMessage, []byte(`{"type":"client/time","payload":{}}`))
	if e := s.readJSON(); e.Type != "client/time" {
		t.Fatalf("server got %s", e.Type)
	}

	// The server's own messages and binary ones reach the library as they are.
	s.writeJSON([]byte(`{"type":"server/time","payload":{}}`))
	if typ, _ := libRead(t, l); typ != "server/time" {
		t.Fatalf("library got %s", typ)
	}
	ct, _ := s.send.Encrypt(nil, nil, []byte{4, 0, 0, 0, 0, 0, 0, 0, 1, 0xaa})
	_ = s.w.WriteMessage(websocket.BinaryMessage, ct)
	_ = l.SetReadDeadline(time.Now().Add(5 * time.Second))
	k, b, err := l.ReadMessage()
	if err != nil || k != websocket.BinaryMessage || len(b) != 10 || b[9] != 0xaa {
		t.Fatalf("library got %d %x %v", k, b, err)
	}
}

func TestWithoutUnpairedAccessAnUnpairedServerIsToldToPair(t *testing.T) {
	st := store(t)
	s, _, libc := setup(t, st, Options{Unpaired: false}, sentinelPSK, Sentinel)
	l := lib(t, libc)
	_ = l.WriteMessage(websocket.TextMessage, []byte(`{"type":"client/hello","payload":{}}`))
	s.writeJSON([]byte(`{"type":"server/hello","payload":{"name":"MA"}}`))
	libRead(t, l)
	s.readJSON()
	s.writeJSON([]byte(`{"type":"server/activate","payload":{"activities":["playback"],"active_roles":["player@v1"]}}`))
	e := s.readJSON()
	if e.Type != "client/goodbye" || !strings.Contains(string(e.Payload), "pairing_required") {
		t.Fatalf("server got %s %s", e.Type, e.Payload)
	}
}

func TestPairingByTokenEndsPairedAfterTheRehandshake(t *testing.T) {
	st := store(t)
	s, _, libc := setup(t, st, Options{Unpaired: true}, st.file.PairingPSK, Pairing)
	l := lib(t, libc)
	clientKey, _ := peerKey(st.ClientID())

	_ = l.WriteMessage(websocket.TextMessage, []byte(`{"type":"client/hello","payload":{}}`))
	s.writeJSON([]byte(`{"type":"server/hello","payload":{"name":"MA"}}`))
	libRead(t, l)
	s.readJSON()

	s.writeJSON([]byte(`{"type":"server/activate","payload":{"activities":["pairing"],"active_roles":[],"pairing":{"method":"pairing_psk"}}}`))
	if e := s.readJSON(); e.Type != "client/pair-init" || !strings.Contains(string(e.Payload), `"pairing_index":1`) {
		t.Fatalf("server got %s %s", e.Type, e.Payload)
	}
	fin := s.readJSON()
	var f struct {
		LongTermPSK string `json:"long_term_psk"`
	}
	_ = json.Unmarshal(fin.Payload, &f)
	lt, err := b64.DecodeString(f.LongTermPSK)
	if fin.Type != "client/pair-finalize" || err != nil || len(lt) != 32 {
		t.Fatalf("server got %s %s", fin.Type, fin.Payload)
	}
	// The player's state, sent while pairing, before any roles are on.
	_ = l.WriteMessage(websocket.TextMessage, []byte(`{"type":"client/state","payload":{"available":true}}`))
	if e := s.readJSON(); e.Type != "client/state" {
		t.Fatalf("server got %s", e.Type)
	}
	s.writeJSON([]byte(`{"type":"server/pair-finalize","payload":{}}`))
	s.rehandshake(clientKey, lt, LongTerm)
	s.writeJSON([]byte(`{"type":"server/activate","payload":{"activities":["playback"],"active_roles":["player@v1"]}}`))

	// The roles are on under the new keys, so the state goes again unasked.
	if e := s.readJSON(); e.Type != "client/state" {
		t.Fatalf("server got %s", e.Type)
	}
	if got, ok := st.resolve(pskID(lt), LongTerm); !ok || got.serverID != s.id {
		t.Fatalf("record %+v %v", got, ok)
	}
}
