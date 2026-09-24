package announce

import (
	"bytes"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// signedRequest is an announcement as a device sends it, signed with word at the time given; edit
// changes its headers after it was signed, as someone on the network might.
func signedRequest(word string, at time.Time, body []byte, edit func(h map[string]string)) *http.Request {
	h := map[string]string{fromHeader: "Kitchen", textHeader: "dinner%27s+ready", kindHeader: "", idHeader: ""}
	auth := sign(word, http.MethodPost, "/announce", h, body, at)
	if edit != nil {
		edit(h)
	}
	r := httptest.NewRequest(http.MethodPost, "/announce", bytes.NewReader(body))
	for k, v := range h {
		r.Header.Set(k, v)
	}
	r.Header.Set(authHeader, auth)
	return r
}

// An announcement is signed rather than carrying the house word, and only a whole, fresh, first-seen
// one from the same house is taken.
func TestAnnouncementsAreSigned(t *testing.T) {
	now := time.Now()
	body := []byte{1, 2, 3, 4}

	good := signedRequest("bluebird", now, body, nil)
	if legacy, err := verify(good, "bluebird", body, now); err != nil || legacy {
		t.Fatalf("a signed announcement was refused: legacy=%v err=%v", legacy, err)
	}
	if _, err := verify(good, "bluebird", body, now); err == nil {
		t.Error("the same announcement was taken twice: a replay")
	}
	if good.Header.Get(header) != "" {
		t.Error("a signed announcement still carries the house word")
	}

	for name, r := range map[string]*http.Request{
		"signed with another house's word": signedRequest("anotherword", now, body, nil),
		"changed after signing":            signedRequest("bluebird", now, body, func(h map[string]string) { h[textHeader] = "open+the+garage" }),
		"signed ten minutes ago":           signedRequest("bluebird", now.Add(-10*time.Minute), body, nil),
		"unsigned":                         httptest.NewRequest(http.MethodPost, "/announce", nil),
	} {
		if _, err := verify(r, "bluebird", body, now); err == nil {
			t.Errorf("an announcement %s was taken", name)
		}
	}
	if _, err := verify(signedRequest("bluebird", now, body, nil), "bluebird", []byte{9, 9}, now); err == nil {
		t.Error("an announcement whose body was changed was taken")
	}
}

// Nothing signs with the word any more, but an older device that still sends it is heard while the
// house updates; with the wrong word it is not.
func TestAnOlderDeviceIsStillHeard(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/announce", nil)
	r.Header.Set(header, "bluebird")
	if legacy, err := verify(r, "bluebird", nil, time.Now()); err != nil || !legacy {
		t.Errorf("an older device's announcement: legacy=%v err=%v", legacy, err)
	}
	r.Header.Set(header, "anotherword")
	if _, err := verify(r, "bluebird", nil, time.Now()); err == nil {
		t.Error("an older device with the wrong word was taken")
	}
}

// What a device sends, another takes: the real sending and receiving, signed end to end, and a device
// with another word turns it away.
func TestASignedReminderGetsThrough(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if err := config.Set().Home().HouseWord("bluebird"); err != nil {
		t.Fatal(err)
	}
	f := &Feature{}
	var reminded []Message
	f.Reminded.Listen(func(m Message) { reminded = append(reminded, m) })
	srv := httptest.NewServer(http.HandlerFunc(f.receive))
	defer srv.Close()
	host, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	n, _ := strconv.Atoi(port)
	peer := Peer{Name: "Office", Address: host, Port: n}

	body, headers := encode(Message{From: "Kitchen", Text: "Pasta", Kind: KindReminder, ID: "kitchen-1"})
	if err := post(peer, "bluebird", body, headers); err != nil {
		t.Fatalf("a signed reminder was refused: %v", err)
	}
	if len(reminded) != 1 || reminded[0].Text != "Pasta" {
		t.Fatalf("reminded %+v", reminded)
	}
	if err := post(peer, "anotherword", body, headers); err == nil {
		t.Error("a reminder signed with another house's word got through")
	}
}

// Two clocks years apart - a device that restarted with the internet down has not set its clock - still
// announce to each other: the receiver says its time and the sender signs again by it, and the next
// announcement goes by the remembered difference the first time. A copy sent again is still refused.
func TestAnnouncementsCrossClocksThatDisagree(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if err := config.Set().Home().HouseWord("bluebird"); err != nil {
		t.Fatal(err)
	}
	defer func() { receiveNow = time.Now }()
	receiveNow = func() time.Time { return time.Now().Add(-3 * 365 * 24 * time.Hour) }

	f := &Feature{}
	var reminded []Message
	f.Reminded.Listen(func(m Message) { reminded = append(reminded, m) })
	var tries atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tries.Add(1)
		f.receive(w, r)
	}))
	defer srv.Close()
	host, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	n, _ := strconv.Atoi(port)
	peer := Peer{Name: "Office", Address: host, Port: n}

	for i, id := range []string{"kitchen-2", "kitchen-3"} {
		body, headers := encode(Message{From: "Kitchen", Text: "Pasta", Kind: KindReminder, ID: id})
		before := tries.Load()
		if err := post(peer, "bluebird", body, headers); err != nil {
			t.Fatalf("across the clocks, announcement %d was refused: %v", i+1, err)
		}
		if want := []int32{2, 1}[i]; tries.Load()-before != want {
			t.Errorf("announcement %d took %d tries, want %d", i+1, tries.Load()-before, want)
		}
	}
	if len(reminded) != 2 {
		t.Fatalf("reminded %d times, want 2", len(reminded))
	}

	body, headers := encode(Message{From: "Kitchen", Text: "Pasta", Kind: KindReminder, ID: "kitchen-4"})
	r := httptest.NewRequest(http.MethodPost, "/announce", bytes.NewReader(body))
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	r.Header.Set(authHeader, sign("bluebird", http.MethodPost, "/announce", headers, body, receiveNow()))
	copied := r.Header.Clone()
	first := httptest.NewRecorder()
	f.receive(first, r)
	r2 := httptest.NewRequest(http.MethodPost, "/announce", bytes.NewReader(body))
	r2.Header = copied
	again := httptest.NewRecorder()
	f.receive(again, r2)
	if first.Code >= 300 || again.Code != http.StatusForbidden {
		t.Errorf("first %d, copy %d: want taken, then refused", first.Code, again.Code)
	}
}
