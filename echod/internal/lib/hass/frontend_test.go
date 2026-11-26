package hass

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/gorilla/websocket"
)

// scriptedLive is a Home Assistant whose answer to each command is answer's: the result written as is
// ("" for a failure), and any events to send after it under the command's id. asked has each command
// as it came.
func scriptedLive(t *testing.T, answer func(cmd map[string]any) (result string, events []string)) (*Live, func() []map[string]any) {
	t.Helper()
	var mu sync.Mutex
	var asked []map[string]any
	up := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		_ = c.WriteJSON(map[string]string{"type": "auth_required"})
		var auth map[string]any
		if c.ReadJSON(&auth) != nil {
			return
		}
		_ = c.WriteJSON(map[string]string{"type": "auth_ok"})
		for {
			var cmd map[string]any
			if c.ReadJSON(&cmd) != nil {
				return
			}
			mu.Lock()
			asked = append(asked, cmd)
			mu.Unlock()
			id := int(cmd["id"].(float64))
			result, events := answer(cmd)
			if result == "" {
				_ = c.WriteJSON(map[string]any{"id": id, "type": "result", "success": false,
					"error": map[string]string{"code": "unknown_command", "message": "Unknown command."}})
				continue
			}
			_ = c.WriteMessage(websocket.TextMessage, fmt.Appendf(nil, `{"id":%d,"type":"result","success":true,"result":%s}`, id, result))
			for _, ev := range events {
				_ = c.WriteMessage(websocket.TextMessage, fmt.Appendf(nil, `{"id":%d,"type":"event","event":%s}`, id, ev))
			}
		}
	}))
	t.Cleanup(srv.Close)
	c := &Client{acc: access{URL: srv.URL, Token: "secret"}}
	l, err := c.OpenLive(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(l.Close)
	return l, func() []map[string]any {
		mu.Lock()
		defer mu.Unlock()
		return append([]map[string]any(nil), asked...)
	}
}

// The icons and words come from frontend/get_icons and frontend/get_translations, in the shapes Home
// Assistant sends them: icons by domain and device class, or by integration, domain and translation
// key; words flat, by their full key (#760).
func TestFrontendIconsAndWords(t *testing.T) {
	l, asked := scriptedLive(t, func(cmd map[string]any) (string, []string) {
		switch cmd["type"] {
		case "frontend/get_icons":
			if cmd["category"] == "entity" {
				return `{"resources":{"tuya_local":{"binary_sensor":{"tank_full":{"default":"mdi:water-outline","state":{"on":"mdi:water-alert"}}}}}}`, nil
			}
			return `{"resources":{"sensor":{"_":{"default":"mdi:eye"},"voltage":{"default":"mdi:sine-wave"},
				"battery":{"default":"mdi:battery","range":{"0":"mdi:battery-outline","50":"mdi:battery-50","100":"mdi:battery"}}},
				"climate":{"_":{"default":"mdi:thermostat","state_attributes":{"fan_mode":{"state":{"low":"mdi:fan-speed-1"}}}}}}}`, nil
		case "frontend/get_translations":
			return `{"resources":{"component.binary_sensor.entity_component.battery_charging.state.off":"Not charging"}}`, nil
		}
		return "", nil
	})
	ctx := context.Background()
	comp, err := l.ComponentIcons(ctx, []string{"sensor", "climate"})
	if err != nil {
		t.Fatal(err)
	}
	if got := comp["sensor"]["voltage"].For("230"); got != "mdi:sine-wave" {
		t.Errorf("voltage: %q", got)
	}
	// A battery at 72% is in the range from 50; one at 3% in the range from 0.
	if got := comp["sensor"]["battery"].For("72"); got != "mdi:battery-50" {
		t.Errorf("battery at 72: %q", got)
	}
	if got := comp["sensor"]["battery"].For("3"); got != "mdi:battery-outline" {
		t.Errorf("battery at 3: %q", got)
	}
	if got := comp["climate"]["_"].For("heat"); got != "mdi:thermostat" {
		t.Errorf("climate, with a block of its state attributes beside its default: %q", got)
	}
	ent, err := l.EntityIcons(ctx, []string{"tuya_local"})
	if err != nil {
		t.Fatal(err)
	}
	if r := ent["tuya_local"]["binary_sensor"]["tank_full"]; r.For("off") != "mdi:water-outline" || r.For("on") != "mdi:water-alert" {
		t.Errorf("tank_full: %+v", r)
	}
	words, err := l.Translations(ctx, "en", "entity_component", []string{"binary_sensor"})
	if err != nil || words["component.binary_sensor.entity_component.battery_charging.state.off"] != "Not charging" {
		t.Errorf("words %v, %v", words, err)
	}
	cmds := asked()
	if len(cmds) != 3 {
		t.Fatalf("asked %d commands, want 3", len(cmds))
	}
	tr := cmds[2]
	if tr["language"] != "en" || tr["category"] != "entity_component" || fmt.Sprint(tr["integration"]) != "[binary_sensor]" {
		t.Errorf("translations asked as %v", tr)
	}
	// Nothing to ask about is not asked.
	if got, err := l.EntityIcons(ctx, nil); err != nil || len(got) != 0 || len(asked()) != 3 {
		t.Errorf("no platforms: %v, %v, %d asked", got, err, len(asked()))
	}
}

// The display listing carries each entity's integration and translation key (#760).
func TestTheDisplayListingGivesPlatformAndKey(t *testing.T) {
	l, _ := scriptedLive(t, func(cmd map[string]any) (string, []string) {
		return `{"entities":[{"ei":"sensor.down","pl":"speedtestdotnet","tk":"download","dp":1},{"ei":"light.hall","pl":"hue","ic":"mdi:ceiling-light"}]}`, nil
	})
	list, err := l.DisplayListing(context.Background())
	if err != nil || len(list) != 2 {
		t.Fatalf("%v, %v", list, err)
	}
	if list[0].Platform != "speedtestdotnet" || list[0].TranslationKey != "download" || list[1].Icon != "mdi:ceiling-light" {
		t.Errorf("listing %+v", list)
	}
}
