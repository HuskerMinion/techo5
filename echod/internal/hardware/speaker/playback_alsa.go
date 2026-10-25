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
// before it opens (f.open), and the playback stream in f, the card's own rate and sample width.
// openDevice also says whether it found the 2nd gen Show 5's mainline card (mt8163cronos), whose
// playback front end is found by name and needs no DRAM hold.
func openDevice(f cardFormat) (playback, *alsa.Mixer, *os.File, bool, error) {
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

	opened.card, opened.device = card, device
	pb, err := openStream(m, f)
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

// opened is the card and device openDevice found, which reopenPlayback opens again.
var opened struct{ card, device int }

// openStream writes the routes the card needs before its front end will open (a card whose front end
// reaches the codec only through mixer routes refuses it before they are on) and opens the stream. The
// ring is the same length of time at whatever rate the card runs, in samples as wide as the card is
// sent (32 is S24_LE, 24 bits in a 32-bit container, see widenS16).
func openStream(m *alsa.Mixer, f cardFormat) (*alsa.Playback, error) {
	applyTo(m, f.open)
	format := alsa.FormatS16_LE
	if f.bits == 32 {
		format = alsa.FormatS24_LE
	}
	return alsa.OpenPlayback(opened.card, opened.device, alsa.Config{
		Channels:   Channels,
		Rate:       f.rate,
		Format:     format,
		Bits:       f.bits,
		PeriodSize: period * f.rate / Rate,
		Periods:    periods,
		StartFull:  startFull,
	})
}

// reopenPlayback opens the stream again in f, on the card openDevice found, once the old one is closed.
func reopenPlayback(m *alsa.Mixer, f cardFormat) (playback, error) {
	pb, err := openStream(m, f)
	if err != nil {
		return nil, err // not a nil *alsa.Playback in a non-nil interface
	}
	return pb, nil
}

// volumeChanged has nothing to tell on an Echo: the step is applied here, in software.
func volumeChanged(int) {}

// followRing has nothing to move on an Echo: a ring's level is worked out in software, in fill.
func (p *Player) followRing(bool, int) {}
