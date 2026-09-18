package hass

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/gorilla/websocket"
)

// fakeLibrary answers browses of a small photo library, as many as are asked on one connection,
// counting the connections.
func fakeLibrary(t *testing.T, conns *int) *httptest.Server {
	folder := func(title, id string, kids ...map[string]any) map[string]any {
		return map[string]any{"title": title, "media_content_id": id, "children": kids}
	}
	sub := func(title, id string) map[string]any {
		return map[string]any{"title": title, "media_content_id": id, "can_expand": true}
	}
	photo := func(id string) map[string]any {
		return map[string]any{"title": id, "media_content_id": id, "media_content_type": "image/jpeg", "can_play": true}
	}
	tree := map[string]map[string]any{
		"root":           folder("photos", "root", sub("2006", "root/2006"), sub("2007", "root/2007"), photo("root/a.jpg")),
		"root/2006":      folder("2006", "root/2006", sub("Trip", "root/2006/Trip"), photo("root/2006/b.jpg")),
		"root/2007":      folder("2007", "root/2007", sub("Broken", "root/2007/Broken"), photo("root/2007/c.jpg")),
		"root/2006/Trip": folder("Trip", "root/2006/Trip", photo("root/2006/Trip/d.jpg"), photo("root/2006/Trip/e.jpg")),
	}
	up := websocket.Upgrader{}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer c.Close()
		*conns++
		_ = c.WriteJSON(map[string]string{"type": "auth_required"})
		var auth map[string]string
		if c.ReadJSON(&auth) != nil {
			return
		}
		_ = c.WriteJSON(map[string]string{"type": "auth_ok"})
		for {
			var cmd map[string]any
			if c.ReadJSON(&cmd) != nil {
				return
			}
			m, ok := tree[cmd["media_content_id"].(string)]
			if !ok {
				_ = c.WriteJSON(map[string]any{"id": cmd["id"], "type": "result", "success": false,
					"error": map[string]string{"code": "browse_media_failed", "message": "unknown"}})
				continue
			}
			_ = c.WriteJSON(map[string]any{"id": cmd["id"], "type": "result", "success": true, "result": m})
		}
	}))
}

// A walk lists every folder under the root, breadth first, over one connection; a folder that fails
// to list is passed over, the folder limit holds, and the walk stops when told to.
func TestBrowseTree(t *testing.T) {
	var conns int
	srv := fakeLibrary(t, &conns)
	defer srv.Close()
	c := &Client{acc: access{URL: srv.URL, Token: "secret"}}

	var seen, photos []string
	err := c.BrowseTree(context.Background(), "root", 100, func(m Media) bool {
		seen = append(seen, m.ID)
		for _, ch := range m.Children {
			if ch.CanPlay {
				photos = append(photos, ch.ID)
			}
		}
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"root", "root/2006", "root/2007", "root/2006/Trip"}; !slices.Equal(seen, want) {
		t.Errorf("folders = %v, want %v (breadth first, Broken passed over)", seen, want)
	}
	if len(photos) != 5 {
		t.Errorf("photos = %v, want all 5", photos)
	}
	if conns != 1 {
		t.Errorf("%d connections, want one for the whole walk", conns)
	}

	seen = nil
	_ = c.BrowseTree(context.Background(), "root", 2, func(m Media) bool { seen = append(seen, m.ID); return true })
	if len(seen) != 2 {
		t.Errorf("a limit of 2 folders listed %v", seen)
	}
	seen = nil
	_ = c.BrowseTree(context.Background(), "root", 100, func(m Media) bool { seen = append(seen, m.ID); return false })
	if len(seen) != 1 {
		t.Errorf("stopping at once still listed %v", seen)
	}
	if err := c.BrowseTree(context.Background(), "nowhere", 100, func(Media) bool { return true }); err == nil {
		t.Error("a root that fails to list gave no error")
	}
}
