package detect

import (
	"context"
	"encoding/binary"
	"errors"
	"log/slog"
	"strings"
	"time"
	"unicode"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/voice"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/mic"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

func (d *Detect) cancelVerification() {
	d.verifyMu.Lock()
	defer d.verifyMu.Unlock()
	if d.verifyCancel != nil {
		d.verifyCancel()
	}
}

func (d *Detect) verifyWake(slot int) {
	model, ok := d.engine.Loaded(slot)
	if !ok || model.Phrase == "" || !voice.Get().CanVerifyWake(slot) {
		return
	}
	d.verifyMu.Lock()
	if d.verifyCancel != nil || !config.Get().Wake.OnHA.VerifySTT {
		d.verifyMu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	d.verifyCancel = cancel
	d.verifyMu.Unlock()
	defer func() {
		cancel()
		d.verifyMu.Lock()
		d.verifyCancel = nil
		d.verifyMu.Unlock()
	}()

	samples := mic.Get().Recent(mic.Confirming)
	if len(samples) == 0 {
		return
	}
	pcm := make([]byte, 2*len(samples))
	for i, sample := range samples {
		binary.LittleEndian.PutUint16(pcm[2*i:], uint16(sample))
	}
	mac, err := layout.FactoryMAC()
	if err != nil {
		slog.Debug("wake verification could not identify the satellite", "err", err)
		return
	}
	check, stop := context.WithTimeout(ctx, 3*time.Second)
	defer stop()
	pipeline, err := hass.Get().WakePipeline(check, mac, slot)
	if err != nil {
		slog.Debug("wake verification could not resolve the Assist pipeline", "err", err)
		return
	}
	transcript, sent, err := hass.Get().Transcribe(check, pipeline, mic.Rate, pcm)
	allowed := err == nil && matchesWakePhrase(transcript, model.Phrase)
	blocked := err == nil && !allowed || errors.Is(err, hass.ErrSTTNoText)
	if sent {
		// Persist after the wake handoff so writing the totals cannot delay the chime.
		defer func() {
			counts, saveErr := config.Set().OnHA().RecordSTTWake(allowed, blocked)
			if saveErr != nil {
				slog.Error("saving wake STT counters failed", "err", saveErr)
			}
			d.onHA.publishSTTWakes(counts)
		}()
	}
	if err != nil {
		slog.Debug("wake verification STT failed", "err", err)
		return
	}
	if check.Err() != nil || !allowed {
		return
	}
	current, ok := d.engine.Loaded(slot)
	if !ok || current.ID != model.ID {
		return
	}
	chime, finish := context.WithTimeout(ctx, 3*time.Second)
	defer finish()
	voice.Get().VerifiedWake(chime, slot)
}

// A wake clip should contain only the wake phrase. Whole-token matching avoids
// accepting similar words or an unrelated sentence containing the phrase.
func matchesWakePhrase(transcript, phrase string) bool {
	normalize := func(value string) string {
		words := strings.FieldsFunc(strings.ToLower(value), func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsNumber(r) && !unicode.IsMark(r)
		})
		return strings.Join(words, " ")
	}
	want := normalize(phrase)
	return want != "" && normalize(transcript) == want
}
