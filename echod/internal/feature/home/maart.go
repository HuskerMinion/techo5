package home

import (
	"image"
	"log/slog"
	"sync"
)

// The picture Music Assistant sends with its track. A station's cover is looked up here from the
// station's own service, but a track from somebody's own library has nowhere to be looked up: Music
// Assistant has the picture, and over Sendspin it sends it, already sized, for the room to show.

var remote struct {
	mu         sync.Mutex
	art, thumb *image.RGBA
}

// HasScreen says whether this device shows pictures at all, so a device without a screen does not ask
// a server to send them.
func HasScreen() bool { return hasScreen }

// RemoteArt takes the picture a remote server sent for the track it is playing, as the encoded image.
// Empty bytes clear it: the server's word for a track without a picture. current is asked once the
// picture is decoded, and a picture the server has since replaced is dropped rather than shown over
// the newer one.
func RemoteArt(b []byte, current func() bool) {
	var art, thumb *image.RGBA
	if len(b) > 0 {
		a, t, err := layoutArt(b, false, "Music Assistant's picture")
		if err != nil {
			// The page draws its own stand-in for a track with no picture, and that is what a
			// picture that cannot be read gets too.
			slog.Info("music assistant picture", "bytes", len(b), "err", err)
		}
		art, thumb = a, t
	}
	remote.mu.Lock()
	if current != nil && !current() {
		remote.mu.Unlock()
		return
	}
	changed := art != remote.art || thumb != remote.thumb
	remote.art, remote.thumb = art, thumb
	remote.mu.Unlock()
	if changed {
		slog.Info("music assistant picture", "shown", art != nil)
		Get().Changed.Emit(struct{}{})
	}
}

// remoteArt is the picture for the remote's track, nil when it sent none.
func remoteArt() (art, thumb *image.RGBA) {
	remote.mu.Lock()
	defer remote.mu.Unlock()
	return remote.art, remote.thumb
}
