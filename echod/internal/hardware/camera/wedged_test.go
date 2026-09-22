//go:build !dot

package camera

import (
	"errors"
	"strings"
	"syscall"
	"testing"
)

// Once the sensor has gone, asking again only produces the same error.
//
// The mute latch cuts the camera's power without telling the sensor driver, and every open after
// that returns EIO until the device is rebooted. It was found in the wild as three and a half hours
// of the same error every twenty seconds — one for each still Home Assistant asked for (#17).
func TestOnceItIsGoneItIsGone(t *testing.T) {
	c := &Camera{}

	if got := c.Wedged(); got != nil {
		t.Errorf("a fresh camera reports %v, want nothing wrong with it", got)
	}

	c.mu.Lock()
	c.wedged = ErrNeedsReboot
	c.mu.Unlock()

	if got := c.Wedged(); !errors.Is(got, ErrNeedsReboot) {
		t.Errorf("got %v, want it to say a reboot is needed", got)
	}
}

// The error says what would fix it. A caller handed a bare I/O error has nothing to act on, and
// Home Assistant shows whatever it is given.
func TestTheErrorSaysWhatToDo(t *testing.T) {
	msg := ErrNeedsReboot.Error()
	for _, want := range []string{"reboot", "camera"} {
		if !strings.Contains(msg, want) {
			t.Errorf("%q does not mention %q", msg, want)
		}
	}
}

// EIO is the signature that latches; anything else is a failure worth trying again. A camera that
// is merely busy must not be written off until the next reboot.
func TestOnlyEIOLatches(t *testing.T) {
	if !errors.Is(syscall.EIO, syscall.EIO) {
		t.Fatal("errors.Is on a syscall errno is not doing what this relies on")
	}
	if errors.Is(syscall.EBUSY, syscall.EIO) {
		t.Error("EBUSY reads as EIO, which would latch on a camera that is only in use")
	}
}
