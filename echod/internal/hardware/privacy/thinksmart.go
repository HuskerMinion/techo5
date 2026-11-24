//go:build !dot && !spot && !panel

package privacy

import (
	"errors"
	"fmt"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/layout"
	"github.com/HuskerMinion/techo5/echod/internal/lib/input"
)

// The Lenovo ThinkSmart View cuts its microphones with a slide switch on top of the device. Software
// cannot move it; gpio-keys reports where it is as SW_MICROPHONE_INSERT (4), 1 while the microphones
// are connected and 0 while they are cut (1 on a unit that was hearing the room, 2026-11-19).
const swMicrophoneInsert = 0x04

type thinkSmartMute struct{}

func (thinkSmartMute) Get() (bool, error) {
	dev, err := input.Find("gpio-keys")
	if err != nil {
		return false, fmt.Errorf("privacy: %w", err)
	}
	defer dev.Close()
	connected, err := dev.Switch(swMicrophoneInsert)
	return !connected, err
}

func (thinkSmartMute) HardwareActs(bool) bool { return true }

func (thinkSmartMute) Lag() time.Duration { return 0 }

func (m thinkSmartMute) Set(muted bool) error {
	is, err := m.Get()
	if err != nil {
		return err
	}
	if is == muted {
		return nil
	}
	if !muted {
		return ErrButtonOnly
	}
	return errors.New("privacy: the microphones are muted with the switch on the device")
}

func (m thinkSmartMute) Toggle() (bool, error) {
	is, err := m.Get()
	if err != nil {
		return false, err
	}
	return !is, m.Set(!is)
}

func init() {
	if layout.ThinkSmart() {
		boardMute = thinkSmartMute{}
	}
}
