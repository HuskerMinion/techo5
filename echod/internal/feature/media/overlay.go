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
	"sync/atomic"
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
// The url is one this player asked for and was sent back, which is the awkward part: it arrives as an
// ordinary media url, on the same path a track does, with nothing in it to say it is not a track. So the
// request is what says so. The device asks for the next url to be played over the music, which is an ask
// with a token of its own, and the url that answers it plays under that token — never as a track, and
// never in the track history, so a station's name and what is playing are where the room left them.
//
// The token is also what the screen's mute button and the end of a camera's view act on. The player owns
// the identity of what it is playing rather than the feature that asked for it working it out from the
// last url it saw: that url is whatever else has played since.
//
// Muting is not stopping. A muted sound keeps its stream: what arrives is read and thrown away, so the
// sound is back the moment it is brought back, and the music it plays over comes up to its own level
// meanwhile. Stopping is the view ending, and gives the sound up entirely.

const (
	// overAhead is how much audio may sit in the speaker's queue, in frames: a second of it, the same
	// as a track keeps.
	overAhead = speaker.Rate

	// overPace is how often the reading looks to see whether the queue has room.
	overPace = 100 * time.Millisecond

	// overStall is how long one read may produce nothing before the sound is given up on.
	overStall = 30 * time.Second

	// overAskFor is how long an ask waits for its url. Home Assistant answers within a few seconds, and
	// an ask that nothing has answered by now is one nothing is coming for: left standing it would take
	// a later url — somebody's music — for a camera's sound.
	overAskFor = 20 * time.Second

	// overDropFor is how long a request that was given up on still catches its url after the call that
	// made it has come back, so that a stream answering it is dropped instead of playing as a track. Short:
	// everything the call was going to send has been sent by then, and a url arriving later is likelier to
	// be something else — the request is over, and the room wants its music.
	overDropFor = 2 * time.Second
)

// OverToken says which request for a sound over the music this is. It is what the thing that asked for
// the sound holds on to, to say whether what is playing is still its own and to stop it.
type OverToken uint64

// OverState is what became of the sound one request asked for.
type OverState int

const (
	// OverGone is a sound that never arrived, was silenced, or ended on its own. The zero value, so a
	// feature that holds no token at all is answered without a case of its own.
	OverGone OverState = iota

	// OverComing is a sound that has been asked for and whose url has not arrived yet.
	OverComing

	// OverPlaying is a sound being heard.
	OverPlaying

	// OverMuted is a sound that is connected and silent: the stream is still arriving and being read, and
	// what is read is thrown away. It is what makes bringing the sound back immediate rather than another
	// trip to Home Assistant, and it is not the same as OverGone — the sound is still this view's.
	OverMuted

	// OverTaken is a sound something else claimed the speaker from — a reply, an announcement, a ring —
	// and which can be asked for again. It is not the same as OverGone: a sound that was taken is one
	// the room still wants, and one that was silenced is not.
	OverTaken
)

// Live is whether the sound this state describes is playing or on its way — so whether a control that
// offers to silence it, rather than to ask for it, is drawn.
func (s OverState) Live() bool { return s == OverComing || s == OverPlaying }

// overClaims takes the speaker for a sound over the music. It is a variable so that which request a sound
// belongs to, and what becomes of it, can be tested without a device: what a claim does to another claim
// is the speaker's own business, and is tested there.
var overClaims = func(name string, play func(ctx context.Context, spk *speaker.Player) error) *speaker.Claim {
	return speaker.Sound().ClaimOver(name, play)
}

// overAsk is one request for the next url to play over the music.
//
// They are queued rather than kept one deep, because a url arrives for the request that produced it and
// a later request can be made before it does: mute a view while its stream is still starting, open
// another camera, and two asks are outstanding at once. The urls come back in the order the requests
// went out, so the oldest unanswered ask is the one a url belongs to.
type overAsk struct {
	token   OverToken
	dropped bool      // given up on: a url for it is dropped rather than played
	muted   bool      // silenced while it was still on its way: it plays silent when it arrives
	until   time.Time // when an unanswered ask stops being one
}

// overSound is the sound playing over the music: which request it answers, how to stop it, the speaker
// it is filling, and whether it was stopped from here rather than taken from us.
type overSound struct {
	token   OverToken
	stop    context.CancelFunc
	spk     *speaker.Player
	claim   *speaker.Claim
	stopped bool

	// muted is read by the reading itself, which keeps the stream and throws the audio away: a sound that
	// is silenced from the screen goes on being connected, so bringing it back is immediate.
	muted atomic.Bool
}

// OverNext asks that the next url the device is sent plays over the music rather than replacing it, and
// returns the token that says which request this is.
//
// It is for a sound the device asked Home Assistant for and is sent back — a camera's stream, converted
// — because the only way to be handed that url is to have it pushed.
func (p *Player) OverNext() OverToken {
	now := time.Now()
	token := OverToken(p.overSeq.Add(1))

	p.overMu.Lock()
	p.dropStale(now)
	p.overAsks = append(p.overAsks, overAsk{token: token, until: now.Add(overAskFor)})
	p.overMu.Unlock()
	return token
}

