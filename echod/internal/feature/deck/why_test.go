//go:build !dot && !spot

package deck

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/lib/deckwire"
	"github.com/HuskerMinion/techo5/echod/internal/lib/obsws"
)

// A failed press is told to the screen by its kind, which is all of the error it is given.
func TestWhyAPressFailed(t *testing.T) {
	scene := config.DeckButton{Action: config.DeckOBSScene, Scene: "Live"}
	keys := config.DeckButton{Action: config.DeckPCKeys, Computer: "Studio", Value: "ctrl+s"}
	refused := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connect: connection refused")}
	for _, c := range []struct {
		what string
		b    config.DeckButton
		err  error
		want Failure
	}{
		{"OBS away", scene, obsws.ErrNotConnected, FailedOBSAway},
		{"OBS away, wrapped", scene, fmt.Errorf("scene: %w", obsws.ErrNotConnected), FailedOBSAway},
		{"OBS said no", scene, errors.New("obs: SetCurrentProgramScene failed (600): no scene of that name"), FailedOBSRefused},
		{"OBS gave no answer", scene, errors.New("obs: SetCurrentProgramScene: no answer"), FailedOBSSilent},
		{"OBS out of time", scene, context.DeadlineExceeded, FailedOBSSilent},
		{"a computer not paired", keys, unpairedError{"Studio"}, FailedComputerUnpaired},
		{"a computer with another key", keys, deckwire.ErrKey, FailedComputerKey},
		{"a computer not there", keys, refused, FailedComputerSilent},
		{"a computer that hung up", keys, io.ErrUnexpectedEOF, FailedComputerSilent},
		{"a computer that said no", keys, errors.New("no script of that name"), FailedSomehow},
	} {
		if got := Why(c.b, c.err); got != c.want {
			t.Errorf("%s: %d, want %d", c.what, got, c.want)
		}
	}
}

// The computer named at the foot of a page is one of that page's that was asked and did not answer:
// not one that answered, one never asked, or another page's.
func TestAComputerAway(t *testing.T) {
	cfg := config.Deck{Cols: 2, Rows: 1, Pages: []config.DeckPage{
		{Buttons: []config.DeckButton{{Action: config.DeckOBSStream}, {Action: config.DeckPCKeys, Computer: "Studio"}}},
		{Buttons: []config.DeckButton{{Action: config.DeckPCOpen, Computer: "Laptop"}}},
	}}
	here := &deckwire.Hello{}
	for _, c := range []struct {
		what string
		pcs  map[string]ComputerState
		page int
		want string
	}{
		{"never asked", nil, 0, ""},
		{"answered", map[string]ComputerState{"Studio": {Hello: here}}, 0, ""},
		{"did not answer", map[string]ComputerState{"Studio": {Err: "not answering"}}, 0, "Studio"},
		{"another page's did not", map[string]ComputerState{"Studio": {Hello: here}, "Laptop": {Err: "not answering"}}, 0, ""},
		{"this page's did not", map[string]ComputerState{"Studio": {Hello: here}, "Laptop": {Err: "not answering"}}, 1, "Laptop"},
	} {
		if got := Away(cfg, c.page, c.pcs); got != c.want {
			t.Errorf("%s: %q, want %q", c.what, got, c.want)
		}
	}
}
