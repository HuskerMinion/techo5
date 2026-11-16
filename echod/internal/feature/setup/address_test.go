//go:build !dot

package setup

import (
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/lib/wifi"
)

// The address, gateway and DNS are for a fixed address: left out of the page while Automatic is
// chosen, and what they hold is not read for Automatic when the form arrives with them all the same.
func TestFixedAddressFieldsAreLeftOutForAutomatic(t *testing.T) {
	s := drawn(func(w *httptest.ResponseRecorder) { addressForm(w, "tok", wifi.Address{}, "") })
	needs(t, fixedRule, s, `name="mode" value="dhcp" checked`, `name="mode" value="fixed"`, `<div class="fixedonly">`)
	hides := s[strings.Index(s, `<div class="fixedonly">`):]
	hides = hides[:strings.Index(hides, `</div>`)]
	for _, in := range []string{`name="address"`, `name="gateway"`, `name="dns"`} {
		if !strings.Contains(hides, in) {
			t.Errorf("%s is not among what is left out", in)
		}
	}
	if strings.Contains(hides, `type="submit"`) || strings.Contains(hides, `name="mode"`) {
		t.Error("the choice itself or Save is left out with the fields")
	}

	// A browser sends the fields whether they are shown or not. Automatic with nonsense left in them is
	// still Automatic, not a refusal about an address nobody can see.
	r := form(url.Values{"mode": {"dhcp"}, "address": {"not an address"}, "gateway": {"nor this"}, "dns": {"x"}})
	if problem, _ := saveAddress(httptest.NewRecorder(), r); strings.Contains(problem, "address") && !strings.Contains(problem, "cannot set") {
		t.Errorf("Automatic was refused for what was left in the fixed fields: %s", problem)
	}
}
