package api

import (
	"testing"

	esphome "github.com/ygelfand/go-esphome-device"
)

func TestKeyKind(t *testing.T) {
	var real esphome.PSK
	real[3] = 7
	for _, c := range []struct {
		k    *esphome.PSK
		want int32
	}{
		{nil, keyNone},
		{esphome.Unprovisioned(), keyZero},
		{&real, keyReal},
	} {
		if got := keyKind(c.k); got != c.want {
			t.Errorf("keyKind(%v) = %d, want %d", c.k, got, c.want)
		}
	}
}

// The record asks for a key only when there is one, and offers the zero-key route only while the zero
// key is served.
func TestTheRecordSaysHowTheKeyStands(t *testing.T) {
	for _, c := range []struct {
		kind                     int32
		encrypted, provisionable bool
	}{
		{keyNone, false, false},
		{keyZero, false, true},
		{keyReal, true, false},
	} {
		e, p := keyTXT(c.kind)
		if e != c.encrypted || p != c.provisionable {
			t.Errorf("keyTXT(%s) = encrypted %v, provisionable %v; want %v, %v",
				keyName(c.kind), e, p, c.encrypted, c.provisionable)
		}
	}
}

// The library keeps a key Home Assistant pushes inside the server that took it, ahead of PSK. So a key
// the device changes afterward has to be served by a new server, or the pushed key goes on being
// served: the window reopened would still want the old Home Assistant's key, and one shut after a
// leave would still let it in.
func TestAKeyChangedAfterAPushIsServedOnANewServer(t *testing.T) {
	adoptFiles(t)
	a := newTestAPI()

	if _, err := a.OpenAdoption(); err != nil {
		t.Fatal(err)
	}
	waiting := a.nextServer()
	if keyKind(waiting.PSK) != keyZero || a.keyed.Load() != keyZero {
		t.Fatalf("with the window open the server has %v (record %s), want the zero key",
			waiting.PSK, keyName(a.keyed.Load()))
	}

	// A Home Assistant adds it: the key it pushed is the key in force, for this server and the next.
	var theirs esphome.PSK
	theirs[5] = 42
	if err := a.keySet(theirs); err != nil {
		t.Fatal(err)
	}
	if a.keyed.Load() != keyReal {
		t.Errorf("after a push the record says %s, want a key", keyName(a.keyed.Load()))
	}
	keyed := a.nextServer()
	if keyed == waiting || keyed.PSK == nil || *keyed.PSK != theirs {
		t.Fatalf("after a push the next server has %v, want the pushed key on a new server", keyed.PSK)
	}

	// The window opened again: a new server, with the zero key, not the one the push left behind.
	if _, err := a.OpenAdoption(); err != nil {
		t.Fatal(err)
	}
	reopened := a.nextServer()
	if reopened == keyed || keyKind(reopened.PSK) != keyZero || a.keyed.Load() != keyZero {
		t.Fatalf("the window reopened serves %v (record %s), want the zero key on a new server",
			reopened.PSK, keyName(a.keyed.Load()))
	}

	// Shut with nobody adding it: the new key, not the old Home Assistant's.
	k, err := shutAdoption()
	if err != nil {
		t.Fatal(err)
	}
	a.serveWith(k)
	shut := a.nextServer()
	if shut == reopened || shut.PSK == nil || *shut.PSK != *k || *shut.PSK == theirs {
		t.Fatalf("the window shut serves %v, want its new key on a new server", shut.PSK)
	}
	if a.keyed.Load() != keyReal {
		t.Errorf("after the window shut the record says %s, want a key", keyName(a.keyed.Load()))
	}
}

// A device that has a key and is given another by a Home Assistant that holds it: no reconnect is
// needed (the server that took it serves with it), but every later server has it too.
func TestAKeyPushedOverAKeyIsKept(t *testing.T) {
	mine := adoptFiles(t)
	a := newTestAPI()
	a.mu.Lock()
	a.useKey(&mine)
	a.mu.Unlock()

	var theirs esphome.PSK
	theirs[8] = 1
	if err := a.keySet(theirs); err != nil {
		t.Fatal(err)
	}
	select {
	case <-a.reconnect:
		t.Error("a key pushed over a key reconnected at once")
	default:
	}
	if s := a.nextServer(); s.PSK == nil || *s.PSK != theirs {
		t.Errorf("the next server has %v, want the pushed key", s.PSK)
	}
}
