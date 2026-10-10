//go:build !dot

package display

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/feature/notify"
)

// A tap on a notification's picture dismisses the notification, which takes the view down with it; a
// tap on any other camera view just takes it down.
func TestATapOnAPictureNotificationDismissesIt(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	notify.SetNight(func(time.Time) bool { return true }) // night, so the chime stays quiet
	t.Cleanup(func() { notify.SetNight(func(time.Time) bool { return false }) })

	if err := notify.Get().NotifyPicture("Someone is here", "Doorbell", "camera.front_door", "", "", "", "", 60); err != nil {
		t.Fatal(err)
	}
	v, up := home.Get().Camera()
	if !up || v.Caption == nil {
		t.Fatalf("view %+v up=%v, want the picture up with its caption", v, up)
	}
	closeCamera(v)
	if _, up := home.Get().Camera(); up {
		t.Error("the camera page is still up after the tap")
	}
	if _, up := notify.Get().Showing(); up {
		t.Error("the notification is still up after the tap")
	}

	home.Get().ShowCamera("camera.deck", time.Minute)
	v, _ = home.Get().Camera()
	closeCamera(v)
	if _, up := home.Get().Camera(); up {
		t.Error("a plain camera view is still up after the tap")
	}
}

// A tap on a view whose notification has been replaced by a newer picture takes down nothing of the
// newer one: closeCamera runs on its own goroutine, so the replacement can come first.
func TestClosingAReplacedPictureLeavesTheNewerOne(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	notify.SetNight(func(time.Time) bool { return true })
	t.Cleanup(func() { notify.SetNight(func(time.Time) bool { return false }) })

	if err := notify.Get().NotifyPicture("First", "", "camera.front_door", "", "", "", "", 60); err != nil {
		t.Fatal(err)
	}
	old, _ := home.Get().Camera()
	if err := notify.Get().NotifyPicture("Second", "", "camera.deck", "", "", "", "", 60); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { notify.Get().Dismiss() })
	closeCamera(old)
	if v, up := home.Get().Camera(); !up || v.Caption == nil || v.Caption.Message != "Second" {
		t.Errorf("view %+v up=%v after closing the replaced one, want the newer picture still up", v, up)
	}
}

// The same for a plain view: the tap closes only the view it was read from, so a picture notification
// that landed before closeCamera ran is not hidden (and Home Assistant told it expired).
func TestClosingAReplacedPlainViewLeavesThePicture(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	notify.SetNight(func(time.Time) bool { return true })
	t.Cleanup(func() { notify.SetNight(func(time.Time) bool { return false }) })

	home.Get().ShowCamera("camera.deck", time.Minute)
	old, _ := home.Get().Camera()
	if err := notify.Get().NotifyPicture("Someone is here", "", "camera.front_door", "", "", "", "", 60); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { notify.Get().Dismiss() })
	closeCamera(old)
	if v, up := home.Get().Camera(); !up || v.Caption == nil || v.Caption.Message != "Someone is here" {
		t.Errorf("view %+v up=%v after closing the replaced plain view, want the picture still up", v, up)
	}
	if _, up := notify.Get().Showing(); !up {
		t.Error("the picture notification was dismissed by a tap on the view before it")
	}
}
