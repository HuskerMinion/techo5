package home

import (
	"log/slog"
	"math"
	"regexp"
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

// glanceMax is the most entities the strip follows. Four or five chips fill a Show 5's width, and each
// entity is four values followed, so a longer list only costs.
const glanceMax = 8

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
	if v, err := strconv.ParseFloat(state, 64); err == nil && v == 0 {
		return Chip{}, false // 0.0 W is as quiet as 0
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
		value := rounded(state)
		if unit = strings.TrimSpace(unit); unit != "" {
			if unit != "%" && unit != "°C" && unit != "°F" {
				value += " "
			}
			value += unit
		}
		text = nameAndValue(name, value)
	default:
		// A word or a sentence: the state is the message, the way a template sensor made for a chip
		// is written. A bare state name reads as words: not_home as "Not home".
		text = spoken(state)
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

// stateName is a state as Home Assistant names it rather than as a person writes it: lowercase, no
// spaces, words joined by underscores ("not_home", "cleaning").
var stateName = regexp.MustCompile(`^[a-z][a-z0-9]*(_[a-z0-9]+)*$`)

// spoken is a state written for a person: a state name with its underscores as spaces and a capital
// first letter; anything else, a template sensor's message say, as it is.
func spoken(state string) string {
	if !stateName.MatchString(state) {
		return state
	}
	return strings.ToUpper(state[:1]) + strings.ReplaceAll(state[1:], "_", " ")
}

// rounded is a number with at most one decimal, which is all a chip has room for: "626.611149449502"
// is "626.6" and "21.50" is "21.5". A number with one decimal or none is left exactly as it came, so a
// long one keeps every digit.
func rounded(s string) string {
	_, decimals, ok := strings.Cut(s, ".")
	if !ok || len(decimals) <= 1 {
		return s
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return s
	}
	return strconv.FormatFloat(math.Round(v*10)/10, 'f', -1, 64)
}

// nameAndValue is a number's chip line: the name, then the value. When the line is too long it is the
// name that gets shortened, since the value is the news.
func nameAndValue(name, value string) string {
	line := name + " " + value
	if utf8.RuneCountInString(line) <= chipMax {
		return line
	}
	keep := chipMax - utf8.RuneCountInString(value) - 2 // a space and the ellipsis
	if keep < 1 {
		return value
	}
	return strings.TrimSpace(string([]rune(name)[:keep])) + "… " + value
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

// glanceList is the entities in a comma-separated list: trimmed, each once, in order, and no more than
// glanceMax of them; dropped is how many past the limit were left out.
func glanceList(s string) (list []string, dropped int) {
	for _, e := range strings.Split(s, ",") {
		if e = strings.TrimSpace(e); !strings.Contains(e, ".") || slices.Contains(list, e) {
			continue
		}
		if len(list) == glanceMax {
			dropped++
			continue
		}
		list = append(list, e)
	}
	return list, dropped
}

// glanceAction sets the entities, comma separated; an empty list takes the strip away.
func (f *Feature) glanceAction() *esphome.Action {
	return &esphome.Action{
		Name: "home_glance",
		Args: []esphome.Arg{{Name: "entities", Type: esphome.ArgString}},
		Run: func(c esphome.Call) (any, error) {
			list, dropped := glanceList(c.String("entities"))
			if err := config.Set().Home().Glance(list); err != nil {
				return nil, err
			}
			slog.Info("home: glance strip", "entities", len(list), "past the limit", dropped)
			f.rewire()
			return nil, nil
		},
	}
}
