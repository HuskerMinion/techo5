// Package web is the device's own web port: the camera's pictures, a screenshot of the panel, and
// the setup page, each behind its own switch and all of them off on a device nobody has told
// otherwise.
//
// The port is only listening while at least one of those is switched on. With all of them off there
// is nothing on the network to find, which is the same behaviour the camera's own server had before
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
		var on []string
		for _, p := range pages {
			if p.label != "" && p.open() {
				on = append(on, p.label+": "+p.path)
			}
		}
		if len(on) == 0 {
			http.NotFound(w, r)
			return
		}
		sort.Strings(on)
		fmt.Fprintln(w, "TECHO5")
		for _, line := range on {
			fmt.Fprintln(w, line)
		}
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
