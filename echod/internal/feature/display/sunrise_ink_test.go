//go:build !dot

package display

import "testing"

// The time can be read against the sky the whole way up: its ink never comes within 3:1 of the sky
// behind it. It used to be 1.4:1 near the middle of the sunrise.
func TestTheTimeStandsOffTheSunriseSkyAllTheWayUp(t *testing.T) {
	for i := 0; i <= 100; i++ {
		p := float64(i) / 100
		for _, f := range []float64{0.2, 0.3, 0.4, 0.5} { // where the time and the date sit
			if c := colorContrast(sunriseInk(p, f), sunriseSkyAt(p, f)); c < 3 {
				t.Errorf("at %d%% of the sunrise, %.0f%% down: the time is %.1f:1 against the sky", i, f*100, c)
			}
		}
	}
}
