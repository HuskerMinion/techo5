package hass

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// fakeHA answers the websocket handshake and one browse, the way Home Assistant does.
func fakeHA(t *testing.T, token string) *httptest.Server {
	up := websocket.Upgrader{}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/states/select.spot_assistant" {
			if r.Header.Get("Authorization") != "Bearer "+token {
				http.Error(w, "invalid access token", http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`{"state":"Spot Voice"}`))
			return
		}
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
		case cmd["type"] == "config/entity_registry/list":
			_ = c.WriteJSON(map[string]any{"id": cmd["id"], "type": "result", "success": true,
				"result": []map[string]string{
					{"entity_id": "select.other_assistant", "platform": "esphome", "unique_id": "001122334456-pipeline"},
					{"entity_id": "select.spot_assistant", "platform": "esphome", "unique_id": "001122334455-pipeline"},
				}})
			if err := c.ReadJSON(&cmd); err != nil {
				t.Error(err)
				return
			}
			if cmd["type"] != "assist_pipeline/pipeline/list" {
				t.Errorf("unexpected command %v", cmd["type"])
				return
			}
			_ = c.WriteJSON(map[string]any{"id": cmd["id"], "type": "result", "success": true,
				"result": map[string]any{"preferred_pipeline": "preferred-id", "pipelines": []map[string]string{
					{"id": "preferred-id", "name": "Preferred"},
					{"id": "spot-id", "name": "Spot Voice"},
				}}})
		case cmd["type"] == "assist_pipeline/run" &&
			(cmd["pipeline"] == "wake-test" || cmd["pipeline"] == "no-text") &&
			cmd["start_stage"] == "stt" && cmd["end_stage"] == "stt":
			event := func(kind string, data map[string]any) error {
				return c.WriteJSON(map[string]any{"id": cmd["id"], "type": "event",
					"event": map[string]any{"type": kind, "data": data}})
			}
			if err := c.WriteJSON(map[string]any{"id": cmd["id"], "type": "result", "success": true}); err != nil {
				t.Error(err)
				return
			}
			if err := event("run-start", map[string]any{"runner_data": map[string]int{"stt_binary_handler_id": 7}}); err != nil {
				t.Error(err)
				return
			}
			for _, want := range [][]byte{{7, 1, 0, 2, 0}, {7}} {
				kind, got, err := c.ReadMessage()
				if err != nil || kind != websocket.BinaryMessage || !bytes.Equal(got, want) {
					t.Errorf("STT audio frame = %v, %v, %v; want %v", kind, got, err, want)
					return
				}
			}
			if cmd["pipeline"] == "no-text" {
				if err := event("error", map[string]any{"code": "stt-no-text-recognized"}); err != nil {
					t.Error(err)
				}
				return
			}
			if err := event("stt-end", map[string]any{"stt_output": map[string]string{"text": "Alexa"}}); err != nil {
				t.Error(err)
			}
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

func TestTranscribeOnly(t *testing.T) {
	srv := fakeHA(t, "secret")
	defer srv.Close()

	c := &Client{acc: access{URL: srv.URL, Token: "secret"}}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	got, sent, err := c.Transcribe(ctx, "wake-test", 16000, []byte{1, 0, 2, 0})
	if err != nil || !sent || got != "Alexa" {
		t.Fatalf("Transcribe = %q, %v, %v; want Alexa and a sent request", got, sent, err)
	}
	if _, sent, err := c.Transcribe(ctx, "no-text", 16000, []byte{1, 0, 2, 0}); !sent || !errors.Is(err, ErrSTTNoText) {
		t.Errorf("no-text result = sent %v, err %v", sent, err)
	}
	if _, sent, err := c.Transcribe(ctx, "wrong-pipeline", 16000, []byte{1, 0}); !sent || err == nil || errors.Is(err, ErrSTTNoText) {
		t.Errorf("rejected pipeline request = sent %v, err %v", sent, err)
	}
	bad := &Client{acc: access{URL: srv.URL, Token: "wrong"}}
	if _, sent, err := bad.Transcribe(ctx, "wake-test", 16000, []byte{1, 0}); sent || err == nil {
		t.Errorf("unauthenticated pipeline request = sent %v, err %v", sent, err)
	}
}

func TestWakePipelineUsesSatelliteSelection(t *testing.T) {
	srv := fakeHA(t, "secret")
	defer srv.Close()

	c := newClient(srv.URL, "secret")
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	got, err := c.WakePipeline(ctx, "00:11:22:33:44:55", 0)
	if err != nil || got != "spot-id" {
		t.Fatalf("WakePipeline = %q, %v; want spot-id", got, err)
	}
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
