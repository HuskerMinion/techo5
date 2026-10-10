package hass

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/websocket"
)

// fakeStreams answers camera/stream as Home Assistant does: the playlist's path, with the stream's own
// token in it, for a camera it can stream, and an error for one it cannot.
func fakeStreams(t *testing.T, asked *[]map[string]any) *httptest.Server {
	up := websocket.Upgrader{}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer c.Close()
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
			*asked = append(*asked, cmd)
			if cmd["type"] == "camera/stream" && cmd["entity_id"] == "camera.porch" {
				_ = c.WriteJSON(map[string]any{"id": cmd["id"], "type": "result", "success": true,
					"result": map[string]string{"url": "/api/hls/5f3a9c/master_playlist.m3u8"}})
				continue
			}
			_ = c.WriteJSON(map[string]any{"id": cmd["id"], "type": "result", "success": false,
				"error": map[string]string{"code": "start_stream_failed", "message": "camera.garage does not support play stream service"}})
		}
	}))
}

// A camera's stream is asked for as HLS, and its playlist comes back as an address on Home Assistant
// that opens without the long-lived token; a camera that cannot stream is an error, for the page to
// show snapshots.
func TestACameraStreamIsAPlaylistOnHomeAssistant(t *testing.T) {
	var asked []map[string]any
	srv := fakeStreams(t, &asked)
	defer srv.Close()
	c := &Client{acc: access{URL: srv.URL, Token: "secret"}}

	url, err := c.CameraStream(context.Background(), "camera.porch")
	if err != nil {
		t.Fatal(err)
	}
	if want := srv.URL + "/api/hls/5f3a9c/master_playlist.m3u8"; url != want {
		t.Errorf("the stream is at %q, want %q", url, want)
	}
	if len(asked) != 1 || asked[0]["format"] != "hls" || asked[0]["entity_id"] != "camera.porch" {
		t.Errorf("asked %v", asked)
	}
	if _, err := c.CameraStream(context.Background(), "camera.garage"); err == nil {
		t.Error("a camera that cannot stream gave a stream")
	}
}