// ForgetOverNext gives a request up: the thing that made it no longer wants the sound, and a url that
// arrives for it is dropped rather than played. Dropped rather than left to arrive as a track, because
// a camera's stream played as a track would replace what the room was listening to and nothing would
// know to stop it; dropped rather than played over the music, because the sound was given up on before
// it began.
// It keeps waiting, and keeps dropping what arrives, until the call that made it comes back (OverSettled)
// or its own wait runs out. That matters for a call that is slow: fifteen seconds is a call Home Assistant
// has been seen to take, and a request forgotten before then would let its url through as a track.
func (p *Player) ForgetOverNext(t OverToken) {
	p.overMu.Lock()
	defer p.overMu.Unlock()
	for i := range p.overAsks {
		if p.overAsks[i].token == t {
			p.overAsks[i].dropped = true
		}
	}
}

// OverSettled is the call that made a request having returned, whichever way it went. A request that was
// given up on is forgotten a few seconds after that: what it was waiting for can still be just behind the
// call, but not much, and from then on a url is likelier to be somebody's music than the sound nobody
// wanted. A request that is still wanted is left alone to wait for its url.
func (p *Player) OverSettled(t OverToken) {
	until := time.Now().Add(overDropFor)
	p.overMu.Lock()
	defer p.overMu.Unlock()
	for i := range p.overAsks {
		if p.overAsks[i].token == t && p.overAsks[i].dropped && until.Before(p.overAsks[i].until) {
			p.overAsks[i].until = until
		}
	}
}

// OverState is what became of the sound a request asked for: playing, on its way, taken from it, or
// never there. It is asked by whatever drew a control for the sound, and the answer changes underneath
// it — the sound can be taken by an announcement, which the feature that asked for it never hears about.
func (p *Player) OverState(t OverToken) OverState {
	p.overMu.Lock()
	defer p.overMu.Unlock()
	p.dropStale(time.Now())

	if p.over != nil && p.over.token == t {
		if p.over.muted.Load() {
			return OverMuted
		}
		return OverPlaying
	}
	for _, a := range p.overAsks {
		if a.token == t {
			if a.muted {
				return OverMuted
			}
			return OverComing
		}
	}
	if s, ok := p.overDone[t]; ok {
		return s
	}
	return OverGone
}

// MuteOver silences the sound a request asked for without giving it up, and brings it back when asked:
// the stream keeps arriving and is read and thrown away, so the sound is back the moment the control is
// tapped rather than a few seconds later.
//
// A request whose url has not arrived is muted when it does, so that muting a sound that is still
// starting is muting rather than something else. It is deliberately not StopOver: that is the view
// ending, where the sound is not wanted again at all, and its url should not be played.
func (p *Player) MuteOver(t OverToken, on bool) {
	p.overMu.Lock()
	if p.over != nil && p.over.token == t {
		p.over.muted.Store(on)
		claim := p.over.claim
		p.overMu.Unlock()
		claim.Mute(on) // the music comes back up to its own level while the sound is silent
		return
	}
	for i := range p.overAsks {
		if p.overAsks[i].token == t {
			p.overAsks[i].muted = on
			break
		}
	}
	p.overMu.Unlock()
}

// StopOver silences the sound a request asked for, and gives the request up if its url has not arrived:
// this is the screen's mute button and the end of a camera's view, and both mean the sound is not wanted
// any more, whether it has started or not.
//
// A sound that something else already stopped is nothing to stop, and a sound that is playing under
// somebody else's token is not this one's to stop.
func (p *Player) StopOver(t OverToken) {
	p.overMu.Lock()
	sound := p.over
	playing := sound != nil && sound.token == t
	var spk *speaker.Player
	if playing {
		sound.stopped = true
		spk = sound.spk
		p.over = nil
		p.recordDone(t, OverGone)
	}
	p.overMu.Unlock()

	if !playing {
		p.ForgetOverNext(t)
		return
	}

	if sound.stop != nil {
		sound.stop()
	}
	if spk != nil {
		// What it had queued goes with it, so the silence is immediate rather than up to a second behind
		// — and the ducked music is queued with it, which comes back up as the claim unwinds.
		spk.Drain()
	}
}

