//go:build !dot

package setup

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// The Video section: both switches off on a new device, saved as ticked, and the allowed addresses
// listed with the day each was last used (one run out is not), and forgotten.
func TestTheVideoSection(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	page := func() string {
		w := httptest.NewRecorder()
		videoSection(w, "tok")
		return w.Body.String()
	}
	if p := page(); strings.Contains(p, "checked") || !strings.Contains(p, `name="what" value="video"`) {
		t.Errorf("a new device's section: %s", p)
	}
	if p := saveVideo(videoPost(url.Values{"on": {"yes"}, "dlna": {"yes"}})); p != "" {
		t.Fatal(p)
	}
	if c := config.Get().Video; !c.On || !c.DLNA {
		t.Errorf("saved %+v", c)
	}
	_ = config.Set().Video().Allow("192.168.1.30", time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC).AddDate(0, 0, 0))
	_ = config.Set().Video().Allow("192.168.1.31", time.Now())
	_ = config.Set().Video().Allow("192.168.1.32", time.Now().Add(-config.AllowedFor-time.Hour)) // run out
	p := page()
	if strings.Count(p, "checked") != 2 || !strings.Contains(p, "192.168.1.31, last used "+time.Now().Format("Jan 2, 2006")) ||
		strings.Contains(p, "192.168.1.32") {
		t.Errorf("with two allowed: %s", p)
	}
	if p := saveVideo(videoPost(url.Values{"on": {"yes"}, "forget": {"yes"}})); p != "" {
		t.Fatal(p)
	}
	if c := config.Get().Video; !c.On || c.DLNA || len(c.Allowed) != 0 {
		t.Errorf("after unticking DLNA and forgetting: %+v", c)
	}
}

func videoPost(form url.Values) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/setup/save", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return r
}
