//go:build !dot

package dashboard

import "testing"

// The Dashboard tile layout setting keeps "" for a tile's icon beside its words, as every device has
// had it, and "below" for the name under the badge and state; a value it does not know is the usual one.
func TestLayoutLabels(t *testing.T) {
	for value, want := range map[string]string{"": "Side by side", "below": "Name below", "sideways": "Side by side"} {
		if got := layoutLabel(value); got != want {
			t.Errorf("layoutLabel(%q) = %q, want %q", value, got, want)
		}
	}
	choices := LayoutChoices()
	if len(choices) != 2 || choices[0] != "Side by side" || choices[1] != "Name below" {
		t.Errorf("LayoutChoices() = %q", choices)
	}
}
