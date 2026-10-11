package streaming

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
)

// An iPhone's session as a Show saw it on 2026-10-11: the sender and how to reach it, the song, a
// pause (caps 2, then the session ended), and the next session as it played again. The pause keeps the
// song and the way back to the phone; the next session plays.
func TestAnAirPlayPauseKeepsTheSenderAndTheSong(t *testing.T) {
	remote, starts := &dacpRemote{}, &startCount{}
	var pauses []bool
	session := &airplaySession{remote: remote, starts: starts, paused: func(p bool) { pauses = append(pauses, p) }}
	stream := item("ssnc", "acre", "1122334455") + item("ssnc", "daid", "0123456789ABCDEF") +
		item("ssnc", "clip", "192.0.2.10") + item("ssnc", "pbeg", "") + item("ssnc", "pfls", "1000000000") +
		item("ssnc", "dapo", "\x00\x00\xf0\x9b") + item("ssnc", "prsm", "") +
		item("core", "minm", "Tush") + item("core", "caps", "\x01") +
		item("core", "caps", "\x02") + item("ssnc", "pend", "")
	var titles []string
	var pictures int
	followAirPlayMetadata(strings.NewReader(stream), func(title, _, _ string) {
		titles = append(titles, title)
	}, func([]byte) { pictures++ }, session)
	if !slices.Equal(titles, []string{"Tush"}) || pictures != 0 {
		t.Errorf("titles %q and %d pictures: a pause cleared the song", titles, pictures)
	}
	if !slices.Equal(pauses, []bool{false, false, false, true}) {
		t.Errorf("paused %v", pauses)
	}
	if remote.id != "0123456789ABCDEF" || remote.token != "1122334455" || remote.port != 61595 ||
		remote.client.String() != "192.0.2.10" {
		t.Errorf("remote %+v", remote)
	}

	followAirPlayMetadata(strings.NewReader(item("ssnc", "acre", "1122334455")+item("ssnc", "daid", "0123456789ABCDEF")+
		item("ssnc", "pbeg", "")), func(string, string, string) {}, func([]byte) {}, session)
	if pauses[len(pauses)-1] {
		t.Error("the session that plays again left the track paused")
	}
	if remote.port != 61595 {
		t.Error("the same sender coming back lost its port")
	}
	if starts.count() != 2 {
		t.Errorf("%d starts counted, want 2", starts.count())
	}
}

// Played again before the session ends, as on 2026-10-11 three seconds after a stop on the screen, an
// iPhone says only that it plays: that is a start, and the track follows; a song changing as it plays
// is not.
func TestPlayingAgainWithinTheSessionIsAStart(t *testing.T) {
	starts := &startCount{}
	var pauses []bool
	session := &airplaySession{remote: &dacpRemote{}, starts: starts, paused: func(p bool) { pauses = append(pauses, p) }}
	stream := item("ssnc", "pbeg", "") + item("core", "caps", "\x01") + item("core", "minm", "Hotel California") +
		item("core", "caps", "\x01") + item("core", "caps", "\x02") + item("core", "caps", "\x01")
	followAirPlayMetadata(strings.NewReader(stream), func(string, string, string) {}, func([]byte) {}, session)
	if starts.count() != 2 {
		t.Errorf("%d starts counted, want 2: the session and the play after the pause", starts.count())
	}
	if !slices.Equal(pauses, []bool{false, false, false, true, false}) {
		t.Errorf("paused %v", pauses)
	}
}

// A session ended without a pause is the phone gone: the song goes, and so does the way back to it.
func TestAnAirPlaySessionEndedWithoutAPauseIsOver(t *testing.T) {
	remote := &dacpRemote{}
	session := &airplaySession{remote: remote, paused: func(bool) {}}
	stream := item("ssnc", "acre", "5566778899") + item("ssnc", "daid", "FEDCBA9876543210") +
		item("ssnc", "pbeg", "") + item("core", "minm", "Get It On") + item("core", "caps", "\x01") +
		item("ssnc", "pend", "")
	var titles []string
	followAirPlayMetadata(strings.NewReader(stream), func(title, _, _ string) {
		titles = append(titles, title)
	}, func([]byte) {}, session)
	if !slices.Equal(titles, []string{"Get It On", ""}) {
		t.Errorf("titles %q", titles)
	}
	if remote.id != "" || remote.token != "" {
		t.Errorf("the sender was kept: %+v", remote)
	}
	if remote.control(media.TransportPause) {
		t.Error("a sender that is gone was told")
	}
}

