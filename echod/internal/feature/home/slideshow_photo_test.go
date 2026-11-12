package home

import (
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// A photo is a photo whether Home Assistant calls it playable or not: its Immich source marks every
// picture can_play false (#523). A folder is never one, nor a picture the device can't decode.
func TestAPhotoNeedNotBePlayable(t *testing.T) {
	for _, c := range []struct {
		m    hass.Media
		want bool
	}{
		{hass.Media{ID: "media-source://immich/a|b|c/x.jpg", Kind: "image/jpeg"}, true},
		{hass.Media{ID: "media-source://media_source/local/a.png", Kind: "image/png", CanPlay: true}, true},
		{hass.Media{ID: "media-source://media_source/local/b.JPG", CanPlay: true}, true},
		{hass.Media{ID: "media-source://media_source/local/trip", Kind: "directory", CanExpand: true}, false},
		{hass.Media{ID: "media-source://immich/a|b", Kind: "image/jpeg", CanExpand: true}, false},
		// Home Assistant's Immich serves its own rendering of a raw picture, a JPEG (#530).
		{hass.Media{ID: "media-source://immich/a|b|c/y.dng", Kind: "image/dng"}, true},
		{hass.Media{ID: "media-source://immich/a|b|c/z.heic", Kind: "image/heic"}, true},
		{hass.Media{ID: "media-source://immich/a|b|c/v.mp4", Kind: "video/mp4"}, false},
		{hass.Media{ID: "media-source://media_source/local/e.dng", Kind: "image/dng", CanPlay: true}, false},
		{hass.Media{ID: "media-source://media_source/local/c.webp", Kind: "image/webp", CanPlay: true}, false},
		{hass.Media{ID: "media-source://media_source/local/d.mp4", Kind: "video/mp4", CanPlay: true}, false},
	} {
		if got := SlideshowPhoto(c.m); got != c.want {
			t.Errorf("%s (%s, can_play %v, can_expand %v): %v, want %v", c.m.ID, c.m.Kind, c.m.CanPlay, c.m.CanExpand, got, c.want)
		}
	}
}

// Home Assistant's Immich integration is a source beside My media, and the folder list offers it at
// the top; cameras, speech and radio are not photo folders (#523).
func TestHomeAssistantsImmichIsAPhotoFolder(t *testing.T) {
	top := hass.Media{Children: []hass.Media{
		{ID: "media-source://media_source", Title: "My media", CanExpand: true},
		{ID: "media-source://immich", Title: "Immich", CanExpand: true},
		{ID: "media-source://camera", Title: "Camera", CanExpand: true},
		{ID: "media-source://tts", Title: "Text-to-speech", CanExpand: true},
		{ID: "media-source://radio_browser", Title: "Radio Browser", CanExpand: true},
	}}
	got := photoSources(top)
	if len(got) != 1 || got[0].ID != "media-source://immich" || got[0].Title != "Immich (Home Assistant)" {
		t.Errorf("photo sources %+v, want Home Assistant's Immich alone", got)
	}
	if got := photoSources(hass.Media{}); len(got) != 0 {
		t.Errorf("no sources gave %+v", got)
	}
}
