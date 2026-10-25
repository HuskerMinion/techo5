package wakeword

import (
	"log/slog"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/lib/wake"
)

// Answer is what the device says when Home Assistant asks what it can hear. The question carries the
// models Home Assistant hosts in its own custom_wake_words directory, each with the URL to fetch it
// from, so the answer is that set combined with what is on disk.
//
// It is asked on connect and again after every selection change, which is why nothing about the answer
// is kept: it is worked out when the question arrives. The offers themselves are remembered only
// because a selection names an id and not a URL.
func Answer(offered []esphome.ExternalWakeWord) []esphome.WakeWord {
	lib := wake.Lib()

	lib.Offered(offered)
	words, shadowed := lib.Advertise()

	slog.Info("wake words offered", "count", len(offered),
		"ours", len(lib.Ours()), "advertised", len(words), "shadowed", shadowed)

	// A word that is running but offered with a new model is fetched and loaded again. Not here: Home
	// Assistant is waiting for this answer, and a download can take seconds.
	if stale := lib.Stale(); len(stale) > 0 && Refresh != nil {
		slog.Info("wake words offered with a new model, fetching", "ids", stale)
		go Refresh()
	}
	return words
}

// Refresh loads the running wake words again, downloading any that changed. Detection sets it.
var Refresh func()
