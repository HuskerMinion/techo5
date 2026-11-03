package remote

import (
	"net/http"
	"testing"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

func TestExternalASPSelectionIsReadOnlyThroughRemoteAPI(t *testing.T) {
	f, entities, _ := testRemote(t)
	selected := &esphome.Select{
		Base:    esphome.Base{ObjectID: "microphone_asp_selected", Category: esphome.CategoryConfig},
		Options: []string{"Built-in", "External"},
	}
	entities[selected.ObjectID] = selected

	for _, local := range []config.ASPMode{config.ASPBuiltin, config.ASPExternal} {
		if err := config.Set().Microphone().ASP(local); err != nil {
			t.Fatal(err)
		}
		selected.Set(local.Label())
		listed := f.list()
		found := false
		for _, e := range listed {
			if e.ID == selected.ObjectID {
				found = true
				if e.Writable || e.Kind != "select" || e.State != local.Label() {
					t.Errorf("API exposes mutable ASP selection: %+v", e)
				}
			}
		}
		if !found {
			t.Fatal("ASP select absent from API")
		}
		next := config.ASPExternal
		if local == config.ASPExternal {
			next = config.ASPBuiltin
		}
		response := call(f, http.MethodPost, "/api/v1/entities/microphone_asp_selected",
			`{"state":"`+next.Label()+`"}`, true)
		if response.Code != http.StatusForbidden {
			t.Errorf("remote write returned %d, want 403", response.Code)
		}
		if got := config.Get().Microphone.ASP; got != local || selected.Get() != local.Label() {
			t.Errorf("remote API changed the local ASP choice: stored=%q, select=%q", got, selected.Get())
		}
	}
}
