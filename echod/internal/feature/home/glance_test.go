package home

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

func TestChipFor(t *testing.T) {
	for _, c := range []struct {
		entity, state, name, icon, unit string
		want                            Chip
		shown                           bool
	}{
		{"input_boolean.guest_mode", "on", "Guest mode", "mdi:account-multiple", "", Chip{"input_boolean.guest_mode", "account-multiple", "Guest mode"}, true},
		{"input_boolean.guest_mode", "off", "Guest mode", "", "", Chip{}, false},
		{"binary_sensor.front_door", "on", "Front door", "", "", Chip{"binary_sensor.front_door", "checkbox-blank-circle", "Front door"}, true},
		{"sensor.washer", "Done", "Washer", "mdi:washing-machine", "", Chip{"sensor.washer", "washing-machine", "Done"}, true},
		{"sensor.washer", "unavailable", "Washer", "", "", Chip{}, false},
		{"sensor.washer", "", "Washer", "", "", Chip{}, false},
		{"sensor.kitchen_temperature", "26.8", "Kitchen", "", "°C", Chip{"sensor.kitchen_temperature", "information-outline", "Kitchen 26.8°C"}, true},
		{"sensor.power", "340", "Power", "mdi:flash", "W", Chip{"sensor.power", "flash", "Power 340 W"}, true},
		{"sensor.note", "Reminder at 6 PM: water the plants and the garden", "", "", "", Chip{"sensor.note", "information-outline", "Reminder at 6 PM: water the…"}, true},
		{"lock.front", "locked", "Front lock", "", "", Chip{}, false},
		{"lock.front", "unlocked", "Front lock", "", "", Chip{"lock.front", "lock-open-variant", "Front lock"}, true},
		{"switch.fan", "on", "", "", "", Chip{"switch.fan", "toggle-switch", "switch.fan"}, true},
		{"sensor.washer_power", "0.0", "Washer", "", "W", Chip{}, false},
		{"sensor.washer_power", "-0", "Washer", "", "W", Chip{}, false},
		{"sensor.washer_power", "0.4", "Washer", "", "W", Chip{"sensor.washer_power", "information-outline", "Washer 0.4 W"}, true},
		{"sensor.downstairs_power", "626.611149449502", "Downstairs Power Minute Average", "mdi:flash", "W", Chip{"sensor.downstairs_power", "flash", "Downstairs Power Mi… 626.6 W"}, true},
		{"sensor.kitchen_temperature", "21.50", "Kitchen", "", "°C", Chip{"sensor.kitchen_temperature", "information-outline", "Kitchen 21.5°C"}, true},
		{"sensor.outside", "-3.26", "Outside", "", "°C", Chip{"sensor.outside", "information-outline", "Outside -3.3°C"}, true},
		{"sensor.long", "12345678901234567890123456.7", "Counter", "", "", Chip{"sensor.long", "information-outline", "12345678901234567890123456.7"}, true},
		{"person.guest", "not_home", "Guest", "", "", Chip{"person.guest", "account", "Not home"}, true},
		{"vacuum.downstairs", "cleaning", "Vacuum", "", "", Chip{"vacuum.downstairs", "information-outline", "Cleaning"}, true},
		{"sensor.note", "Washer done", "", "", "", Chip{"sensor.note", "information-outline", "Washer done"}, true},
		{"sensor.note", "iPhone low_battery", "", "", "", Chip{"sensor.note", "information-outline", "iPhone low_battery"}, true},
	} {
		got, ok := chipFor(c.entity, c.state, c.name, c.icon, c.unit)
		if ok != c.shown || got != c.want {
			t.Errorf("chipFor(%q, %q): %+v %v, want %+v %v", c.entity, c.state, got, ok, c.want, c.shown)
		}
	}
}

func TestRedrawOnlyWhenTheChipsChange(t *testing.T) {
	f := &Feature{others: map[string]bool{"weather.home": true}}
	glance := []string{"sensor.washer", "weather.home"}
	washer := Chip{Entity: "sensor.washer", Icon: "washing-machine", Text: "Washer done"}
	var shown []Chip
	chips := func() []Chip { return shown }

	steps := []struct {
		name   string
		entity string
		shown  []Chip
		want   bool
	}{
		{"an entity the strip does not show", "light.kitchen", nil, true},
		{"a glance value that leaves the strip empty", "sensor.washer", nil, false},
		{"the chip appears", "sensor.washer", []Chip{washer}, true},
		{"a new value, the same chip", "sensor.washer", []Chip{washer}, false},
		{"an entity followed for the weather too", "weather.home", []Chip{washer}, true},
		{"the chip goes", "sensor.washer", nil, true},
	}
	for _, s := range steps {
		shown = s.shown
		if got := f.redraw(s.entity, glance, chips); got != s.want {
			t.Errorf("%s: redraw = %v, want %v", s.name, got, s.want)
		}
	}
}

func TestGlanceList(t *testing.T) {
	list, dropped := glanceList(" sensor.a, input_boolean.b ,sensor.a,, nonsense, sensor.c")
	if want := []string{"sensor.a", "input_boolean.b", "sensor.c"}; !slices.Equal(list, want) || dropped != 0 {
		t.Errorf("trim and dedupe: %v %d, want %v 0", list, dropped, want)
	}
	var many []string
	for i := range glanceMax + 3 {
		many = append(many, fmt.Sprintf("sensor.s%d", i))
	}
	list, dropped = glanceList(strings.Join(many, ","))
	if len(list) != glanceMax || dropped != 3 || list[0] != "sensor.s0" {
		t.Errorf("a long list: %d kept, %d dropped, first %q; want %d, 3, sensor.s0", len(list), dropped, list[0], glanceMax)
	}
	if list, _ := glanceList(""); len(list) != 0 {
		t.Errorf("an empty list gives %v", list)
	}
}
