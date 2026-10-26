package remind

import (
	"strings"
	"testing"
)

// The satellite is asked for by the device's name, in quotes the name does not contain, since Home
// Assistant's templates take no escapes; a name no quotes can hold is not asked for.
func TestSatelliteTemplate(t *testing.T) {
	for name, want := range map[string]string{
		"Kitchen":    `'name', 'Kitchen')`,
		"Kids' Room": `'name', "Kids' Room")`,
	} {
		got, ok := satelliteTemplate(name)
		if !ok || !strings.Contains(got, want) || !strings.HasPrefix(got, "{{ states.assist_satellite") {
			t.Errorf("%q: template %s", name, got)
		}
	}
	for _, name := range []string{`Den's "Echo"`, `A\B`} {
		if _, ok := satelliteTemplate(name); ok {
			t.Errorf("%q was asked for", name)
		}
	}
}
