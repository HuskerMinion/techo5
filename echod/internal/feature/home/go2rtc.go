package home

import (
	"context"
	"log/slog"
	"strings"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/lib/go2rtc"
)

// Cameras from a go2rtc server (lib/go2rtc), beside Home Assistant's and a Reolink recorder's: its streams
// by name, their pictures from go2rtc, and their live view from the RTSP address go2rtc serves each at,
// with no camera's own login needed. Home Assistant is not asked about them at all, so they work with it
// down, and a camera Home Assistant does not have is one more on the list.

// go2rtcPrefix marks a go2rtc stream in the camera list: "go2rtc:porch".
const go2rtcPrefix = "go2rtc:"

func isGo2RTC(entity string) bool { return strings.HasPrefix(entity, go2rtcPrefix) }

// IsGo2RTC is whether a camera on the list is one of go2rtc's.
func IsGo2RTC(entity string) bool { return isGo2RTC(entity) }

// SetGo2RTC reads the streams of the go2rtc server at address into the camera list, and answers how many.
// An empty address takes them away again.
func SetGo2RTC(address string) (int, error) {
	if strings.TrimSpace(address) == "" {
		if err := config.Set().Home().Go2RTC(config.Go2RTC{}); err != nil {
			return 0, err
		}
		Get().Changed.Emit(struct{}{})
		return 0, nil
	}
	base, err := go2rtc.Base(address)
	if err != nil {
		return 0, err
	}
	c, err := go2rtc.New(base.String())
	if err != nil {
		return 0, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	names, err := c.Streams(ctx)
	if err != nil {
		return 0, err
	}
	// A stream given a name of its own on the setup page keeps it when the list is read again.
	given := map[string]string{}
	for _, c := range config.Get().Home.Go2RTC.Cameras {
		given[c.Entity] = c.Name
	}
	cams := make([]config.Camera, 0, len(names))
	for _, n := range names {
		name := n
		if g := given[go2rtcPrefix+n]; g != "" {
			name = g
		}
		cams = append(cams, config.Camera{Entity: go2rtcPrefix + n, Name: name})
	}
	if err := config.Set().Home().Go2RTC(config.Go2RTC{Base: base.String(), Cameras: cams}); err != nil {
		return 0, err
	}
	Get().Changed.Emit(struct{}{})
	return len(cams), nil
}

// go2rtcClient is the client for the server set up here.
func go2rtcClient() (*go2rtc.Client, error) {
	return go2rtc.New(config.Get().Home.Go2RTC.Base)
}

// go2rtcSnap is a picture of a go2rtc stream, about width wide.
func go2rtcSnap(ctx context.Context, entity string, width int) ([]byte, error) {
	c, err := go2rtcClient()
	if err != nil {
		return nil, err
	}
	return c.Frame(ctx, strings.TrimPrefix(entity, go2rtcPrefix), width)
}

// go2rtcRTSP is the RTSP address go2rtc serves a stream at.
func go2rtcRTSP(entity string) (string, bool) {
	c, err := go2rtcClient()
	if err != nil {
		return "", false
	}
	return c.RTSP(strings.TrimPrefix(entity, go2rtcPrefix)), true
}

// go2rtcAction sets the go2rtc server from Home Assistant, or takes it away with an empty address, and
// answers how many cameras it has.
func (f *Feature) go2rtcAction() *esphome.Action {
	return &esphome.Action{
		Name:    "home_go2rtc",
		Args:    []esphome.Arg{{Name: "address", Type: esphome.ArgString}},
		Answers: true,
		Run: func(c esphome.Call) (any, error) {
			n, err := SetGo2RTC(c.String("address"))
			if err != nil {
				return nil, err
			}
			slog.Info("home: go2rtc cameras set", "cameras", n)
			return map[string]int{"cameras": n}, nil
		},
	}
}
