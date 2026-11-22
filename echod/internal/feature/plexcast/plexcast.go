//go:build !dot

// Package plexcast makes the device a player Plex can cast to.
//
// Plex finds players two ways, and this package does both. On the home network it answers Plex's GDM
// discovery -- a bare "M-SEARCH * HTTP/1.0" to 239.0.0.250 and a unicast answer that names this
// device and the port its control server listens on -- which is how Plex Web, Plex Desktop and Plex
// for Android TV list it. Through plex.tv it links an account with a four-character PIN and keeps a
// socket open from the account's side, which is how the Plex apps on a phone and Plexamp reach a
// device that is not on their network.
//
// The commands a casting app sends -- playMedia, play, pause, seekTo, setParameters and the rest --
// arrive on port 32500 and are the same commands over either path: the cloud's messages are turned
// into requests against the same handlers, so the two can never drift apart. The device plays what it
// is told with its own player: music as a received track (feature/media), named on Now Playing, and a
// film full screen (feature/video), with Plex asked for a stream this hardware decodes -- H.264 at
// 720p, with subtitles burned in when they are on. What it is playing is reported back to the Plex
// server every ten seconds, so the server's Now Playing page and the phone's transport stay in step.
//
// Only a device with a screen is a player, so this package is not built for the Dot (here.go,
// none.go), and nothing of it listens until an account is linked: no token, no socket, no port, no
// registration. A device nobody casts to costs nothing at all.
package plexcast

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
)

func init() {
	component.Register(component.Network, Get(), component.Order(84))
}

const (
	// Port is the Companion control port: the one number every Plex client is told, in the GDM answer
	// and in the cloud's registration alike. It is not a web page and needs no password: the commands
	// are the casting app's, and what they play is what the owner asked for.
	Port = 32500

	// What the device says it is. product is the name Plex shows in an account's device list; platform
	// and device are the same strings Plex's own players send, because some of its servers key a
	// transcode profile on them.
	product     = "TECHO5"
	platform    = "Linux"
	device      = "Echo Show 5"
	deviceClass = "tv"

	// protocolVersion is the Companion API this implements: version 1, advertised as 3 as Plex Web
	// advertises it, which controllers read as "at least the version I need".
	protocolVersion = 3

	// protocolCaps is what a controller may ask of the device. Only what is implemented is claimed:
	// a controller draws its buttons from this list.
	protocolCaps = "timeline,playback,navigation,playqueues"
)

// Feature is the device as a Plex player. It runs while an account is linked, and the loop below is
// the whole of its lifetime: linked starts its servers, unlinked stops them, and a network change
// starts them again so the device announces itself at its new address.
type Feature struct {
	mu   sync.Mutex
	wake chan struct{}
	st   Status
	els  []func()

	// pin is the link being made, if any (pin.go). pb is what is playing (playback.go), and tl is what
	// the controllers are told about it (timeline.go). mux is the Companion server's commands
	// (server.go), built once: the cloud's messages are run against it too (pubsub.go).
	pin pinFlow
	pb  *playback
	tl  *timeline
	mux *http.ServeMux
}

var (
	once   sync.Once
	shared *Feature
)

// Get is the device's one Plex player.
func Get() *Feature {
	once.Do(func() {
		shared = &Feature{wake: make(chan struct{}, 1)}
		shared.pb = newPlayback(shared)
		shared.tl = newTimeline(shared)
		shared.mux = shared.buildMux()
	})
	return shared
}

func (f *Feature) Name() string { return "plex" }

// On is whether the device is a Plex player now: an account is linked, so there is something to cast
// to and somebody to cast for. The setup page and the settings screen both ask this.
func On() bool { return config.Get().Plex.Linked() }

// Linked is On, for the code that has the config in hand already.
func (f *Feature) Linked() bool { return config.Get().Plex.Linked() }

// Current is what the settings screen and the setup page say about the account. It is a copy: a caller
// drawing a row must never hold anything the pin poll writes to.
func Current() Status { return Get().status() }

func (f *Feature) status() Status {
	f.mu.Lock()
	defer f.mu.Unlock()
	st := f.st
	st.Linked = f.Linked()
	if u := config.Get().Plex.User; st.Linked {
		st.User = u
	}
	return st
}

// Listen is told when the account or what is playing changes, for a screen that is showing either.
func Listen(fn func()) {
	g := Get()
	g.mu.Lock()
	g.els = append(g.els, fn)
	g.mu.Unlock()
}

// changed tells the listeners, and starts or stops the servers when the account went on or off.
func (f *Feature) changed() {
	f.mu.Lock()
	els := append([]func(){}, f.els...)
	f.mu.Unlock()
	for _, fn := range els {
		fn()
	}
	select {
	case f.wake <- struct{}{}:
	default:
	}
}

// Run is the player's life: nothing at all while no account is linked, and four things while one is --
// the control server, the discovery answer, the account's socket, and the clock that reports what is
// playing. The account going away ends them, and linking one starts them again. A new address needs
// none of that: the control port is every interface, and the announcements name the addresses the
// device has each time they go out.
func (f *Feature) Run(ctx context.Context) error {
	for {
		if !f.Linked() {
			f.off()
			select {
			case <-ctx.Done():
				return nil
			case <-f.wake:
			}
			continue
		}
		sctx, cancel := context.WithCancel(ctx)
		done := make(chan struct{})
		go func() {
			var wg sync.WaitGroup
			for _, serve := range []func(context.Context){
				f.serve,     // the Companion server on Port
				f.announce,  // Plex's GDM: searches answered, the player announced
				f.cloud,     // the account's own socket, for a phone that is elsewhere
				f.heartbeat, // what is playing, reported to the server
			} {
				wg.Add(1)
				go func(serve func(context.Context)) {
					defer wg.Done()
					serve(sctx)
				}(serve)
			}
			wg.Wait()
			close(done)
		}()
		f.openPorts()
		f.pb.off()
		safe.Go("plex register", func() { f.register(sctx) })
		slog.Info("plex: player on", "name", friendlyName())
		for f.Linked() && ctx.Err() == nil {
			select {
			case <-ctx.Done():
			case <-f.wake:
			}
		}
		cancel()
		<-done
		f.closePorts()
		f.pb.stop()
		f.tl.clear()
		slog.Info("plex: player off")
		select {
		case <-ctx.Done():
			return nil
		default:
		}
	}
}

// off forgets an account that is no longer linked: a link that was in progress, what was playing, and
// the status the screens read.
func (f *Feature) off() {
	f.mu.Lock()
	if f.pin.cancel != nil {
		f.pin.cancel()
	}
	f.pin = pinFlow{}
	f.st = Status{}
	f.mu.Unlock()
}

// heartbeat is the clock: a tick a second while something plays, which is what the timeline is built
// from and when the server is told (timeline.go, report.go). Idle, it does nothing at all.
func (f *Feature) heartbeat(ctx context.Context) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			f.pb.tick()
		}
	}
}
