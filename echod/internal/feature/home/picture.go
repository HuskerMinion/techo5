package home

import (
	"errors"
	"fmt"
	"image"
	_ "image/png" // a picture from Home Assistant's www folder is as often a PNG as a JPEG
	"log/slog"
	"regexp"
	"strings"
	"time"

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
	switch {
	case strings.HasPrefix(src, "camera."):
		return true, src, nil
	case strings.HasPrefix(src, "image."):
		return false, "/api/image_proxy/" + src, nil
	case strings.HasPrefix(src, "/"), strings.HasPrefix(src, "http://"), strings.HasPrefix(src, "https://"):
		return false, src, nil
	}
	return false, "", fmt.Errorf("picture %q: not a camera, an image entity, a path on Home Assistant or an http(s) address", src)
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
		f.showCameraCaptioned(src, d, soundAsked(sound, CameraSound()), &c)
		return nil
	}
	name := c.Title
	if name == "" {
		name = untitled
	}
	f.mu.Lock()
	f.cam = CameraView{Entity: src, Name: name, Until: time.Now().Add(d), Caption: &c, span: d}
	f.camMuted = false
	f.mu.Unlock()
	slog.Info("picture up", "source", redactURL(src), "for", d)
	go f.fetchStill(path, c.ID)
	if name == untitled && strings.HasPrefix(src, "image.") {
		go f.lookUpName(src, untitled)
	}
	f.Changed.Emit(struct{}{})
	return nil
}

// fetchStill fetches a still once and puts it on the view, if the view is still this notification's.
// As with a camera, the time on screen counts from the picture arriving.
func (f *Feature) fetchStill(path, id string) {
	frame, err := still(path)
	if err != nil {
		// A picture's address can carry a token in its query, and the fetch's error repeats the
		// address - not always as it was given, since Home Assistant's own full address is fetched as
		// a path - so every query in the error comes off before it goes on the screen or into the log.
		err = errors.New(redactError(err.Error()))
	}
	f.mu.Lock()
	if f.cam.Caption != nil && f.cam.Caption.ID == id {
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

// redactURL is s without its query and fragment, where an address keeps a token.
func redactURL(s string) string {
	if i := strings.IndexAny(s, "?#"); i >= 0 {
		return s[:i]
	}
	return s
}

// queryInError is an address's query or fragment as an error repeats it: from the ? or # to the
// end of the address, which is where a space or a quote starts.
var queryInError = regexp.MustCompile(`[?#][^\s"']*`)

// redactError is an error's text with the query and fragment taken off every address in it. The colon
// an error puts after an address stays, so the rest of the text still reads.
func redactError(s string) string {
	return queryInError.ReplaceAllStringFunc(s, func(q string) string {
		if strings.HasSuffix(q, ":") {
			return ":"
		}
		return ""
	})
}

// still fetches a picture and scales it to the panel. Home Assistant's own addresses get the token and
// any other host does not (hass.FetchURL).
func still(path string) (*image.RGBA, error) {
	b, err := hass.Get().FetchURL(path)
	if err != nil {
		return nil, err
	}
	src, err := decodeWithin(b, maxFramePixels, "picture")
	if err != nil {
		return nil, err
	}
	return fitFrame(src), nil
}

// PictureUp is whether the camera page is up with notification id's words on it.
func (f *Feature) PictureUp(id string) bool {
	v, up := f.Camera()
	return up && v.Caption != nil && v.Caption.ID == id
}
