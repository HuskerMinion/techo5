package media

import (
	"context"
	"log/slog"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
)

// AnnounceBuffer is how much of an announcement is collected before any of it plays (#559). A text to
// speech engine that streams sentence by sentence is slower than speech for the first words, and this
// is enough to ride over that without making a fast engine wait noticeably longer than it used to.
const AnnounceBuffer = 750 * time.Millisecond

// SpeechResume is how much to hold after a dip, rather than the whole buffer again: rebuilding it
// every time turns a shortfall of a few milliseconds into a third of a second of silence.
const SpeechResume = 80 * time.Millisecond

// VoiceOut is where streamed speech goes: the speaker's player, or a test's stand-in.
type VoiceOut interface {
	PlayVoice(mono []int16)
	Queued() int
}

// SpeechStream is speech arriving in pieces, played as it goes through a cushion: Buffer of it is held
// before anything plays, and after the queue runs dry SpeechResume is held before playing again. It is
// what a streamed reply and a streamed announcement both play through. One is used for one piece of
// speech, from one goroutine; the zero value plays with no cushion, no gain and nothing else.
type SpeechStream struct {
	// Buffer is how much to collect before playing any of it.
	Buffer time.Duration

	// Gain brings the speech toward the speech loudness target, by its loudness so far. The first
	// flush waits for a buffer's worth, so it already has something to measure, and later flushes
	// move the gain only as the speech's level changes.
	Gain bool

	// Louder runs Louder speech's compressor, kept across flushes, when that switch is on.
	Louder bool

	// Say, when set, is handed each flush as well, with the gain but not the compressor: a call
	// that is up hears the speech as the room does.
	Say func([]int16)

	// Started is told once, just before the first audio is queued.
	Started func()

	// held is audio waiting for the cushion to fill, and flowing says the cushion is built and audio
	// is going straight through.
	held    []int16
	flowing bool

	louder Louder

	// energy and samples are the loudness so far, for the gain.
	energy  float64
	samples int

	bytes     int
	peak      int
	playingAt time.Time
}

// Take is a piece of the speech: 16-bit mono samples at speaker.VoiceRate in little-endian bytes. It
// reports whether anything was queued, which is not until the cushion has filled.
func (s *SpeechStream) Take(data []byte, p VoiceOut) bool {
	samples := make([]int16, len(data)/2)
	for i := range samples {
		samples[i] = int16(uint16(data[i*2]) | uint16(data[i*2+1])<<8)
		s.peak = max(s.peak, int(samples[i]), -int(samples[i]))
	}
	s.bytes += len(data)
	s.energy += SumSquares(samples)
	s.samples += len(samples)

	s.held = append(s.held, samples...)
	if p.Queued() == 0 {
		s.flowing = false
	}

	want := s.Buffer
	if !s.playingAt.IsZero() {
		want = SpeechResume
	}
	if !s.flowing && len(s.held) < voiceSamples(want) {
		return false
	}

	if s.playingAt.IsZero() {
		s.playingAt = time.Now()
	}
	s.flowing = true
	s.flush(p)
	return true
}

// Finish plays whatever is still held, which will never be joined by more.
func (s *SpeechStream) Finish(p VoiceOut) { s.flush(p) }

func (s *SpeechStream) flush(p VoiceOut) {
	out := s.held
	s.held = nil
	if len(out) == 0 {
		return
	}
	if s.Gain {
		if gain := SpeechGain(s.energy, s.samples); gain > 1 {
			out = Scale(out, gain)
		}
	}
	if s.Started != nil {
		s.Started()
		s.Started = nil
	}
	if s.Say != nil {
		s.Say(out)
	}
	if s.Louder {
		out = ForSpeaker(out, &s.louder)
	}
	p.PlayVoice(out)
}

// Bytes is how much speech has arrived, Peak the loudest sample of it as it arrived, and PlayingAt
// when it began to play (zero until the cushion first filled).
func (s *SpeechStream) Bytes() int           { return s.bytes }
func (s *SpeechStream) Peak() int            { return s.peak }
func (s *SpeechStream) PlayingAt() time.Time { return s.playingAt }

// voiceSamples is a duration as a count of samples at speaker.VoiceRate.
func voiceSamples(d time.Duration) int {
	return int(d/time.Millisecond) * speaker.VoiceRate / 1000
}

// PlaySpeech fetches speech Home Assistant is serving and plays it through s as it arrives, which is
// what an announcement does (#559): an engine that makes speech sentence by sentence is heard from its
// first sentence rather than after its last. A WAVE body plays as it comes; anything else (MP3) is
// read whole and decoded first, as it always was. The tail of silence follows what played.
//
// Waiting for it to be heard is the driver's: this returns once the last of it has been queued. One
// that fails before any audio fails; one that fails part way, or stalls, has played what came and ends
// there. One canceled returns the context's error, so whatever was to follow it is not started.
func PlaySpeech(ctx context.Context, url string, s *SpeechStream, p VoiceOut) error {
	err := fetchVoice(ctx, url, speechStall, func(data []byte) { s.Take(data, p) })
	if ctx.Err() != nil {
		return ctx.Err()
	}
	s.Finish(p)
	if s.bytes > 0 {
		p.PlayVoice(make([]int16, speaker.VoiceRate*Tail/1000))
	}
	if err != nil && s.bytes > 0 {
		slog.Warn("the announcement ended early", "err", err, "bytes", s.bytes)
		return nil
	}
	if err == nil {
		slog.Info("announcement played", "samples", s.bytes/2, "peak", s.peak)
	}
	return err
}
