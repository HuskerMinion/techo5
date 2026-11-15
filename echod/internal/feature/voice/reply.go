package voice

import (
	"context"
	"log/slog"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
	"github.com/HuskerMinion/techo5/echod/internal/feature/phone"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
)

// chunkQueue is how many chunks may wait for the errand. Home Assistant sends 32 ms at a time, so this
// is a couple of seconds of slack: enough that the conversation never waits to hand one over, and
// bounded because audio nobody has played yet is audio arriving faster than it can be heard.
const chunkQueue = 64

// stream is a reply arriving over the API, chunk by chunk, played as it goes. It belongs to the errand
// that plays it: the conversation writes chunks and reads the counters once the claim is done.
type stream struct {
	chunks chan []byte

	// speech is the cushion the reply plays through, which an announcement plays through too: its
	// Buffer is how much to collect before playing any of it. Home Assistant paces itself to stay 384
	// ms ahead, which is enough when speech arrives faster than it plays and not when it does not.
	speech media.SpeechStream

	bytes       int
	peak        int
	splicesAt   uint64
	underrunsAt uint64

	// ended is done called: send and done are the conversation's, on its one goroutine.
	ended bool

	// playingAt is when playback began, once the cushion had filled. Wall clock from here to the queue
	// running dry is what measures gapping; a starved buffer is silence the seam count never sees.
	playingAt time.Time
}

func newStream(splices, underruns uint64, buffer time.Duration) *stream {
	s := &stream{
		chunks:      make(chan []byte, chunkQueue),
		splicesAt:   splices,
		underrunsAt: underruns,
	}
	// The gain follows the reply's loudness so far, and the compressor runs on across flushes. A reply
	// to a turn during a call goes to the far end too, with Assistant replies go into calls on.
	s.speech = media.SpeechStream{Buffer: buffer, Gain: true, Louder: true, Say: func(out []int16) {
		if phone.RepliesIntoCall() {
			phone.Get().Say(out)
		}
	}}
	return s
}

// send hands a chunk to whoever is playing it. A queue this deep only fills if audio is arriving
// faster than it plays, in which case a dropped chunk is the least of it. A chunk after the end is
// dropped: the queue is closed, and a send on it would end the conversation.
func (s *stream) send(data []byte) {
	if s.ended {
		return
	}
	select {
	case s.chunks <- data:
	default:
		slog.Warn("reply chunk dropped", "bytes", len(data))
	}
}

// done says no more chunks are coming, which is what ends the errand; once.
func (s *stream) done() {
	if s.ended {
		return
	}
	s.ended = true
	close(s.chunks)
}

// play is the errand: it takes chunks until there are no more and queues them once enough has arrived
// to play through a dip. Returning hands over to the driver, which waits for the audio to be heard.
func (s *stream) play(ctx context.Context, p *speaker.Player, started func()) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case data, ok := <-s.chunks:
			if !ok {
				s.finish(p)
				return nil
			}
			s.take(data, p, started)
		}
	}
}

// fetch is the errand for a reply fetched from its url while Home Assistant is still writing it: the
// body arrives the way a streamed reply does, and goes through the same cushion. A fetch that fails
// before any audio is the url's fault, and is said; one that fails part way has played what came.
func (s *stream) fetch(ctx context.Context, p *speaker.Player, url string, started func()) error {
	err := media.FetchVoice(ctx, url, func(data []byte) { s.take(data, p, started) })
	if ctx.Err() != nil {
		return nil
	}
	s.finish(p)
	if err != nil && s.bytes > 0 {
		slog.Warn("the early reply ended early", "err", err, "bytes", s.bytes)
		return nil
	}
	return err
}

// finish plays whatever is still held, which will never be joined by more.
func (s *stream) finish(p *speaker.Player) {
	s.speech.Finish(p)
	if s.bytes > 0 {
		at := s.playingAt
		if at.IsZero() {
			at = time.Now() // a reply shorter than the cushion starts here
		}
		replyPlaying(at, replyAudioSeconds(s.bytes), true)
	}
}

func (s *stream) take(data []byte, p *speaker.Player, started func()) {
	// The first chunk is the pipeline delivering, so its limit is done with.
	if s.bytes == 0 {
		slog.Info("reply audio started", "bytes", len(data))
		started()
	}

	// How loud the reply arrives decides whether interpolating it can overflow, and the byte count
	// against how long it takes to say is how a wrong sample rate would show up.
	s.bytes += len(data)
	queued := s.speech.Take(data, p)
	s.peak = s.speech.Peak()
	if queued {
		s.playingAt = s.speech.PlayingAt()
		replyPlaying(s.playingAt, replyAudioSeconds(s.bytes), false)
	}
}
