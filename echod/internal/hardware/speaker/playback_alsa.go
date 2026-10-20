//go:build !panel

package speaker

import (
	"fmt"
	"log/slog"
	"os"
	"syscall"

	"github.com/HuskerMinion/techo5/echod/internal/layout"
	"github.com/HuskerMinion/techo5/echod/internal/lib/alsa"
)

// openDevice takes the mixer, the DRAM hold where the board needs one, the routes the card needs
// before it opens (openSequence), and the playback stream at the card's own rate (hwRate).
// openDevice also says whether it found the 2nd gen Show 5's mainline card (mt8163cronos), whose
// playback front end is found by name and needs no DRAM hold.
func openDevice() (playback, *alsa.Mixer, *os.File, bool, error) {
	card, device, holdPath := Card, PlaybackDevice, DRAMHold
	mainline := false
	if layout.Board == "cronos" {
		c, d, found, err := alsa.FindPCM("mt8163cronos", "MultiMedia1_Playback", false)
		if err != nil {
			return nil, nil, nil, false, fmt.Errorf("speaker: selecting playback: %w", err)
		}
		if found {
			card, device, holdPath, mainline = c, d, "", true
		}
	}
	m, err := alsa.OpenMixer(card)
	if err != nil {
		return nil, nil, nil, false, fmt.Errorf("speaker: opening mixer: %w", err)
	}

	// Before the playback device, so its driver sees the AFE in use and takes the DRAM ring.
	var hold *os.File
	if holdPath != "" {
		hold, err = os.OpenFile(holdPath, os.O_RDWR|syscall.O_NONBLOCK, 0)
		if err != nil {
			_ = m.Close()
			return nil, nil, nil, false, fmt.Errorf("speaker: holding %s: %w", holdPath, err)
		}
	}

	// A card whose front end reaches the codec only through mixer routes will not open the device
	// before they are on (openSequence).
	applyTo(m, openSequence)

	// The ring is the same length of time at whatever rate the card runs (hwRate).
	pb, err := alsa.OpenPlayback(card, device, alsa.Config{
		Channels:   Channels,
		Rate:       hwRate,
		Format:     alsa.FormatS16_LE,
		Bits:       Bits,
		PeriodSize: period * hwRate / Rate,
		Periods:    periods,
		StartFull:  startFull,
	})
	if err != nil {
		_ = m.Close()
		if hold != nil {
			_ = hold.Close()
		}
		return nil, nil, nil, false, fmt.Errorf("speaker: opening playback: %w", err)
	}
	if startFull && !pb.StartsFull() {
		slog.Warn("the kernel would not hold playback until its ring is full; it starts on the first period")
	}
	return pb, m, hold, mainline, nil
}

// volumeChanged has nothing to tell on an Echo: the step is applied here, in software.
func volumeChanged(int) {}

// followRing has nothing to move on an Echo: a ring's level is worked out in software, in fill.
func (p *Player) followRing(bool, int) {}
