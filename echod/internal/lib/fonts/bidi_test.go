package fonts

import "testing"

func TestHebrewVisualOrder(t *testing.T) {
	tests := []struct{ logical, visual string }{
		{"Hello, Master Bedroom!", "Hello, Master Bedroom!"},
		{"שלום עולם", "םלוע םולש"},
		{"האורות בMaster Bedroom כובו", "ובוכ Master Bedroomב תורואה"},
		{"טמפרטורה 23.5°", "23.5° הרוטרפמט"},
		{"23 מעלות", "תולעמ 23"},
		{"שלום (בית)", "(תיב) םולש"},
		{"שָׁלוֹם", "םוֹלשָׁ"},
	}
	for _, tt := range tests {
		if got := hebrewVisualOrder(tt.logical); got != tt.visual {
			t.Errorf("visual order of %q = %q, want %q", tt.logical, got, tt.visual)
		}
	}
}
