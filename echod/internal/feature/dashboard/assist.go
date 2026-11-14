//go:build !dot

package dashboard

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/voice"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// A card's assist action starts a voice request on the device, as saying a wake word would. The device
// has two assistants, one for each wake word, and Home Assistant keeps which pipeline each one runs in a
// select of its own on the device (assist_pipeline's select.py: unique ids "<mac>-pipeline" and
// "<mac>-pipeline_2"). The action's pipeline_id is matched against those two, so a card that asks for a
// pipeline gets the assistant that runs it. Without a pipeline_id, with "preferred" or "last_used", or
// when no assistant runs the one asked for, it is the first assistant, as a press of the action button
// is.

// assistWait is how long Home Assistant is given to say which assistant runs the pipeline. A tap is
// somebody waiting to talk: past this, the first assistant answers rather than nobody.
const assistWait = 3 * time.Second

// assistFacts is what the choice is made from: the pipelines, the preferred one's id, and what each of
// the device's two assistant selects says (a pipeline's name, or "preferred").
type assistFacts struct {
	pipelines []hass.Pipeline
	preferred string
	selects   [2]string
}

// Stand-ins for tests.
var (
	lookupAssist = askAssist
	startVoice   = func(slot int) { voice.Get().Start(slot) }
)

func startAssist(pipeline string) {
	slot := 0
	if wantsPipeline(pipeline) {
		ctx, cancel := context.WithTimeout(context.Background(), assistWait)
		facts, err := lookupAssist(ctx)
		cancel()
		if err != nil {
			slog.Warn("dashboard assist: asking Home Assistant which assistant runs the pipeline", "pipeline", pipeline, "err", err)
		} else {
			slot = assistSlot(pipeline, facts)
		}
	}
	slog.Info("dashboard assist", "pipeline", pipeline, "assistant", slot+1)
	startVoice(slot)
}

// wantsPipeline is whether the action names a pipeline of its own rather than leaving it to the device.
func wantsPipeline(pipeline string) bool {
	return pipeline != "" && pipeline != "preferred" && pipeline != "last_used"
}

// assistSlot is which assistant (0 or 1) runs the pipeline with this id, 0 when neither does.
func assistSlot(pipeline string, f assistFacts) int {
	name := ""
	for _, p := range f.pipelines {
		if p.ID == pipeline {
			name = p.Name
		}
	}
	if name == "" {
		return 0
	}
	for slot, picked := range f.selects {
		if picked == name || (picked == "preferred" && pipeline == f.preferred) {
			return slot
		}
	}
	return 0
}

func askAssist(ctx context.Context) (assistFacts, error) {
	var f assistFacts
	mac, err := layout.FactoryMAC()
	if err != nil {
		return f, err
	}
	c := hass.Get()
	if f.pipelines, f.preferred, err = c.Pipelines(ctx); err != nil {
		return f, err
	}
	devices, entities, err := c.Registries(ctx)
	if err != nil {
		return f, err
	}
	for slot, id := range ownSelects(devices, entities, mac) {
		if id == "" {
			continue
		}
		if st, err := c.State(id); err == nil {
			f.selects[slot] = st.State
		}
	}
	return f, nil
}

// ownSelects is the entity ids of the device's two assistant selects, found by the address the device
// carries in Home Assistant's device registry (a rename changes neither) and the select's unique id.
func ownSelects(devices []hass.Device, entities []hass.RegistryEntity, mac string) [2]string {
	var out [2]string
	ours := map[string]bool{}
	for _, d := range devices {
		for _, c := range d.Connections {
			if len(c) == 2 && c[0] == "mac" && strings.EqualFold(c[1], mac) {
				ours[d.ID] = true
			}
		}
	}
	for _, e := range entities {
		if !ours[e.DeviceID] || e.Platform != "esphome" || !strings.HasPrefix(e.ID, "select.") {
			continue
		}
		switch {
		case strings.HasSuffix(e.UniqueID, "-pipeline"):
			out[0] = e.ID
		case strings.HasSuffix(e.UniqueID, "-pipeline_2"):
			out[1] = e.ID
		}
	}
	return out
}
