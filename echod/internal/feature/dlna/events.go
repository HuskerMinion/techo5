package dlna

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
)

// Eventing (GENA): a controller subscribes to a service and is told when it changes, which is how most
// show play and pause without asking every second. Only the controller's own address is called back,
// so a subscription cannot point the device at anything else on the network.

const (
	subscribeFor  = 30 * time.Minute
	mostSubs      = 16
	mostSubsEach  = 4 // one controller subscribes to three services; one host may not take every place
	notifyTimeout = 5 * time.Second
)

type subscription struct {
	service  string
	callback string
	seq      uint32
	until    time.Time
}

type events struct {
	mu   sync.Mutex
	subs map[string]*subscription
}

func (e *events) handle(w http.ResponseWriter, r *http.Request, service string) {
	if service != "AVTransport" && service != "RenderingControl" && service != "ConnectionManager" {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case "SUBSCRIBE":
		e.subscribe(w, r, service)
	case "UNSUBSCRIBE":
		e.mu.Lock()
		delete(e.subs, r.Header.Get("SID"))
		e.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (e *events) subscribe(w http.ResponseWriter, r *http.Request, service string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.expireLocked()
	if sid := r.Header.Get("SID"); sid != "" {
		s, ok := e.subs[sid]
		if !ok || s.service != service {
			w.WriteHeader(http.StatusPreconditionFailed)
			return
		}
		s.until = time.Now().Add(subscribeFor)
		w.Header().Set("SID", sid)
		w.Header().Set("TIMEOUT", "Second-"+strconv.Itoa(int(subscribeFor/time.Second)))
		w.WriteHeader(http.StatusOK)
		return
	}
	cb := firstCallback(r.Header.Get("CALLBACK"))
	u, err := url.Parse(cb)
	if err != nil || u.Scheme != "http" || r.Header.Get("NT") != "upnp:event" {
		w.WriteHeader(http.StatusPreconditionFailed)
		return
	}
	if host := u.Hostname(); host != remoteHost(r) {
		// Only the subscriber's own address: never a third machine.
		w.WriteHeader(http.StatusPreconditionFailed)
		return
	}
	mine := 0
	for _, s := range e.subs {
		if u, err := url.Parse(s.callback); err == nil && u.Hostname() == remoteHost(r) {
			mine++
		}
	}
	if len(e.subs) >= mostSubs || mine >= mostSubsEach {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	if e.subs == nil {
		e.subs = map[string]*subscription{}
	}
	sid := newSID()
	s := &subscription{service: service, callback: cb, until: time.Now().Add(subscribeFor)}
	e.subs[sid] = s
	w.Header().Set("SID", sid)
	w.Header().Set("TIMEOUT", "Second-"+strconv.Itoa(int(subscribeFor/time.Second)))
	w.WriteHeader(http.StatusOK)
	// The first event is the whole state, sent once the answer is out.
	body := lastChange(service)
	safe.Go("dlna event", func() {
		time.Sleep(200 * time.Millisecond)
		send(sid, s.callback, 0, body)
	})
	s.seq = 1
}

// firstCallback is the first URL of a CALLBACK header ("<http://...><http://...>").
func firstCallback(h string) string {
	h = strings.TrimSpace(h)
	if !strings.HasPrefix(h, "<") {
		return ""
	}
	end := strings.IndexByte(h, '>')
	if end < 0 {
		return ""
	}
	return h[1:end]
}

func newSID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return fmt.Sprintf("uuid:%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func (e *events) expireLocked() {
	now := time.Now()
	for sid, s := range e.subs {
		if now.After(s.until) {
			delete(e.subs, sid)
		}
	}
}

// changed tells the transport's and the volume's subscribers what they look like now.
func (e *events) changed() {
	e.mu.Lock()
	e.expireLocked()
	type note struct {
		sid, cb string
		seq     uint32
		body    string
	}
	var notes []note
	for sid, s := range e.subs {
		if s.service == "ConnectionManager" {
			continue
		}
		notes = append(notes, note{sid, s.callback, s.seq, lastChange(s.service)})
		s.seq++
	}
	e.mu.Unlock()
	for _, n := range notes {
		safe.Go("dlna event", func() { send(n.sid, n.cb, n.seq, n.body) })
	}
}

// lastChange is a service's event: its state variables, in UPnP's LastChange form.
func lastChange(service string) string {
	var inner string
	switch service {
	case "AVTransport":
		r := &Get().r
		st := r.sync()
		r.mu.Lock()
		cur := r.cur
		r.mu.Unlock()
		inner = `<Event xmlns="urn:schemas-upnp-org:metadata-1-0/AVT/"><InstanceID val="0">` +
			`<TransportState val="` + st + `"/><TransportStatus val="OK"/>` +
			`<AVTransportURI val="` + esc(cur.uri) + `"/><CurrentTrackURI val="` + esc(cur.uri) + `"/>` +
			`<CurrentTrackMetaData val="` + esc(cur.meta) + `"/><CurrentTrackDuration val="` + hms(cur.dur) + `"/>` +
			`</InstanceID></Event>`
	case "RenderingControl":
		vol := media.Get().Volume() * 100 / config.VolumeSteps
		inner = `<Event xmlns="urn:schemas-upnp-org:metadata-1-0/RCS/"><InstanceID val="0">` +
			`<Volume channel="Master" val="` + strconv.Itoa(vol) + `"/><Mute channel="Master" val="0"/></InstanceID></Event>`
	case "ConnectionManager":
		return `<e:propertyset xmlns:e="urn:schemas-upnp-org:event-1-0"><e:property><SinkProtocolInfo>` + esc(sinkProtocols()) +
			`</SinkProtocolInfo></e:property><e:property><SourceProtocolInfo></SourceProtocolInfo></e:property>` +
			`<e:property><CurrentConnectionIDs>0</CurrentConnectionIDs></e:property></e:propertyset>`
	}
	return `<e:propertyset xmlns:e="urn:schemas-upnp-org:event-1-0"><e:property><LastChange>` + esc(inner) +
		`</LastChange></e:property></e:propertyset>`
}

var notifyClient = &http.Client{
	Timeout: notifyTimeout,
	Transport: &http.Transport{
		DialContext: (&net.Dialer{Timeout: 3 * time.Second}).DialContext,
	},
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

func send(sid, callback string, seq uint32, body string) {
	ctx, cancel := context.WithTimeout(context.Background(), notifyTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "NOTIFY", callback, bytes.NewBufferString(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", `text/xml; charset="utf-8"`)
	req.Header.Set("NT", "upnp:event")
	req.Header.Set("NTS", "upnp:propchange")
	req.Header.Set("SID", sid)
	req.Header.Set("SEQ", strconv.FormatUint(uint64(seq), 10))
	resp, err := notifyClient.Do(req)
	if err != nil {
		return
	}
	resp.Body.Close()
}
