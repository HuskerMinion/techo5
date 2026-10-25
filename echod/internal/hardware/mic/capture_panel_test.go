//go:build panel

package mic

import (
	"net"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/lib/loopback"
)

// The speaker down holds the writer still while the microphone reads on: the reference is not lined
// up again on every read (it was, about eight times a second, each one logged).
func TestNoLineUpWhileTheSpeakerIsDown(t *testing.T) {
	c := &socketCapture{}
	loopback.Played.Write(make([]int16, 320))
	c.lineUp()
	at := c.offset
	for range 200 {
		c.read += 320
		c.lineUp()
	}
	if c.offset != at {
		t.Errorf("the reference moved from %d to %d with nothing played", at, c.offset)
	}

	// Once the writer moves again, the line-up follows it.
	loopback.Played.Write(make([]int16, 320))
	for range 50 {
		c.read += 320
		loopback.Played.Write(make([]int16, 320))
		c.lineUp()
	}
	if c.offset == at {
		t.Error("the reference never caught up with the writer once it moved again")
	}
}

// A stream that sends nothing for a while is waited out, not given up on: the read returns the
// samples once they come, and the stream is marked quiet in between only.
func TestQuietStreamIsWaitedOut(t *testing.T) {
	was := quietLimit
	quietLimit = 20 * time.Millisecond
	defer func() { quietLimit = was }()

	app, daemon := net.Pipe()
	defer app.Close()
	c := &socketCapture{conn: daemon}
	done := make(chan error, 1)
	in := make([]byte, 4)
	go func() { done <- c.readFull(in) }()

	time.Sleep(80 * time.Millisecond)
	if _, err := app.Write([]byte{1, 2, 3, 4}); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatalf("readFull: %v", err)
	}
	if string(in) != "\x01\x02\x03\x04" {
		t.Errorf("read %v", in)
	}
	if c.quiet {
		t.Error("still marked quiet after sound came")
	}

	// The app going away still ends the read.
	go func() { done <- c.readFull(in) }()
	_ = app.Close()
	if err := <-done; err == nil {
		t.Error("readFull returned nothing after the app closed the stream")
	}
}
