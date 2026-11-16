package camera

import (
	"context"
	"errors"
	"image"
	"log/slog"
	"sync"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"
	"github.com/ygelfand/go-esphome-device/api"
	"google.golang.org/protobuf/proto"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
)

// The Screen camera entity: what the panel shows, as /screen.png has it, sent to Home Assistant over
// the API link, which the device key encrypts, rather than on the web port with no login. It answers
// only while its switch is on (config Security.ScreenCamera, feature/security), a switch apart from
// Screen web access, and is listed on every device with a screen whether the switch is on or not, so
// that turning it on needs no reload of the integration in Home Assistant.
//
// An image request names no entity: ESPHome's protocol has one camera per device. So a request goes to
// the camera entity (entity.go) and to this one alike, and each answers with its own key, which is how
// Home Assistant tells the pictures apart. Asking for the screen therefore also takes a still from the
// camera, where the device has one, and asking for the camera takes a screenshot while this is on.

const (
	// screenObjectID is the entity's identifier: camera.<device>_screen in Home Assistant.
	screenObjectID = "screen"

	// screenQuality is the screenshot's JPEG quality: flat colors and text, which stay clean at this.
	screenQuality = 85

	// screenWait bounds taking one screenshot: the next frame drawn, which a busy panel takes a moment
	// to get to.
	screenWait = 3 * time.Second
)

// screenEvery paces a stream: Home Assistant asks again as soon as a picture arrives, and the link
// carries voice as well. A screen changes far less often than this.
var screenEvery = time.Second

// screenShot is the panel's picture now (screen.go, screen_spot.go); nil on a device with no screen,
// which has no Screen entity either.
var screenShot func(ctx context.Context) (image.Image, error)

// screenOn is whether the Screen camera's switch is on, asked at each request.
var screenOn = func() bool { return config.Get().Security.ScreenCamera }

// screenAnswers is, per connection, whether a picture is on its way and whether another was asked for
// meanwhile, and when the last one was taken. One goroutine sends a connection's pictures, since two
// would mix their chunks under the one key; a request while it is busy is kept for it, not dropped.
var screenAnswers = struct {
	mu      sync.Mutex
	pending map[any]bool // a connection is in it while its goroutine runs; true when asked again
	last    time.Time
}{pending: map[any]bool{}}

// screenListing is the Screen entity's entry in the entity list.
func screenListing() *api.ListEntitiesCameraResponse {
	return &api.ListEntitiesCameraResponse{
		ObjectId: screenObjectID,
		Key:      key(screenObjectID),
		Name:     "Screen",
		Icon:     "mdi:monitor-screenshot",
	}
}

// screenRequest answers an image request for the Screen entity, off the connection's goroutine: a
// screenshot waits for the next frame, and the link must keep carrying voice meanwhile.
func screenRequest(c *esphome.Conn, m *api.CameraImageRequest) {
	screenRequestTo(c, c.Send, m)
}

// screenRequestTo is screenRequest for any connection, by what tells it apart and how to send to it.
//
// Home Assistant asks for a stream's next picture the moment the last one is complete, which is just
// before the goroutine that sent it is done. Dropped there, the request left the stream waiting for
// whatever asked next, the dashboard's still a few seconds on: the picture ran a few seconds, stood, and
// jumped. So a request that finds a picture on its way is noted, and the goroutine sends another.
func screenRequestTo(who any, send func(proto.Message) error, m *api.CameraImageRequest) {
	if screenShot == nil || !screenOn() || (!m.GetSingle() && !m.GetStream()) {
		return
	}
	screenAnswers.mu.Lock()
	if _, busy := screenAnswers.pending[who]; busy {
		screenAnswers.pending[who] = true
		screenAnswers.mu.Unlock()
		return
	}
	screenAnswers.pending[who] = false
	paced := m.GetStream() && !m.GetSingle()
	screenAnswers.mu.Unlock()

	safe.Go("screen entity", func() {
		for {
			if paced {
				screenAnswers.mu.Lock()
				wait := time.Until(screenAnswers.last.Add(screenEvery))
				screenAnswers.mu.Unlock()
				if wait > 0 {
					time.Sleep(wait)
				}
			}
			screenAnswers.mu.Lock()
			screenAnswers.last = time.Now()
			screenAnswers.mu.Unlock()
			if err := sendScreen(send); err != nil {
				slog.Debug("screen entity", "err", err)
			}
			screenAnswers.mu.Lock()
			again := screenAnswers.pending[who]
			if !again || !screenOn() {
				delete(screenAnswers.pending, who)
				screenAnswers.mu.Unlock()
				return
			}
			screenAnswers.pending[who] = false
			screenAnswers.mu.Unlock()
			paced = true // a request that came while sending is paced like a stream's
		}
	})
}

// sendScreen takes a screenshot and sends it as the Screen entity's picture, in chunks, the last one
// marked done. The switch is asked again here: one turned off while the picture was being asked for
// sends nothing.
func sendScreen(send func(proto.Message) error) error {
	ctx, cancel := context.WithTimeout(context.Background(), screenWait)
	defer cancel()
	img, err := screenShot(ctx)
	if err != nil {
		return err
	}
	if img == nil {
		return errors.New("the screen is not up")
	}
	jpg, err := encodeWhole(img, screenQuality)
	if err != nil {
		return err
	}
	if !screenOn() {
		return nil
	}
	k := key(screenObjectID)
	for off := 0; off < len(jpg); off += chunk {
		end := min(off+chunk, len(jpg))
		if err := send(&api.CameraImageResponse{Key: k, Data: jpg[off:end], Done: end == len(jpg)}); err != nil {
			return err
		}
	}
	return nil
}
