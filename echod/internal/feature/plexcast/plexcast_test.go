//go:build !dot

package plexcast

import (
	"strings"
	"testing"
	"time"
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
