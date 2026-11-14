//go:build !dot

package dashboard

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

func TestAssistActionOf(t *testing.T) {
	var card raw
	if err := json.Unmarshal([]byte(`{"type": "picture", "image": "/local/a.png",
		"tap_action": {"action": "assist", "pipeline_id": "01abc", "start_listening": false}}`), &card); err != nil {
		t.Fatal(err)
	}
	a := pictureTap(card, "")
	if a == nil || !a.Assist || a.Pipeline != "01abc" {
		t.Fatalf("assist tap: %+v", a)
	}
}

func TestAssistSlot(t *testing.T) {
	f := assistFacts{
		pipelines: []hass.Pipeline{{ID: "p1", Name: "Home"}, {ID: "p2", Name: "Helper"}, {ID: "p3", Name: "Other"}},
		preferred: "p1",
		selects:   [2]string{"preferred", "Helper"},
	}
	for pipeline, want := range map[string]int{
		"p1": 0, // the preferred pipeline, which the first assistant runs as "preferred"
		"p2": 1, // the second assistant's own
		"p3": 0, // run by neither: the first
		"no": 0, // not a pipeline at all
	} {
		if got := assistSlot(pipeline, f); got != want {
			t.Errorf("assistSlot(%q) = %d, want %d", pipeline, got, want)
		}
	}
}

func TestStartAssist(t *testing.T) {
	asked, started := 0, -1
	lookupAssist = func(context.Context) (assistFacts, error) {
		asked++
		return assistFacts{pipelines: []hass.Pipeline{{ID: "p2", Name: "Helper"}}, selects: [2]string{"preferred", "Helper"}}, nil
	}
	startVoice = func(slot int) { started = slot }
	defer func() { lookupAssist, startVoice = askAssist, func(int) {} }()

	startAssist("p2")
	if started != 1 || asked != 1 {
		t.Errorf("pipeline p2: started %d after %d lookups", started, asked)
	}
	for _, p := range []string{"", "preferred", "last_used"} {
		startAssist(p)
		if started != 0 || asked != 1 {
			t.Errorf("pipeline %q: started %d after %d lookups", p, started, asked)
		}
	}
}

func TestOwnSelects(t *testing.T) {
	devices := []hass.Device{
		{ID: "d1", Connections: [][]string{{"mac", "AA:BB:CC:00:11:22"}}},
		{ID: "d2", Connections: [][]string{{"mac", "aa:bb:cc:99:99:99"}}},
	}
	entities := []hass.RegistryEntity{
		{ID: "select.desk_assistant", DeviceID: "d1", Platform: "esphome", UniqueID: "AA:BB:CC:00:11:22-pipeline"},
		{ID: "select.desk_assistant_2", DeviceID: "d1", Platform: "esphome", UniqueID: "AA:BB:CC:00:11:22-pipeline_2"},
		{ID: "select.desk_wake_word", DeviceID: "d1", Platform: "esphome", UniqueID: "AA:BB:CC:00:11:22-wake_word"},
		{ID: "select.hall_assistant", DeviceID: "d2", Platform: "esphome", UniqueID: "aa:bb:cc:99:99:99-pipeline"},
	}
	got := ownSelects(devices, entities, "aa:bb:cc:00:11:22")
	if got != [2]string{"select.desk_assistant", "select.desk_assistant_2"} {
		t.Errorf("own selects %v", got)
	}
}

// A picture-entity card with state_image shows the picture for its entity's state, and its own
// image for any other state.
func TestStateImage(t *testing.T) {
	var cards []any
	if err := json.Unmarshal([]byte(`[{"type": "picture-entity", "entity": "assist_satellite.desk",
		"image": "/local/idle.png", "show_name": false, "show_state": false,
		"state_image": {"listening": "/local/ear.png", "responding": "/local/talk.png"},
		"grid_options": {"columns": 6}}]`), &cards); err != nil {
		t.Fatal(err)
	}
	c := &compiler{seen: map[string]bool{}, need: needs{graphs: map[string]int{}}}
	n := c.cards(cards)
	if len(c.need.pictures) != 3 {
		t.Fatalf("%d pictures fetched, want 3", len(c.need.pictures))
	}
	if len(c.need.entities) != 1 || c.need.entities[0] != "assist_satellite.desk" {
		t.Fatalf("followed %v", c.need.entities)
	}
	p := n[0].(pictureNode)
	for state, want := range map[string]string{"listening": "/local/ear.png", "responding": "/local/talk.png",
		"idle": "/local/idle.png", "processing": "/local/idle.png"} {
		l := look{states: map[string]hass.LiveEntity{"assist_satellite.desk": {State: state}}}
		if got := p.shown(l).url; got != want {
			t.Errorf("%s: shows %s, want %s", state, got, want)
		}
	}
}
