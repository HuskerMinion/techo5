package layout

import "testing"

// Home Assistant builds an entity id from the friendly name with underscores, so this has to match
// what it does or the device names an entity nobody has. The names are made up, and shaped like the
// ones that catch it out: an apostrophe, a space, and whitespace at both ends.
func TestEntitySlugMatchesHomeAssistant(t *testing.T) {
	for name, want := range map[string]string{
		"Guest's Desk":   "guest_s_desk",
		"Kid's Room":     "kid_s_room",
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
	if got := Slug("Guest's Desk"); got != "guest-s-desk" {
		t.Errorf("Slug(%q) = %q, want the dashed node name", "Guest's Desk", got)
	}
}
