// Package remote is the device's remote API: a small HTTP interface on its web port for scripts and
// tools that are not Home Assistant. It reads what the device is and what its entities say, sets the
// ones that are not about privacy or security, and follows the log as it is written.
//
// It is off on a new device, and on only once a key has been made on the setup page. Every request is
// signed with that key (auth.go); the key is shown once and never kept. What it may change is narrower
// than what Home Assistant may (entities.go): nothing that opens a door, listens, watches, records, updates
// or restarts.
package remote

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/web"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
)

func init() {
	component.Register(component.Device, Get(), component.Order(95))
}

const (
	// prefix is where the API is, on the device's web port.
	prefix = "/api/v1/"

	// maxBody is more than any request needs.
	maxBody = 64 << 10

	// warnEvery is how often a request that does not check out is written to the log.
	warnEvery = time.Minute
)

// bodyWithin is how long a signed request's body has to arrive; a variable for the tests.
var bodyWithin = 10 * time.Second

// started is when the daemon started, for the uptime.
var started = time.Now()

// Remote is the switch and the API behind it.
type Remote struct {
	on *esphome.Switch

	// entities is what the API reads and sets; the registry's, replaced in tests.
	entities func() []esphome.Entity

	mu       sync.Mutex
	lastWarn time.Time
}

var (
	once   sync.Once
	shared *Remote
)

// Get is the one.
func Get() *Remote {
	once.Do(func() { shared = build() })
	return shared
}

func build() *Remote {
	f := &Remote{
		on: &esphome.Switch{Base: esphome.Base{
			ObjectID: "remote_api", Name: "Remote API", Icon: "mdi:api", Category: esphome.CategoryConfig,
		}},
		entities: func() []esphome.Entity { return component.Default().Entities() },
	}
	f.on.OnCommand = func(on bool) { f.SetOn(on) }
	web.Handle(prefix, "", f.open, f.serve)
	return f
}

func (f *Remote) Name() string { return "remote api" }

func (f *Remote) Entities() []esphome.Entity { return []esphome.Entity{f.on} }

func (f *Remote) Restore(c config.Config) {
	f.on.Set(c.Remote.On)
	slog.Info("restored", "what", f.on.ObjectID, "using", c.Remote.On)
}

// SetOn turns the API on or off.
func (f *Remote) SetOn(on bool) {
	f.on.Set(on)
	if err := config.Set().Remote().On(on); err != nil {
		slog.Error("saving the remote api switch failed", "err", err)
	}
	slog.Info("remote api", "on", on)
	web.Wake()
}

// NewKey makes a new key, keeps its secret and answers the key, to be shown once. Any key made before
// stops working.
func (f *Remote) NewKey() (string, error) {
	key := NewKey()
	if err := config.Set().Remote().Secret(SecretOf(key)); err != nil {
		return "", err
	}
	slog.Warn("remote api: a new key was made; any earlier one no longer works")
	web.Wake()
	return key, nil
}

// HasKey is whether a key has been made.
func HasKey() bool { return config.Get().Remote.Secret != "" }

// open is whether the API is served: on, with a key to check requests against.
func (f *Remote) open() bool {
	c := config.Get()
	return c.Remote.On && c.Remote.Secret != ""
}

// serve checks a request and hands it on.
func (f *Remote) serve(w http.ResponseWriter, r *http.Request) {
	// Never for one from away, over the tailnet: the server's gate turns it away first, and so does this.
	if web.AwayRequest(r) {
		http.NotFound(w, r)
		return
	}
	now := time.Now()
	addr := source(r)
	if turnedAway(addr, now) {
		refuse(w, "too many requests that did not check out; wait a minute", http.StatusTooManyRequests)
		return
	}
	// Nothing is read from a request that is not even signed: the server has no read timeout (the
	// intercom's streams cannot have one), so a body trickled in by anybody would hold the connection for
	// as long as they liked. One that is signed has bodyWithin to arrive.
	if !looksSigned(r.Header.Get(authHeader)) {
		failed(addr, now)
		f.warn("remote api: a request with no signature", "from", addr)
		refuse(w, errUnsigned.Error(), http.StatusUnauthorized)
		return
	}
	// A time out of reach is not counted against the address: it is as likely a clock as a guesser, and
	// counting it would lock a script out over its clock.
	if !inTime(r.Header.Get(authHeader), now) {
		f.warn("remote api: a request signed at the wrong time", "from", addr)
		refuse(w, errStale.Error(), http.StatusUnauthorized)
		return
	}
	// Bodies not yet checked are read a few at a time: each is waited on for up to bodyWithin.
	select {
	case unverified <- struct{}{}:
		defer func() { <-unverified }()
	default:
		refuse(w, "busy; try again", http.StatusServiceUnavailable)
		return
	}
	// The deadline is taken off again once the whole body is in: the server goes on reading the connection
	// in the background, and a deadline left on it would end a log stream when it passed. A body that did not
	// come whole keeps it, and the connection is closed with the answer.
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(now.Add(bodyWithin))
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
	switch {
	case err != nil:
		failed(addr, now)
		refuse(w, "the request did not arrive whole in time", http.StatusRequestTimeout)
		return
	case len(body) > maxBody:
		failed(addr, now)
		refuse(w, "that request was too big", http.StatusRequestEntityTooLarge)
		return
	}
	_ = rc.SetReadDeadline(time.Time{})
	if err := verify(config.Get().Remote.Secret, r.Header.Get(authHeader), r.Method, r.URL.RequestURI(), body, now); err != nil {
		if err != errStale {
			failed(addr, now)
		}
		f.warn("remote api: a request that does not check out", "from", addr, "err", err)
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}

	switch path := strings.TrimPrefix(r.URL.Path, prefix); {
	case path == "device" && r.Method == http.MethodGet:
		f.device(w)
	case path == "entities" && r.Method == http.MethodGet:
		writeJSON(w, http.StatusOK, f.list())
	case strings.HasPrefix(path, "entities/") && r.Method == http.MethodPost:
		f.set(w, strings.TrimPrefix(path, "entities/"), body)
	case path == "logs" && r.Method == http.MethodGet:
		f.logs(w, r)
	default:
		http.Error(w, "no such thing here", http.StatusNotFound)
	}
}

// refuse answers a request that is turned away before its body has been read whole, and closes the
// connection with it (web.Refuse).
func refuse(w http.ResponseWriter, msg string, code int) { web.Refuse(w, msg, code) }

// device is what the device is.
func (f *Remote) device(w http.ResponseWriter) {
	writeJSON(w, http.StatusOK, map[string]any{
		"name":     config.Get().Device.Name,
		"model":    layout.Model,
		"software": layout.Version,
		"uptime_s": int64(time.Since(started) / time.Second),
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// warn logs, at most once every warnEvery.
func (f *Remote) warn(msg string, args ...any) {
	f.mu.Lock()
	now := time.Now()
	quiet := !f.lastWarn.IsZero() && now.Sub(f.lastWarn) < warnEvery
	if !quiet {
		f.lastWarn = now
	}
	f.mu.Unlock()
	if !quiet {
		slog.Warn(msg, args...)
	}
}

// unverified holds a place for each request whose body is being read before its signature is checked,
// so a few slow senders can only ever hold a few of them.
var unverified = make(chan struct{}, maxUnverified)

// maxUnverified is how many such bodies are read at once.
const maxUnverified = 4
