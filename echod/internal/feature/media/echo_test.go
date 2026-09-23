package media

import (
	"testing"
	"time"
)

// A play or pause from Home Assistant for a remote's stream is passed on only when it changes what
// the remote is doing, and not again straight after: Music Assistant pauses its Home Assistant player
// for this device when the Sendspin one pauses, so a pause passed on came straight back.
func TestHomeAssistantDoesNotEchoToTheRemote(t *testing.T) {
	p := &Player{stream: &Stream{}}
	p.remoteLast.Store(true)
	var sent []Transport
	p.OnTransport.Listen(func(t Transport) { sent = append(sent, t) })

	p.remoteState.Store("paused")
	p.fromHA(TransportPause)
	if len(sent) != 0 {
		t.Fatalf("a pause for a remote already paused was passed on: %v", sent)
	}

	p.fromHA(TransportPlay)
	if len(sent) != 1 || sent[0] != TransportPlay {
		t.Fatalf("a play for a paused remote was not passed on: %v", sent)
	}

	// The echo: the remote says playing, and a pause arrives straight back.
	p.remoteState.Store("playing")
	p.fromHA(TransportPause)
	if len(sent) != 1 {
		t.Fatalf("a pause straight after a play was passed on: %v", sent)
	}

	// Later, a pause is somebody asking.
	p.haFwdAt.Store(time.Now().Add(-2 * haEcho).UnixNano())
	p.fromHA(TransportPause)
	if len(sent) != 2 || sent[1] != TransportPause {
		t.Fatalf("a later pause was not passed on: %v", sent)
	}

	// The screen is never taken for an echo.
	p.Transport(TransportPlay)
	if len(sent) != 3 {
		t.Fatalf("the screen's play was held back: %v", sent)
	}
}
