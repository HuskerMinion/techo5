package dlna

import (
	"encoding/xml"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
)

// notified is one NOTIFY a test subscriber was sent.
type notified struct {
	sid, seq, body string
}

// subscriber is a controller listening on the loopback, recording each event it is sent.
func subscriber(t *testing.T) (string, chan notified) {
	t.Helper()
	got := make(chan notified, 8)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if r.Method == "NOTIFY" {
			got <- notified{r.Header.Get("SID"), r.Header.Get("SEQ"), string(b)}
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/event", got
}

// requestIn reads a request event's X_TECHO5_Request out of its LastChange: what was asked for, and
// the request's number after the last colon.
func requestIn(t *testing.T, body string) (what, seq string) {
	t.Helper()
	var ps struct {
		LastChange string `xml:"property>LastChange"`
	}
	if err := xml.Unmarshal([]byte(body), &ps); err != nil {
		t.Fatalf("the event is not a propertyset: %v\n%s", err, body)
	}
	var ev struct {
		Instance struct {
			Val     string `xml:"val,attr"`
			Request []struct {
				Val string `xml:"val,attr"`
			} `xml:"X_TECHO5_Request"`
		} `xml:"InstanceID"`
	}
	if err := xml.Unmarshal([]byte(ps.LastChange), &ev); err != nil {
		t.Fatalf("LastChange is not an event: %v\n%s", err, html.UnescapeString(ps.LastChange))
	}
	if ev.Instance.Val != "0" {
		t.Errorf("the request is for InstanceID %q", ev.Instance.Val)
	}
	if len(ev.Instance.Request) != 1 {
		t.Fatalf("%d requests in one event", len(ev.Instance.Request))
	}
	v := ev.Instance.Request[0].Val
	i := strings.LastIndex(v, ":")
	if i < 0 {
		t.Fatalf("request %q has no number", v)
	}
	what, seq = v[:i], v[i+1:]
	if _, err := strconv.ParseUint(seq, 10, 32); err != nil {
		t.Errorf("request number %q: %v", seq, err)
	}
	return what, seq
}

func playingRenderer(t *testing.T) *Feature {
	t.Helper()
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	_ = config.Set().Streaming().DLNA(true)
	f := &Feature{}
	f.r.f = f
	// Set from the loopback, where the tests' subscribers listen: the controller that set it.
	f.r.cur = song{uri: "http://192.0.2.10/song.flac", title: "Winding Road", from: "127.0.0.1"}
	f.r.state = stPlaying
	return f
}

// Next pressed on the device for the renderer's song is told to the controller subscribed to the
// transport, once, as TECHO5's own request in a LastChange; previous goes the same way under the next
// number. A subscriber to the volume alone has no queue to skip through and hears nothing of it.
func TestASkipOnTheDeviceIsToldToTheController(t *testing.T) {
	f := playingRenderer(t)
	avt, got := subscriber(t)
	rc, rcGot := subscriber(t)
	f.e.subs = map[string]*subscription{
		"uuid:avt": {service: "AVTransport", callback: avt, seq: 5, until: time.Now().Add(time.Hour), asks: askNext | askPrevious},
		"uuid:rc":  {service: "RenderingControl", callback: rc, seq: 1, until: time.Now().Add(time.Hour)},
	}
	if !f.Skippable() {
		t.Fatal("a skip is not offered with a controller listening")
	}

	if !f.Skip(media.TransportNext) {
		t.Fatal("Skip says nobody was told")
	}
	n := nextEvent(t, got)
	if n.sid != "uuid:avt" || n.seq != "5" {
		t.Errorf("sent as %s, SEQ %s", n.sid, n.seq)
	}
	what, seq1 := requestIn(t, n.body)
	if what != "next" || seq1 == "" {
		t.Errorf("request %q, number %q", what, seq1)
	}
	if strings.Contains(html.UnescapeString(n.body), "TransportState") {
		t.Error("the request came with the transport's state: it is an event of its own")
	}
	noEvent(t, got)

	f.Skip(media.TransportPrevious)
	what, seq2 := requestIn(t, nextEvent(t, got).body)
	if what != "previous" || seq2 == seq1 {
		t.Errorf("request %q, number %q after %q", what, seq2, seq1)
	}
	noEvent(t, got)

	// Next twice in a row: two values, so a controller comparing with the last sees both.
	f.Skip(media.TransportNext)
	_, seq3 := requestIn(t, nextEvent(t, got).body)
	f.Skip(media.TransportNext)
	_, seq4 := requestIn(t, nextEvent(t, got).body)
	if seq3 == seq4 || seq3 == seq2 {
		t.Errorf("numbers %q then %q after %q", seq3, seq4, seq2)
	}
	noEvent(t, got)
	noEvent(t, rcGot)

	if f.Skip(media.TransportPause) {
		t.Error("a pause was told as a request")
	}
	noEvent(t, got)
}

// With no controller subscribed to the transport, or nothing of the renderer's playing, a skip is not
// offered and nothing is sent.
func TestNoSkipWithNobodyListening(t *testing.T) {
	f := playingRenderer(t)
	rc, rcGot := subscriber(t)
	f.e.subs = map[string]*subscription{
		"uuid:rc": {service: "RenderingControl", callback: rc, until: time.Now().Add(time.Hour)},
	}
	if f.Skippable() || f.Skip(media.TransportNext) {
		t.Error("a skip was offered with nobody subscribed to the transport")
	}
	noEvent(t, rcGot)

	avt, got := subscriber(t)
	f.e.subs["uuid:gone"] = &subscription{service: "AVTransport", callback: avt, until: time.Now().Add(-time.Second), asks: askNext}
	if f.Skippable() || f.Skip(media.TransportNext) {
		t.Error("a skip was offered to a subscription that has run out")
	}
	f.e.subs["uuid:avt"] = &subscription{service: "AVTransport", callback: avt, until: time.Now().Add(time.Hour), asks: askNext}
	f.r.state = stStopped
	if f.Skippable() || f.Skip(media.TransportNext) {
		t.Error("a skip was offered with the renderer stopped")
	}
	f.r.state = stPaused
	if !f.Skippable() {
		t.Error("a skip was not offered for a paused song")
	}
	_ = config.Set().Streaming().DLNA(false)
	if f.Skippable() {
		t.Error("a skip was offered with the renderer off")
	}
	noEvent(t, got)
}

// A video is skippable only as the renderer's own: the video the player has under its id.
func TestAVideoIsSkippableOnlyAsTheRenderers(t *testing.T) {
	f := playingRenderer(t)
	avt, _ := subscriber(t)
	f.e.subs = map[string]*subscription{"uuid:avt": {service: "AVTransport", callback: avt, until: time.Now().Add(time.Hour), asks: askNext}}
	f.r.cur.video, f.r.videoID = true, 7
	if !f.VideoSkippable(7) {
		t.Error("the renderer's video is not skippable")
	}
	if f.VideoSkippable(8) || f.VideoSkippable(0) {
		t.Error("another video was skippable")
	}
}

func nextEvent(t *testing.T, got chan notified) notified {
	t.Helper()
	select {
	case n := <-got:
		return n
	case <-time.After(5 * time.Second):
		t.Fatal("no event was sent")
	}
	return notified{}
}

func noEvent(t *testing.T, got chan notified) {
	t.Helper()
	select {
	case n := <-got:
		t.Fatalf("an event was sent: %s", html.UnescapeString(n.body))
	case <-time.After(300 * time.Millisecond):
	}
}