// playOver plays url over the music until it stops, under the token of the request it answers. The claim is
// held for as long as the reading lasts, so the music stays down for the whole of it; it is given back
// when the claim ends, which is the reader returning — by itself at the end of the stream, because
// somebody silenced it, or because a claim of another sort took the speaker.
func (p *Player) playOver(url string, token OverToken, muted bool) {
	stop, cancel := context.WithCancel(context.Background())

	p.overMu.Lock()
	previous := p.over
	sound := &overSound{token: token, stop: cancel}
	sound.muted.Store(muted)
	p.over = sound
	if previous != nil {
		// One at a time: a second sound replaces the first rather than playing under it. Stopped from
		// here, so it is not taken as one to ask for again — whatever replaced it is the newer wish.
		previous.stopped = true
	}
	p.overMu.Unlock()
	if previous != nil && previous.stop != nil {
		previous.stop()
	}

	claim := overClaims("over the music", func(claimCtx context.Context, spk *speaker.Player) error {
		defer context.AfterFunc(claimCtx, cancel)()

		p.overMu.Lock()
		if p.over == sound {
			// The speaker to drain if this is stopped: not known until the claim has one.
			sound.spk = spk
		}
		p.overMu.Unlock()
		return readOver(stop, url, spk, sound.isMuted)
	})

	p.overMu.Lock()
	if p.over == sound {
		sound.claim = claim
	}
	p.overMu.Unlock()
	// A sound asked for while it was already silenced starts silent, and the music it is playing over
	// comes back up: muting is not a quieter version of hearing it.
	claim.Mute(sound.muted.Load())

	// Home Assistant is told this is playing, as it is for an announcement: it is something the room
	// hears that is not a track, and a player reporting itself idle while a doorbell rings is worse than
	// one describing it loosely.
	p.Sounding(true)

	// The claim ends once the audio has been heard, not once it has been queued, so this is where the
	// player stops saying it is playing.
	safe.Go("sound over the music", func() {
		<-claim.Done()
		if err := claim.Err(); err != nil {
			slog.Warn("the sound over the music ended badly", "url", url, "err", err)
		}

		p.overMu.Lock()
		if p.over == sound {
			p.over = nil
		}
		done := OverGone
		if claim.Stopped() && !sound.stopped {
			// Taken from it rather than stopped: a reply or an announcement claimed the speaker. Kept
			// apart from a sound that ended or was silenced, because this one can be asked for again and
			// the others should not be.
			done = OverTaken
		}
		p.recordDone(token, done)
		p.overMu.Unlock()
		p.Sounding(false)
	})
}

// overURL is a url arriving: whether it was asked for over the music, and if so, that it has been dealt
// with. It reports whether the url was one of this player's own asks, and so whether the caller should
// play it as a track instead.
func (p *Player) overURL(url string) bool {
	now := time.Now()
	p.overMu.Lock()
	p.dropStale(now)
	if len(p.overAsks) == 0 {
		p.overMu.Unlock()
		return false
	}
	// The oldest unanswered ask, which is the one this url answers. Nothing in a url says which request it
	// belongs to — they arrive on the same media path a track does — so this is order and nothing else: two
	// cameras opened back to back and answered out of order would swap their sounds. What could be done
	// about it is a guess at the url's shape, which is worse than the order Home Assistant answers in.
	ask := p.overAsks[0]
	p.overAsks = p.overAsks[1:]
	p.overMu.Unlock()

	if ask.dropped {
		slog.Info("a sound asked for over the music was let go before its stream arrived", "url", url)
		return true
	}
	p.playOver(url, ask.token, ask.muted)
	return true
}

// dropStale forgets asks that have waited too long, which is what stops an ask nothing answers from
// lying in wait for a url. Unanswered and given up on alike: the difference was whether to play the url
// or drop it, and with no url either way there is nothing left to decide.
func (p *Player) dropStale(now time.Time) {
	kept := p.overAsks[:0]
	for _, a := range p.overAsks {
		if now.Before(a.until) {
			kept = append(kept, a)
			continue
		}
		slog.Debug("a sound asked for over the music was never answered", "token", uint64(a.token))
	}
	p.overAsks = kept
}

// recordDone remembers what became of a token, which is how a request that is over is told apart from
// one that was taken. Only the recent ones: a feature asks about the request it is making now, and
// there is one of it at a time.
func (p *Player) recordDone(t OverToken, s OverState) {
	if p.overDone == nil {
		p.overDone = map[OverToken]OverState{}
	}
	p.overDone[t] = s
	for token := range p.overDone {
		if token+overDoneKept < t {
			delete(p.overDone, token)
		}
	}
}

// overDoneKept is how many finished requests are remembered, which is more than the one in play: a
// screen redrawn across the moment a sound is taken still asks about the token it drew with.
const overDoneKept = 8

// readOver plays a url into the speaker as it arrives, until stop is done. muted reports whether the sound
// is silenced for now.
//
// Being silenced is not a failure anywhere in it: the context can go away during the request, while the
// header is being read, or between reads, and every one of those is somebody tapping the control rather
// than something going wrong. A warning each time would be noise about the feature working.
func readOver(stop context.Context, url string, spk *speaker.Player, muted func() bool) error {
	err := readOverOnce(stop, url, spk, muted)
	if stop.Err() != nil {
		return nil
	}
	return err
}

// isMuted is whether this sound is silenced from the screen, for the reading to ask.
func (s *overSound) isMuted() bool { return s.muted.Load() }

// readOverOnce is the reading itself: the same live WAV a track is, read into the speaker as it arrives.
//
// The body is what Home Assistant serves for a converted stream: its sizes were written before the
// length was known, so the data chunk runs until the connection ends, and there is no end to wait for
// but the connection.
func readOverOnce(stop context.Context, url string, spk *speaker.Player, muted func() bool) error {
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

		// A muted sound is read and thrown away rather than paused: what is coming is live, so keeping up
		// with it is what makes bringing the sound back immediate, and nothing queues towards a burst of
		// the seconds that were silent.
		if n >= frame && !muted() {
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
