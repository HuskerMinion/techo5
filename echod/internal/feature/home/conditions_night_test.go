package home

import "testing"

func TestAtNight(t *testing.T) {
	for _, c := range []struct {
		cond  string
		night bool
		want  string
	}{
		{"sunny", false, "sunny"},
		{"sunny", true, "clear-night"},
		{"partlycloudy", false, "partlycloudy"},
		{"partlycloudy", true, PartlyCloudyNight},
		{"rainy", true, "rainy"},
		{"clear-night", true, "clear-night"},
		{"", true, ""},
	} {
		if got := atNight(c.cond, c.night); got != c.want {
			t.Errorf("atNight(%q, %v) = %q, want %q", c.cond, c.night, got, c.want)
		}
	}
	if got := ConditionWords(PartlyCloudyNight); got != "Partly cloudy" {
		t.Errorf("ConditionWords(PartlyCloudyNight) = %q", got)
	}
}
