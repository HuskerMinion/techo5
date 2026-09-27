package media

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
)

// A url played over whatever is playing, rather than instead of it.
//
// The music keeps going, ducked, and comes back up when this ends — so nothing has to be remembered
// about what was playing, and a Music Assistant group is not left, because this player never takes the
// track from the room in the first place. It is how a camera's own sound is heard: the stream Home
// Assistant converts for this device is read as it arrives and played under a claim that holds the
// music down for as long as the claim lasts.
//
// This is the stream's reading without the track around it: no gate to wait on, no pause, no queue of
// its own to hand back. What ends it is its context going away, which is the claim ending.

const (
	// overAhead is how much audio may sit in the speaker's queue, in frames: a second of it, the same
	// as a track keeps.
	overAhead = speaker.Rate

	// overPace is how often the reading looks to see whether the queue has room.
	overPace = 100 * time.Millisecond

	// overStall is how long one read may produce nothing before the sound is given up on.
	overStall = 30 * time.Second
)

// OverNext says that the next url to arrive plays over the music rather than replacing it. It is for a
// sound the device asked Home Assistant for and is sent back — a camera's stream, converted — because
// the only way to be handed that url is to have it pushed.
func (p *Player) OverNext() {
	p.overNext.Store(true)
}

// ForgetOverNext takes that back, for a request Home Assistant refused: the ask is for the next url and
// no more, and left standing it would take the next track of somebody's music for a camera's sound.
func (p *Player) ForgetOverNext() {
	p.overNext.Store(false)
}

// takeOverNext is whether the url arriving now was asked for over the music, once: the ask is for one
// url and no more.
func (p *Player) takeOverNext() bool {
	return p.overNext.CompareAndSwap(true, false)
}

// Overing reports whether something is playing over the music now, which is what the screen asks before
// offering to silence it.
func (p *Player) Overing() bool {
	p.overMu.Lock()
	defer p.overMu.Unlock()
	return p.overStop != nil
}

// StopOver silences whatever is playing over the music, which lets the music back up. A sound that
// something else already stopped is nothing to stop.
func (p *Player) StopOver() {
	p.overMu.Lock()
	stop := p.overStop
	p.overMu.Unlock()

	if stop != nil {
		stop()
	}
}

// over plays url over the music until it stops. The claim is held for as long as the reading lasts, so
// the music stays down for the whole of it; it is taken back when the claim ends, which is the reader
// returning — by itself at the end of the stream, or because StopOver cancelled it.
//
// Being taken from is the other way this ends: a reply or an announcement claims the speaker too, and
// that cancels this claim's context and drains what it had queued. The reading is cancelled with it, or
// it would go on filling the queue that was just emptied.
func (p *Player) over(url string) {
	stop, cancel := context.WithCancel(context.Background())
	claim := speaker.Sound().ClaimOver("camera sound", func(claimCtx context.Context, spk *speaker.Player) error {
		defer context.AfterFunc(claimCtx, cancel)()
		return readOver(stop, url, spk)
	})

	// One at a time: a second camera's sound replaces the first rather than playing under it.
	p.overMu.Lock()
	if p.overStop != nil {
		p.overStop()
	}
	p.overStop = cancel
	p.overMu.Unlock()

	// Home Assistant is told this is playing, as it is for an announcement: it is something the room
	// hears that is not a track, and a player reporting itself idle while a doorbell rings is worse
	// than one describing it loosely.
	p.Sounding(true)

	// The claim ends once the audio has been heard, not once it has been queued, so this is where the
	// player stops saying it is playing.
	safe.Go("camera sound", func() {
		<-claim.Done()
		if err := claim.Err(); err != nil {
			slog.Warn("the camera's sound ended badly", "url", url, "err", err)
		}

		p.overMu.Lock()
		p.overStop = nil
		p.overMu.Unlock()
		p.Sounding(false)
	})
}

// readOver plays a url into the speaker as it arrives, until stop is done.
//
// The body is the same live WAV a track is: Home Assistant writes its sizes before it knows the length,
// so the data chunk runs until the connection ends, and there is no end to wait for but the connection.
func readOver(stop context.Context, url string, spk *speaker.Player) error {
	// No timeout on the client: a camera is watched for as long as somebody watches it. What is bounded
	// is a single read, because a wedged connection otherwise holds the sound open for as long as the
	// kernel keeps retrying.
	fetch, giveUp := context.WithCancel(stop)
	defer giveUp()

	req, err := http.NewRequestWithContext(fetch, http.MethodGet, url, nil)
	if err != nil {
		return err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", url, resp.Status)
	}

	body := bufio.NewReaderSize(resp.Body, chunk)
	if err := header(body); err != nil {
		return err
	}

	buf := make([]byte, chunk)
	for {
		if stop.Err() != nil {
			return nil // silenced, which is not a failure
		}

		// About a second ahead of the speaker and no more, so a stream that arrives faster than the
		// device plays it does not grow in memory.
		for spk.Queued() > overAhead {
			select {
			case <-stop.Done():
				return nil
			case <-time.After(overPace):
			}
		}

		watchdog := time.AfterFunc(overStall, giveUp)
		n, err := io.ReadFull(body, buf)
		watchdog.Stop()

		if n >= frame {
			samples := make([]int16, (n-n%frame)/2)
			for i := range samples {
				samples[i] = int16(binary.LittleEndian.Uint16(buf[i*2:]))
			}
			spk.Play(samples)
		}

		switch {
		case err == nil:
		case stop.Err() != nil:
			return nil
		case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
			return nil // the stream ended, which is its own sort of over
		case fetch.Err() != nil:
			return fmt.Errorf("nothing arrived for %s", overStall)
		default:
			return err
		}
	}
}
