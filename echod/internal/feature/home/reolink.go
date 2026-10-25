package home

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/lib/reolink"
)

// Reolink cameras read directly from their NVR, Home Hub or camera (config.Home.Reolink), for a device
// with no Home Assistant to proxy them. Each is a camera like Home Assistant's, "reolink:<channel>",
// shown by the same camera page from the same kind of snapshot, and named for voice as the recorder
// names it.

const reolinkPrefix = "reolink:"

func isReolink(entity string) bool { return strings.HasPrefix(entity, reolinkPrefix) }

var rl struct {
	sync.Mutex
	c  *reolink.Client
	of config.Reolink
}

// reolinkClient is the client for the recorder as configured, made again when that changes.
func reolinkClient() (*reolink.Client, error) {
	r := config.Get().Home.Reolink
	if r.Base == "" {
		return nil, fmt.Errorf("no Reolink recorder is set up")
	}
	rl.Lock()
	defer rl.Unlock()
	if rl.c == nil || rl.of.Base != r.Base || rl.of.User != r.User || rl.of.Pass != r.Pass || rl.of.Fingerprint != r.Fingerprint {
		rl.c = &reolink.Client{Base: r.Base, User: r.User, Pass: r.Pass, Fingerprint: r.Fingerprint}
		rl.of = r
	}
	return rl.c, nil
}

func reolinkSnap(entity string) ([]byte, error) {
	fw, _ := cameraFrameSize()
	return reolinkSnapAt(entity, fw)
}

// reolinkSnapAt is a Reolink camera's snapshot about width wide, as the recorder makes it.
func reolinkSnapAt(entity string, width int) ([]byte, error) {
	ch, err := strconv.Atoi(strings.TrimPrefix(entity, reolinkPrefix))
	if err != nil {
		return nil, fmt.Errorf("not a Reolink camera: %s", entity)
	}
	c, err := reolinkClient()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return c.Snap(ctx, ch, width)
}

// SetReolink sets up a recorder: it is found and logged in to, its certificate pinned, and its cameras
// listed by name. An empty address takes it away.
func SetReolink(host, user, pass string) (int, error) {
	if strings.TrimSpace(host) == "" {
		return 0, config.Set().Home().Reolink(config.Reolink{})
	}
	if pass == "" {
		pass = config.Get().Home.Reolink.Pass // left empty on the form: the one kept
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c, err := reolink.Probe(ctx, host, user, pass)
	if err != nil {
		return 0, err
	}
	chs, err := c.Channels(ctx)
	if err != nil {
		return 0, err
	}
	var cams []config.Camera
	for _, ch := range chs {
		cams = append(cams, config.Camera{Entity: fmt.Sprintf("%s%d", reolinkPrefix, ch.Number), Name: ch.Name})
	}
	r := config.Reolink{Base: c.Base, User: user, Pass: pass, Fingerprint: c.Fingerprint, Cameras: cams}
	if err := config.Set().Home().Reolink(r); err != nil {
		return 0, err
	}
	Get().Changed.Emit(struct{}{})
	return len(cams), nil
}

// ReolinkRTSP is the RTSP address and login of a camera on the Reolink recorder set up here, for
// talking through it (feature/talkback): the recorder's host on RTSP's own port, and the channel's
// main stream, which is the one that carries the backchannel. False for any other camera.
func ReolinkRTSP(entity string) (addr, user, pass string, ok bool) {
	if !isReolink(entity) {
		return "", "", "", false
	}
	ch, err := strconv.Atoi(strings.TrimPrefix(entity, reolinkPrefix))
	r := config.Get().Home.Reolink
	if err != nil || ch < 0 || r.Base == "" {
		return "", "", "", false
	}
	u, err := url.Parse(r.Base)
	if err != nil || u.Hostname() == "" {
		return "", "", "", false
	}
	host := u.Hostname()
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return fmt.Sprintf("rtsp://%s:554/Preview_%02d_main", host, ch+1), r.User, r.Pass, true
}
