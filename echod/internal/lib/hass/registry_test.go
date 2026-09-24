package hass

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// fakeRegistries answers the two registry commands with what Home Assistant itself sends, and records what
// it was asked for. The shapes are its own, field for field: a device as DeviceEntry.dict_repr, and an
// entity as RegistryEntry.as_partial_dict — which is what config/entity_registry/list returns, keys and
// all. Both carry a dozen fields nothing here reads, kept in so a payload that grows or reorders does not
// make this pass by accident.
func fakeRegistries(t *testing.T, asked *[]string) *httptest.Server {
	devices := []map[string]any{
		{
			"id": "device-this", "area_id": "hall", "name": "Techo5 Hall", "name_by_user": nil,
			"connections":    []any{[]any{"mac", "aa:bb:cc:dd:ee:02"}},
			"identifiers":    []any{[]any{"esphome", "aa:bb:cc:dd:ee:02"}},
			"config_entries": []any{"entry-1"}, "config_entries_subentries": map[string]any{},
			"config_entry_id": "entry-1", "config_subentry_id": nil, "created_at": 1.5,
			"disabled_by": nil, "entry_type": nil, "hw_version": nil, "labels": []any{},
			"manufacturer": "Espressif", "model": "ESP32-S3", "model_id": nil, "modified_at": 2.5,
			"parent_device_id": nil, "primary_config_entry": "entry-1", "serial_number": nil,
			"sw_version": "2026.9.0", "via_device_id": nil, "configuration_url": nil,
		},
		{
			"id": "device-phone", "area_id": nil, "name": "A phone", "name_by_user": nil,
			// A connection is a [type, value] pair and only one kind of them is an address, so a device
			// known by something else is a device known by something else.
			"connections":     []any{[]any{"bluetooth", "aa:bb:cc:dd:ee:03"}},
			"identifiers":     []any{[]any{"esphome", "aa:bb:cc:dd:ee:03"}},
			"config_entry_id": "entry-2", "created_at": 1.5, "modified_at": 2.5,
		},
	}
	entities := []map[string]any{
		{
			"entity_id": "media_player.hall_techo5_hall_speaker", "device_id": "device-this",
			"platform": "esphome", "area_id": "hall", "config_entry_id": "entry-1",
			"categories": map[string]any{}, "config_subentry_id": nil, "created_at": 1.5,
			"disabled_by": nil, "entity_category": nil, "has_entity_name": true, "hidden_by": nil,
			"icon": nil, "id": "entity-1", "labels": []any{}, "modified_at": 2.5, "name": nil,
			"options": map[string]any{}, "original_name": "Speaker", "translation_key": nil,
			"unique_id": "aa:bb:cc:dd:ee:02-media_player",
		},
		{
			// Music Assistant's own player for the same room is a device of its own, and it is a
			// media_player too: what tells them apart is who provides it, not what it is called.
			"entity_id": "media_player.hall", "device_id": "device-ma", "platform": "music_assistant",
			"id": "entity-2", "config_entry_id": "entry-4",
		},
		{
			"entity_id": "switch.hall_sendspin", "device_id": "device-this", "platform": "esphome",
			"id": "entity-3", "config_entry_id": "entry-1",
		},
	}

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
			kind, _ := cmd["type"].(string)
			*asked = append(*asked, kind)
			var result any
			switch kind {
			case "config/device_registry/list":
				result = devices
			case "config/entity_registry/list":
				result = entities
			default:
				_ = c.WriteJSON(map[string]any{"id": cmd["id"], "type": "result", "success": false,
					"error": map[string]string{"code": "unknown_command", "message": kind}})
				continue
			}
			_ = c.WriteJSON(map[string]any{"id": cmd["id"], "type": "result", "success": true, "result": result})
		}
	}))
}

// Which device provides which entity comes from Home Assistant's registries, and both commands are
// websocket-only. This pins the two command names and the keys they answer with, which is the part that
// cannot be checked by reading: a wrong name here is a lookup that silently finds nothing and falls back
// to a guess, which is exactly the bug the lookup exists to fix.
func TestRegistriesReadWhoProvidesWhat(t *testing.T) {
	var asked []string
	srv := fakeRegistries(t, &asked)
	defer srv.Close()

	c := &Client{acc: access{URL: srv.URL, Token: "secret"}}
	devices, entities, err := c.Registries(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	want := []string{"config/device_registry/list", "config/entity_registry/list"}
	if !slices.Equal(asked, want) {
		t.Errorf("asked for %v, want %v", asked, want)
	}

	if len(devices) != 2 {
		t.Fatalf("%d devices, want 2", len(devices))
	}
	if got := devices[0].ID; got != "device-this" {
		t.Errorf("first device = %q", got)
	}
	if got := devices[0].Connections; len(got) != 1 || len(got[0]) != 2 || got[0][0] != "mac" ||
		got[0][1] != "aa:bb:cc:dd:ee:02" {
		t.Errorf("connections = %v, want the [type, value] pair it was sent", got)
	}
	if got := devices[0].Area; got != "hall" {
		t.Errorf("area = %q, want the area the dashboard also reads from here", got)
	}

	if len(entities) != 3 {
		t.Fatalf("%d entities, want 3", len(entities))
	}
	first := entities[0]
	if first.ID != "media_player.hall_techo5_hall_speaker" || first.DeviceID != "device-this" ||
		first.Platform != "esphome" {
		t.Errorf("entity = %+v, want the id, the device and the platform", first)
	}
	// The two that are not it, and why: a media player someone else provides, and a switch this device
	// does provide. Neither is what the lookup is for.
	if entities[1].Platform != "music_assistant" || entities[1].DeviceID != "device-ma" {
		t.Errorf("Music Assistant's own player = %+v, want it a different platform and device", entities[1])
	}
	if entities[2].ID != "switch.hall_sendspin" || entities[2].Platform != "esphome" {
		t.Errorf("the switch = %+v, want it this device's and not a media player", entities[2])
	}
}

// A connection opened without a deadline of its own does not cap the commands that follow: each gets its
// own thirty seconds. A caller that gave one keeps it, so a lookup that must not wait still does not, and
// a walk that may run for minutes is not cut off thirty seconds in.
//
// The bug this pins read the deadline after wrapping the context in thirty seconds, so every session was
// thirty seconds long and every command after that failed — the photo walk gave itself five minutes for up
// to a thousand folders and quietly lost all of them past the first thirty seconds.
func TestTheConnectionsDeadlineIsTheCallers(t *testing.T) {
	var asked []string
	srv := fakeRegistries(t, &asked)
	defer srv.Close()
	c := &Client{acc: access{URL: srv.URL, Token: "secret"}}

	s, err := c.wsOpen(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer s.conn.Close()
	if !s.deadline.IsZero() {
		t.Errorf("deadline = %v with no deadline given, want none: each command gets its own thirty seconds",
			s.deadline)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s2, err := c.wsOpen(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.conn.Close()
	if dl, ok := ctx.Deadline(); !ok || !s2.deadline.Equal(dl) {
		t.Errorf("deadline = %v, want the caller's %v", s2.deadline, dl)
	}
}
