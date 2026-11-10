package hass

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/gorilla/websocket"
)

// WakePipeline resolves the ESPHome Assist selector for the given wake slot.
func (c *Client) WakePipeline(ctx context.Context, mac string, slot int) (string, error) {
	if !macForm.MatchString(mac) || slot < 0 {
		return "", errors.New("hass: invalid wake pipeline device or slot")
	}
	s, err := c.wsOpen(ctx)
	if err != nil {
		return "", err
	}
	defer s.conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = s.conn.Close() })
	defer stop()

	raw, err := s.call(map[string]any{"type": "config/entity_registry/list"})
	if err != nil {
		return "", err
	}
	var entities []struct {
		ID       string `json:"entity_id"`
		Platform string `json:"platform"`
		UniqueID string `json:"unique_id"`
	}
	if err := json.Unmarshal(raw, &entities); err != nil {
		return "", err
	}
	uniqueID := strings.ReplaceAll(mac, ":", "") + "-pipeline"
	if slot > 0 {
		uniqueID += fmt.Sprintf("_%d", slot+1)
	}
	var selection string
	for _, entity := range entities {
		if entity.Platform == "esphome" && strings.HasPrefix(entity.ID, "select.") &&
			strings.EqualFold(strings.ReplaceAll(entity.UniqueID, ":", ""), uniqueID) {
			selection = entity.ID
			break
		}
	}
	if selection == "" {
		return "", errors.New("hass: wake pipeline selector is missing")
	}
	raw, err = c.doContext(ctx, "GET", "/api/states/"+selection, nil)
	if err != nil {
		return "", err
	}
	var state struct {
		Value string `json:"state"`
	}
	if err := json.Unmarshal(raw, &state); err != nil {
		return "", err
	}
	raw, err = s.call(map[string]any{"type": "assist_pipeline/pipeline/list"})
	if err != nil {
		return "", err
	}
	var pipelines struct {
		Preferred string `json:"preferred_pipeline"`
		Pipelines []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"pipelines"`
	}
	if err := json.Unmarshal(raw, &pipelines); err != nil {
		return "", err
	}
	if state.Value == "preferred" && pipelines.Preferred != "" {
		return pipelines.Preferred, nil
	}
	for _, pipeline := range pipelines.Pipelines {
		if pipeline.Name == state.Value && pipeline.ID != "" {
			return pipeline.ID, nil
		}
	}
	return "", errors.New("hass: selected wake pipeline is unavailable")
}

// ErrSTTNoText means Home Assistant ran STT but no words were recognized.
var ErrSTTNoText = errors.New("hass: STT recognized no text")

// Transcribe sends a finite mono PCM clip through just the STT stage of an
// existing Assist pipeline. The caller supplies 16-bit little-endian samples
// and a deadline. It reports whether the pipeline request was sent, even if
// the run later fails. No intent or TTS stage runs.
func (c *Client) Transcribe(ctx context.Context, pipeline string, sampleRate int, pcm []byte) (text string, requested bool, err error) {
	if pipeline == "" {
		return "", false, errors.New("hass: STT pipeline is required")
	}
	if sampleRate <= 0 || len(pcm) == 0 || len(pcm)%2 != 0 {
		return "", false, errors.New("hass: invalid STT audio")
	}
	if _, ok := ctx.Deadline(); !ok {
		return "", false, errors.New("hass: STT deadline is required")
	}
	s, err := c.wsOpen(ctx)
	if err != nil {
		return "", false, err
	}
	defer s.conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = s.conn.Close() })
	defer stop()

	const id = 1
	if err := s.conn.WriteJSON(map[string]any{
		"id":          id,
		"type":        "assist_pipeline/run",
		"pipeline":    pipeline,
		"start_stage": "stt",
		"end_stage":   "stt",
		"input":       map[string]int{"sample_rate": sampleRate},
	}); err != nil {
		return "", false, err
	}

	sent := false
	for {
		var msg struct {
			ID      int    `json:"id"`
			Type    string `json:"type"`
			Success bool   `json:"success"`
			Error   struct {
				Code string `json:"code"`
			} `json:"error"`
			Event struct {
				Type string `json:"type"`
				Data struct {
					Code       string `json:"code"`
					RunnerData struct {
						Handler *byte `json:"stt_binary_handler_id"`
					} `json:"runner_data"`
					Output struct {
						Text string `json:"text"`
					} `json:"stt_output"`
				} `json:"data"`
			} `json:"event"`
		}
		if err := s.conn.ReadJSON(&msg); err != nil {
			if ctx.Err() != nil {
				return "", true, ctx.Err()
			}
			return "", true, err
		}
		if msg.ID != id {
			continue
		}
		if msg.Type == "result" && !msg.Success {
			return "", true, fmt.Errorf("hass: STT request rejected: %s", msg.Error.Code)
		}
		switch msg.Event.Type {
		case "run-start":
			handler := msg.Event.Data.RunnerData.Handler
			if handler == nil || sent {
				return "", true, errors.New("hass: invalid STT audio handler")
			}
			if err := sendSTTClip(s.conn, *handler, pcm); err != nil {
				return "", true, err
			}
			sent = true
		case "stt-end":
			if !sent {
				return "", true, errors.New("hass: STT ended before audio was sent")
			}
			return msg.Event.Data.Output.Text, true, nil
		case "error":
			if sent && msg.Event.Data.Code == "stt-no-text-recognized" {
				return "", true, ErrSTTNoText
			}
			return "", true, fmt.Errorf("hass: STT failed: %s", msg.Event.Data.Code)
		case "run-end":
			return "", true, errors.New("hass: STT ended without a transcript")
		}
	}
}

func sendSTTClip(conn *websocket.Conn, handler byte, pcm []byte) error {
	const chunk = 4096
	frame := make([]byte, 1+chunk)
	frame[0] = handler
	for len(pcm) > 0 {
		n := copy(frame[1:], pcm)
		if err := conn.WriteMessage(websocket.BinaryMessage, frame[:n+1]); err != nil {
			return err
		}
		pcm = pcm[n:]
	}
	return conn.WriteMessage(websocket.BinaryMessage, frame[:1])
}
