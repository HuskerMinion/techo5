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

// At capacity the entry seen longest ago goes, but not a paired server's and not one seen in the last
// day; past twice capacity the oldest unpaired goes anyway, so the list stays bounded.
func TestTheStoreKeepsBoundedEncryptedServers(t *testing.T) {
	st := store(t)
	clock := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	restore := now
	t.Cleanup(func() { now = restore })
	now = func() time.Time { return clock }
	if err := st.remember("server-paired", make([]byte, keySize)); err != nil {
		t.Fatal(err)
	}
	if err := st.SpokeEncrypted("192.0.2.100", "server-paired"); err != nil {
		t.Fatal(err)
	}
	// Seen days ago, all but the paired one may go.
	clock = clock.Add(72 * time.Hour)
	for i := range capacity + 2 {
		clock = clock.Add(time.Minute)
		if err := st.SpokeEncrypted(fmt.Sprintf("192.0.2.%d", i), fmt.Sprintf("server-%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	// All seen in the last day: none is pushed out below twice capacity.
	if n := len(st.file.Encrypted); n != capacity+3 {
		t.Fatalf("%d entries, want %d: recent ones were evicted", n, capacity+3)
	}
	for i := capacity + 2; i < 3*capacity; i++ {
		clock = clock.Add(time.Minute)
		if err := st.SpokeEncrypted(fmt.Sprintf("192.0.2.%d", i), fmt.Sprintf("server-%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(st.file.Encrypted); n > 2*capacity {
		t.Fatalf("%d entries, more than twice capacity", n)
	}
	if _, spoken := st.HungUp("192.0.2.100"); !spoken {
		t.Error("the paired server's entry was evicted")
	}
}

// A server that is not paired is written at most once an hour; a paired one at once.
func TestUnpairedServersAreWrittenAtMostHourly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys.json")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	clock := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	restore := now
	t.Cleanup(func() { now = restore })
	now = func() time.Time { return clock }
	_ = st.SpokeEncrypted("192.0.2.1", "server-a")
	clock = clock.Add(time.Minute)
	_ = st.SpokeEncrypted("192.0.2.2", "server-b")
	reread := func() int {
		r, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		return len(r.file.Encrypted)
	}
	if n := reread(); n != 1 {
		t.Errorf("%d written within the hour, want 1", n)
	}
	_ = st.remember("server-c", make([]byte, keySize))
	clock = clock.Add(time.Minute)
	_ = st.SpokeEncrypted("192.0.2.3", "server-c")
	if n := reread(); n != 3 {
		t.Errorf("a paired server's handshake was not written at once: %d written", n)
	}
}

// On a clock not set yet no failing streak starts, and one kept from such a clock starts again once
// the clock is set.
func TestNoStreakOnAnUnsetClock(t *testing.T) {
	st := store(t)
	clock := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	restore := now
	t.Cleanup(func() { now = restore })
	now = func() time.Time { return clock }
	_ = st.SpokeEncrypted("192.0.2.1", "server-a")
	clock = time.Date(1970, 1, 1, 0, 5, 0, 0, time.UTC)
	if since, spoken := st.HungUp("192.0.2.1"); !spoken || !since.IsZero() {
		t.Errorf("on 1970's clock: since %v, spoken %v", since, spoken)
	}
	// A streak a build from before kept from 1970.
	st.file.Encrypted[0].Failing = time.Date(1970, 1, 1, 0, 1, 0, 0, time.UTC)
	clock = time.Date(2026, 11, 25, 9, 0, 0, 0, time.UTC)
	if since, _ := st.HungUp("192.0.2.1"); !since.Equal(clock) {
		t.Errorf("a streak from 1970 was not started again: %v", since)
	}
}
