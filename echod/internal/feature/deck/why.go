//go:build !dot && !spot

package deck

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/lib/deckwire"
	"github.com/HuskerMinion/techo5/echod/internal/lib/obsws"
)

// Failure is the kind of thing that went wrong with a press, for the screen to say under the button.
// The error's own words stay in the log: they can carry an address, and are written for whoever reads
// a log, not for somebody standing at the deck.
type Failure int

const (
	// FailedSomehow is anything not told apart below: what was asked for was refused, or could not
	// be made sense of.
	FailedSomehow Failure = iota
	// FailedOBSAway: OBS is not connected.
	FailedOBSAway
	// FailedOBSRefused: OBS answered that it would not, or could not.
	FailedOBSRefused
	// FailedOBSSilent: OBS was sent the request and gave no answer in time.
	FailedOBSSilent
	// FailedComputerSilent: the button's computer could not be reached, or did not answer.
	FailedComputerSilent
	// FailedComputerUnpaired: the button names a computer that is not paired.
	FailedComputerUnpaired
	// FailedComputerKey: the computer answered, and its pairing key is not the one kept here.
	FailedComputerKey
)

// unpairedError is a press on a button whose computer is not paired (pcDo).
type unpairedError struct{ name string }

func (e unpairedError) Error() string {
	return fmt.Sprintf("deck: no computer called %q is paired", e.name)
}

// unreached is whether err is the network's: nothing there to take the request, or nothing said back
// in time.
func unreached(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)
}

// Why is the kind of failure err was for a press of b.
func Why(b config.DeckButton, err error) Failure {
	if b.Action.OnComputer() {
		var unpaired unpairedError
		switch {
		case errors.As(err, &unpaired):
			return FailedComputerUnpaired
		case errors.Is(err, deckwire.ErrKey):
			return FailedComputerKey
		case unreached(err):
			return FailedComputerSilent
		}
		return FailedSomehow
	}
	switch {
	case errors.Is(err, obsws.ErrNotConnected):
		return FailedOBSAway
	case errors.Is(err, context.DeadlineExceeded), strings.HasSuffix(err.Error(), ": no answer"), unreached(err):
		return FailedOBSSilent
	case errors.Is(err, ErrEmpty):
		return FailedSomehow
	}
	return FailedOBSRefused
}

// Away is the first computer on page p's buttons that is paired and did not answer when it was last
// asked, by name; empty when they all did, or none has been asked yet.
func Away(cfg config.Deck, p int, pcs map[string]ComputerState) string {
	cols, rows := cfg.Grid()
	for i := range cols * rows {
		b := cfg.Button(p, i)
		if !b.Action.OnComputer() {
			continue
		}
		if s, asked := pcs[b.Computer]; asked && s.Hello == nil {
			return b.Computer
		}
	}
	return ""
}
