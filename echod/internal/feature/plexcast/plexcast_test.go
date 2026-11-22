//go:build !dot

package plexcast

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/layout"
)

// A search is Plex's own and not SSDP's: one line, "M-SEARCH * " and a version this player does not
// read, then whatever blank lines the sender sends.
func TestWhichDatagramsAreSearches(t *testing.T) {
	yes := []string{
		"M-SEARCH * HTTP/1.0\r\n\r\n",
		"M-SEARCH * HTTP/1.1\r\n\r\n",
		"M-SEARCH * HTTP/1.0\n\n",
		"M-SEARCH * HTTP/1.0\r\n",
		"M-SEARCH * HTTP/1.0",
	}
	no := []string{
		"",
		"NOTIFY * HTTP/1.0\r\n\r\n",
		"M-SEARCH : HTTP/1.0\r\n",
		"M-SEARCH * ",
		"garbage\r\n\r\n",
	}
	for _, s := range yes {
		if !isSearch([]byte(s)) {
			t.Errorf("%q is not read as a search", s)
		}
	}
	for _, s := range no {
		if isSearch([]byte(s)) {
			t.Errorf("%q is read as a search", s)
		}
	}
}

// A name from the owner cannot become a second header in the answer or the announcement.
func TestANameIsOneHeader(t *testing.T) {
	if got := header("Living\r\nPort: 1"); strings.ContainsAny(got, "\r\n") {
		t.Errorf("a name kept a line break: %q", got)
	}
	if got := header("Kitchen"); got != "Kitchen" {
		t.Errorf("a plain name came out %q", got)
	}
}

// The play queue's number and the item's are read out of the containerKey a controller sends, in any
// of the shapes it sends it in.
func TestTheQueueIsReadFromItsKey(t *testing.T) {
	for _, c := range []struct{ key, queue, item string }{
		{"/playQueues/12?window=100&own=1", "12", ""},
		{"/playQueues/7?playQueueItemID=99&own=1", "7", "99"},
		{"playQueues/3", "3", ""},
		{"", "", ""},
	} {
		if q, i := queueIDs(c.key); q != c.queue || i != c.item {
			t.Errorf("%q: queue %q item %q, want %q and %q", c.key, q, i, c.queue, c.item)
		}
	}
}

