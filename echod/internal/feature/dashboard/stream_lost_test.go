//go:build !dot

package dashboard

import (
	"bytes"
	"image"
	"image/jpeg"
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// pictureMessage is a kindPicture of a w by h gray picture at the top left.
func pictureMessage(t *testing.T, w, h int) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := jpeg.Encode(&b, image.NewGray(image.Rect(0, 0, w, h)), nil); err != nil {
		t.Fatal(err)
	}
	return append([]byte{kindPicture, 0, 0, 0, 0}, b.Bytes()...)
}

// streamTo is a stream of 480 by 320 from a live test server, and a wait for its view to be as asked.
func streamTo(t *testing.T) (f *Feature, send chan<- []byte, until func(what string, ok func(View) bool) View) {
	t.Helper()
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	const key = "a-long-enough-test-key"
	addr, send, _ := liveDashcast(t, key)
	if err := config.Set().Dashboard().Server(addr, key); err != nil {
		t.Fatal(err)
	}
	f = &Feature{}
	f.Stream(480, 320)
	t.Cleanup(f.Close)
	return f, send, func(what string, ok func(View) bool) View {
		t.Helper()
		for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
			if v := f.Stream(480, 320); ok(v) {
				return v
			}
		}
		t.Fatalf("%s: the view is %+v", what, f.Stream(480, 320))
		return View{}
	}
}

// A server that says it is there and then says nothing at all is a connection lost: the picture stays
// up, and the view says since when it has had nothing behind it.
func TestAStalledStreamIsLost(t *testing.T) {
	was := aliveWithin
	aliveWithin = 150 * time.Millisecond
	t.Cleanup(func() { aliveWithin = was })
	_, send, until := streamTo(t)

	send <- pictureMessage(t, 480, 320)
	if v := until("the first picture", func(v View) bool { return v.Ready }); !v.Lost.IsZero() {
		t.Fatalf("a picture just arrived and the view says it was lost at %v", v.Lost)
	}
	send <- []byte{kindAlive}
	began := time.Now()
	v := until("the stall", func(v View) bool { return !v.Lost.IsZero() })
	if !v.Ready {
		t.Error("the last picture went with the connection")
	}
	if waited := time.Since(began); waited < aliveWithin {
		t.Errorf("lost after %v, sooner than the %v a server may be silent", waited, aliveWithin)
	}
}

// A server from before it said it was there is waited on as it always was: a page that does not change
// sends nothing, and that is not a connection lost.
func TestASilentOldServerIsNotLost(t *testing.T) {
	was := aliveWithin
	aliveWithin = 50 * time.Millisecond
	t.Cleanup(func() { aliveWithin = was })
	f, send, until := streamTo(t)

	send <- pictureMessage(t, 480, 320)
	until("the first picture", func(v View) bool { return v.Ready })
	time.Sleep(6 * aliveWithin)
	if v := f.Stream(480, 320); !v.Lost.IsZero() || v.Problem != "" {
		t.Errorf("a still page from a server that never said it was there: %+v", v)
	}
}

// What keeps arriving keeps the connection: pictures count as much as the server saying it is there.
func TestAStreamThatKeepsTalkingIsNotLost(t *testing.T) {
	was := aliveWithin
	aliveWithin = 200 * time.Millisecond
	t.Cleanup(func() { aliveWithin = was })
	f, send, until := streamTo(t)

	send <- pictureMessage(t, 480, 320)
	send <- []byte{kindAlive}
	until("the first picture", func(v View) bool { return v.Ready })
	for i := 0; i < 8; i++ {
		time.Sleep(aliveWithin / 4)
		if i%2 == 0 {
			send <- []byte{kindAlive}
		} else {
			send <- pictureMessage(t, 16, 16)
		}
	}
	if v := f.Stream(480, 320); !v.Lost.IsZero() {
		t.Errorf("a server heard from every %v was lost at %v", aliveWithin/4, v.Lost)
	}
}

// The note is made once, when the connection goes with a picture up, and the next picture takes it
// away; with no picture up there is nothing to call stale.
func TestLostIsUntilTheNextPicture(t *testing.T) {
	s := &stream{f: &Feature{}, w: 32, h: 32}
	s.lost()
	if !s.view.Lost.IsZero() {
		t.Fatal("lost with no picture ever drawn")
	}
	s.paint(image.Point{}, image.NewGray(image.Rect(0, 0, 32, 32)))
	s.lost()
	first := s.view.Lost
	if first.IsZero() {
		t.Fatal("the connection went with a picture up and the view does not say so")
	}
	time.Sleep(2 * time.Millisecond)
	s.lost()
	if !s.view.Lost.Equal(first) {
		t.Errorf("a second failure moved when it was lost from %v to %v", first, s.view.Lost)
	}
	s.paintDoubled(image.Point{}, image.NewGray(image.Rect(0, 0, 16, 16)))
	if !s.view.Lost.IsZero() {
		t.Error("a new picture arrived and the view still says lost")
	}
}
