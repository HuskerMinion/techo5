package secure

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// A hang-up or silence from an unpaired device's server is told apart from a reply that is not
// server/init, so the caller can hold a server that has spoken encrypted to it (#721).
func TestAHangUpIsToldApartFromAnOldServersReply(t *testing.T) {
	for _, e := range []error{io.EOF, timeoutErr{}} {
		if _, err := Handshake(errWire{e}, store(t)); !errors.Is(err, ErrHungUp) || !errors.Is(err, ErrNotEncrypted) {
			t.Errorf("%v: err = %v", e, err)
		}
	}
	dev, srv := pipes()
	errc := make(chan error, 1)
	st := store(t)
	go func() {
		_, err := Handshake(dev, st)
		errc <- err
	}()
	_, _, _ = srv.ReadMessage()
	_ = srv.WriteMessage(websocket.TextMessage, []byte(`{"type":"server/hello","payload":{}}`))
	if err := <-errc; !errors.Is(err, ErrNotEncrypted) || errors.Is(err, ErrHungUp) {
		t.Errorf("server/hello: err = %v", err)
	}
}

// A server that has spoken encrypted is remembered across a restart, its failing streak starts at its
// first hang-up and is not moved by later ones, and an encrypted handshake ends it.
func TestTheStoreRemembersServersThatSpokeEncrypted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys.json")
	a, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, spoken := a.HungUp("192.0.2.1"); spoken {
		t.Fatal("a server never seen is taken as one that spoke encrypted")
	}
	if err := a.SpokeEncrypted("192.0.2.1", "server-a"); err != nil {
		t.Fatal(err)
	}

	b, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	first, spoken := b.HungUp("192.0.2.1")
	if !spoken || first.IsZero() {
		t.Fatalf("after reopening: spoken %v, since %v", spoken, first)
	}
	time.Sleep(time.Millisecond)
	if again, _ := b.HungUp("192.0.2.1"); !again.Equal(first) {
		t.Errorf("a second hang-up moved the streak from %v to %v", first, again)
	}
	c, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if since, _ := c.HungUp("192.0.2.1"); !since.Equal(first) {
		t.Errorf("the streak did not survive a restart: %v, was %v", since, first)
	}
	if _, spoken := c.HungUp("192.0.2.2"); spoken {
		t.Error("another address is taken as the server that spoke encrypted")
	}

	// The same server on a new address takes its entry along, and its streak is over.
	time.Sleep(time.Millisecond)
	if err := c.SpokeEncrypted("192.0.2.9", "server-a"); err != nil {
		t.Fatal(err)
	}
	if _, spoken := c.HungUp("192.0.2.1"); spoken {
		t.Error("the server's old address is still remembered")
	}
	if since, spoken := c.HungUp("192.0.2.9"); !spoken || !since.After(first) {
		t.Errorf("new address: spoken %v, since %v (the old streak was %v)", spoken, since, first)
	}
	if len(c.file.Encrypted) != 1 {
		t.Errorf("%d entries for one server", len(c.file.Encrypted))
	}
}

func TestTheStoreKeepsAtMostCapacityEncryptedServers(t *testing.T) {
	st := store(t)
	for i := range capacity + 2 {
		if err := st.SpokeEncrypted(fmt.Sprintf("192.0.2.%d", i), fmt.Sprintf("server-%d", i)); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Millisecond)
	}
	if len(st.file.Encrypted) != capacity {
		t.Fatalf("%d entries", len(st.file.Encrypted))
	}
	if _, spoken := st.HungUp("192.0.2.0"); spoken {
		t.Error("the server seen longest ago was kept")
	}
}
