package home

import "testing"

// The widest credit line the page can get, which display's TestRadarCreditFits measures: keep the two
// the same when the credits change.
const widestRadarCredit = "Radar RainViewer · Clouds NOAA · Map NASA · Places GeoNames"

func TestWidestRadarCredit(t *testing.T) {
	for _, src := range []radarSource{rainViewer, nws} {
		if got := radarCredit(src, -96); len(got) > len(widestRadarCredit) {
			t.Errorf("%q is wider than the one display measures, %q", got, widestRadarCredit)
		}
	}
	if got := radarCredit(rainViewer, -96); got != widestRadarCredit {
		t.Errorf("the widest credit is now %q; update widestRadarCredit and display's test", got)
	}
}
