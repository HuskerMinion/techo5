package dlna

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
)

// Home Assistant's DLNA player, Music Assistant's and BubbleUPnP subscribe to the transport without
// saying they act on TECHO5's own requests: with only such a controller listening, nothing is offered
// (the next-song word, the video's buttons and a swipe on the Spot do what they do for any received
// track) and nothing is sent. A controller is offered and sent only the requests it named.
func TestNextAndPreviousGoOnlyToAControllerThatSaysItActsOnThem(t *testing.T) {
	f := playingRenderer(t)
	avt, got := subscriber(t)
	f.e.subs = map[string]*subscription{"uuid:avt": {service: "AVTransport", callback: avt, until: time.Now().Add(time.Hour)}}
	if f.Skippable() || f.Skip(media.TransportNext) || f.Skip(media.TransportPrevious) {
		t.Error("a skip went to a controller that did not say it acts on one")
	}
	f.e.subs["uuid:avt"].asks = askSeek
	if f.Skippable() || f.Skip(media.TransportNext) {
		t.Error("a skip went to a controller that said only seek")
	}
	noEvent(t, got)

	f.e.subs["uuid:avt"].asks = askNext
	if !f.Skippable() {
		t.Fatal("no skip offered to a controller that said next")
	}
	if !f.Skip(media.TransportNext) {
		t.Fatal("next was not told")
	}
	if what, _ := requestIn(t, nextEvent(t, got).body); what != "next" {
		t.Errorf("request %q", what)
	}
	if f.Skip(media.TransportPrevious) {
		t.Error("previous went to a controller that said only next")
	}
	noEvent(t, got)
}

// A request is about the song on the device, and goes only to the controller that set it: another
// subscribed from elsewhere (an app looking on) is neither counted nor sent it, whatever it said it
// acts on.
func TestRequestsGoOnlyToTheSongsOwnController(t *testing.T) {
	f := playingRenderer(t)
	f.r.cur.dur = 5 * time.Minute
	avt, got := subscriber(t)
	all := askNext | askPrevious | askSeek
	f.e.subs = map[string]*subscription{
		"uuid:avt":   {service: "AVTransport", callback: avt, seq: 1, until: time.Now().Add(time.Hour), asks: all},
		"uuid:other": {service: "AVTransport", callback: "http://192.0.2.50:9/event", seq: 3, until: time.Now().Add(time.Hour), asks: all},
	}
	if !f.Skip(media.TransportNext) || !f.SeekFromDevice(0, time.Minute) {
		t.Fatal("the song's own controller was not told")
	}
	nextEvent(t, got)
	nextEvent(t, got)
	if seq := f.e.subs["uuid:other"].seq; seq != 3 {
		t.Errorf("another controller was sent the requests (SEQ now %d)", seq)
	}

	// Set by a controller that is not subscribed: nobody to skip or seek for.
	f.r.cur.from = "192.0.2.77"
	if f.Skippable() || f.Skip(media.TransportNext) {
		t.Error("a skip was offered for a song whose controller is not listening")
	}
	if way, _ := f.SeekWay(); way != SeekNone {
		t.Errorf("a seek was offered for a song whose controller is not listening: %v", way)
	}
	noEvent(t, got)
}

// A SUBSCRIBE to the transport keeps what its requests header named; one to the volume keeps nothing.
func TestASubscribeKeepsWhatItActsOn(t *testing.T) {
	stubs(t)
	var e events
	sub := func(service, header string) requests {
		req := httptest.NewRequest("SUBSCRIBE", pathPrefix+service+"/event", nil)
		req.RemoteAddr = "192.0.2.20:5000"
		req.Header.Set("CALLBACK", "<http://192.0.2.20:5001/event>")
		req.Header.Set("NT", "upnp:event")
		if header != "" {
			req.Header.Set(requestsHeader, header)
		}
		w := httptest.NewRecorder()
		e.handle(w, req, service)
		e.mu.Lock()
		defer e.mu.Unlock()
		s := e.subs[w.Header().Get("SID")]
		if s == nil {
			t.Fatalf("%s: not subscribed (%d)", service, w.Code)
		}
		return s.asks
	}
	if a := sub("AVTransport", "next, previous, seek"); a != askNext|askPrevious|askSeek {
		t.Errorf("the transport's: %b", a)
	}
	if a := sub("AVTransport", ""); a != 0 {
		t.Errorf("without the header: %b", a)
	}
	if a := sub("RenderingControl", "next"); a != 0 {
		t.Errorf("the volume's: %b", a)
	}
}
