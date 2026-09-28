package home

import "testing"

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
