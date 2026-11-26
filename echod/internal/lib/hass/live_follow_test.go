package hass

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// fakeLive is a Home Assistant that answers subscribe_entities with its result and then init, as it
// does, written as is (empty for no first event at all), and get_states with states.
func fakeLive(t *testing.T, init string, states string) *Live {
	t.Helper()
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
			id := int(cmd["id"].(float64))
			switch cmd["type"] {
			case "subscribe_entities":
				_ = c.WriteJSON(map[string]any{"id": id, "type": "result", "success": true, "result": nil})
				if init != "" {
					_ = c.WriteMessage(websocket.TextMessage, fmt.Appendf(nil, `{"id":%d,"type":"event","event":%s}`, id, init))
				}
			case "get_states":
				_ = c.WriteMessage(websocket.TextMessage, fmt.Appendf(nil, `{"id":%d,"type":"result","success":true,"result":%s}`, id, states))
			default:
				_ = c.WriteJSON(map[string]any{"id": id, "type": "result", "success": true, "result": nil})
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
	return l
}

// follow follows ids on l and returns what has arrived so far, by entity.
func follow(t *testing.T, l *Live, ids ...string) func() map[string]string {
	t.Helper()
	var mu sync.Mutex
	got := map[string]string{}
	err := l.FollowEntities(context.Background(), ids, func(e LiveEntity) {
		mu.Lock()
		got[e.ID] = e.State
		mu.Unlock()
	}, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	return func() map[string]string {
		mu.Lock()
		defer mu.Unlock()
		out := map[string]string{}
		for k, v := range got {
			out[k] = v
		}
		return out
	}
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for range 400 {
		if ok() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal(what)
}

// One entity in the first event that can't be read here, a number too large for a float, dropped the
// whole event, and with it every other entity's state, for as long as the dashboard was up (#740).
func TestOneUnreadableEntityLeavesTheOthersFirstStates(t *testing.T) {
	l := fakeLive(t, `{"a":{"switch.kettle":{"s":"on","a":{}},"sensor.odd":{"s":"1","a":{"big":1e999}},"switch.fan":{"s":"on","a":{"friendly_name":"Fan"}}}}`, `[]`)
	got := follow(t, l, "switch.kettle", "sensor.odd", "switch.fan")
	waitFor(t, "the first states never arrived", func() bool { return len(got()) >= 2 })
	if g := got(); g["switch.kettle"] != "on" || g["switch.fan"] != "on" {
		t.Errorf("got %v, want both switches on", g)
	}
}

// A first event that never comes, after the subscription's result, is made up for by reading the states
// once, so the page isn't drawn from nothing until the session ends (#740). Only the entities followed
// are taken.
func TestFirstStatesThatNeverComeAreReadInstead(t *testing.T) {
	was := firstStatesWait
	firstStatesWait = 20 * time.Millisecond
	t.Cleanup(func() { firstStatesWait = was })

	states, _ := json.Marshal([]map[string]any{
		{"entity_id": "switch.kettle", "state": "on", "attributes": map[string]any{"friendly_name": "Kettle"}},
		{"entity_id": "light.other", "state": "off", "attributes": map[string]any{}},
	})
	l := fakeLive(t, "", string(states))
	got := follow(t, l, "switch.kettle", "switch.fan")
	waitFor(t, "the states were not read after the first event failed to come", func() bool { return len(got()) > 0 })
	if g := got(); len(g) != 1 || g["switch.kettle"] != "on" {
		t.Errorf("got %v, want only switch.kettle, on", g)
	}
}

// With the first event in, the states are not read again.
func TestFirstStatesThatComeAreNotReadAgain(t *testing.T) {
	was := firstStatesWait
	firstStatesWait = 20 * time.Millisecond
	t.Cleanup(func() { firstStatesWait = was })

	l := fakeLive(t, `{"a":{"switch.kettle":{"s":"off","a":{}}}}`, `[{"entity_id":"switch.kettle","state":"on","attributes":{}}]`)
	got := follow(t, l, "switch.kettle")
	waitFor(t, "the first states never arrived", func() bool { return len(got()) > 0 })
	time.Sleep(100 * time.Millisecond)
	if g := got(); g["switch.kettle"] != "off" {
		t.Errorf("got %v, want the first event's off, not get_states' on", g)
	}
}

// A follower that gives up before the first event was waited out is not read for: nobody is left to
// fill.
func TestFirstStatesAreNotReadForAFollowerThatGaveUp(t *testing.T) {
	was := firstStatesWait
	firstStatesWait = 50 * time.Millisecond
	t.Cleanup(func() { firstStatesWait = was })

	l := fakeLive(t, "", `[{"entity_id":"switch.kettle","state":"on","attributes":{}}]`)
	ctx, cancel := context.WithCancel(context.Background())
	var mu sync.Mutex
	filled := 0
	err := l.FollowEntities(ctx, []string{"switch.kettle"}, func(LiveEntity) {
		mu.Lock()
		filled++
		mu.Unlock()
	}, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	time.Sleep(200 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if filled != 0 {
		t.Errorf("filled %d states for a follower that gave up", filled)
	}
}
