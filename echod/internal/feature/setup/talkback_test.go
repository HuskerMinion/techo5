//go:build !dot

package setup

import (
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/video"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
)

// The Talk through cameras form: an address per camera on the list, kept only for cameras on it and
// only as rtsp:// with no login in it, and a password that is written but never shown, and kept when
// its field is left empty.
func TestTalkBackForm(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if err := config.Set().Home().Cameras([]config.Camera{{Entity: "camera.front_door", Name: "Front <door>"}, {Entity: "camera.deck", Name: "Deck"}}); err != nil {
		t.Fatal(err)
	}
	post := func(v url.Values) string {
		v.Set("token", "the-pages-own-token")
		v.Set("what", "talkback")
		return saveTalkBack(form(v))
	}
	ok := url.Values{"user": {"admin"}, "pass": {"cam-secret"},
		"entity": {"camera.front_door", "camera.deck", "camera.not_on_the_list"},
		"addr":   {"rtsp://192.168.1.40:554/h264Preview_01_main", "", ""}}
	if p := post(ok); p != "" {
		t.Fatal(p)
	}
	c := config.Get().TalkBack
	if c.User != "admin" || c.Pass != "cam-secret" || len(c.Cameras) != 1 || c.Cameras["camera.front_door"] != "rtsp://192.168.1.40:554/h264Preview_01_main" {
		t.Errorf("saved %+v", c)
	}
	// Again with the password left empty: the saved one stays.
	ok.Del("pass")
	if p := post(ok); p != "" || config.Get().TalkBack.Pass != "cam-secret" {
		t.Errorf("an empty field changed the password (%s)", p)
	}
	for _, bad := range []url.Values{
		{"entity": {"camera.deck"}, "addr": {"rtsp://admin:pw@192.168.1.40/x"}},
		{"entity": {"camera.deck"}, "addr": {"http://192.168.1.40/x"}},
		{"entity": {"camera.deck"}, "addr": {"rtsp://192.168.1.40/x y"}},
		{"entity": {"camera.deck", "camera.front_door"}, "addr": {"rtsp://192.168.1.40/x"}},
		{"user": {"admin\r\nX: y"}},
		{"user": {`ad"min`}},
		{"user": {strings.Repeat("a", 129)}},
		// A camera not on the list: Home Assistant's list is not to hand, and the address is not
		// dropped quietly.
		{"entity": {"camera.not_on_the_list"}, "addr": {"rtsp://192.168.1.41/x"}},
	} {
		if post(bad) == "" {
			t.Errorf("%v was saved", bad)
		}
	}

	// The page: the camera's name escaped, the address shown, the password never.
	rec := httptest.NewRecorder()
	talkBackSection(rec, "tok")
	page := rec.Body.String()
	for _, want := range []string{`autocomplete="new-password"`, "Front &lt;door&gt;", "rtsp://192.168.1.40:554/h264Preview_01_main", secretSaved, "The switch is off"} {
		if !strings.Contains(page, want) {
			t.Errorf("the section does not say %q", want)
		}
	}
	if strings.Contains(page, "cam-secret") || strings.Contains(page, "<door>") {
		t.Error("the section shows the password, or a name unescaped")
	}
}

// A picture address typed for a camera that has left the list is not dropped quietly either, with no
// talk address beside it.
func TestAPictureForACameraOffTheListIsNotDroppedQuietly(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if err := config.Set().Home().Cameras([]config.Camera{{Entity: "camera.deck", Name: "Deck"}}); err != nil {
		t.Fatal(err)
	}
	v := url.Values{"token": {"the-pages-own-token"}, "what": {"talkback"}, "user": {"admin"},
		"entity": {"camera.deck", "camera.gone"}, "addr": {"", ""}, "pic": {"", "rtsp://192.168.1.41/sub"}}
	if p := saveTalkBack(form(v)); p == "" {
		t.Error("a picture address for a camera no longer on the list was dropped without a word")
	}
}

// The picture address is offered only where it is used: a decoder that reads RTSP (and refuses Basic),
// on a device whose kernel fences it. The Spot's does not, so it never reads a camera's picture this way.
func TestThePictureAddressIsOfferedOnlyWhereItIsUsed(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if err := config.Set().Home().Cameras([]config.Camera{{Entity: "camera.deck", Name: "Deck"}}); err != nil {
		t.Fatal(err)
	}
	dec := filepath.Join(t.TempDir(), "ffmpeg")
	if err := os.WriteFile(dec, []byte("..rtsp_transport..techo5: the camera offers a Basic login, which is ignored; only Digest is answered\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(video.UseDecoderForTests(dec, nil))
	rec := httptest.NewRecorder()
	talkBackSection(rec, "tok")
	offered := strings.Contains(rec.Body.String(), `name="pic"`)
	if spot := layout.Board == "rook"; offered == spot {
		t.Errorf("picture address offered: %v, on a Spot: %v", offered, spot)
	}
}
