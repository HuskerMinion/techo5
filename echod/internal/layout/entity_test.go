package layout

import "testing"

// Home Assistant builds an entity id from the friendly name with underscores, so this has to match
// what it does or the device names an entity nobody has. The names here are real ones.
func TestEntitySlugMatchesHomeAssistant(t *testing.T) {
	for name, want := range map[string]string{
		"Terry's Desk":   "terry_s_desk",
		"Deanna's Desk":  "deanna_s_desk",
		"Laundry Room":   "laundry_room",
		"Kitchen":        "kitchen",
		"Echo Show":      "echo_show",
		"  Spaced  Out ": "spaced_out",
	} {
		if got := EntitySlug(name); got != want {
			t.Errorf("EntitySlug(%q) = %q, want %q", name, got, want)
		}
	}
}

// The node name keeps its dashes: it is an mDNS host, not an entity id.
func TestSlugIsStillTheNodeName(t *testing.T) {
	if got := Slug("Terry's Desk"); got != "terry-s-desk" {
		t.Errorf("Slug(%q) = %q, want the dashed node name", "Terry's Desk", got)
	}
}
