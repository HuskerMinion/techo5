package setup

import (
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
)

func TestSpeakerGainFormValidation(t *testing.T) {
	for _, tc := range []struct {
		value, was              string
		available, changed, bad bool
	}{
		{"5", "0", true, true, false}, {"0", "5", true, true, false},
		{"6", "0", true, true, false}, {"5", "5", true, false, false},
		{"7", "0", true, false, true}, {"10", "0", true, false, true},
		{"11", "0", true, false, true}, {"-1", "0", true, false, true},
		{"1.5", "0", true, false, true}, {"NaN", "0", true, false, true},
		{"5", "0", false, false, true},
	} {
		form := url.Values{"gain": {tc.value}, "was_gain": {tc.was}}
		r := httptest.NewRequest("POST", "/setup/save", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		_ = r.ParseForm()
		_, changed, problem := gainLevel(r, tc.available)
		if changed != tc.changed || (problem != "") != tc.bad {
			t.Errorf("gain=%q was=%q supported=%t: changed=%t problem=%q", tc.value, tc.was, tc.available, changed, problem)
		}
	}
}

// Speaker and voice changes what was changed on the page and leaves the rest as it now stands.
func TestSpeakerAndVoiceSavesOnlyWhatChanged(t *testing.T) {
	f, c := in(t)
	quiet := media.Get().QuietChoices()
	if len(quiet) < 2 {
		t.Fatalf("quiet hours offer %v", quiet)
	}
	vol := strconv.Itoa(media.Get().Volume())
	to := post(t, f, c, url.Values{"what": {"speaker"}, "tab": {"sound"},
		"volume": {vol}, "was_volume": {vol},
		"quiet": {quiet[1]}, "was_quiet": {quiet[0]},
		"dnd": {"yes"}, "was_dnd": {"false"}})
	if to.Query().Get("problem") != "" {
		t.Fatalf("saving said %q", to.Query().Get("problem"))
	}
	if got := media.Get().Quiet(); got != quiet[1] {
		t.Errorf("quiet hours are %q, want %q", got, quiet[1])
	}
	if !config.Get().Home.DoNotDisturb {
		t.Error("do not disturb did not turn on")
	}

	// The same page saved again, with do not disturb now off elsewhere: what it showed is not put back.
	if err := config.Set().Home().DoNotDisturb(false); err != nil {
		t.Fatal(err)
	}
	post(t, f, c, url.Values{"what": {"speaker"}, "tab": {"sound"}, "quiet": {quiet[1]}, "was_quiet": {quiet[1]},
		"dnd": {"yes"}, "was_dnd": {"true"}})
	if config.Get().Home.DoNotDisturb {
		t.Error("a save that did not change do not disturb turned it back on")
	}

	if to := post(t, f, c, url.Values{"what": {"speaker"}, "tab": {"sound"}, "quiet": {"Sometimes"}, "was_quiet": {quiet[0]}}); to.Query().Get("problem") == "" {
		t.Error("quiet hours took a choice that is not offered")
	}
}
