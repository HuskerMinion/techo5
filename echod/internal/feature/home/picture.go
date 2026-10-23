package home

import (
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/png" // a picture from Home Assistant's www folder is as often a PNG as a JPEG
	"log/slog"
	"net/url"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/video"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// Pictures for a notification (feature/notify): the camera page, with the notification's words over
// it. A camera stays live, the same view "show the front door" gives; anything else is one still,
// fetched once. Either way the page's own timing, its tap to close and its sound control are the
// camera's, so a notification is not a second way of drawing a picture.

// Caption is a notification's words on the camera page, and which notification they are, so that a
// view can be told from a later one with the same picture.
type Caption struct {
	ID      string
	Title   string
	Message string
	// UntilTapped is a still a notification gave no time to, which stays until somebody taps it (twelve
	// hours at most): the page shows no countdown for it. A live camera's no-time is thirty seconds,
	// which does count down.
	UntilTapped bool
}

// Wordless is whether a notification sent its picture with no words at all, which the page shows as
// the picture alone: no name, no clock, no caption, only the way to close it.
func (c Caption) Wordless() bool { return c.Title == "" && c.Message == "" }

// untitled is what a still without a title is called until Home Assistant names its image entity.
const untitled = "Notification"

// pictureSource reads a notification's picture: a camera (live, by entity), an image entity, a path
// on Home Assistant, or an http(s) address. path is what is fetched for a still. Anything else is
// refused here, before anything is shown, so the automation hears why.
func pictureSource(src string) (live bool, path string, err error) {
	lower := strings.ToLower(src) // a scheme in any case, as the video player takes it
	switch {
	case strings.HasPrefix(src, "camera."):
		return true, src, nil
	case strings.HasPrefix(src, "image."):
		return false, "/api/image_proxy/" + src, nil
	case strings.HasPrefix(src, "/"):
		return false, src, nil
	case strings.HasPrefix(lower, "http://"), strings.HasPrefix(lower, "https://"):
		// Home Assistant's own address is fetched as a path, with the token, so it is Home Assistant
		// that is asked, wherever it lives (a home server is often on a private address, and a test's
		// is on loopback), and it is not judged as some other host. Any other host is fetched bare
		// and is held to the rules the video player holds a stream to: not this device, not a
		// link-local address. The login and query are left out of the refusal, which the automation
		// and the log both hear.
		if !hass.Get().IsOwn(src) {
			if _, err := video.CheckURL(src); err != nil {
				return false, "", fmt.Errorf("picture %q: %v", redactURL(src), err)
			}
		}
		return false, src, nil
	}
	return false, "", fmt.Errorf("picture %q: not a camera, an image entity, a path on Home Assistant or an http(s) address", redactURL(src))
}

// CheckPicture says whether src is a picture ShowPicture can show. Built on every device, so a Dot
// refuses the same pictures a Show does.
func CheckPicture(src string) error {
	_, _, err := pictureSource(src)
	return err
}

// ShowPicture puts a notification's picture up for d, with its words. sound is a camera's audio as
// home_show_camera_sound takes it: "on" or "off" for this view, anything else for the device's own
// Camera sound setting. A still has no audio, so it ignores sound.
func (f *Feature) ShowPicture(src string, c Caption, d time.Duration, sound string) error {
	live, path, err := pictureSource(src)
	if err != nil {
		return err
	}
	if live {
		f.showCameraCaptioned(src, d, ParseOnOff(sound, CameraSound()), &c)
		return nil
	}
	f.mu.Lock()
	if f.plainCameraUp() {
		// A camera somebody is watching (a doorbell's, or one asked for) is not taken from them, its
		// stream and sound ended, for a picture: the picture waits for the view to end, for as long as
		// it was to be up, and replaces any picture already waiting.
		first := f.waiting == nil
		f.dropWaiting("replaced")
		f.waiting = &waitingStill{src: src, path: path, c: c, until: time.Now().Add(d), gone: make(chan struct{})}
		camera := f.cam.Entity
		f.mu.Unlock()
		slog.Info("picture waits for the camera view up", "source", redactURL(src), "camera", camera)
		if first {
			f.spawn(f.showWaiting)
		}
		return nil
	}
	shown := f.stillLocked(src, path, c, d)
	f.mu.Unlock()
	shown()
	return nil
}

// waitingStill is a picture notification's still that waits for the camera view up to end.
type waitingStill struct {
	src, path string
	c         Caption
	until     time.Time     // when its notification would have ended, had it been shown when asked
	gone      chan struct{} // closed once it is no longer the still waiting (dropWaiting)
}

// waitingPoll is how often a waiting still looks to see whether the camera view is over; a variable
// for the tests.
var waitingPoll = 500 * time.Millisecond

// plainCameraUp is whether the view up is a camera with no notification's words on it. The caller holds
// f.mu.
func (f *Feature) plainCameraUp() bool {
	return f.cam.Entity != "" && f.cam.Caption == nil && time.Now().Before(f.cam.Until)
}

// showWaiting shows the still waiting for the camera view once that view is over, unless it was taken
// down or replaced meanwhile, or its time ran out while it waited. A still taken down, replaced, or
// dropped as the device shuts down wakes it at once rather than at its next look: it ends then, or
// carries on for a still that took the waiting one's place.
func (f *Feature) showWaiting() {
	for {
		f.mu.Lock()
		w := f.waiting
		f.mu.Unlock()
		if w == nil {
			return
		}
		look := time.NewTimer(waitingPoll)
		select {
		case <-look.C:
		case <-w.gone:
			look.Stop()
		}
		f.mu.Lock()
		w = f.waiting
		switch {
		case w == nil:
			f.mu.Unlock()
			return
		case !time.Now().Before(w.until):
			f.dropWaiting("")
			f.mu.Unlock()
			return
		case f.plainCameraUp():
			f.mu.Unlock()
			continue
		}
		shown := f.stillLocked(w.src, w.path, w.c, time.Until(w.until))
		f.mu.Unlock()
		shown()
		return
	}
}

// stopWaiting drops the still waiting for the camera view as the device shuts down: what waits with it
// ends at once.
func (f *Feature) stopWaiting() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dropWaiting("")
}

