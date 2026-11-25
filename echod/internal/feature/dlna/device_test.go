//go:build !dot

package dlna

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/video"
)

// The address a video is said to come from, which decides whether the screen asks about it
// (video.FromDevice), is the connection's own: a controller on the device's loopback is the device's
// when the program behind it runs as root or as an installed add-on, and asked about under a name of its
// own when it runs as anybody else; a header that says otherwise changes nothing either way.
func TestAVideoIsFromTheConnectionsAddress(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	exe, _ := os.Executable()
	t.Setenv("TECHO5_FFMPEG", exe)
	_ = config.Set().Video().On(true)
	_ = config.Set().Video().DLNA(true)
	oldPlay, oldStopID, oldStopSong := playVideo, stopVideoID, stopOurSong
	t.Cleanup(func() { playVideo, stopVideoID, stopOurSong = oldPlay, oldStopID, oldStopSong })
	var got video.Request
	playVideo = func(req video.Request) (uint64, error) { got = req; return 1, nil }
	stopVideoID = func(uint64) {}
	stopOurSong = func() {}

	oldOwner := loopbackOwner
	t.Cleanup(func() { loopbackOwner = oldOwner })
	for _, tc := range []struct {
		remote, host, want string
		device             bool
		uid                int // the loopback peer's user: 0 root, 30000 an installed add-on's, else another
	}{
		{"127.0.0.1:40000", "127.0.0.1:8181", "127.0.0.1", true, 0},
		{"[::1]:40000", "[::1]:8181", "::1", true, 30000},
		{"127.0.0.1:40001", "127.0.0.1:8181", "a program on this device (user 88)", false, 88},
		{"192.168.1.30:40000", "192.168.1.20:8181", "192.168.1.30", false, 0},
	} {
		loopbackOwner = func(string, string) (bool, int) { return tc.uid == 0 || tc.uid == 30000, tc.uid }
		f := &Feature{}
		f.r.f = f
		send := func(action, args string) int {
			body, header := soap("AVTransport", action, args)
			req := httptest.NewRequest(http.MethodPost, pathPrefix+"AVTransport/control", strings.NewReader(body))
			req.Header.Set("SOAPACTION", header)
			req.Header.Set("X-Forwarded-For", "127.0.0.1") // nothing here reads it
			req.Header.Set("X-Real-IP", "::1")
			req.Host, req.RemoteAddr = tc.host, tc.remote
			w := httptest.NewRecorder()
			f.serve(w, req)
			return w.Code
		}
		got = video.Request{}
		if code := send("SetAVTransportURI", `<InstanceID>0</InstanceID><CurrentURI>http://192.0.2.10/film.mp4</CurrentURI>`+
			`<CurrentURIMetaData>`+esc(videoDIDL)+`</CurrentURIMetaData>`); code != http.StatusOK {
			t.Fatalf("%s: SetAVTransportURI %d", tc.remote, code)
		}
		if code := send("Play", `<InstanceID>0</InstanceID><Speed>1</Speed>`); code != http.StatusOK {
			t.Fatalf("%s: Play %d", tc.remote, code)
		}
		if got.Origin != video.FromDLNA || got.From != tc.want {
			t.Errorf("%s: the video came from %q (origin %v), want %q", tc.remote, got.From, got.Origin, tc.want)
		}
		if device := video.FromDevice(got.From); device != tc.device {
			t.Errorf("%s: taken for the device %v", tc.remote, device)
		}
	}
}
