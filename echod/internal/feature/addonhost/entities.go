package addonhost

import (
	"context"
	"fmt"
	"strings"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/feature/addons"
	"github.com/HuskerMinion/techo5/echod/pkg/addonapi"
)

// What an add-on adds to Home Assistant: a button for each command that needs no arguments, and the
// entities its addon.json declares, at most addonapi.MaxEntities, every object id under its own
// prefix (addons.ObjectPrefix: addon_<id>_<name>), so none can take the name of one of the device's
// own. Home Assistant reads the entity list once per connection; an install, an update or a removal
// asks for a reconnect (feature/addons), which is how they come and go. Their states are not saved:
// an add-on sets them again after hello.

// addonEntities is one add-on's entities.
type addonEntities struct {
	version string // the manifest's they were made from
	list    []esphome.Entity
	byID    map[string]esphome.Entity // the declared ones, by their id in addon.json
}

// entitiesOf is an add-on's entities, as Home Assistant was last given them.
func (h *Host) entitiesOf(id string) *addonEntities {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.entities[id]
}

// set gives one of them a state, already checked against its declaration (addonapi.Entity.CheckState).
func (a *addonEntities) set(id string, state any) error {
	switch e := a.byID[id].(type) {
	case *esphome.Sensor:
		e.Set(float32(state.(float64)))
	case *esphome.TextSensor:
		e.Set(state.(string))
	case *esphome.BinarySensor:
		e.Set(state.(bool))
	case *esphome.Switch:
		e.Set(state.(bool))
	case *esphome.Select:
		e.Set(state.(string))
	case *esphome.Number:
		e.Set(float32(state.(float64)))
	default:
		return addonapi.Errorf(addonapi.CodeBadRequest, "the add-on has no entity %q with a state", id)
	}
	return nil
}

// Entities is every installed add-on's that speaks the API and was granted them.
func (h *Host) Entities() []esphome.Entity {
	if !addons.Supported() {
		return nil
	}
	names := h.names()
	grants := h.grants()
	h.mu.Lock()
	defer h.mu.Unlock()
	keep := map[string]bool{}
	var out []esphome.Entity
	for _, g := range grants {
		keep[g.ID] = true
		ae := h.entities[g.ID]
		if ae == nil || ae.version != g.Man.Version {
			name := names[g.ID]
			if name == "" {
				name = g.ID
			}
			ae = h.build(g, name)
			h.entities[g.ID] = ae
		}
		out = append(out, ae.list...)
	}
	for id := range h.entities {
		if !keep[id] {
			delete(h.entities, id)
		}
	}
	return out
}

// words is a name as an entity's: "start_cooking" as "start cooking".
func words(s string) string { return strings.ReplaceAll(s, "_", " ") }

// build makes an add-on's entities from its grant.
func (h *Host) build(g addons.APIGrant, name string) *addonEntities {
	id, prefix := g.ID, addons.ObjectPrefix(g.ID)
	caps := g.Capabilities()
	ae := &addonEntities{version: g.Man.Version, byID: map[string]esphome.Entity{}}
	if has(caps, addonapi.CapCommands) {
		for _, c := range g.Man.Commands {
			if !c.NeedsNothing() {
				continue
			}
			cmd := c.Name
			ae.list = append(ae.list, &esphome.Button{
				Base: esphome.Base{ObjectID: prefix + cmd, Name: name + " " + words(cmd), Icon: "mdi:puzzle"},
				// It runs on Home Assistant's connection: the command is sent and not waited for.
				OnPress: func() {
					if _, err := h.srv.Command(context.Background(), id, cmd, nil, "button", false); err != nil {
						logCommand(id, cmd, err)
					}
				},
			})
		}
	}
	if !has(caps, addonapi.CapEntities) {
		return ae
	}
	for _, e := range g.Man.Entities {
		objectID, label, icon := prefix+e.ID, name+" "+e.Name, e.Icon
		base := func() esphome.Base { return esphome.Base{ObjectID: objectID, Name: label, Icon: icon} }
		eid := e.ID
		// A command from Home Assistant is the add-on's to carry out: it is told, and sets the state
		// itself once it has, so the entity never says what did not happen.
		tell := func(state any) {
			h.srv.Emit(id, addonapi.EventEntityCommand, addonapi.EntityCommand{ID: eid, State: state})
		}
		var ent esphome.Entity
		switch e.Domain {
		case addonapi.DomainSensor:
			if e.Text {
				ent = &esphome.TextSensor{Base: base()}
			} else {
				ent = &esphome.Sensor{Base: base(), Unit: e.Unit}
			}
		case addonapi.DomainBinarySensor:
			ent = &esphome.BinarySensor{Base: base()}
		case addonapi.DomainSwitch:
			ent = &esphome.Switch{Base: base(), OnCommand: func(v bool) { tell(v) }}
		case addonapi.DomainSelect:
			ent = &esphome.Select{Base: base(), Options: e.Options, OnCommand: func(v string) { tell(v) }}
		case addonapi.DomainNumber:
			ent = &esphome.Number{Base: base(), Min: e.Min, Max: e.Max, Step: e.Step, Unit: e.Unit,
				OnCommand: func(v float32) { tell(float64(v)) }}
		case addonapi.DomainButton:
			ent = &esphome.Button{Base: base(), OnPress: func() {
				h.srv.Emit(id, addonapi.EventEntityCommand, addonapi.EntityCommand{ID: eid, Press: true})
			}}
		default:
			continue
		}
		ae.list = append(ae.list, ent)
		ae.byID[e.ID] = ent
	}
	return ae
}

// Actions is addon_command: Home Assistant calling an add-on's command with its arguments as JSON, and
// answered with what the add-on says back, which Home Assistant can speak on the device that heard the
// sentence. Home Assistant passes every argument on every call, so args is "" or "{}" for none.
func (h *Host) Actions() []*esphome.Action {
	if !addons.Supported() {
		return nil
	}
	return []*esphome.Action{{
		Name: "addon_command",
		Args: []esphome.Arg{
			{Name: "addon", Type: esphome.ArgString},
			{Name: "command", Type: esphome.ArgString},
			{Name: "args", Type: esphome.ArgString},
		},
		Answers: true,
		Run: func(c esphome.Call) (any, error) {
			id, cmd := strings.TrimSpace(c.String("addon")), strings.TrimSpace(c.String("command"))
			text, err := h.Command(id, cmd, strings.TrimSpace(c.String("args")))
			if err != nil {
				logCommand(id, cmd, err)
				return nil, fmt.Errorf("addon_command: %v", err)
			}
			return map[string]string{"text": text}, nil
		},
	}}
}