// stillLocked puts a still's view up in place of whatever is up. The caller holds f.mu, and runs what
// it returns once it has let go of it.
func (f *Feature) stillLocked(src, path string, c Caption, d time.Duration) func() {
	name := c.Title
	if name == "" {
		name = untitled
	}
	// The same picture again, already on the screen (a run of notifications over one picture, only
	// their words changing): the new view keeps its frame, and the picture is not fetched and decoded
	// again. A view put up bare while the same bytes came back left the screen blank between each,
	// which flickered. A picture that changes behind one address (a snapshot) needs the address to
	// change too, a query that differs each time, to be fetched anew.
	var keep *image.RGBA
	if f.viewIsUp(src) && f.cam.Caption != nil && f.cam.Frame != nil && f.cam.Error == "" {
		keep = f.cam.Frame
	}
	f.endCaption("replaced")
	if f.waiting != nil && f.waiting.c.ID != c.ID {
		f.dropWaiting("replaced") // another still, not the waiting one coming up
	} else {
		f.dropWaiting("")
	}
	f.cancelStill() // the still this one replaces is not wanted any more
	// Nor the camera, and its stream's decoder ends now.
	f.stopCameraFetch()
	ctx, cancel := context.WithCancel(context.Background())
	f.stillCancel = cancel
	f.cam = CameraView{Entity: src, Name: name, Until: time.Now().Add(d), Caption: &c, span: d, View: f.newView(), Frame: keep}
	f.camMuted = false
	return func() {
		slog.Info("picture up", "source", redactURL(src), "for", d, "kept", keep != nil)
		if keep != nil {
			cancel() // nothing to fetch: the picture is the one already up
		} else {
			f.spawn(func() {
				defer cancel() // the context is done with once the fetch is
				f.fetchStill(ctx, src, path, c.ID)
			})
		}
		if name == untitled && strings.HasPrefix(src, "image.") {
			f.spawn(func() { f.lookUpName(src, untitled) })
		}
		f.Changed.Emit(struct{}{})
	}
}

// stillTurn lets one still be fetched and decoded at a time. A picture can be 8 MB of bytes and a
// 12 megapixel PNG decodes to about 48 MB, on a device with little memory to spare, so a burst of
// notifications waits its turn instead of decoding side by side.
var stillTurn = make(chan struct{}, 1)

// stillsDecoded counts the stills that got as far as being decoded, which is what a test of the
// drop-before-decode check reads.
var stillsDecoded atomic.Int32

// errReplaced is a still's fetch finding that its view is gone or no longer its notification's.
var errReplaced = errors.New("the picture's view is gone")

// stillIsUp is whether the view on the screen is still the one src was shown for, by notification id.
func (f *Feature) stillIsUp(src, id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.viewIsUp(src) && f.cam.Caption != nil && f.cam.Caption.ID == id
}

// cancelStill gives up the fetch of the still on the view, if there is one: its view is being replaced
// or taken down, and its bytes would be dropped anyway. The caller holds f.mu.
func (f *Feature) cancelStill() {
	if f.stillCancel != nil {
		f.stillCancel()
		f.stillCancel = nil
	}
}

