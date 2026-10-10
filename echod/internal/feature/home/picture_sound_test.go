package home

import (
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// soundsAsked is how many camera sounds have been asked for so far.
func soundsAsked(f *fakeOver) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.asked)
}

// A camera on a picture notification plays its sound when the notification asks for it, stays silent
// when it says "off", and otherwise follows the device's Camera sound setting, exactly as
// home_show_camera_sound decides.
func TestAPictureCamerasSoundIsAskedFor(t *testing.T) {
	cases := []struct {
		sound   string
		setting bool
		want    bool
	}{
		{"on", false, true},
		{"off", true, false},
		{"", true, true},
		{"", false, false},
	}
	for _, c := range cases {
		fakePictures(t)
		fake := fakeOverFor(t)
		if err := config.Set().Home().CameraSound(c.setting); err != nil {
			t.Fatal(err)
		}
		f := &Feature{}
		if err := f.ShowPicture("camera.front_door", Caption{ID: "1", Message: "x"}, time.Minute, c.sound); err != nil {
			t.Fatal(err)
		}
		if c.want {
			eventually(t, "the sound asked for", func() bool { return soundsAsked(fake) == 1 })
			continue
		}
		time.Sleep(100 * time.Millisecond) // long enough for a request to be made, were there one
		if n := soundsAsked(fake); n != 0 {
			t.Errorf("sound %q, setting %v: asked for the sound %d times, want none", c.sound, c.setting, n)
		}
	}
}

// A still has no sound to play, whatever the notification asks.
func TestAStillHasNoSound(t *testing.T) {
	fakePictures(t)
	fake := fakeOverFor(t)
	f := &Feature{}
	if err := f.ShowPicture("/local/door.png", Caption{ID: "1", Message: "x"}, time.Minute, "on"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if n := soundsAsked(fake); n != 0 {
		t.Errorf("asked for a still's sound %d times", n)
	}
}
