package wallclock

import (
	"context"
	"encoding/hex"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/lib/agt"
)

// inboundDiscoverResponse is a real clock reply (agt/testdata/golden-frames.txt).
const inboundDiscoverResponse = "f0 02 00 00 0a 2e 0a 24 0a 0f 41 6c 65 78 61 2e 44 69 73 63 6f 76 65 72 79 12 11 44 69 73 63 6f 76 65 72 2e 52 65 73 70 6f 6e 73 65 12 06 0a 04 74 65 73 74 0f 17 f1"

// socketPair returns the two ends of a stream socket: one plays the clock.
func socketPair(t *testing.T) (clockSide, linkSide *os.File) {
	t.Helper()
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Fatalf("socketpair: %v", err)
	}
	clockSide = os.NewFile(uintptr(fds[0]), "clock")
	linkSide = os.NewFile(uintptr(fds[1]), "link")
	t.Cleanup(func() {
		clockSide.Close()
		linkSide.Close()
	})
	return clockSide, linkSide
}

// collect reads until want frames have been decoded.
func collect(t *testing.T, f *os.File, want int) []agt.Frame {
	t.Helper()
	var df agt.Deframer
	var frames []agt.Frame
	buf := make([]byte, 4096)
	for len(frames) < want {
		n, err := f.Read(buf)
		if n > 0 {
			frames = append(frames, df.Feed(buf[:n])...)
		}
		if err != nil {
			t.Errorf("read: %v", err)
			return frames
		}
	}
	return frames
}

func TestHandshakeAndSendOverLink(t *testing.T) {
	clockFile, linkFile := socketPair(t)
	link := newConn(linkFile)
	defer link.close()

	handshakeSaw := make(chan agt.Message, 1)
	go func() {
		frames := collect(t, clockFile, 1)
		if len(frames) == 0 {
			return
		}
		if !frames[0].ChecksumOK {
			t.Error("discover arrived with a bad checksum")
		}
		handshakeSaw <- agt.DecodeMessage(frames[0].Payload)
		reply, err := hex.DecodeString(strings.ReplaceAll(inboundDiscoverResponse, " ", ""))
		if err != nil {
			t.Error(err)
			return
		}
		_, _ = clockFile.Write(reply)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := link.handshake(ctx, 3*time.Second); err != nil {
		t.Fatalf("handshake: %v", err)
	}

	select {
	case msg := <-handshakeSaw:
		if msg.Namespace != "Alexa.Discovery" || msg.Name != "Discover" {
			t.Fatalf("clock would see %+v", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("clock never saw Discover")
	}

	// The bridge's own traffic must arrive decoded and in order.
	got := make(chan []agt.Frame, 1)
	go func() {
		got <- collect(t, clockFile, 2)
	}()

	if err := link.Send(agt.StateUpdate(agt.State{Name: "timeinfo", Value: "2026-11-02T21:00:00+00:00"})); err != nil {
		t.Fatal(err)
	}
	if err := link.Send(agt.ClearIndicator()); err != nil {
		t.Fatal(err)
	}

	select {
	case frames := <-got:
		if len(frames) != 2 {
			t.Fatalf("got %d frames, want 2", len(frames))
		}
		first, second := agt.DecodeMessage(frames[0].Payload), agt.DecodeMessage(frames[1].Payload)
		if first.Name != "StateUpdate" || second.Name != "ClearIndicator" {
			t.Fatalf("unexpected messages %q then %q", first.Name, second.Name)
		}
		if frames[0].Seq == frames[1].Seq {
			t.Fatalf("sequence id did not advance: %d then %d", frames[0].Seq, frames[1].Seq)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("clock never saw the directives")
	}
}

func TestHandshakeTimesOutWithoutReply(t *testing.T) {
	_, linkFile := socketPair(t)
	link := newConn(linkFile)
	defer link.close()

	if err := link.handshake(context.Background(), 100*time.Millisecond); err == nil {
		t.Fatal("expected a timeout")
	}
}

func TestSendAfterCloseFails(t *testing.T) {
	_, linkFile := socketPair(t)
	link := newConn(linkFile)
	link.close()

	if err := link.Send(agt.Discover()); err == nil {
		t.Fatal("expected an error on a closed link")
	}
}

func TestLinkLostWhenPeerCloses(t *testing.T) {
	clockFile, linkFile := socketPair(t)
	link := newConn(linkFile)
	defer link.close()

	clockFile.Close()
	select {
	case <-link.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("link did not report the closed peer")
	}
	if link.Err() == nil {
		t.Fatal("Err() should describe the failure")
	}
}
