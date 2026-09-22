// Package web is the device's own web port: the camera's pictures, a screenshot of the panel, and
// the setup page, each behind its own switch and all of them off on a device nobody has told
// otherwise.
//
// The port is only listening while at least one of those is switched on. With all of them off there
// is nothing on the network to find, which is the same behavior the camera's own server had before
// this became a place several features share.
//
// It lives on its own rather than inside the camera feature because a Dot has no camera, so that
// server never started there — and the setup page is the only way to configure a device with no
// screen.
package web

import (
	"context"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"net"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/feature/security"
)

func init() {
	component.Register(component.Network, Get(), component.Order(70))
}

// Port is where all of it is served: http://<device>:8181/.
const Port = 8181

// page is one path and the switch that decides whether it is there at all.
type page struct {
	path  string
	label string // what the index calls it, empty to leave it out
	open  func() bool
	h     http.HandlerFunc
}

type Feature struct {
	mu    sync.Mutex
	pages []page
	poke  chan struct{}
}

var (
	once   sync.Once
	shared *Feature
)

func Get() *Feature {
	once.Do(func() { shared = &Feature{poke: make(chan struct{}, 1)} })
	return shared
}

func (f *Feature) Name() string { return "web" }

// Handle adds a path, served while open reports true and answered as not found while it does not.
// Features call this as they are built, before anything runs.
func Handle(path, label string, open func() bool, h http.HandlerFunc) {
	f := Get()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pages = append(f.pages, page{path: path, label: label, open: open, h: h})
}

// letIn answers whether a request carries a session the setup page has let in, which is the only
// authorization this port has: a press on the device, made by somebody standing at it.
//
// The setup page hands the check in rather than being imported here, because it is the page that
// imports this package. It is handed in while the features are being built, before anything is
// served, and never changed after that. A build without a setup page leaves it nil, and then
// nothing on this port is authorized, which is the answer that refuses rather than allows.
var letIn func(*http.Request) bool

// Guard is how the setup page hands its session check in. Called once, as that feature is built.
func Guard(check func(*http.Request) bool) { letIn = check }

// LetIn reports whether this request comes from a browser the setup page has let in. Pages on this
// port use it for anything that changes what the device is doing; reading needs only the switch.
func LetIn(r *http.Request) bool { return letIn != nil && letIn(r) }

// Wake has the port looked at again, for a switch that is not one of Home Assistant's — the setup
// page closing itself after its idle time, say.
func Wake() {
	select {
	case Get().poke <- struct{}{}:
	default:
	}
}

// anyOpen is whether anything is switched on, which is whether the port should be listening.
func (f *Feature) anyOpen() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, p := range f.pages {
		if p.open() {
			return true
		}
	}
	return false
}

// mux is built once everything has registered: the pages, each behind its switch, and an index that
// lists the ones that are on.
func (f *Feature) mux() *http.ServeMux {
	f.mu.Lock()
	pages := append([]page(nil), f.pages...)
	f.mu.Unlock()

	m := http.NewServeMux()
	for _, p := range pages {
		m.HandleFunc(p.path, allowed(p.open, p.h))
	}
	m.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		var on []page
		for _, p := range pages {
			if p.label != "" && p.open() {
				on = append(on, p)
			}
		}
		if len(on) == 0 {
			http.NotFound(w, r)
			return
		}
		// Whoever typed the address wanted the one thing this port is open for. A Dot's setup page is
		// always that: an index of one is a list somebody has to read and then type the rest of.
		if len(on) == 1 {
			http.Redirect(w, r, on[0].path, http.StatusSeeOther)
			return
		}
		sort.Slice(on, func(i, j int) bool { return on[i].label < on[j].label })
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, "<!doctype html><meta name=viewport content=\"width=device-width,initial-scale=1\">"+
			"<title>TECHO5</title><h1>TECHO5</h1><ul>")
		for _, p := range on {
			fmt.Fprintf(w, `<li><a href="%s">%s</a></li>`, p.path, html.EscapeString(p.label))
		}
		fmt.Fprint(w, "</ul>")
	})
	return m
}

// allowed serves a page only while its switch is on; otherwise the page is not there.
func allowed(open func() bool, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !open() {
			http.NotFound(w, r)
			return
		}
		h(w, r)
	}
}

// Run keeps the port open while anything is switched on and shut while nothing is.
func (f *Feature) Run(ctx context.Context) error {
	mux := f.mux()

	changed := make(chan struct{}, 1)
	defer security.Get().Changed.Listen(func(struct{}) {
		select {
		case changed <- struct{}{}:
		default:
		}
	})()

	var srv *http.Server
	defer func() {
		if srv != nil {
			_ = srv.Close()
		}
	}()
	for {
		switch want := f.anyOpen(); {
		case want && srv == nil:
			ln, err := net.Listen("tcp", ":"+strconv.Itoa(Port))
			if err != nil {
				slog.Error("web port", "port", Port, "err", err)
				break
			}
			srv = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
			go func(srv *http.Server) {
				if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
					slog.Error("web port", "err", err)
				}
			}(srv)
			slog.Info("web port open", "port", Port)
		case !want && srv != nil:
			_ = srv.Close() // streams in progress end here too
			srv = nil
			slog.Info("web port closed", "port", Port)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-changed:
		case <-f.poke:
		case <-time.After(time.Minute): // a port that failed to open is tried again
		}
	}
}