// A sender is asked over DACP: the command's own path, with the session's token.
func TestTheSenderIsAskedOverDACP(t *testing.T) {
	var mu sync.Mutex
	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Header.Get("Active-Remote") != "1234" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		asked = append(asked, r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")
	resolveTo(t, func(_ context.Context, id string) (string, error) {
		if id != "ABCD" {
			return "", errors.New("no such sender")
		}
		return host, nil
	})

	d := &dacpRemote{}
	if d.control(media.TransportPause) {
		t.Fatal("told a sender before there was one")
	}
	d.setID("ABCD")
	d.setToken("1234")
	for _, tr := range []media.Transport{media.TransportPause, media.TransportPlay, media.TransportToggle,
		media.TransportNext, media.TransportPrevious} {
		if !d.control(tr) {
			t.Errorf("%v not taken", tr)
		}
	}
	if d.control(media.TransportStop) {
		t.Error("a stop, which DACP has no word for here, was taken")
	}
	want := []string{"/ctrl-int/1/pause", "/ctrl-int/1/play", "/ctrl-int/1/playpause", "/ctrl-int/1/nextitem",
		"/ctrl-int/1/previtem"}
	mu.Lock()
	if !slices.Equal(asked, want) {
		t.Errorf("asked %q", asked)
	}
	mu.Unlock()

	d.setToken("wrong")
	if d.control(media.TransportPause) {
		t.Error("a refused command was taken")
	}
}

// Where avahi cannot find the sender, or finds it somewhere that does not answer, the address the
// session gave and the port shairport-sync found are tried; a link-local one, which needs an interface
// to reach, is not.
func TestTheSessionsAddressIsTheFallback(t *testing.T) {
	var hits int
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	// The server's port as shairport-sync sends one: four bytes, most significant first.
	port := binary.BigEndian.AppendUint32(nil, uint32(srv.Listener.Addr().(*net.TCPAddr).Port))

	// A port nothing listens on: what avahi found answers no more.
	dead, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	deadAddr := dead.Addr().String()
	dead.Close()

	for _, tc := range []struct {
		name    string
		resolve func(context.Context, string) (string, error)
		client  string
		taken   bool
	}{
		{"avahi finds nothing", func(context.Context, string) (string, error) { return "", errors.New("timeout") }, "127.0.0.1", true},
		{"what avahi found does not answer", func(context.Context, string) (string, error) { return deadAddr, nil }, "127.0.0.1", true},
		{"only a link-local address", func(context.Context, string) (string, error) { return "", errors.New("timeout") }, "fe80::1", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resolveTo(t, tc.resolve)
			mu.Lock()
			hits = 0
			mu.Unlock()
			d := &dacpRemote{}
			d.setID("ABCD")
			d.setToken("1234")
			d.setClient(tc.client)
			d.setPort(port)
			if got := d.control(media.TransportPause); got != tc.taken {
				t.Errorf("taken %v, want %v", got, tc.taken)
			}
			mu.Lock()
			defer mu.Unlock()
			if want := map[bool]int{true: 1, false: 0}[tc.taken]; hits != want {
				t.Errorf("%d requests, want %d", hits, want)
			}
		})
	}
}

// A sender choosing this speaker again under another DACP-ID is another sender: what was found of the
// last one is not used for it.
func TestAnotherSenderForgetsTheLastOnesAddress(t *testing.T) {
	d := &dacpRemote{}
	d.setID("ONE")
	d.setClient("192.0.2.10")
	d.setPort([]byte{0, 0, 0xf0, 0x9b})
	d.setID("ONE")
	if d.port == 0 || !d.client.IsValid() {
		t.Fatal("the same sender lost its address")
	}
	d.setID("TWO")
	if d.port != 0 || d.client.IsValid() {
		t.Errorf("another sender kept the last one's address: %+v", d)
	}
}

// resolveTo stands in for avahi for the length of the test.
func resolveTo(t *testing.T, f func(context.Context, string) (string, error)) {
	was := dacpResolve
	dacpResolve = f
	t.Cleanup(func() { dacpResolve = was })
}
