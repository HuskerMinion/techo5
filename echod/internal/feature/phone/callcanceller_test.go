package phone

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// engines records what callCanceller asks the microphones for, settling on whatever is asked.
func engines(t *testing.T) (*[]string, func(string) string) {
	t.Helper()
	var asked []string
	return &asked, func(name string) string {
		asked = append(asked, name)
		return name
	}
}

// inCalls starts a test with this canceller chosen for calls, and the default one for the rest.
func inCalls(t *testing.T, e config.CallCancelEngine) {
	t.Helper()
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if err := config.Set().Microphone().CallCancelEngine(e); err != nil {
		t.Fatal(err)
	}
}

// WebRTC's chosen for calls runs during one, and the one chosen for the rest comes back after it.
func TestACallRunsItsCancellerAndPutsTheOtherBack(t *testing.T) {
	inCalls(t, config.CallCancelEngine(config.CancelWebRTC))
	asked, set := engines(t)

	restore := callCanceller(set)
	if want := []string{"webrtc"}; !slices.Equal(*asked, want) {
		t.Fatalf("during the call: asked for %v, want %v", *asked, want)
	}
	restore()
	if want := []string{"webrtc", "builtin"}; !slices.Equal(*asked, want) {
		t.Fatalf("after the call: asked for %v, want %v", *asked, want)
	}
}

// The built-in filter can be the one for calls with WebRTC's at other times, too.
func TestTheBuiltInFilterCanBeTheOneForCalls(t *testing.T) {
	inCalls(t, config.CallCancelEngine(config.CancelBuiltin))
	if err := config.Set().Microphone().CancelEngine(config.CancelWebRTC); err != nil {
		t.Fatal(err)
	}
	asked, set := engines(t)

	callCanceller(set)()
	if want := []string{"builtin", "webrtc"}; !slices.Equal(*asked, want) {
		t.Fatalf("asked for %v, want %v", *asked, want)
	}
}

// Same as outside calls, which is the default, or the same one chosen twice, leaves the canceller
// alone.
func TestACallLeavesTheCancellerAloneWhenItIsTheSame(t *testing.T) {
	for _, e := range []config.CallCancelEngine{config.CallCancelSame, config.CallCancelEngine(config.CancelBuiltin)} {
		inCalls(t, e)
		asked, set := engines(t)

		callCanceller(set)()
		if len(*asked) != 0 {
			t.Fatalf("with %q: asked for %v, want nothing", e, *asked)
		}
	}
}

// Two calls at once (an intercom call beside a phone call) switch once, and back only after the last.
func TestTwoCallsSwitchBackAfterTheLast(t *testing.T) {
	inCalls(t, config.CallCancelEngine(config.CancelWebRTC))
	asked, set := engines(t)

	first := callCanceller(set)
	second := callCanceller(set)
	first()
	if want := []string{"webrtc"}; !slices.Equal(*asked, want) {
		t.Fatalf("with one call still up: asked for %v, want %v", *asked, want)
	}
	second()
	if want := []string{"webrtc", "builtin"}; !slices.Equal(*asked, want) {
		t.Fatalf("after both: asked for %v, want %v", *asked, want)
	}
}

// A canceller chosen for outside calls during the call is the one it goes back to.
func TestTheCancellerChosenDuringACallIsKept(t *testing.T) {
	inCalls(t, config.CallCancelEngine(config.CancelWebRTC))
	asked, set := engines(t)

	restore := callCanceller(set)
	if err := config.Set().Microphone().CancelEngine(config.CancelWebRTC); err != nil {
		t.Fatal(err)
	}
	restore()
	if want := []string{"webrtc", "webrtc"}; !slices.Equal(*asked, want) {
		t.Fatalf("asked for %v, want %v", *asked, want)
	}
}
