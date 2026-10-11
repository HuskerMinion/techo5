package streaming

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
	"github.com/HuskerMinion/techo5/echod/internal/lib/netguard"
)

// An iPhone's session as a Show saw it on 2026-10-11: the sender and how to reach it, the song, a
// pause (caps 2, then the session ended), and the next session as it played again. The pause keeps the
// song and the way back to the phone; the next session plays.
func TestAnAirPlayPauseKeepsTheSenderAndTheSong(t *testing.T) {
	timers := keptTimers(t)
	remote, starts := &dacpRemote{}, &startCount{}
	var pauses []bool
	gone := 0
	session := &airplaySession{remote: remote, starts: starts, paused: func(p bool) { pauses = append(pauses, p) },
		gone: func() { gone++ }}
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
	if len(*timers) != 1 || gone != 0 {
		t.Fatalf("%d waits started and the sender taken as gone %d times, want one wait and none", len(*timers), gone)
	}

	followAirPlayMetadata(strings.NewReader(item("ssnc", "acre", "1122334455")+item("ssnc", "daid", "0123456789ABCDEF")+
		item("ssnc", "clip", "192.0.2.10")+item("ssnc", "pbeg", "")), func(string, string, string) {}, func([]byte) {}, session)
	if pauses[len(pauses)-1] {
		t.Error("the session that plays again left the track paused")
	}
	if remote.port != 61595 {
		t.Error("the same sender coming back lost its port")
	}
	if starts.count() != 2 {
		t.Errorf("%d starts counted, want 2", starts.count())
	}
	// The wait for the paused phone runs out after it came back: nothing is let go of.
	(*timers)[0]()
	if gone != 0 || remote.id != "0123456789ABCDEF" || remote.token != "1122334455" {
		t.Errorf("the sender back was taken as gone: %d, %+v", gone, remote)
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

// A session ended without a pause is the phone gone: the song goes, and so does the way back to it,
// and a track the device left paused for it ends.
func TestAnAirPlaySessionEndedWithoutAPauseIsOver(t *testing.T) {
	timers := keptTimers(t)
	remote := &dacpRemote{}
	gone := 0
	session := &airplaySession{remote: remote, paused: func(bool) {}, gone: func() { gone++ }}
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
	if remote.control(media.TransportPause)() {
		t.Error("a sender that is gone was told")
	}
	if gone != 1 || len(*timers) != 0 {
		t.Errorf("the sender taken as gone %d times, and %d waits started; want once and none", gone, len(*timers))
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
	if d.control(media.TransportPause)() {
		t.Fatal("told a sender before there was one")
	}
	d.take(dacpSender{id: "ABCD", token: "1234"})
	for _, tr := range []media.Transport{media.TransportPause, media.TransportPlay, media.TransportToggle,
		media.TransportNext, media.TransportPrevious} {
		if !d.control(tr)() {
			t.Errorf("%v not taken", tr)
		}
	}
	if d.control(media.TransportStop)() {
		t.Error("a stop, which DACP has no word for here, was taken")
	}
	want := []string{"/ctrl-int/1/pause", "/ctrl-int/1/play", "/ctrl-int/1/playpause", "/ctrl-int/1/nextitem",
		"/ctrl-int/1/previtem"}
	mu.Lock()
	if !slices.Equal(asked, want) {
		t.Errorf("asked %q", asked)
	}
	mu.Unlock()

	d.take(dacpSender{id: "ABCD", token: "wrong"})
	if d.control(media.TransportPause)() {
		t.Error("a refused command was taken")
	}
}

// Where avahi cannot find the sender, or finds it somewhere that does not answer, the address the
// session gave and the port shairport-sync found are tried; a link-local one, which needs an interface
// to reach, is not. (The phone here listens on loopback, which a session's own address is never taken
// as (senderAddr), so it is given whole.)
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
			d.take(dacpSender{id: "ABCD", token: "1234", client: netip.MustParseAddr(tc.client)})
			d.setPort(port)
			if got := d.control(media.TransportPause)(); got != tc.taken {
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
// last one is not used for it. So is one with none, every time. The port found for a DACP-ID stays
// with it, though, through the phone going and a session that named none, since shairport-sync does
// not look it up again for that phone; and a port that comes with no sender is passed over.
func TestAnotherSenderForgetsTheLastOnesAddress(t *testing.T) {
	d := &dacpRemote{}
	if !d.take(dacpSender{id: "ONE", token: "1234", client: netip.MustParseAddr("192.0.2.10")}) {
		t.Error("the first sender was not another")
	}
	d.setPort([]byte{0, 0, 0xf0, 0x9b})
	if d.take(dacpSender{id: "ONE", token: "1234", client: netip.MustParseAddr("192.0.2.11")}) || d.port == 0 ||
		d.client.String() != "192.0.2.11" {
		t.Fatalf("the same sender lost its port, or kept its old address: %+v", d)
	}
	d.forget()
	d.take(dacpSender{client: netip.MustParseAddr("198.51.100.7")})
	if !d.take(dacpSender{id: "ONE", token: "1234"}) || d.port != 61595 {
		t.Fatalf("the phone back after it had gone was not another, or lost its port: %+v", d)
	}
	// Its service seen again while it is gone: the port is still its own.
	d.forget()
	d.setPort([]byte{0, 0, 0xf0, 0x9c})
	if d.take(dacpSender{id: "ONE", token: "1234"}); d.port != 61596 {
		t.Fatalf("the port the phone's service gave while it was gone was not kept: %+v", d)
	}
	if !d.take(dacpSender{id: "TWO", token: "5678"}) || d.port != 0 || d.client.IsValid() || d.token != "5678" {
		t.Errorf("another sender kept the last one's address: %+v", d)
	}
	for range 2 {
		if !d.take(dacpSender{}) || d.id != "" || d.token != "" {
			t.Errorf("a sender with no DACP-ID was not another, or kept the last one's: %+v", d)
		}
	}
	d.setPort([]byte{0, 0, 0xf0, 0x9b})
	if d.take(dacpSender{id: "TWO", token: "5678"}); d.port != 0 {
		t.Errorf("a port that came with no sender was taken: %+v", d)
	}
}

// An iPhone that ended its session as it paused and has not played again in airplayKeptFor has gone:
// the way back to it is dropped, and the track it left paused ends.
func TestAnAirPlaySenderPausedThisLongIsGone(t *testing.T) {
	timers := keptTimers(t)
	resolveTo(t, func(context.Context, string) (string, error) {
		t.Error("a sender that has gone was looked for")
		return "", errors.New("no such sender")
	})
	f := newFollowed()
	f.follow(pausedAndGone("1122334455", "0123456789ABCDEF", "192.0.2.10", "Tush"))
	if len(*timers) != 1 || f.gone != 0 || f.s.remote.id != "0123456789ABCDEF" {
		t.Fatalf("%d waits, gone %d times, remote %+v: the pause did not keep the sender", len(*timers), f.gone, f.s.remote)
	}
	(*timers)[0]()
	if f.gone != 1 {
		t.Errorf("the sender taken as gone %d times, want once", f.gone)
	}
	if r := f.s.remote; r.id != "" || r.token != "" || r.client.IsValid() || r.port != 61595 {
		t.Errorf("the sender was kept, or the port found for it was not: %+v", r)
	}
	if f.s.remote.control(media.TransportPlay)() {
		t.Error("a sender that has gone was told")
	}
	// The same phone back later: shairport-sync keeps the port it found for it, and sends none again.
	f.follow(item("ssnc", "acre", "1122334455") + item("ssnc", "daid", "0123456789ABCDEF") +
		item("ssnc", "clip", "192.0.2.10") + item("ssnc", "pbeg", ""))
	if r := f.s.remote; r.id != "0123456789ABCDEF" || r.port != 61595 {
		t.Errorf("the phone back after the wait lost its port: %+v", r)
	}
}

// Another app playing after an iPhone that paused and went, one that names no remote control of its own
// (no daid, no acre), is not sent the iPhone's commands: a pause here asks nobody, and is the track
// stopped here instead (media's tellSender). The iPhone's song and cover go, and its wait does not end
// the new one.
func TestASenderThatNamesNoRemoteIsNotGivenTheLastOnes(t *testing.T) {
	timers := keptTimers(t)
	resolveTo(t, func(context.Context, string) (string, error) {
		t.Error("avahi was asked for a sender the session did not name")
		return "", errors.New("no such sender")
	})
	f := newFollowed()
	f.follow(pausedAndGone("1122334455", "0123456789ABCDEF", "192.0.2.10", "Tush") +
		item("ssnc", "snua", "ExampleCast/2.1") + item("ssnc", "clip", "198.51.100.7") + item("ssnc", "pbeg", "") +
		item("core", "minm", "Other"))
	r := f.s.remote
	if r.id != "" || r.token != "" || r.client.String() != "198.51.100.7" {
		t.Errorf("remote %+v, want only the new session's address", r)
	}
	if f.pauses[len(f.pauses)-1] {
		t.Error("the new session left the track paused")
	}
	if !slices.Equal(f.titles, []string{"Tush", "", "Other"}) || !slices.Equal(f.covers, []string{"cover", ""}) {
		t.Errorf("titles %q, covers %q: the last sender's song stayed", f.titles, f.covers)
	}
	if r.control(media.TransportPause)() {
		t.Error("a pause was taken with no sender named")
	}
	(*timers)[0]()
	if f.gone != 0 || r.client.String() != "198.51.100.7" {
		t.Errorf("the last sender's wait ended the new one: gone %d, %+v", f.gone, r)
	}
}

// A session that gives a DACP-ID but no token has no token, not the last session's: it is not asked.
func TestASessionGivesOnlyTheTokenItGave(t *testing.T) {
	keptTimers(t)
	resolveTo(t, func(context.Context, string) (string, error) {
		t.Error("avahi was asked for a sender without a token")
		return "", errors.New("no such sender")
	})
	f := newFollowed()
	f.follow(pausedAndGone("1122334455", "0123456789ABCDEF", "192.0.2.10", "Tush") +
		item("ssnc", "daid", "FEDCBA9876543210") + item("ssnc", "clip", "192.0.2.20") + item("ssnc", "pbeg", ""))
	if r := f.s.remote; r.id != "FEDCBA9876543210" || r.token != "" || r.port != 0 {
		t.Errorf("remote %+v", r)
	}
	if f.s.remote.control(media.TransportPause)() {
		t.Error("a sender without a token was told")
	}
}

// What a session says of its sender as it is set up is taken only as it plays (pbeg): until then the
// sender is the last one. What was said for a setup that never played (failed, then another sender
// announced) is not taken for the next session; the last session's end coming late, between the next
// one's setup and its play (as shairport-sync 5.5.1 can send it for a sender that vanished), does not
// lose that setup.
func TestASendersSetupIsTakenAsItPlays(t *testing.T) {
	keptTimers(t)
	f := newFollowed()
	f.follow(pausedAndGone("1122334455", "0123456789ABCDEF", "192.0.2.10", "Tush") +
		item("ssnc", "acre", "5566778899") + item("ssnc", "daid", "FEDCBA9876543210") + item("ssnc", "clip", "192.0.2.20"))
	if r := f.s.remote; r.id != "0123456789ABCDEF" || r.token != "1122334455" || r.client.String() != "192.0.2.10" {
		t.Errorf("a session not yet playing was taken: %+v", r)
	}
	f.follow(item("ssnc", "pbeg", ""))
	if r := f.s.remote; r.id != "FEDCBA9876543210" || r.token != "5566778899" || r.client.String() != "192.0.2.20" || r.port != 0 {
		t.Errorf("the session playing was not taken: %+v", r)
	}

	f = newFollowed()
	f.follow(item("ssnc", "acre", "5566778899") + item("ssnc", "daid", "FEDCBA9876543210") +
		item("ssnc", "snua", "ExampleCast/2.1") + item("ssnc", "clip", "192.0.2.20") + item("ssnc", "pbeg", ""))
	if r := f.s.remote; r.id != "" || r.token != "" {
		t.Errorf("a setup that never played was taken: %+v", r)
	}

	f = newFollowed()
	f.follow(item("ssnc", "pbeg", "") + item("ssnc", "acre", "5566778899") + item("ssnc", "daid", "FEDCBA9876543210") +
		item("ssnc", "clip", "192.0.2.20") + item("ssnc", "pend", "") + item("ssnc", "pbeg", ""))
	if r := f.s.remote; r.id != "FEDCBA9876543210" || r.token != "5566778899" {
		t.Errorf("the last session's end lost the next one's setup: %+v", r)
	}
}

// The same phone playing again after its pause keeps its song and cover: they are still what it plays.
// Another sender has them taken down, a cover shown without a song as well.
func TestTheSongKeptIsTheSameSendersOnly(t *testing.T) {
	keptTimers(t)
	f := newFollowed()
	f.follow(pausedAndGone("1122334455", "0123456789ABCDEF", "192.0.2.10", "Tush") +
		item("ssnc", "acre", "1122334455") + item("ssnc", "daid", "0123456789ABCDEF") + item("ssnc", "clip", "192.0.2.10") +
		item("ssnc", "pbeg", "") + item("ssnc", "prsm", ""))
	if !slices.Equal(f.titles, []string{"Tush"}) || !slices.Equal(f.covers, []string{"cover"}) {
		t.Errorf("titles %q, covers %q: the same phone back lost its song", f.titles, f.covers)
	}

	f = newFollowed()
	f.follow(pausedAndGone("1122334455", "0123456789ABCDEF", "192.0.2.10", "") +
		item("ssnc", "acre", "5566778899") + item("ssnc", "daid", "FEDCBA9876543210") + item("ssnc", "clip", "192.0.2.20") +
		item("ssnc", "pbeg", ""))
	if !slices.Equal(f.covers, []string{"cover", ""}) {
		t.Errorf("covers %q: the last phone's cover stayed for another", f.covers)
	}
}

// shairport-sync starting afresh, after it stopped (svna), ends the session it had open, which sends
// no end of its own: one playing is over, and one paused is waited on as if it had ended. With no
// session open, as when it starts the first time, nothing changes.
func TestShairportSyncStartingAfreshEndsTheSession(t *testing.T) {
	timers := keptTimers(t)
	f := newFollowed()
	f.follow(item("ssnc", "svna", "Speaker") + item("ssnc", "acre", "1122334455") +
		item("ssnc", "daid", "0123456789ABCDEF") + item("ssnc", "clip", "192.0.2.10") + item("ssnc", "pbeg", "") +
		item("core", "minm", "Tush") + item("core", "caps", "\x01") + item("ssnc", "svna", "Speaker") +
		item("ssnc", "svna", "Speaker"))
	if !slices.Equal(f.titles, []string{"Tush", ""}) || f.gone != 1 || len(*timers) != 0 {
		t.Errorf("titles %q, gone %d, %d waits: the session playing did not end", f.titles, f.gone, len(*timers))
	}
	if r := f.s.remote; r.id != "" || r.token != "" {
		t.Errorf("the sender was kept: %+v", r)
	}

	f = newFollowed()
	f.follow(item("ssnc", "acre", "1122334455") + item("ssnc", "daid", "0123456789ABCDEF") +
		item("ssnc", "clip", "192.0.2.10") + item("ssnc", "pbeg", "") + item("core", "minm", "Tush") +
		item("core", "caps", "\x02") + item("ssnc", "svna", "Speaker"))
	if !slices.Equal(f.titles, []string{"Tush"}) || f.gone != 0 || len(*timers) != 1 {
		t.Errorf("titles %q, gone %d, %d waits: the paused session was not waited on", f.titles, f.gone, len(*timers))
	}

	// A setup the restart cut short, before it played, is not the next session's sender.
	f = newFollowed()
	f.follow(item("ssnc", "acre", "1122334455") + item("ssnc", "daid", "0123456789ABCDEF") +
		item("ssnc", "clip", "192.0.2.10") + item("ssnc", "svna", "Speaker") + item("ssnc", "clip", "198.51.100.7") +
		item("ssnc", "pbeg", ""))
	if r := f.s.remote; r.id != "" || r.token != "" {
		t.Errorf("a setup cut short by the restart was taken: %+v", r)
	}
}

// A command is for the sender there as it is given: one still waiting, behind an ask of a phone slow to
// answer, when another phone has started to play is not passed on to that one. The same phone playing
// again, which a play from here does by starting a session, is still the one asked.
func TestACommandGoesToTheSenderItWasGivenFor(t *testing.T) {
	var asked []string
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		asked = append(asked, r.Header.Get("Active-Remote")+" "+r.URL.Path)
		mu.Unlock()
	}))
	t.Cleanup(srv.Close)
	resolveTo(t, func(context.Context, string) (string, error) { return srv.Listener.Addr().String(), nil })
	d := &dacpRemote{}
	d.take(dacpSender{id: "0123456789ABCDEF", token: "1122334455"})
	next := d.control(media.TransportNext)
	d.take(dacpSender{id: "FEDCBA9876543210", token: "5566778899"})
	if !next() {
		t.Error("a command for the last sender was not let go of")
	}
	d.take(dacpSender{id: "FEDCBA9876543210", token: "5566778899"})
	pause := d.control(media.TransportPause)
	d.take(dacpSender{id: "FEDCBA9876543210", token: "5566778899"})
	if !pause() {
		t.Error("the same phone in a new session was not asked")
	}
	mu.Lock()
	defer mu.Unlock()
	if !slices.Equal(asked, []string{"5566778899 /ctrl-int/1/pause"}) {
		t.Errorf("asked %q", asked)
	}
}

// An address no sender could be at is not taken, from avahi or from the session (clip): the device's
// own, link-local, nowhere, or a group.
func TestAnAddressNoSenderCouldBeAtIsLeftOut(t *testing.T) {
	was := netguard.OwnIP
	netguard.OwnIP = func(ip net.IP) bool { return ip.Equal(net.ParseIP("192.0.2.99")) }
	t.Cleanup(func() { netguard.OwnIP = was })
	for addr, want := range map[string]string{
		"192.0.2.10": "192.0.2.10", "198.51.100.7": "198.51.100.7", "2001:db8::10": "2001:db8::10",
		"::ffff:192.0.2.10": "192.0.2.10", "192.0.2.99": "", "::ffff:192.0.2.99": "", "169.254.10.20": "", "fe80::1": "",
		"127.0.0.1": "", "127.8.9.10": "", "::1": "", "::ffff:127.0.0.1": "", "0.0.0.0": "", "0.1.2.3": "", "::": "",
		"224.0.0.251": "", "239.255.255.250": "", "ff02::fb": "", "255.255.255.255": "", "": "", "not an address": "",
	} {
		got := senderAddr(addr)
		if (want == "" && got.IsValid()) || (want != "" && got.String() != want) {
			t.Errorf("senderAddr(%q) = %v, want %q", addr, got, want)
		}
	}
	for _, tc := range []struct {
		addr string
		port uint16
		want string
	}{
		{"192.0.2.10", 3689, "192.0.2.10:3689"}, {"::ffff:192.0.2.10", 3689, "192.0.2.10:3689"},
		{"2001:db8::10", 3689, "[2001:db8::10]:3689"}, {"192.0.2.10", 0, ""}, {"127.0.0.1", 3689, ""}, {"0.0.0.0", 3689, ""},
	} {
		got, err := dacpHost(tc.addr, tc.port)
		if got != tc.want || (tc.want == "") != (err != nil) {
			t.Errorf("dacpHost(%q, %d) = %q, %v; want %q", tc.addr, tc.port, got, err, tc.want)
		}
	}
	f := newFollowed()
	f.follow(item("ssnc", "acre", "1122334455") + item("ssnc", "daid", "0123456789ABCDEF") +
		item("ssnc", "clip", "127.0.0.1") + item("ssnc", "pbeg", ""))
	if r := f.s.remote; r.client.IsValid() || r.id != "0123456789ABCDEF" {
		t.Errorf("remote %+v, want the sender without the loopback address", r)
	}
}

// One ask, finding the sender and every try at it, is bounded as a whole: avahi slow to answer, and a
// sender that does not answer at the session's address, hold it up no longer than dacpAskTimeout, not
// the resolve's and each try's own timeouts one after the other.
func TestAnAskIsBoundedAsAWhole(t *testing.T) {
	was := dacpAskTimeout
	dacpAskTimeout = 500 * time.Millisecond
	t.Cleanup(func() { dacpAskTimeout = was })
	// A sender that takes the connection and never answers.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	resolveTo(t, func(ctx context.Context, _ string) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	})
	d := &dacpRemote{}
	// Given whole: the session's own address would not be taken on the loopback (senderAddr).
	d.take(dacpSender{id: "ABCD", token: "1234", client: netip.MustParseAddr("127.0.0.1")})
	d.setPort(binary.BigEndian.AppendUint32(nil, uint32(l.Addr().(*net.TCPAddr).Port)))
	start := time.Now()
	if d.control(media.TransportPause)() {
		t.Error("a sender that never answered took the pause")
	}
	if took := time.Since(start); took > 1500*time.Millisecond {
		t.Errorf("the ask took %v", took)
	}
}

// followed is a session as the tests follow it: what it passed on to the track and the screen, and how
// many times it took the sender as gone.
type followed struct {
	s              *airplaySession
	pauses         []bool
	gone           int
	titles, covers []string
}

func newFollowed() *followed {
	f := &followed{}
	f.s = &airplaySession{remote: &dacpRemote{}, starts: &startCount{},
		paused: func(p bool) { f.pauses = append(f.pauses, p) }, gone: func() { f.gone++ }}
	return f
}

// follow reads stream as the metadata pipe would give it. The song is the reading's own: one stream
// is one reading.
func (f *followed) follow(stream string) {
	followAirPlayMetadata(strings.NewReader(stream), func(title, _, _ string) { f.titles = append(f.titles, title) },
		func(b []byte) { f.covers = append(f.covers, string(b)) }, f.s)
}

// pausedAndGone is an iPhone's session, as a Show saw one, ended as it paused: the sender as it set
// up (acre, daid, clip), playing, the port avahi found, the song and its cover, the pause, the end.
func pausedAndGone(token, id, addr, title string) string {
	return item("ssnc", "acre", token) + item("ssnc", "daid", id) + item("ssnc", "clip", addr) + item("ssnc", "pbeg", "") +
		item("ssnc", "dapo", "\x00\x00\xf0\x9b") + item("ssnc", "prsm", "") + item("core", "minm", title) +
		item("ssnc", "PICT", "cover") + item("core", "caps", "\x01") + item("core", "caps", "\x02") + item("ssnc", "pend", "")
}

// keptTimers stands in for the timer that ends the wait for a paused sender, for the length of the
// test: none is started, and the test runs out the waits it was given by hand.
func keptTimers(t *testing.T) *[]func() {
	t.Helper()
	var waits []func()
	was := airplayKeptAfter
	airplayKeptAfter = func(d time.Duration, f func()) *time.Timer {
		if d != airplayKeptFor {
			t.Errorf("a paused sender waited for %v, want %v", d, airplayKeptFor)
		}
		waits = append(waits, f)
		return nil
	}
	t.Cleanup(func() { airplayKeptAfter = was })
	return &waits
}

// resolveTo stands in for avahi for the length of the test.
func resolveTo(t *testing.T, f func(context.Context, string) (string, error)) {
	was := dacpResolve
	dacpResolve = f
	t.Cleanup(func() { dacpResolve = was })
}
