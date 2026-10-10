package hass

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
)

// CameraCanStream is supported_features' bit for a camera Home Assistant can stream
// (CameraEntityFeature.STREAM); one without it only has snapshots.
const CameraCanStream = 2

// CameraStream is the address of a camera's live stream as an HLS playlist, which Home Assistant starts
// on asking (camera/stream). The address carries its own access token, made for this one stream and
// good while it is being watched, so it is opened without the long-lived token: nothing that reads it
// is handed the house.
func (c *Client) CameraStream(ctx context.Context, entity string) (string, error) {
	s, err := c.wsOpen(ctx)
	if err != nil {
		return "", err
	}
	defer s.conn.Close()
	raw, err := s.call(map[string]any{"type": "camera/stream", "entity_id": entity, "format": "hls"})
	if err != nil {
		return "", err
	}
	var out struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", err
	}
	if !strings.HasPrefix(out.URL, "/api/hls/") {
		return "", errors.New("hass: camera/stream answered no playlist")
	}
	return strings.TrimSuffix(c.baseURL(), "/") + out.URL, nil
}
