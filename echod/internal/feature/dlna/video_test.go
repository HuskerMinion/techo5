//go:build !dot

package dlna

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/video"
)

const videoDIDL = `<DIDL-Lite xmlns="urn:schemas-upnp-org:metadata-1-0/DIDL-Lite/" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:upnp="urn:schemas-upnp-org:metadata-1-0/upnp/">` +
	`<item id="2" parentID="0" restricted="1"><dc:title>A Short Film</dc:title><upnp:class>object.item.videoItem.movie</upnp:class>` +
	`<res duration="0:09:56" protocolInfo="http-get:*:video/mp4:*">http://192.0.2.10/film.mp4</res></item></DIDL-Lite>`

// Videos are offered to controllers only while Video, DLNA video and the decoder are all there; music
// is offered the same either way.
func TestVideoIsOfferedOnlyWhileDLNAVideoIsOn(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	exe, _ := os.Executable()
	t.Setenv("TECHO5_FFMPEG", exe) // a decoder that is there
	f := &Feature{}
	f.r.f = f
	protocols := func() string {
		_, body := call(t, f, "ConnectionManager", "GetProtocolInfo", "")
		return body
	}
	if p := protocols(); strings.Contains(p, "video/") || !strings.Contains(p, "audio/flac") {
		t.Errorf("videos off: %s", p)
	}
	_ = config.Set().Video().DLNA(true)
	if p := protocols(); strings.Contains(p, "video/") {
		t.Errorf("DLNA video on but Video off: %s", p)
	}
	_ = config.Set().Video().On(true)
	p := protocols()
	for _, want := range []string{"http-get:*:video/mp4:*", "http-get:*:video/x-matroska:*", "http-get:*:video/mp2t:*", "audio/flac"} {
		if !strings.Contains(p, want) {
			t.Errorf("videos on: no %s in %s", want, p)
		}
	}
	t.Setenv("TECHO5_FFMPEG", filepath.Join(t.TempDir(), "none"))
	if p := protocols(); strings.Contains(p, "video/") {
		t.Errorf("no decoder: %s", p)
	}
}

// A video sent while DLNA video is off is refused with UPnP's code for it; on, it is taken as a video,
// and a song is still a song.
func TestAVideoIsTakenOnlyWhileDLNAVideoIsOn(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	exe, _ := os.Executable()
	t.Setenv("TECHO5_FFMPEG", exe)
	f := &Feature{}
	f.r.f = f
	set := func(uri, meta string) (int, string) {
		return call(t, f, "AVTransport", "SetAVTransportURI",
			`<InstanceID>0</InstanceID><CurrentURI>`+uri+`</CurrentURI><CurrentURIMetaData>`+esc(meta)+`</CurrentURIMetaData>`)
	}
	if code, body := set("http://192.0.2.10/film.mp4", videoDIDL); code != 500 || !strings.Contains(body, "<errorCode>714</errorCode>") {
		t.Errorf("a video with DLNA video off: %d %s", code, body)
	}
	_ = config.Set().Video().On(true)
	_ = config.Set().Video().DLNA(true)
	if code, body := set("http://192.0.2.10/film.mp4", videoDIDL); code != http.StatusOK {
		t.Fatalf("a video with DLNA video on: %d %s", code, body)
	}
	if !f.r.cur.video || f.r.cur.title != "A Short Film" {
		t.Errorf("song %+v", f.r.cur)
	}
	if code, _ := set("http://192.0.2.10/song.flac", didl); code != http.StatusOK || f.r.cur.video {
		t.Errorf("a song after a video: %d, video %v", code, f.r.cur.video)
	}
	// A video queued to follow is a video too, and refused the same way while DLNA video is off.
	if code, _ := call(t, f, "AVTransport", "SetNextAVTransportURI",
		`<InstanceID>0</InstanceID><NextURI>http://192.0.2.10/film.mp4</NextURI><NextURIMetaData>`+esc(videoDIDL)+`</NextURIMetaData>`); code != http.StatusOK || !f.r.next.video {
		t.Errorf("a queued video: %d, video %v", code, f.r.next.video)
	}
	_ = config.Set().Video().DLNA(false)
	if code, _ := call(t, f, "AVTransport", "SetNextAVTransportURI",
		`<InstanceID>0</InstanceID><NextURI>http://192.0.2.10/film.mp4</NextURI><NextURIMetaData>`+esc(videoDIDL)+`</NextURIMetaData>`); code != 500 {
		t.Errorf("a queued video with DLNA video off: %d", code)
	}
}

func TestWhatIsAVideo(t *testing.T) {
	for _, tc := range []struct {
		s    song
		want bool
	}{
		{song{uri: "http://192.0.2.10/x", class: "object.item.videoItem.movie"}, true},
		{song{uri: "http://192.0.2.10/x.mp4", class: "object.item.audioItem.musicTrack"}, false},
		{song{uri: "http://192.0.2.10/x", mime: "video/x-matroska"}, true},
		{song{uri: "http://192.0.2.10/x", mime: "application/vnd.apple.mpegurl"}, true},
		{song{uri: "http://192.0.2.10/x.mkv", mime: "audio/flac"}, false},
		{song{uri: "http://192.0.2.10/stream.ts?token=1"}, true},
		{song{uri: "http://192.0.2.10/film.MP4", mime: "*"}, true},
		{song{uri: "http://192.0.2.10/song.mp3"}, false},
		{song{uri: "http://192.0.2.10/song"}, false},
	} {
		if got := isVideo(tc.s); got != tc.want {
			t.Errorf("%+v: video %v, want %v", tc.s, got, tc.want)
		}
	}
	s := parseSong("http://192.0.2.10/film.mp4", videoDIDL)
	if s.class != "object.item.videoItem.movie" || s.mime != "video/mp4" {
		t.Errorf("parsed %+v", s)
	}
}

// A video the player refuses leaves the song playing; one it takes stops it, and only then.
func TestARefusedVideoLeavesTheSongPlaying(t *testing.T) {
	oldPlay, oldStopID, oldStopSong := playVideo, stopVideoID, stopOurSong
	t.Cleanup(func() { playVideo, stopVideoID, stopOurSong = oldPlay, oldStopID, oldStopSong })
	stopped := 0
	stopOurSong = func() { stopped++ }
	stopVideoID = func(uint64) {}
	f := &Feature{}
	f.r.f = f
	film := song{uri: "http://192.0.2.10/film.mp4", title: "A Short Film", video: true, from: "192.0.2.20"}
	for _, refusal := range []error{video.ErrOff, video.ErrBusy, video.ErrDeclined} {
		playVideo = func(video.Request) (uint64, error) {
			if stopped != 0 {
				t.Errorf("%v: the song was stopped before the player answered", refusal)
			}
			return 0, refusal
		}
		if err := f.r.startVideo(f.r.gen, film); err == nil {
			t.Errorf("%v: taken", refusal)
		}
		if stopped != 0 {
			t.Errorf("%v: the song was stopped", refusal)
		}
	}
	playVideo = func(video.Request) (uint64, error) {
		if stopped != 0 {
			t.Error("the song was stopped before the player answered")
		}
		return 7, nil
	}
	if err := f.r.startVideo(f.r.gen, film); err != nil {
		t.Fatalf("taken: %v", err)
	}
	if stopped != 1 || f.r.videoID != 7 || f.r.state != stLoading {
		t.Errorf("taken: song stopped %d times, video %d, state %v", stopped, f.r.videoID, f.r.state)
	}
}
