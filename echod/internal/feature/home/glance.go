package home

import (
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/hastate"
)

// The glance strip is a row of chips along the foot of the clock page, one for each Home Assistant
// entity it was given (home_glance) that has something to say right now: a switch or a sensor that
// is on, a door that is open, a message a template sensor holds. An entity that is off, idle, empty
// or unavailable shows nothing, so the strip is only there when there is news, and Home Assistant,
// not the device, decides what counts: a template sensor makes any rule a chip.
//
// The entities are followed like the weather (hastate), so a chip changes as the entity does, and the
// list persists and survives a restart without Home Assistant sending anything again. A value that
// leaves the chips as they were redraws nothing (redraw).

// Chip is one entity's chip: an mdi icon name and a short line.
type Chip struct {
	Entity string
	Icon   string
	Text   string
}

// chipMax is the longest line a chip holds; a longer one ends in an ellipsis.
const chipMax = 28

// quiet are the states that mean an entity has nothing to say, compared lowercased.
var quiet = map[string]bool{
	"": true, "off": true, "unknown": true, "unavailable": true, "idle": true, "standby": true,
	"none": true, "false": true, "closed": true, "locked": true, "docked": true, "0": true,
}

// onOff are the domains whose states are switch-like: their chip is the entity's name, since "on"
// says nothing a chip's presence does not.
var onOff = map[string]bool{
	"binary_sensor": true, "input_boolean": true, "switch": true, "light": true, "fan": true,
	"lock": true, "cover": true, "siren": true, "automation": true, "script": true,
}

// defaultIcon is a domain's chip icon when the entity has none of its own.
var defaultIcon = map[string]string{
	"binary_sensor": "mdi:checkbox-blank-circle", "input_boolean": "mdi:toggle-switch",
	"switch": "mdi:toggle-switch", "light": "mdi:lightbulb", "lock": "mdi:lock-open-variant",
	"cover": "mdi:window-shutter-open", "timer": "mdi:timer-outline", "calendar": "mdi:calendar",
	"person": "mdi:account", "media_player": "mdi:play-circle",
}

// chipFor is an entity's chip, or false when it has nothing to say.
func chipFor(entity, state, name, icon, unit string) (Chip, bool) {
	state = strings.TrimSpace(state)
	if quiet[strings.ToLower(state)] {
		return Chip{}, false
	}
	domain, _, _ := strings.Cut(entity, ".")
	if name = strings.TrimSpace(name); name == "" {
		name = entity
	}
	var text string
	switch {
	case onOff[domain]:
		text = name
	case isNumber(state):
		text = name + " " + state
		if unit = strings.TrimSpace(unit); unit != "" {
			if unit != "%" && unit != "°C" && unit != "°F" {
				text += " "
			}
			text += unit
		}
	default:
		// A word or a sentence: the state is the message, the way a template sensor made for a chip
		// is written.
		text = state
	}
	if utf8.RuneCountInString(text) > chipMax {
		text = string([]rune(text)[:chipMax-1]) + "…"
	}
	if icon = strings.TrimSpace(icon); icon == "" {
		icon = defaultIcon[domain]
		if icon == "" {
			icon = "mdi:information-outline"
		}
	}
	return Chip{Entity: entity, Icon: strings.TrimPrefix(icon, "mdi:"), Text: text}, true
}

func isNumber(s string) bool {
	_, err := strconv.ParseFloat(s, 64)
	return err == nil
}

// glanceKeys are what following the glance entities takes: each one's state and the attributes a
// chip is made from.
func glanceKeys(h config.Home) []hastate.Key {
	var keys []hastate.Key
	for _, e := range h.Glance {
		keys = append(keys, hastate.Key{Entity: e}, hastate.Key{Entity: e, Attribute: "friendly_name"},
			hastate.Key{Entity: e, Attribute: "icon"}, hastate.Key{Entity: e, Attribute: "unit_of_measurement"})
	}
	return keys
}

// Glance is the chips to show now, in the order the entities were given.
func (f *Feature) Glance() []Chip {
	t := hastate.Get()
	var chips []Chip
	for _, e := range config.Get().Home.Glance {
		name, _ := t.Value(e, "friendly_name")
		icon, _ := t.Value(e, "icon")
		unit, _ := t.Value(e, "unit_of_measurement")
		if c, ok := chipFor(e, t.State(e), name, icon, unit); ok {
			chips = append(chips, c)
		}
	}
	return chips
}

// stateChanged passes a Home Assistant value on to the screen as Changed, unless it only concerns the
// glance strip and leaves its chips as they were.
func (f *Feature) stateChanged(u hastate.Update) {
	if f.redraw(u.Entity, config.Get().Home.Glance, f.Glance) {
		f.Changed.Emit(struct{}{})
	}
}

// redraw is whether a value arriving for entity is worth a new frame. Most of what a glance entity
// sends changes no chip: a washer's power reading while it is off, a sensor going from unknown to
// unavailable, an attribute that is not shown. Each Changed redraws the whole screen, so those stop
// here. An entity also followed for something else (the weather, the radio) always counts. chips is
// the strip as it would be drawn now.
func (f *Feature) redraw(entity string, glance []string, chips func() []Chip) bool {
	if !slices.Contains(glance, entity) {
		return true
	}
	now := chips()
	f.mu.Lock()
	defer f.mu.Unlock()
	fresh := !slices.Equal(now, f.chips)
	if fresh {
		f.chips = now
	}
	return fresh || f.others[entity]
}

// glanceAction sets the entities, comma separated; an empty list takes the strip away.
func (f *Feature) glanceAction() *esphome.Action {
	return &esphome.Action{
		Name: "home_glance",
		Args: []esphome.Arg{{Name: "entities", Type: esphome.ArgString}},
		Run: func(c esphome.Call) (any, error) {
			var list []string
			for _, e := range strings.Split(c.String("entities"), ",") {
				if e = strings.TrimSpace(e); strings.Contains(e, ".") {
					list = append(list, e)
				}
			}
			if err := config.Set().Home().Glance(list); err != nil {
				return nil, err
			}
			slog.Info("home: glance strip", "entities", list)
			f.rewire()
			return nil, nil
		},
	}
}
