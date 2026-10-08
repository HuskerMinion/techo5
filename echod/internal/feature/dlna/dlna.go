// Package dlna makes the device a DLNA (UPnP) media renderer: a speaker that apps and music servers on
// the home network can send songs to. BubbleUPnP on a phone, a Jellyfin, Plex or Emby server, a NAS's
// own music app, foobar2000 and Windows' "Cast to device" all find it by name and play to it.
//
// The device answers SSDP on the network (who is there), describes itself and its three services over
// HTTP on the web port, and takes the controller's commands as SOAP: a song's address and what it is,
// play, pause, stop, the volume. The song itself is fetched by the device, decoded (MP3, FLAC or WAV)
// and played as a received track, as AirPlay's is: a voice turn ducks it, an alarm sounds over it, and
// whatever starts last has the speaker. Its name, artist and cover go on Now Playing.
//
// Off until it is turned on (a switch in Home Assistant, on the settings screen and on the setup page):
// it answers anybody on the network who asks, as every DLNA speaker does.
package dlna

import (
	"context"
	"crypto/rand"
	"fmt"
	"log/slog"
	"sync"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/video"
	"github.com/HuskerMinion/techo5/echod/internal/feature/web"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
)

func init() {
	component.Register(component.Network, Get(), component.Order(82))
	web.Handle(pathPrefix, "", On, Get().serve)
}

// pathPrefix is where everything of the renderer's is on the web port.
const pathPrefix = "/dlna/"

type Feature struct {
	sw   *esphome.Switch
	wake chan struct{}

	r renderer
	e events
}

var (
	once   sync.Once
	shared *Feature
)

func Get() *Feature {
	once.Do(func() {
		shared = &Feature{wake: make(chan struct{}, 1)}
		shared.sw = &esphome.Switch{
			Base:      esphome.Base{ObjectID: "dlna", Name: "DLNA", Icon: "mdi:cast-audio", Category: esphome.CategoryConfig},
			OnCommand: shared.Set,
		}
		shared.r.f = shared
		// A video's state is the transport's when the song is one: its controllers hear when it
		// starts, pauses and ends.
		video.Listen(func() { shared.e.changed() })
	})
	return shared
}

func (f *Feature) Name() string { return "dlna" }

func (f *Feature) Entities() []esphome.Entity { return []esphome.Entity{f.sw} }

func (f *Feature) Restore(c config.Config) { f.sw.Set(c.Streaming.DLNA) }

// On is whether the device is a DLNA renderer now.
func On() bool { return config.Get().Streaming.DLNA }

// Set is the switch, from Home Assistant, the screen or the setup page.
func (f *Feature) Set(on bool) {
	if err := config.Set().Streaming().DLNA(on); err != nil {
		slog.Error("saving a setting failed", "setting", "dlna", "err", err)
		f.sw.Set(!on)
		return
	}
	f.sw.Set(on)
	slog.Info("setting changed", "setting", "dlna", "using", on)
	web.Wake()
	select {
	case f.wake <- struct{}{}:
	default:
	}
}

// Run answers on the network while the switch is on, and says goodbye when it goes off.
func (f *Feature) Run(ctx context.Context) error {
	for {
		if On() {
			sctx, cancel := context.WithCancel(ctx)
			done := make(chan struct{})
			go func() {
				defer close(done)
				f.announce(sctx)
			}()
			go f.r.watch(sctx)
			for On() && ctx.Err() == nil {
				select {
				case <-ctx.Done():
				case <-f.wake:
				}
			}
			cancel()
			<-done
			f.r.off()
		}
		select {
		case <-ctx.Done():
			return nil
		case <-f.wake:
		}
	}
}

// udn is the device's own UPnP id: random, made once and kept, so a controller keeps it as one speaker
// across restarts and nothing about the device (its MAC) can be worked out from it.
func udn() string {
	if id := config.Get().Streaming.DLNAID; id != "" {
		return id
	}
	udnMu.Lock()
	defer udnMu.Unlock()
	if id := config.Get().Streaming.DLNAID; id != "" {
		return id
	}
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40 // a random UUID
	b[8] = b[8]&0x3f | 0x80
	id := fmt.Sprintf("uuid:%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
	if err := config.Set().Streaming().DLNAID(id); err != nil {
		slog.Warn("dlna: saving the renderer's id failed", "err", err)
	}
	return id
}

var udnMu sync.Mutex

// friendlyName is what controllers list the device as.
func friendlyName() string {
	if n := config.Get().Device.Name; n != "" {
		return n
	}
	return layout.DefaultName
}
