package hass

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/websocket"
)

// fakeHA answers the websocket handshake and one browse, the way Home Assistant does.
func fakeHA(t *testing.T, token string) *httptest.Server {
	up := websocket.Upgrader{}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/websocket" {
			http.NotFound(w, r)
			return
		}
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer c.Close()
		_ = c.WriteJSON(map[string]string{"type": "auth_required", "ha_version": "2026.9.0"})
		var auth map[string]string
		if err := c.ReadJSON(&auth); err != nil {
			return
		}
		if auth["access_token"] != token {
			_ = c.WriteJSON(map[string]string{"type": "auth_invalid", "message": "Invalid access token"})
			return
		}
		_ = c.WriteJSON(map[string]string{"type": "auth_ok"})
		var cmd map[string]any
		if err := c.ReadJSON(&cmd); err != nil {
			return
		}
		fail := func() {
			_ = c.WriteJSON(map[string]any{"id": cmd["id"], "type": "result", "success": false,
				"error": map[string]string{"code": "browse_media_failed", "message": "unknown"}})
		}
		switch {
		case cmd["type"] == "media_source/browse_media" && cmd["media_content_id"] == "media-source://radio_browser/local":
			_ = c.WriteJSON(map[string]any{"type": "event", "id": 99})
			_ = c.WriteJSON(map[string]any{"id": cmd["id"], "type": "result", "success": true, "result": map[string]any{
				"title": "Local stations", "media_content_id": "media-source://radio_browser/local",
				"children": []map[string]any{{
					"title": "KXYZ", "media_content_id": "media-source://radio_browser/uuid-1",
					"media_content_type": "audio/mpeg", "can_play": true, "thumbnail": "http://logo",
				}},
			}})
		case cmd["type"] == "media_source/resolve_media" && cmd["media_content_id"] == "media-source://immich/photo-1":
			_ = c.WriteJSON(map[string]any{"id": cmd["id"], "type": "result", "success": true, "result": map[string]any{
				"url": "/api/media/photo-1.jpg", "mime_type": "image/jpeg",
			}})
		default:
			fail()
		}
	}))
}

func TestBrowse(t *testing.T) {
	srv := fakeHA(t, "secret")
	defer srv.Close()

	c := &Client{acc: access{URL: srv.URL, Token: "secret"}}
	m, err := c.Browse(context.Background(), "media-source://radio_browser/local")
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Children) != 1 || m.Children[0].Title != "KXYZ" || !m.Children[0].CanPlay || m.Children[0].Kind != "audio/mpeg" {
		t.Fatalf("listing = %+v", m)
	}

	bad := &Client{acc: access{URL: srv.URL, Token: "wrong"}}
	if _, err := bad.Browse(context.Background(), "media-source://radio_browser/local"); err == nil {
		t.Error("a wrong token browsed")
	}
	if _, err := c.Browse(context.Background(), "media-source://radio_browser/nowhere"); err == nil {
		t.Error("a failed browse reported no error")
	}
}

func TestResolveMedia(t *testing.T) {
	srv := fakeHA(t, "secret")
	defer srv.Close()

	c := &Client{acc: access{URL: srv.URL, Token: "secret"}}
	r, err := c.ResolveMedia(context.Background(), "media-source://immich/photo-1")
	if err != nil {
		t.Fatal(err)
	}
	if r.URL != "/api/media/photo-1.jpg" || r.MIME != "image/jpeg" {
		t.Fatalf("resolved = %+v", r)
	}

	if _, err := c.ResolveMedia(context.Background(), "media-source://immich/nowhere"); err == nil {
		t.Error("a failed resolve reported no error")
	}
}

func TestEntities(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`[{"entity_id":"weather.forecast_home","attributes":{"friendly_name":"Forecast Home"}},
			{"entity_id":"sensor.weather_temp","attributes":{}},
			{"entity_id":"weather.backyard","attributes":{"friendly_name":"Backyard"}}]`))
	}))
	defer srv.Close()
	c := &Client{acc: access{URL: srv.URL, Token: "secret"}, http: srv.Client()}
	list, err := c.Entities("weather")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].ID != "weather.forecast_home" || list[1].Name != "Backyard" {
		t.Fatalf("entities = %+v", list)
	}
}

func TestUnknownMediaSource(t *testing.T) {
	missing := &CommandError{Command: "media_source/browse_media", Code: "browse_media_failed",
		Message: "Failed to browse media with content ID media-source://radio_browser/local: Unknown media source: radio_browser"}
	if !UnknownMediaSource(fmt.Errorf("wrapped: %w", missing), "radio_browser") {
		t.Error("the missing integration was not recognized")
	}
	if UnknownMediaSource(missing, "immich") {
		t.Error("another domain's absence was taken for this one's")
	}
	failing := &CommandError{Command: "media_source/browse_media", Code: "browse_media_failed", Message: "Connection timed out"}
	if UnknownMediaSource(failing, "radio_browser") || UnknownMediaSource(errors.New("Unknown media source: radio_browser"), "radio_browser") {
		t.Error("a failure was taken for a missing integration")
	}
	if got := missing.Error(); got != "hass: media_source/browse_media: browse_media_failed "+missing.Message {
		t.Errorf("error reads %q", got)
	}
}
