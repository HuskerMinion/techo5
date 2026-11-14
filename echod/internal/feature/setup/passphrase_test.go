package setup

import (
	"context"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/feature/firstrun"
)

// shownField is that a page has the passphrase as a password field with a Show box of its own, and the
// script that gives the box its use after it.
func shownField(t *testing.T, what, page string) {
	t.Helper()
	for _, want := range []string{`<label for="passphrase">Passphrase</label>`,
		`<input id="passphrase" name="passphrase" type="password" value="" autocomplete="new-password">`,
		`<label class="show" hidden><input type="checkbox" data-show="passphrase"> Show</label>`} {
		if !strings.Contains(page, want) {
			t.Errorf("%s lacks %q", what, want)
		}
	}
	if n := strings.Count(page, `name="passphrase"`); n != 1 {
		t.Errorf("%s posts the passphrase %d times", what, n)
	}
}

// The passphrase of a network being joined can be looked at before it is sent: a typo there is only
// found out after the join has been waited for.
func TestTheWifiPassphraseCanBeShown(t *testing.T) {
	w := httptest.NewRecorder()
	wifiJoinForm(context.Background(), w, "walnut-token", false)
	shownField(t, "the form that joins a network", w.Body.String())

	// The Connections tab's script is what brings the box out; every tab carries it (script_test.go).
	script := httptest.NewRecorder()
	pageScript(script)
	if !strings.Contains(script.Body.String(), "input[data-show]") || !strings.Contains(script.Body.String(), "'text':'password'") {
		t.Error("the page's script does nothing for a Show box")
	}
}

// First run's Wi-Fi step has the same field, and the script with it.
func TestFirstRunsPassphraseCanBeShown(t *testing.T) {
	if !slices.Contains(firstrun.Steps(), firstrun.Wifi) {
		t.Skip("first run has no Wi-Fi step on this device")
	}
	f, c, _ := firstRunIn(t)
	for i := 0; firstrun.Step() != firstrun.Wifi; i++ {
		if i > len(firstrun.Steps()) {
			t.Fatal("first run never reached its Wi-Fi step")
		}
		firstRunPost(t, f, c, url.Values{"what": {"next"}})
	}
	page := get(f, firstRunPath, c).Body.String()
	shownField(t, "first run's Wi-Fi step", page)
	field, script := strings.Index(page, `data-show="passphrase"`), strings.Index(page, "input[data-show]")
	if script < 0 || script < field {
		t.Error("first run's page has no script after the field to bring its Show box out")
	}
	if n := strings.Count(page, "<script>"); n != 1 {
		t.Errorf("first run's Wi-Fi step carries %d scripts", n)
	}
}
