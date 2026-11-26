//go:build !dot

package dashboard

import (
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// issueLook is Home Assistant's icons and words for the entities in #760's table, as it serves them.
func issueLook() *haLook {
	return &haLook{
		entities: byID([]hass.Registered{
			{ID: "binary_sensor.tank", Platform: "tuya_local", TranslationKey: "tank_full"},
			{ID: "sensor.download", Platform: "speedtestdotnet", TranslationKey: "download"},
			{ID: "sensor.uv_risk", Platform: "openuv", TranslationKey: "uv_risk"},
			{ID: "sensor.meter", Platform: "utility_meter", TranslationKey: "utility_meter"},
			{ID: "binary_sensor.charging", Platform: "esphome"},
		}),
		component: map[string]map[string]hass.IconRule{
			"vacuum":        {"_": {Default: "mdi:robot-vacuum"}},
			"binary_sensor": {"_": {Default: "mdi:radiobox-blank", State: map[string]string{"on": "mdi:checkbox-marked-circle"}}, "battery_charging": {Default: "mdi:battery", State: map[string]string{"on": "mdi:battery-charging"}}},
			"sensor":        {"_": {Default: "mdi:eye"}, "voltage": {Default: "mdi:sine-wave"}, "energy": {Default: "mdi:lightning-bolt"}},
			"light":         {"_": {Default: "mdi:lightbulb", State: map[string]string{"off": "mdi:lightbulb-off"}}},
		},
		entity: map[string]map[string]map[string]hass.IconRule{
			"tuya_local":      {"binary_sensor": {"tank_full": {Default: "mdi:water-outline", State: map[string]string{"on": "mdi:water-alert"}}}},
			"speedtestdotnet": {"sensor": {"download": {Default: "mdi:speedometer"}}},
			"utility_meter":   {"sensor": {"utility_meter": {Default: "mdi:counter"}}},
			// An icon the device's font does not have is passed over for the next one.
			"openuv": {"sensor": {"uv_risk": {Default: "mdi:no-such-icon-in-this-font"}}},
		},
		words: map[string]string{
			"component.binary_sensor.entity_component.battery_charging.state.off": "Not charging",
			"component.binary_sensor.entity_component.battery_charging.state.on":  "Charging",
			"component.binary_sensor.entity_component._.state.off":                "Off",
			"component.tuya_local.entity.binary_sensor.tank_full.state.off":       "OK",
			"component.vacuum.entity_component._.state.docked":                    "Docked",
			"component.openuv.entity.sensor.uv_risk.state.no_risk":                "No risk",
		},
	}
}

func entity(id, state string, attrs map[string]any) hass.LiveEntity {
	if attrs == nil {
		attrs = map[string]any{}
	}
	return hass.LiveEntity{ID: id, State: state, Attrs: attrs}
}

// The tiles of #760's table show what the browser shows when Home Assistant has served its icons and
// words: the integration's for the translation key, then the domain's for the class, then the domain's.
func TestTilesTakeHomeAssistantsIconsAndWords(t *testing.T) {
	ha := issueLook()
	for _, c := range []struct {
		e          hass.LiveEntity
		icon, word string
	}{
		{entity("vacuum.robot", "docked", nil), "mdi:robot-vacuum", "Docked"},
		{entity("binary_sensor.charging", "off", map[string]any{"device_class": "battery_charging"}), "mdi:battery", "Not charging"},
		{entity("binary_sensor.charging", "on", map[string]any{"device_class": "battery_charging"}), "mdi:battery-charging", "Charging"},
		{entity("binary_sensor.tank", "off", nil), "mdi:water-outline", "OK"},
		{entity("sensor.volts", "231.4", map[string]any{"device_class": "voltage", "unit_of_measurement": "V"}), "mdi:sine-wave", "231 V"},
		{entity("sensor.download", "512.3", map[string]any{"unit_of_measurement": "Mbit/s"}), "mdi:speedometer", "512 Mbit/s"},
		{entity("sensor.meter", "1204", map[string]any{"device_class": "energy", "unit_of_measurement": "kWh"}), "mdi:counter", "1204 kWh"},
		// An enum sensor's option in Home Assistant's words; its integration's icon is not in the font,
		// so the domain's own is drawn.
		{entity("sensor.uv_risk", "no_risk", map[string]any{"device_class": "enum"}), "mdi:eye", "No risk"},
		// The state's own icon still comes first.
		{entity("sensor.volts", "230", map[string]any{"device_class": "voltage", "icon": "mdi:flash"}), "mdi:flash", "230"},
	} {
		tile := describeIn(c.e, "x", nil, ha)
		if tile.Icon != c.icon || tile.Value != c.word {
			t.Errorf("%s %s: icon %q, value %q; want %q, %q", c.e.ID, c.e.State, tile.Icon, tile.Value, c.icon, c.word)
		}
	}
}

// Without anything from Home Assistant, an older one or a fetch that failed, the device's own tables
// stand as before.
func TestTilesFallBackOnTheDevicesOwn(t *testing.T) {
	for _, ha := range []*haLook{nil, {}} {
		door := describeIn(entity("binary_sensor.door", "on", map[string]any{"device_class": "door"}), "Door", nil, ha)
		if door.Icon != "door-open" || door.Value != "Open" {
			t.Errorf("door: %+v", door)
		}
		vac := describeIn(entity("vacuum.robot", "docked", nil), "Robot", nil, ha)
		if vac.Icon != "help-circle-outline" || vac.Value != "Docked" {
			t.Errorf("vacuum: %+v", vac)
		}
		uv := describeIn(entity("sensor.uv_risk", "no_risk", nil), "UV", nil, ha)
		if uv.Value != "no_risk" {
			t.Errorf("enum: %+v", uv)
		}
	}
}

// A light's words stay the device's own "On" and "Off", which a row's switch and a tap's pending state
// read; its icon is Home Assistant's.
func TestALightKeepsItsOwnWords(t *testing.T) {
	light := describeIn(entity("light.hall", "off", nil), "Hall", nil, issueLook())
	if light.Value != "Off" || light.Icon != "mdi:lightbulb-off" {
		t.Errorf("light: %+v", light)
	}
}

// A changed registry keeps the icons and words and takes the new entries.
func TestALookWithANewListing(t *testing.T) {
	ha := issueLook().withListing([]hass.Registered{{ID: "sensor.x", Platform: "p", TranslationKey: "k"}})
	if ha.entities["sensor.x"].TranslationKey != "k" || ha.words == nil || len(ha.entities) != 1 {
		t.Errorf("look %+v", ha)
	}
	var none *haLook
	if got := none.withListing(nil); got == nil {
		t.Error("a nil look with a listing is nil")
	}
	if haLanguage("") != "en" || haLanguage("ca-valencia") != "ca" || haLanguage("de") != "de" {
		t.Error("languages")
	}
}
