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

	if err := notify.Get().NotifyPicture("Someone is here", "Doorbell", "camera.front_door", "", "", "", 60); err != nil {
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