// A controller's playMedia is acted on only when it says where the item is and how to fetch it: the
// address is a host and nothing else, the protocol is one that can be fetched, and the key a path.
func TestARequestMustMakeSense(t *testing.T) {
	ok := mediaRequest{protocol: "http", address: "192.168.1.20", port: "32400",
		key: "/library/metadata/1", machine: "abc", token: "tok"}
	if err := ok.check(); err != nil {
		t.Fatalf("a plain request was refused: %v", err)
	}
	for name, bad := range map[string]func(*mediaRequest){
		"no protocol":            func(m *mediaRequest) { m.protocol = "file" },
		"an address with a path": func(m *mediaRequest) { m.address = "192.168.1.20/x" },
		"no port":                func(m *mediaRequest) { m.port = "" },
		"a port out of range":    func(m *mediaRequest) { m.port = "70000" },
		"a key with no slash":    func(m *mediaRequest) { m.key = "library/metadata/1" },
		"no machine":             func(m *mediaRequest) { m.machine = "" },
		"a hostname with a slash": func(m *mediaRequest) {
			m.address = "server.local/../x"
		},
	} {
		m := ok
		bad(&m)
		if err := m.check(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// Choosing a stream marks one of its kind and the rest not, which is how the transcode is asked for the
// right one; 0 chooses none of that kind.
func TestChoosingAStream(t *testing.T) {
	p := part{streams: []stream{
		{id: 1, kind: streamAudio, codec: "aac", selected: true},
		{id: 2, kind: streamAudio, codec: "ac3", selected: true},
		{id: 3, kind: streamSubtitle, codec: "srt"},
	}}
	if p.audioStream() != 1 {
		t.Errorf("selected audio %d, want the first of the two flagged", p.audioStream())
	}
	p.selectStream(streamAudio, 1)
	p.selectStream(streamSubtitle, 3)
	if p.audioStream() != 1 || p.subtitleStream() != 3 {
		t.Errorf("after choosing: audio %d subtitles %d", p.audioStream(), p.subtitleStream())
	}
	p.selectStream(streamSubtitle, 0)
	if p.subtitleStream() != 0 {
		t.Errorf("subtitles off still chose %d", p.subtitleStream())
	}
}

// A song the device decodes as it is comes straight from the server; one it does not is asked for as
// MP3.
func TestWhichSongsPlayAsTheyAre(t *testing.T) {
	for _, c := range []struct {
		p    part
		want bool
	}{
		{part{audioCodec: "mp3"}, true},
		{part{audioCodec: "flac"}, true},
		{part{container: "flac"}, true},
		{part{audioCodec: "aac", container: "m4a"}, false},
		{part{audioCodec: "alac", container: "m4a"}, false},
		{part{audioCodec: "vorbis", container: "ogg"}, false},
	} {
		if got := c.p.playable(); got != c.want {
			t.Errorf("%+v: playable %v, want %v", c.p, got, c.want)
		}
	}
}

// The position is the offset it started at, the time since, less the time spent paused, and never past
// the end.
func TestWhereItIs(t *testing.T) {
	now := time.Now()
	p := &playback{it: &item{dur: time.Minute}, state: statePlaying,
		base: 10 * time.Second, started: now.Add(-30 * time.Second)}
	if got := p.atLocked(now); got != 40*time.Second {
		t.Errorf("playing: %v, want 40s", got)
	}
	p.state, p.pausedAt = statePaused, now.Add(-20*time.Second)
	p.started = now.Add(-40 * time.Second)
	// 40s since it began, 20 of them paused: 20 played, plus the 10 it started at.
	if got := p.atLocked(now); got != 30*time.Second {
		t.Errorf("paused: %v, want 30s", got)
	}
	p.state, p.pausedAt = statePlaying, time.Time{}
	p.base, p.started = time.Minute, now
	if got := p.atLocked(now); got != time.Minute {
		t.Errorf("past the end: %v, want the length", got)
	}
}

// A link tells the screens and not the player's life. Waking the life with no account linked has it
// clean up (off), which cancels the link it was just asked for: a device said "context canceled" ten
// milliseconds after Link Account was tapped because of exactly that (plexcast.go).
func TestALinkDoesNotWakeThePlayer(t *testing.T) {
	f := &Feature{wake: make(chan struct{}, 1)}
	told := 0
	f.els = []func(){func() { told++ }}
	f.listeners()
	if told != 1 {
		t.Errorf("the listeners told the screens %d times, want 1", told)
	}
	select {
	case <-f.wake:
		t.Error("a link's news woke the player's life, which cancels it")
	default:
	}
	f.changed()
	if told != 2 {
		t.Errorf("changed told the screens %d times, want 2", told)
	}
	select {
	case <-f.wake:
	default:
		t.Error("an account going on or off did not wake the player's life")
	}
}

// A link put away while plex.tv was answering says nothing: it is not an error, and the screen should
// not read "plex.tv could not be reached" for somebody's own Cancel (pin.go, finishPin).
func TestAPinPutAwaySaysNothing(t *testing.T) {
	f := &Feature{}
	f.finishPin("", context.Canceled)
	if st := f.status(); st.Err != "" || st.Waiting {
		t.Errorf("a cancelled link left %q on the screen, waiting %v", st.Err, st.Waiting)
	}
}

// The profile is the one place the server is told what this device takes, so what it says is checked:
// H.264 at 720p for a Show or a Spot, and at 1080p for the Skylight (transcode.go).
func TestProfileSize(t *testing.T) {
	show := capProfileAt(1280, 720)
	sky := capProfileAt(1920, 1080)
	for _, want := range []struct{ profile, name string }{{show, "the 720p one"}, {sky, "the 1080p one"}} {
		for _, s := range []string{
			"add-transcode-target(type=videoProfile&context=streaming&protocol=hls",
			"&container=mpegts&videoCodec=h264&",
			"&audioCodec=aac,mp3,ac3&",
			"&subtitleCodec=srt&",
			"name=video.bitDepth&value=8&",
		} {
			if !strings.Contains(want.profile, s) {
				t.Errorf("%s does not say %q: %s", want.name, s, want.profile)
			}
		}
	}
	for _, want := range []struct {
		profile string
		w, h    int
	}{{show, 1280, 720}, {sky, 1920, 1080}} {
		if !strings.Contains(want.profile, "name=video.width&value="+strconv.Itoa(want.w)+"&") ||
			!strings.Contains(want.profile, "name=video.height&value="+strconv.Itoa(want.h)+"&") {
			t.Errorf("the profile does not hold the picture to %dx%d: %s", want.w, want.h, want.profile)
		}
	}
	if show == sky {
		t.Error("720p and 1080p ask the server for the same thing")
	}
	// H.264 and nothing else. Plex may encode to a codec a client lists, and its HEVC encoding is
	// hardware-only and experimental (docs/plex.md): one it cannot make, or one this board cannot
	// decode, would be a film that does not play.
	for _, p := range []string{show, sky} {
		if strings.Contains(p, "hevc") || strings.Contains(p, "h265") {
			t.Errorf("the profile offers H.265: %s", p)
		}
	}
}

// A board's own answer, and the one this build runs with: the init that sets capProfile has to have
// picked this board's profile, or a Skylight is sent what a Show is (transcode_board.go).
func TestProfileFollowsTheBoard(t *testing.T) {
	if got, want := profileFor(false), capProfileAt(1280, 720); got != want {
		t.Errorf("a Show is asked for %s, want the 720p profile", got)
	}
	if got, want := profileFor(true), capProfileAt(1920, 1080); got != want {
		t.Errorf("the Skylight is asked for %s, want the 1080p profile", got)
	}
	if got, want := capProfile, profileFor(layout.Skylight()); got != want {
		t.Errorf("this board's profile is %s, want %s", got, want)
	}
}
