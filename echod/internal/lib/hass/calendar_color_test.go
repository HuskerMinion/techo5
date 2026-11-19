package hass

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/gorilla/websocket"
)

// A calendar's color is read from the entity registry, all the calendars in one command, from the
// entries as Home Assistant sends them (RegistryEntry.extended_dict): a calendar with a color picked, one
// with options but no color, and one that is not in the registry, which comes back as null.
func TestCalendarColorsReadTheRegistry(t *testing.T) {
	var asked []map[string]any
	up := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
			asked = append(asked, cmd)
			if cmd["type"] != "config/entity_registry/get_entries" {
				_ = c.WriteJSON(map[string]any{"id": cmd["id"], "type": "result", "success": false,
					"error": map[string]string{"code": "unknown_command", "message": "unknown"}})
				continue
			}
			result := map[string]any{
				"calendar.work": map[string]any{
					"entity_id": "calendar.work", "platform": "google", "aliases": []any{},
					"capabilities": nil, "device_class": nil, "original_icon": nil,
					"options": map[string]any{"calendar": map[string]any{"color": "#16a765"},
						"conversation": map[string]any{"should_expose": false}},
				},
				"calendar.home": map[string]any{
					"entity_id": "calendar.home", "platform": "local_calendar",
					"options": map[string]any{"conversation": map[string]any{"should_expose": false}},
				},
				"calendar.gone": nil,
			}
			_ = c.WriteJSON(map[string]any{"id": cmd["id"], "type": "result", "success": true, "result": result})
		}
	}))
	defer srv.Close()

	c := &Client{acc: access{URL: srv.URL, Token: "secret"}}
	ids := []string{"calendar.work", "calendar.home", "calendar.gone"}
	got, err := c.CalendarColors(context.Background(), ids)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got["calendar.work"] != "#16a765" {
		t.Errorf("colors = %v, want only the one picked", got)
	}
	if len(asked) != 1 || asked[0]["type"] != "config/entity_registry/get_entries" {
		t.Fatalf("asked %v, want one get_entries", asked)
	}
	var sent []string
	for _, id := range asked[0]["entity_ids"].([]any) {
		sent = append(sent, id.(string))
	}
	if !slices.Equal(sent, ids) {
		t.Errorf("asked for %v, want %v", sent, ids)
	}

	if got, err := c.CalendarColors(context.Background(), nil); err != nil || got != nil || len(asked) != 1 {
		t.Errorf("no calendars asked Home Assistant anyway: %v %v", got, err)
	}
}
