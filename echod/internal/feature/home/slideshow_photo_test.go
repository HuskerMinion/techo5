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
		{hass.Media{ID: "media-source://media_source/local/c.webp", Kind: "image/webp", CanPlay: true}, false},
		{hass.Media{ID: "media-source://media_source/local/d.mp4", Kind: "video/mp4", CanPlay: true}, false},
	} {
		if got := SlideshowPhoto(c.m); got != c.want {
			t.Errorf("%s (%s, can_play %v, can_expand %v): %v, want %v", c.m.ID, c.m.Kind, c.m.CanPlay, c.m.CanExpand, got, c.want)
		}
	}
}
