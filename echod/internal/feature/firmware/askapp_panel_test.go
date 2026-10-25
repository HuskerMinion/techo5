//go:build panel

package firmware

import (
	"context"
	"testing"
)

// Check only asks the app what its release server offers; Install is the one thing that has the app
// install, so a check on connect, on the timer or from the button never installs anything.
func TestAPanelChecksAndInstallsApart(t *testing.T) {
	var asked []string
	tell := tellApp
	tellApp = func(line string) { asked = append(asked, line) }
	t.Cleanup(func() { tellApp = tell })

	u := &Firmware{}
	u.Check(context.Background())
	u.Install(context.Background())
	if len(asked) != 2 || asked[0] != "update check" || asked[1] != "update install" {
		t.Errorf("Check then Install asked the app %q, want [update check, update install]", asked)
	}
}
