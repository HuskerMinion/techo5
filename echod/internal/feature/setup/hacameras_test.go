package setup

import (
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// The Home Assistant cameras form: None, All or These, saved only when something changed, and the
// section says which it is.
func TestHACamerasForm(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	save := func(v url.Values) string {
		v.Set("token", "the-pages-own-token")
		v.Set("what", "hacameras")
		return saveHACameras(form(v))
	}
	section := func() string {
		rec := httptest.NewRecorder()
		haCamerasSection(rec, "tok")
		return rec.Body.String()
	}
	if p := section(); !strings.Contains(p, "every camera Home Assistant has") || !strings.Contains(p, `value="all" checked`) {
		t.Errorf("never set does not say All:\n%s", p)
	}

	if p := save(url.Values{"mode": {"none"}, "was_mode": {"all"}}); p != "" {
		t.Fatal(p)
	}
	if !config.Get().Home.NoHACameras() {
		t.Errorf("None was not saved: %+v", config.Get().Home)
	}
	if p := section(); !strings.Contains(p, "none of Home Assistant's cameras") || !strings.Contains(p, `value="none" checked`) {
		t.Errorf("the section does not say None:\n%s", p)
	}

	if p := save(url.Values{"mode": {"these"}, "was_mode": {"none"}, "list": {"camera.porch=Back <porch>\r\ncamera.drive\r\n"}}); p != "" {
		t.Fatal(p)
	}
	h := config.Get().Home
	if len(h.Cameras) != 2 || h.Cameras[0].Name != "Back <porch>" || h.Cameras[1] != (config.Camera{Entity: "camera.drive", Name: "drive"}) {
		t.Errorf("These saved %+v", h.Cameras)
	}
	if p := section(); !strings.Contains(p, "these 2: Back &lt;porch&gt;, drive") || strings.Contains(p, "<porch>") {
		t.Errorf("the section does not list These, escaped:\n%s", p)
	}

	// Unchanged: nothing written, even when the device's setting moved meanwhile.
	if err := config.Set().Home().NoCameras(); err != nil {
		t.Fatal(err)
	}
	if p := save(url.Values{"mode": {"these"}, "was_mode": {"these"}, "list": {"camera.porch"}, "was_list": {"camera.porch"}}); p != "" || !config.Get().Home.NoHACameras() {
		t.Errorf("an unchanged form was saved (%s): %+v", p, config.Get().Home)
	}

	for _, bad := range []url.Values{
		{"mode": {"these"}, "was_mode": {"none"}, "list": {""}},
		{"mode": {"these"}, "was_mode": {"none"}, "list": {"none"}},
		{"mode": {"some"}, "was_mode": {"none"}},
	} {
		if save(bad) == "" {
			t.Errorf("%v was saved", bad)
		}
	}

	if p := save(url.Values{"mode": {"all"}, "was_mode": {"none"}}); p != "" || !config.Get().Home.AllHACameras() {
		t.Errorf("All was not saved (%s): %+v", p, config.Get().Home)
	}
}
