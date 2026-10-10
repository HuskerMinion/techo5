package home

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// fakeNames is Home Assistant knowing two entities by name, the porch camera and a doorbell snapshot,
// and nothing else. It counts the state requests made of it.
func fakeNames(t *testing.T) *atomic.Int32 {
	t.Helper()
	var asked atomic.Int32
	names := map[string]string{
		"camera.porch":            "Porch",
		"image.doorbell_snapshot": "Doorbell snapshot",
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if id, ok := strings.CutPrefix(r.URL.Path, "/api/states/"); ok {
			asked.Add(1)
			if name, known := names[id]; known {
				_, _ = w.Write([]byte(`{"state":"idle","attributes":{"friendly_name":"` + name + `"}}`))
				return
			}
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	config.Use(filepath.Join(dir, "state.json"))
	was := hass.Path
	hass.Path = filepath.Join(dir, "hass.json")
	t.Cleanup(func() { hass.Path = was })
	if err := hass.Get().Set(srv.URL, "token"); err != nil {
		t.Fatal(err)
	}
	return &asked
}

// A camera the device has no name for is named as Home Assistant names it, once the answer arrives.
func TestAnUnlistedCameraGetsItsFriendlyName(t *testing.T) {
	fakeNames(t)
	f := &Feature{}
	f.ShowCamera("camera.porch", time.Minute)
	eventually(t, "the friendly name", func() bool { v, _ := f.Camera(); return v.Name == "Porch" })
}

// A camera on the device's own list keeps the name it was given there, and Home Assistant is not asked.
func TestAListedCameraKeepsItsOwnName(t *testing.T) {
	asked := fakeNames(t)
	if err := config.Set().Home().Cameras([]config.Camera{{Entity: "camera.porch", Name: "Back porch"}}); err != nil {
		t.Fatal(err)
	}
	f := &Feature{}
	f.ShowCamera("camera.porch", time.Minute)
	time.Sleep(200 * time.Millisecond) // long enough for a lookup to land, were there one
	if v, _ := f.Camera(); v.Name != "Back porch" {
		t.Errorf("name %q, want the list's own", v.Name)
	}
	if n := asked.Load(); n != 0 {
		t.Errorf("asked Home Assistant %d times for a camera already named", n)
	}
}

// A camera Home Assistant has no name for, or cannot be asked about, keeps its entity id.
func TestAnUnnamedCameraKeepsItsEntityID(t *testing.T) {
	asked := fakeNames(t)
	f := &Feature{}
	f.ShowCamera("camera.nowhere", time.Minute)
	eventually(t, "the lookup", func() bool { return asked.Load() > 0 })
	time.Sleep(100 * time.Millisecond)
	if v, _ := f.Camera(); v.Name != "camera.nowhere" {
		t.Errorf("name %q, want the entity id", v.Name)
	}
}

// A name that arrives after its camera has gone does not land on the view that replaced it.
func TestALateNameStaysWithItsCamera(t *testing.T) {
	fakeNames(t)
	f := &Feature{}
	f.ShowCamera("camera.porch", time.Minute)
	f.ShowCamera("camera.nowhere", time.Minute)
	time.Sleep(200 * time.Millisecond)
	if v, _ := f.Camera(); v.Entity != "camera.nowhere" || v.Name == "Porch" {
		t.Errorf("view %q named %q: the porch's name landed on another camera", v.Entity, v.Name)
	}
}

// An image entity with no title is named as Home Assistant names it, in place of the placeholder.
func TestAnImageWithNoTitleGetsItsFriendlyName(t *testing.T) {
	fakeNames(t)
	f := &Feature{}
	if err := f.ShowPicture("image.doorbell_snapshot", Caption{ID: "1"}, time.Minute, ""); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the friendly name", func() bool { v, _ := f.Camera(); return v.Name == "Doorbell snapshot" })
}

// A picture's words carry no title or message when it was sent with none, so the page can draw the
// picture alone.
func TestACaptionKnowsWhenItHasNoWords(t *testing.T) {
	for _, c := range []struct {
		caption Caption
		want    bool
	}{
		{Caption{ID: "1"}, true},
		{Caption{ID: "1", Title: "Doorbell"}, false},
		{Caption{ID: "1", Message: "Someone is here"}, false},
	} {
		if got := c.caption.Wordless(); got != c.want {
			t.Errorf("%+v: Wordless %v, want %v", c.caption, got, c.want)
		}
	}
}
