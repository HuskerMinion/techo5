package camera

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"sync"
	"testing"
	"time"

	"github.com/ygelfand/go-esphome-device/api"
	"google.golang.org/protobuf/proto"
)

// fakeScreen stands in for the panel and the switch for one test.
func fakeScreen(t *testing.T, on bool, shot func(context.Context) (image.Image, error)) *bool {
	t.Helper()
	savedShot, savedOn := screenShot, screenOn
	t.Cleanup(func() { screenShot, screenOn = savedShot, savedOn })
	sw := on
	screenShot, screenOn = shot, func() bool { return sw }
	return &sw
}

// screen is a panel's worth of picture with enough in it to take several chunks.
func screen() image.Image {
	img := image.NewRGBA(image.Rect(0, 0, 400, 240))
	for y := 0; y < 240; y++ {
		for x := 0; x < 400; x++ {
			img.Set(x, y, color.RGBA{uint8(x * 7), uint8(y * 13), uint8(x ^ y), 255})
		}
	}
	return img
}

// The picture goes as the Screen entity's, in chunks, only the last marked done, and the chunks put
// together are a JPEG of the screen.
func TestTheScreenGoesAsItsOwnEntitysJPEG(t *testing.T) {
	fakeScreen(t, true, func(context.Context) (image.Image, error) { return screen(), nil })
	var got []*api.CameraImageResponse
	if err := sendScreen(func(m proto.Message) error {
		got = append(got, m.(*api.CameraImageResponse))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(got) < 2 {
		t.Fatalf("%d chunks; the test picture should take several", len(got))
	}
	var all bytes.Buffer
	for i, r := range got {
		if r.GetKey() != key(screenObjectID) || r.GetKey() == key(objectID) {
			t.Fatalf("chunk %d has key %d, not the Screen entity's", i, r.GetKey())
		}
		if r.GetDone() != (i == len(got)-1) {
			t.Errorf("chunk %d of %d: done = %t", i, len(got), r.GetDone())
		}
		all.Write(r.GetData())
	}
	img, err := jpeg.Decode(&all)
	if err != nil {
		t.Fatalf("not a JPEG: %v", err)
	}
	if b := img.Bounds(); b.Dx() != 400 || b.Dy() != 240 {
		t.Errorf("picture is %v", b)
	}
}

// With the switch off nobody's request takes a screenshot, single or stream.
func TestTheScreenSaysNothingWhileItsSwitchIsOff(t *testing.T) {
	fakeScreen(t, false, func(context.Context) (image.Image, error) {
		t.Error("a screenshot was taken with the Screen camera off")
		return screen(), nil
	})
	screenRequest(nil, &api.CameraImageRequest{Single: true})
	screenRequest(nil, &api.CameraImageRequest{Stream: true})
	screenAnswers.mu.Lock()
	defer screenAnswers.mu.Unlock()
	if len(screenAnswers.pending) != 0 {
		t.Error("an answer was started with the switch off")
	}
}

// A switch turned off while the screenshot was being taken sends nothing of it.
func TestTheScreenSendsNothingOnceSwitchedOffMidway(t *testing.T) {
	var on *bool
	on = fakeScreen(t, true, func(context.Context) (image.Image, error) {
		*on = false
		return screen(), nil
	})
	if err := sendScreen(func(proto.Message) error {
		t.Error("a picture was sent after the switch went off")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// The Screen entity is listed under a key of its own, apart from the camera's.
func TestTheScreenEntityIsListedWithTheScreen(t *testing.T) {
	l := screenListing()
	if l.GetObjectId() != "screen" || l.GetKey() != key("screen") || l.GetKey() == key(objectID) || l.GetName() != "Screen" {
		t.Errorf("listing = %v", l)
	}
}

// A stream's next request comes the moment the last picture is complete, while its goroutine is still
// finishing. It must get a picture of its own: dropped, the stream stood until something else asked.
func TestARequestWhileAPictureIsOnItsWayGetsAnother(t *testing.T) {
	savedEvery := screenEvery
	t.Cleanup(func() { screenEvery = savedEvery })
	screenEvery = 50 * time.Millisecond
	fakeScreen(t, true, func(context.Context) (image.Image, error) { return screen(), nil })

	var mu sync.Mutex
	pictures := 0
	asked := false
	done := make(chan struct{}, 4)
	who := new(int)
	send := func(m proto.Message) error {
		if !m.(*api.CameraImageResponse).GetDone() {
			return nil
		}
		mu.Lock()
		pictures++
		first := !asked
		asked = true
		mu.Unlock()
		if first {
			// Home Assistant asks for the next one as soon as this one is whole.
			screenRequestTo(who, nil, &api.CameraImageRequest{Stream: true})
		}
		done <- struct{}{}
		return nil
	}
	screenRequestTo(who, send, &api.CameraImageRequest{Stream: true})
	for i := 0; i < 2; i++ {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatalf("%d pictures; the request made while the first was sent got none", i)
		}
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		screenAnswers.mu.Lock()
		_, busy := screenAnswers.pending[who]
		screenAnswers.mu.Unlock()
		if !busy {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the connection's goroutine never finished")
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if pictures != 2 {
		t.Errorf("%d pictures for two requests", pictures)
	}
}
