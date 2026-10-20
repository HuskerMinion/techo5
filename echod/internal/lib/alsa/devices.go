package alsa

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// FindPCM resolves a named PCM on a named card, independently of enumeration order.
// found distinguishes an absent card from a card whose requested stream is missing.
func FindPCM(cardID, stream string, capture bool) (card, device int, found bool, err error) {
	return findPCM("/sys/class/sound", "/proc/asound/pcm", cardID, stream, capture)
}

func findPCM(soundDir, pcmPath, cardID, stream string, capture bool) (card, device int, found bool, err error) {
	paths, err := filepath.Glob(filepath.Join(soundDir, "card*", "id"))
	if err != nil {
		return 0, 0, false, err
	}
	for _, path := range paths {
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return 0, 0, false, readErr
		}
		if strings.TrimSpace(string(raw)) != cardID {
			continue
		}
		card, err = strconv.Atoi(strings.TrimPrefix(filepath.Base(filepath.Dir(path)), "card"))
		if err != nil {
			return 0, 0, true, err
		}
		raw, err = os.ReadFile(pcmPath)
		if err != nil {
			return card, 0, true, err
		}
		device, err = pcmDevice(string(raw), card, stream, capture)
		return card, device, true, err
	}
	return 0, 0, false, nil
}

func pcmDevice(list string, card int, stream string, capture bool) (int, error) {
	direction := "playback"
	if capture {
		direction = "capture"
	}
	for _, line := range strings.Split(list, "\n") {
		parts := strings.Split(line, ":")
		if len(parts) < 3 {
			continue
		}
		var c, d int
		if n, err := fmt.Sscanf(strings.TrimSpace(parts[0]), "%d-%d", &c, &d); n != 2 || err != nil || c != card {
			continue
		}
		name := strings.Fields(parts[1])
		if len(name) == 0 || name[0] != stream {
			continue
		}
		for _, field := range parts[2:] {
			words := strings.Fields(field)
			if len(words) == 2 && words[0] == direction {
				count, err := strconv.Atoi(words[1])
				if err == nil && count > 0 {
					return d, nil
				}
			}
		}
	}
	return 0, fmt.Errorf("ALSA card %d has no %s stream %q", card, direction, stream)
}