// fetchStill fetches a still once and puts it on the view, if the view is still this notification's
// when its turn comes, when its bytes arrive and when the picture is ready. A view that is gone costs
// the next stage nothing: bytes for a notification that was tapped away or replaced are dropped
// without being decoded. As with a camera, the time on screen counts from the picture arriving. ctx is
// canceled when the view is replaced or taken down: the fetch then lets go of its place in line, or of
// the turn it holds, at once instead of when its bytes arrive, and writes nothing.
func (f *Feature) fetchStill(ctx context.Context, src, path, id string) {
	select {
	case stillTurn <- struct{}{}:
	case <-ctx.Done():
		return
	}
	defer func() { <-stillTurn }()
	mine := func() bool { return f.stillIsUp(src, id) }
	if !mine() {
		return
	}
	frame, err := still(ctx, path, mine)
	if errors.Is(err, errReplaced) || ctx.Err() != nil {
		return
	}
	if err != nil {
		// A picture's address can carry a token in its query or a login before its host, and the
		// fetch's error repeats the address - not always as it was given, since Home Assistant's own
		// full address is fetched as a path - so every query and login in the error comes off before
		// it goes on the screen or into the log.
		err = errors.New(redactError(err.Error()))
	}
	f.mu.Lock()
	if f.viewIsUp(src) && f.cam.Caption != nil && f.cam.Caption.ID == id {
		if err != nil {
			f.cam.Error = err.Error()
		} else {
			f.cam.Frame, f.cam.Error = frame, ""
			if until := time.Now().Add(f.cam.span); until.After(f.cam.Until) {
				f.cam.Until = until
			}
		}
	}
	f.mu.Unlock()
	if err != nil {
		slog.Warn("picture", "source", redactURL(path), "err", err)
	}
	f.Changed.Emit(struct{}{})
}

// redactURL is s without its login, query and fragment, which is where an address keeps a camera's
// password or a token. An address that does not parse is cut at its first ? or # and has any login taken
// off by hand.
func redactURL(s string) string {
	if u, err := url.Parse(s); err == nil && u.Host != "" {
		u.User, u.RawQuery, u.ForceQuery, u.Fragment, u.RawFragment = nil, "", false, "", ""
		return u.String()
	}
	if i := strings.IndexAny(s, "?#"); i >= 0 {
		s = s[:i]
	}
	return loginInError.ReplaceAllString(s, "//")
}

// queryInError is an address's query or fragment as an error repeats it: from the ? or # to the
// end of the address, which is where a space or a quote starts.
var queryInError = regexp.MustCompile(`[?#][^\s"']*`)

// loginInError is an address's user:pass@ (or user@) as an error repeats it, from the // before it.
var loginInError = regexp.MustCompile(`//[^/\s"']*@`)

// redactError is an error's text with the login, query and fragment taken off every address in it. The
// colon an error puts after an address stays, so the rest of the text still reads. The query goes first
// so that an @ inside one is not taken for a login.
func redactError(s string) string {
	s = queryInError.ReplaceAllStringFunc(s, func(q string) string {
		if strings.HasSuffix(q, ":") {
			return ":"
		}
		return ""
	})
	return loginInError.ReplaceAllString(s, "//")
}

// still fetches a picture and scales it to the panel. Home Assistant's own addresses get the token and
// any other host does not (hass.FetchURL). wanted is asked once the bytes are in, before they are
// decoded: if it says no, the picture is dropped (errReplaced).
func still(ctx context.Context, path string, wanted func() bool) (*image.RGBA, error) {
	b, err := hass.Get().FetchURLContext(ctx, path)
	if err != nil {
		return nil, err
	}
	if !wanted() {
		return nil, errReplaced
	}
	stillsDecoded.Add(1)
	src, err := decodeWithin(b, maxFramePixels, "picture")
	if err != nil {
		return nil, err
	}
	return fitFrame(src), nil
}

// PictureIsLive is whether a notification's picture is a camera, shown live, rather than one still.
func PictureIsLive(src string) bool { return strings.HasPrefix(src, "camera.") }

// PictureUp is whether the camera page is up with notification id's words on it, or its still is
// waiting for the camera view up to end.
func (f *Feature) PictureUp(id string) bool {
	f.mu.Lock()
	waiting := f.waiting != nil && f.waiting.c.ID == id && time.Now().Before(f.waiting.until)
	f.mu.Unlock()
	if waiting {
		return true
	}
	v, up := f.Camera()
	return up && v.Caption != nil && v.Caption.ID == id
}
