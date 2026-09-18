//go:build !dot && !spot

package display

import (
	"strings"
	"time"
)

const (
	// Buttons at the right of the Wi-Fi pages' rows.
	buttonWide = 124
	buttonGap  = 10

	// topEdge is how far from the top a swipe down has to start to be the sheet rather than the volume.
	// A quarter of the panel: a finger reaching for the top lands 60-100 px down more often than on
	// the bezel (a swipe from y=81 was a volume step on 2026-09-16), and volume swipes start lower.
	topEdge = 120

	// restartWindow is how long a second tap on Restart (or Delete, on an alarm) is honoured after the
	// first.
	restartWindow = 4 * time.Second
)

// settings is what the settings screen shows, gathered by the display each frame.
type settings struct {
	cat         category
	picker      string // the row whose list of choices is open, or empty
	cardScroll  int    // how far the card is scrolled, in pixels
	pickScroll  int    // how far an open list is scrolled
	brightness  int    // ceiling, percent
	auto        bool
	muted       bool
	wakeWord    string
	weather     string // the weather source's name
	volume      int    // step out of media.VolumeSteps
	night       string
	wifi        string
	wifiName    string // the network joined, or what the Wi-Fi is doing
	wifiOK      bool   // Wi-Fi is managed here, so it can be changed
	btProxy     bool
	checking    bool       // an update check from the screen is out
	colours     bool       // the custom colours editor is open
	demo        bool       // placeholders for the owner's details, for published screenshots
	folder      folderView // the slideshow folder list, while it is open
	name        string
	version     string
	slot        string
	address     string
	sendspin    bool
	insecureTLS bool
	restartArm  time.Time // set after a first tap on Restart
	now         time.Time
}

// stationLabel drops the service a station list names in each entry ("101.1 WXYZ on iHeartRadio"): on a
// list of them it is the same words on every row.
func stationLabel(name string) string {
	for _, tail := range []string{" on iHeartRadio", " on iHeart", " on TuneIn", " on Tune In"} {
		if t, ok := strings.CutSuffix(name, tail); ok {
			return t
		}
	}
	return name
}
