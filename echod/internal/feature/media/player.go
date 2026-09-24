// Package media is the speaker as Home Assistant sees it: one media_player entity carrying the
// level, the mute and what is playing, plus the playback behind it.
//
// A turn takes the speaker away from a track and gives it back, so music through the middle of a
// conversation is heard by neither the room nor the microphones.
package media

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"sync/atomic"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/ring"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/buttons"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/led"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
	"github.com/HuskerMinion/techo5/echod/internal/lib/asp"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hook"
	"github.com/HuskerMinion/techo5/echod/internal/lib/noise"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
)

func init() {
	component.Register(component.Device, Get(), component.Order(25))
}

// VolumeSteps runs 0..30, the range Android gives STREAM_MUSIC and the one the vendor's volume
// curves are indexed by, so a step here is a step there. Home Assistant works in 0..1.
const VolumeSteps = speaker.VolumeSteps

// volumeFlash is how long the ring shows the level after a change.
const volumeFlash = 2 * time.Second

// stoppedFor is how long a stopped track stays paused before it is ended.
const stoppedFor = 30 * time.Minute

type Player struct {
	mp     *esphome.MediaPlayer
	jack   *esphome.BinarySensor
	stream *Stream

	// resampling is how voice is stretched to the playback rate. A reply arrives at the pipeline's
	// rate and the speaker runs at the codec's, so something has to bridge them and which filter does
	// it is audible.
	resampling *esphome.Select
	onTurn     *esphome.Select
	duck       *esphome.Number

	// asp is the driver's tuning: on applies it, off sends the signal as it came.
	asp          *esphome.Switch
	bass, treble *esphome.Number
	quiet        *esphome.Select

	// nearMiss ducks a playing track for a few seconds after a wake word that nearly fired, so the
	// next try is heard; see feature/detect/nearmiss.go.
	nearMiss *esphome.Switch

	// haSounds is the Home Assistant satellites' recorded sounds for muting and a finished timer, or
	// TECHO5's own notes (hardware/speaker/clips.go).
	haSounds *esphome.Switch

	// sleep stops what is playing after a while, the way a clock radio does.
	sleep *sleeper

	// layers are sounds the device makes on its own, for as long as they are left set. More than one,
	// because a bed with a texture over it — crickets under wind — is worth having and the native API
	// has no entity that holds more than one value.
	layers []*esphome.Select

	// speaking is set while a reply or an announcement is sounding, which Home Assistant is told is
	// playing: its mapper has no case for announcing and raises on it.
	speaking atomic.Bool

	// remote is the speaker lent to something this player did not start, and who is holding it now.
	remote claim

	// remoteLast is whether the last thing the room heard came from a remote. A paused stream ends, so
	// the mark comes off while the track is still the remote's to resume: this is what sends a play back
	// to it rather than to a stream this player is not playing.
	remoteLast atomic.Bool

	// remoteState is what the server says the stream it is sending is doing: playing, paused or
	// stopped. It is the only account of whether a carried stream is paused, because the audio never
	// passes through this player.
	remoteState atomic.Value  // string
	remoteGen   atomic.Uint64 // counts RemoteState, so a pause's timeout knows whether it still stands
	haFwdAt     atomic.Int64  // when a Home Assistant play or pause was last passed on to a remote (fromHA)

	// held is a remote's track paused from here, kept for the screen. Music Assistant does not pause a
	// Sendspin stream: it ends it and clears the track, so without this the page went back to the clock
	// the moment somebody paused, with no play button to come back with.
	held atomic.Value // heldTrack

	// extTrack is what a remote says it is playing: a phone over Bluetooth, or Music Assistant over
	// Sendspin. The stream is the remote's business; this is only what to call it.
	extTrack atomic.Value // remoteTrack

	// lastExt is the last track a remote named, kept when it stops naming one: Music Assistant clears
	// the track just before it says it stopped, so what was playing has to come from here. Only just
	// before: a name kept longer is whatever played hours ago, and a stream that never named its track
	// would be held and shown as that.
	lastExt atomic.Value // lastTrack

	// stoppedAt counts stops, so the timer that ends a stopped track knows whether a later stop or a
	// play has come since.
	stoppedAt atomic.Uint64

	step int

	// OnVolume fires with the new step whenever the level is changed on purpose — a button, a swipe,
	// Home Assistant — so a screen can show it. A restore is silent, as it is on the ring.
	OnVolume hook.Hook[int]

	// OnPlay fires with the URL whenever a track (not an announcement) is started, so a screen can
	// find out what it is.
	OnPlay hook.Hook[string]

	// OnResumeRemote fires when play is asked for on a remote's track that was paused from here and
	// has since been let go. The remote has to be asked some other way than over its own connection -
	// Music Assistant ignores a play from the player it ended - and what knows how lives elsewhere.
	OnResumeRemote hook.Hook[struct{}]

	// OnTransport fires when a track's own controls are asked for — the screen's buttons, or Home
	// Assistant — so that whoever is playing the track hears. It is not an instruction to this player:
	// see Transport.
	OnTransport hook.Hook[Transport]

	// OnEnd fires when a track stops of its own accord, with the URL that ended. A track that was
	// stopped or replaced does not fire: the difference is the whole use of it, since a radio stream
	// that ends by itself has been dropped and one that was stopped was stopped by somebody.
	OnEnd hook.Hook[string]
}

var (
	once   sync.Once
	shared *Player
)

func Get() *Player {
	once.Do(func() { shared = build() })
	return shared
}

func build() *Player {
	p := &Player{
		sleep: newSleeper(),
		mp: &esphome.MediaPlayer{
			Base: esphome.Base{ObjectID: "speaker", Name: "Speaker", Icon: "mdi:speaker"},
			Features: esphome.MediaPlayerFeatureVolumeSet |
				esphome.MediaPlayerFeatureVolumeStep |
				esphome.MediaPlayerFeatureVolumeMute |
				esphome.MediaPlayerFeaturePlayMedia |
				esphome.MediaPlayerFeaturePlay |
				esphome.MediaPlayerFeaturePause |
				esphome.MediaPlayerFeatureStop |
				esphome.MediaPlayerFeatureBrowseMedia |
				esphome.MediaPlayerFeatureAnnounce,
			SupportsPause:    true,
			SupportedFormats: Formats,
		},
		jack: &esphome.BinarySensor{
			Base: esphome.Base{
				ObjectID: "headphones",
				Name:     "Headphones",
				Icon:     "mdi:headphones",
				Category: esphome.CategoryDiagnostic,
			},
			DeviceClass: "plug",
		},
		resampling: &esphome.Select{
			Base: esphome.Base{
				ObjectID: "voice_resampling",
				Name:     "Voice resampling",
				Icon:     "mdi:sine-wave",
				Category: esphome.CategoryConfig,
			},
		},
		onTurn: &esphome.Select{
			Base: esphome.Base{
				ObjectID: "media_on_turn",
				Name:     "Music during a turn",
				Icon:     "mdi:music-note-eighth",
				Category: esphome.CategoryConfig,
			},
		},
		duck: &esphome.Number{
			Base: esphome.Base{
				ObjectID: "media_duck_level",
				Name:     "Music ducking",
				Icon:     "mdi:volume-medium",
				Category: esphome.CategoryConfig,
			},
			Min: -40, Max: -3, Step: 1, Unit: "dB",
			Mode: esphome.NumberBox,
		},
		asp: &esphome.Switch{
			Base: esphome.Base{
				ObjectID: "speaker_eq",
				Name:     "Speaker EQ",
				Icon:     "mdi:equalizer",
				Category: esphome.CategoryConfig,
			},
		},
		bass: &esphome.Number{
			Base: esphome.Base{
				ObjectID: "bass",
				Name:     "Bass",
				Icon:     "mdi:tune-vertical",
				Category: esphome.CategoryConfig,
			},
			Min: -asp.ToneRange, Max: asp.ToneRange, Step: 1, Unit: "dB",
			Mode: esphome.NumberBox,
		},
		treble: &esphome.Number{
			Base: esphome.Base{
				ObjectID: "treble",
				Name:     "Treble",
				Icon:     "mdi:tune-vertical",
				Category: esphome.CategoryConfig,
			},
			Min: -asp.ToneRange, Max: asp.ToneRange, Step: 1, Unit: "dB",
			Mode: esphome.NumberBox,
		},
		haSounds: &esphome.Switch{
			Base: esphome.Base{
				ObjectID: "home_assistant_sounds",
				Name:     "Home Assistant sounds for muting and timers",
				Icon:     "mdi:home-sound-in",
				Category: esphome.CategoryConfig,
			},
		},
		nearMiss: &esphome.Switch{
			Base: esphome.Base{
				ObjectID: "duck_on_near_miss",
				Name:     "Duck after a near miss",
				Icon:     "mdi:volume-low",
				Category: esphome.CategoryConfig,
			},
		},
	}
	p.quiet = newQuiet()
	p.layers = noiseLayers()

	// The player itself stays on the device: it is what people reach for. These are how it behaves.
	bases := []*esphome.Base{&p.resampling.Base, &p.onTurn.Base, &p.duck.Base, &p.jack.Base, &p.asp.Base,
		&p.bass.Base, &p.treble.Base, &p.quiet.Base, &p.nearMiss.Base, &p.haSounds.Base}
	for _, sel := range p.layers {
		bases = append(bases, &sel.Base)
	}
	for _, b := range bases {
		b.DeviceID = component.DevicePlayback
	}

	p.mp.OnCommand = p.command
	component.Bind(p.resampling, speaker.Resamplings(), speaker.Get().SetResampling,
		config.Set().Speaker().Resampling)

	// The speaker settles this: a device whose tuning would not load stays off however it is set. What
	// is saved is what was asked, not what it managed — a device that cannot tune today may be able to
	// tomorrow, when its coefficients arrive or a release learns its tuning, and writing the settled
	// false back would leave it untuned for ever with nobody having chosen that.
	p.asp.OnCommand = func(want bool) {
		settled := speaker.Get().SetASP(want)
		p.asp.Set(settled)
		if err := config.Set().Speaker().ASP(want); err != nil {
			slog.Error("saving a setting failed", "setting", p.asp.ObjectID, "err", err)
		}
		slog.Info("setting changed", "setting", p.asp.ObjectID, "using", settled, "asked", want)
	}

	// Nothing to apply: the stream reads the setting when a turn begins, so changing it takes effect on
	// the next one rather than in the middle of this one.
	component.Bind(p.onTurn, onTurns(), func(v config.OnTurn) config.OnTurn { return v },
		config.Set().Media().OnTurn)

	// Not saved and not restored: it is a sound somebody asked for, and a device that came back from an
	// update hissing in a dark room would be a fault as far as anyone in it is concerned.
	for _, sel := range p.layers {
		sel.OnCommand = func(chosen string) {
			if chosen != noiseOff && !noise.Has(chosen) {
				slog.Warn("unknown option", "setting", sel.ObjectID, "value", chosen)
				return
			}

			sel.Set(chosen)
			p.sound()
		}
	}

	p.haSounds.OnCommand = p.SetHASounds

	p.nearMiss.OnCommand = func(v bool) {
		p.nearMiss.Set(v)
		if err := config.Set().Media().DuckOnNearMiss(v); err != nil {
			slog.Error("saving a setting failed", "setting", p.nearMiss.ObjectID, "err", err)
		}
		slog.Info("setting changed", "setting", p.nearMiss.ObjectID, "using", v)
	}

	p.duck.OnCommand = func(v float32) {
		p.duck.Set(v)
		if err := config.Set().Media().DuckDB(int(v)); err != nil {
			slog.Error("saving the ducking level failed", "err", err)
		}
	}
	// The tone control is the tuning's own stage, so what it is set to is kept whether or not the
	// tuning is on: a device that cannot tune today still remembers what somebody asked for.
	p.bass.OnCommand = func(v float32) {
		p.bass.Set(v)
		if err := config.Set().Speaker().Bass(float64(v)); err != nil {
			slog.Error("saving a setting failed", "setting", p.bass.ObjectID, "err", err)
		}
		p.applyTone()
	}
	p.treble.OnCommand = func(v float32) {
		p.treble.Set(v)
		if err := config.Set().Speaker().Treble(float64(v)); err != nil {
			slog.Error("saving a setting failed", "setting", p.treble.ObjectID, "err", err)
		}
		p.applyTone()
	}
	p.stream = NewStream(speaker.Sound(), speaker.Get(), p.refresh, p.OnEnd.Emit)

	// Volume acts on every tap and on every repeat, so a held button ramps.
	buttons.Get().Events.Listen(func(e buttons.Event) {
		if e.Kind == buttons.Hold {
			return
		}
		switch e.Name {
		case buttons.VolumeUp, buttons.VolumeDown:
			// A ring takes the press. Somebody reaching for a volume button over a ringing alarm
			// wants it to stop, not to be one step quieter — and on a Show or a Spot there is no
			// other button to reach for, so this is the only stop that works with no screen and no
			// microphone. The level is left where it was, so the press costs nothing but the ring.
			if ring.Offered() {
				ring.Accept()
				return
			}
			if ring.Silence() {
				return
			}
		}
		switch e.Name {
		case buttons.VolumeUp:
			p.Adjust(1)
		case buttons.VolumeDown:
			p.Adjust(-1)
		}
	})

	spk := speaker.Get()
	spk.OnOutput.Listen(func(out speaker.Output) { p.jack.Set(out == speaker.OutputHeadphone) })
	p.jack.Set(spk.Output() == speaker.OutputHeadphone)

	p.mp.SetState(esphome.MediaPlayerIdle)
	for _, sel := range p.layers {
		sel.Set(noiseOff)
	}
	return p
}

func (p *Player) Name() string { return "media player" }

// SetHASounds chooses the Home Assistant satellites' sounds for muting and timers, or TECHO5's own.
func (p *Player) SetHASounds(on bool) {
	if err := config.Set().Speaker().ClassicSounds(!on); err != nil {
		slog.Error("saving a setting failed", "setting", p.haSounds.ObjectID, "err", err)
		return
	}
	p.haSounds.Set(on)
	slog.Info("setting changed", "setting", p.haSounds.ObjectID, "using", on)
}

func (p *Player) Entities() []esphome.Entity {
	out := []esphome.Entity{p.mp, p.jack, p.resampling, p.onTurn, p.duck, p.asp, p.bass, p.treble,
		p.quiet, p.nearMiss, p.haSounds, p.sleep.sel}
	for _, sel := range p.layers {
		out = append(out, sel)
	}
	return out
}

// Restore puts the volume back where it was, without flashing the arc: nothing happened, the device
// is starting where it left off.
func (p *Player) Restore(c config.Config) {
	p.apply(c.Speaker.Volume, false)
	slog.Info("restored", "what", "volume", "step", c.Speaker.Volume, "of", VolumeSteps)

	component.Restore(p.resampling, c.Speaker.Resampling, speaker.Get().SetResampling)

	component.Restore(p.onTurn, c.Media.OnTurn, func(v config.OnTurn) config.OnTurn { return v })

	p.nearMiss.Set(c.Media.DuckOnNearMiss)
	slog.Info("restored", "what", p.nearMiss.ObjectID, "using", c.Media.DuckOnNearMiss)
	p.haSounds.Set(!c.Speaker.ClassicSounds)
	slog.Info("restored", "what", p.haSounds.ObjectID, "using", !c.Speaker.ClassicSounds)

	p.duck.Set(float32(c.Media.DuckDB))
	slog.Info("restored", "what", p.duck.ObjectID, "using", c.Media.DuckDB)

	want := c.Speaker.ASPWanted()
	settled := speaker.Get().SetASP(want)
	p.asp.Set(settled)
	slog.Info("restored", "what", p.asp.ObjectID, "using", settled, "asked", want)

	restoreQuiet(p.quiet, c)
	p.bass.Set(float32(c.Speaker.Bass))
	p.treble.Set(float32(c.Speaker.Treble))
	p.applyTone()
	slog.Info("restored", "what", "tone", "bass", c.Speaker.Bass, "treble", c.Speaker.Treble)
}

// applyTone hands the speaker what the two numbers say.
func (p *Player) applyTone() {
	c := config.Get().Speaker
	speaker.Get().SetTone(asp.Tone{Bass: c.Bass, Treble: c.Treble})
}

// onTurns is what music may do about a turn.
func onTurns() []config.OnTurn { return []config.OnTurn{config.OnTurnDuck, config.OnTurnPause} }

// noiseOff is the way out of the list, and what the entities read whenever the speaker is doing
// anything else.
const noiseOff = "None"

// noiseLayers are the slots a sound can be put in. They are peers: any sound in any slot, one on its
// own or both mixed.
func noiseLayers() []*esphome.Select {
	const slots = 2

	out := make([]*esphome.Select, 0, slots)
	for i := 1; i <= slots; i++ {
		out = append(out, &esphome.Select{
			Base: esphome.Base{
				ObjectID: fmt.Sprintf("noise_layer_%d", i),
				Name:     fmt.Sprintf("White noise layer %d", i),
				Icon:     "mdi:blur",
			},
			Options: append([]string{noiseOff}, noise.Names()...),
		})
	}
	return out
}

// sound starts whatever the slots add up to, and stops when they add up to nothing.
func (p *Player) sound() {
	var sounds []string
	for _, sel := range p.layers {
		if chosen := sel.Get(); chosen != noiseOff && chosen != "" {
			sounds = append(sounds, chosen)
		}
	}

	if len(sounds) == 0 {
		p.stream.Stop()
		return
	}
	p.stream.PlayNoise(sounds...)
}

// command handles what Home Assistant sends. Volume arrives as a fraction; the buttons and the
// vendor's curves work in steps, so it is rounded to one.
//
// It runs on the connection's read loop, so nothing here may wait for audio: starting a track hands
// it to a goroutine and returns.
func (p *Player) command(c esphome.MediaCommand) {
	if c.HasVolume {
		p.Set(int(math.Round(float64(c.Volume) * VolumeSteps)))
	}

	// An announcement is a url too, but a short one at the pipeline's rate, and it interrupts rather
	// than replacing what is playing. It goes through the same path as one from the voice assistant.
	if c.HasMediaURL && c.MediaURL != "" {
		if c.Announcement {
			p.announce(c.MediaURL)
		} else {
			p.ours()
			p.stream.Play(c.MediaURL)
			p.OnPlay.Emit(c.MediaURL)
		}
	}
	if !c.HasCommand {
		return
	}

	switch c.Command {
	case esphome.MediaPlayerVolumeUp:
		p.Adjust(1)
	case esphome.MediaPlayerVolumeDown:
		p.Adjust(-1)
	case esphome.MediaPlayerMute:
		p.Mute(true)
	case esphome.MediaPlayerUnmute:
		p.Mute(false)
	case esphome.MediaPlayerStop:
		// Stop is what people say to a speaker to make it quiet, and what Home Assistant sends for it:
		// kept as a pause, so the screen still shows what was playing and play picks it up again. A
		// track left stopped that long is really over. A track somebody else is playing is theirs to
		// stop, so the remote is asked to pause it instead.
		p.fromHA(TransportPause)
		n := p.stoppedAt.Add(1)
		time.AfterFunc(stoppedFor, func() {
			if _, paused := p.stream.Playing(); paused && p.stoppedAt.Load() == n {
				p.stream.Stop()
			}
		})
	case esphome.MediaPlayerPause:
		p.fromHA(TransportPause)
	case esphome.MediaPlayerPlay:
		p.fromHA(TransportPlay)
	case esphome.MediaPlayerToggle:
		p.fromHA(TransportToggle)
	}
}

// announce plays a url over whatever is going on, which is what Home Assistant means by one: a
// doorbell or a spoken alert, not a track.
func (p *Player) announce(url string) {
	p.Sounding(true)
	claim := speaker.Sound().ClaimSpeech("announce", func(ctx context.Context, spk *speaker.Player) error {
		samples, err := Fetch(ctx, url)
		if err != nil {
			return err
		}
		spk.PlayVoice(samples)
		spk.PlayVoice(make([]int16, speaker.VoiceRate*Tail/1000))
		return nil
	})

	// The claim ends once the audio has been heard, not once it has been queued, so this is where
	// the player stops saying it is playing.
	safe.Go("announce", func() {
		<-claim.Done()
		p.Sounding(false)

		if err := claim.Err(); err != nil {
			slog.Error("playing the announcement failed", "url", url, "err", err)
		}
	})
}

// Sounding marks a reply or an announcement as playing, and puts back whatever the player was doing
// once it ends.
func (p *Player) Sounding(on bool) {
	p.speaking.Store(on)
	p.refresh()
}

// remoteTrack is a name for what is playing that this player did not choose.
type remoteTrack struct{ Title, Artist, Album string }

// Transport is a track's own controls, which belong to whoever is playing it. A stream this player is
// only carrying is somebody else's — Music Assistant plays it — so these go to the remote rather than
// to a stream that is not what is in the room.
type Transport int

const (
	TransportPlay Transport = iota
	TransportPause

	// TransportToggle is the screen's one button: play or pause, decided against what the remote says
	// it is doing rather than against this player's own stream.
	TransportToggle

	TransportNext
	TransportPrevious

	// TransportStop is the Stop row on the screen: somebody standing in front of the device, saying
	// they want the music off in this room. It is deliberately not what Home Assistant's `stop` does -
	// see command - because the rule is who is asking, not what the stream is.
	TransportStop
)

// Command is the word the controller role uses for each of these, which is also what a server lists in
// the commands it will take. Toggle has no word of its own: it is settled here, before it is sent.
func (t Transport) Command() string {
	switch t {
	case TransportPlay:
		return "play"
	case TransportPause:
		return "pause"
	case TransportNext:
		return "next"
	case TransportPrevious:
		return "previous"
	case TransportStop:
		return "stop"
	}
	return ""
}

// remoteIsTheTrack is whether a transport command belongs to a remote rather than to this player's own
// stream: what the room is hearing is theirs, or theirs was the last thing that played and this player
// has nothing of its own to resume.
//
// It is not "a claim is held". A claim outlives a pause by design, and reading it as ownership is how a
// paused Music Assistant took the radio's own play button: a tap on the station's page was resolved
// against the remote's state, which said paused, so it asked Music Assistant to play while the station
// underneath it went on playing and the two handed the speaker back and forth.
func (p *Player) remoteIsTheTrack() bool {
	if p.Carried() {
		return true
	}
	if !p.remoteLast.Load() {
		return false
	}
	// A remote that has stopped, with a queue still to resume and nothing of this player's on the screen
	// to resume instead.
	playing, paused := p.stream.Playing()
	return !playing && !paused
}

// Transport asks for a track's own controls. When somebody else is playing, the command is theirs: this
// player must not pause or skip underneath audio it is not playing. A play or pause with no opinion of
// its own is settled against what the remote last said it was doing.
// haEcho is how soon after passing one of Home Assistant's play or pause on to a remote another is
// taken for an echo rather than a request.
const haEcho = 1500 * time.Millisecond

// fromHA is a play, pause or toggle from Home Assistant's media player.
//
// For this player's own stream it is Transport. For a remote's it is passed on only when it would change
// what the remote is doing, and not again within haEcho. Music Assistant knows a TECHO5 device twice -
// as the Sendspin player and as this Home Assistant media player - and it pauses one when the other
// pauses: a pause passed on to it came back through Home Assistant, was passed on again, and the two
// went round thousands of times a second, flickering the play button and flooding Music Assistant until
// its clients dropped. The screen and the buttons still go straight to Transport: a finger is not an echo.
func (p *Player) fromHA(t Transport) {
	if !p.remoteIsTheTrack() {
		p.Transport(t)
		return
	}
	playing, paused := p.RemotePlaying()
	if t == TransportToggle {
		t = TransportPause
		if paused {
			t = TransportPlay
		}
	}
	state, _ := p.remoteState.Load().(string)
	if (t == TransportPause && (paused || state == "stopped")) || (t == TransportPlay && playing) {
		slog.Debug("home assistant transport already so, not passed on", "transport", t, "remote", state)
		return
	}
	now := time.Now().UnixNano()
	if last := p.haFwdAt.Load(); last != 0 && time.Duration(now-last) < haEcho {
		slog.Info("home assistant transport too soon after the last, taken for an echo", "transport", t)
		return
	}
	p.haFwdAt.Store(now)
	p.Transport(t)
}

func (p *Player) Transport(t Transport) {
	// A remote's track paused from here and since let go: play asks for it back, stop forgets it.
	if _, _, _, ok := p.Held(); ok {
		switch t {
		case TransportPlay, TransportToggle:
			slog.Info("resuming a remote's paused track")
			p.OnResumeRemote.Emit(struct{}{})
			return
		case TransportStop:
			// A stop is the music, not only this player's hold on it: while there is still a remote to
			// ask — one that played recently, or one still holding the speaker — it is asked as well, so
			// its queue ends rather than waiting to be started again. The hold goes either way - the
			// track is over.
			//
			// Dropped before the ask rather than after it, because the ask can put it back: a server
			// that will only take a pause answers a stop by holding the track again for the screen, and
			// dropping this player's hold afterwards threw that away. Play on the page then had nowhere
			// to go, and the track somebody had just stopped could not be picked up from Home Assistant
			// either.
			p.held.Store(heldTrack{})
			if p.remoteLast.Load() || p.remote.playing() {
				p.OnTransport.Emit(TransportStop)
			}
			p.refresh()
			return
		}
	}
	if p.remoteIsTheTrack() {
		if t == TransportToggle {
			if _, paused := p.RemotePlaying(); paused {
				t = TransportPlay
			} else {
				t = TransportPause
			}
		}
		p.OnTransport.Emit(t)
		return
	}

	switch t {
	case TransportPlay:
		p.stoppedAt.Add(1)
		p.Resume()
	case TransportPause:
		p.Pause()
	case TransportToggle:
		// The screen's one button, and what it means is settled against what is playing, the way it was
		// before there was a remote to ask. Without this a second tap pauses what is already paused and
		// there is no way back to playing from the screen at all.
		if playing, _ := p.Playing(); playing {
			p.Pause()
		} else {
			p.stoppedAt.Add(1)
			p.Resume()
		}
	case TransportStop:
		p.Stop()
	case TransportNext, TransportPrevious:
		// Nothing to skip to: the tracks this player has are its own stations, and it plays one
		// stream at a time.
	}
}

// RemoteState takes what the server says the stream it is sending is doing: "playing", "paused" or
// "stopped".
//
// A pause left that way for stoppedFor becomes a stop, as this player's own paused track is ended then.
// The server reports a pause as a stop, and sends only what changes, so a queue stopped from the app
// after a pause says nothing at all here - and without this the screen and Home Assistant went on
// saying "paused", or worse "playing", for as long as the connection lasted.
func (p *Player) RemoteState(state string) {
	p.remoteState.Store(state)
	n := p.remoteGen.Add(1)
	if state == "playing" {
		p.held.Store(heldTrack{})
	}
	if state == "paused" {
		time.AfterFunc(stoppedFor, func() {
			if p.remoteGen.Load() == n {
				slog.Info("a remote left paused this long is taken as stopped", "after", stoppedFor)
				p.RemoteState("stopped")
			}
		})
	}
	p.refresh()
}

// CarriedState is what a carried stream is doing, for whatever has to show or report it: what the
// remote said, and playing until it has said anything. A remote that said it stopped is not playing,
// however long it goes on holding the speaker.
func (p *Player) CarriedState() (playing, paused bool) {
	state, _ := p.remoteState.Load().(string)
	switch state {
	case "playing":
		return true, false
	case "paused":
		return false, true
	case "stopped":
		return false, false
	}
	return true, false
}

// RemotePlaying reports what the remote last said, for the controls that have to choose between play
// and pause.
func (p *Player) RemotePlaying() (playing, paused bool) {
	state, _ := p.remoteState.Load().(string)
	return state == "playing", state == "paused"
}

// claim is the speaker lent to something this player did not start. Claims are handed out in order and
// only the newest one is honored, because the server decides the order: Music Assistant ends one stream
// and starts the next in whichever order it likes, and the session being torn down must not free what
// the session taking over is holding.
//
// A mutex rather than a pair of atomics, because the two move together: holding and the number are one
// fact, and a store that lands between another goroutine's two reads makes a claim that nobody honors.
// With `held` stored before `newest` was taken, an older `letGo` read the older number, matched it, and
// cleared a hold the newer taker had just set - the room then shows nothing playing until the next
// stream. The claim is once per stream rather than once per frame, so the lock costs nothing.
type claim struct {
	mu     sync.Mutex
	held   bool
	newest uint64
}

// take makes the speaker the caller's, and returns what it has to give back.
func (c *claim) take() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.newest++
	c.held = true
	return c.newest
}

// letGo gives the speaker back if the caller is still the one holding it, and reports whether it was. A
// claim already given back is holding nothing, so letting go of it twice is not a second pass, and a
// claim older than the one holding the speaker is not this caller's to give back.
func (c *claim) letGo(n uint64) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.held || c.newest != n {
		return false
	}
	c.held = false
	return true
}

func (c *claim) playing() bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.held
}

// External marks the speaker as busy with something this player did not start, and returns the claim on
// it. Music Assistant ends one stream and starts the next in either order, and a session on its way out
// used to clear the mark that the session taking over had just set - which is why the first track after
// a connection looked right and every later one was named as nothing.
func (p *Player) External() uint64 {
	p.remoteLast.Store(true)
	n := p.remote.take()
	p.refresh()
	return n
}

// LetGo gives the speaker back, if the claim is still the one holding it. An older claim does nothing:
// something newer has it.
func (p *Player) LetGo(n uint64) {
	if p.remote.letGo(n) {
		p.refresh()
	}
}

// RemoteGone is a session ending: the remote that was playing is not there any more, so a transport goes
// back to this player's own stream rather than out to a hook with nobody listening on it.
//
// Deliberately not part of LetGo. Giving the speaker back is what a skip does in the middle of a
// connection, and the session that started the next track is still there to be asked — that hold is what
// changeGrace is for. Only the connection going means there is nothing left to ask, which is also why it
// is called after the listener has been removed rather than before.
func (p *Player) RemoteGone() {
	p.remoteLast.Store(false)
	// What the last session said about its stream is not what the next one's first stream is doing.
	p.remoteState.Store("")
	p.remoteGen.Add(1)
}

// ExternalPlaying reports whether something this player did not start is using the speaker, which is
// what the screen asks before it says what the room is playing.
func (p *Player) ExternalPlaying() bool { return p.remote.playing() }

// Carried reports whether what the room is hearing is somebody else's: a remote holds the speaker and is
// the thing playing.
//
// Holding the speaker is not the same as being what the room hears. Music Assistant leaves a track it
// paused holding the speaker for as long as its queue waits there, so a station this device is playing
// underneath it is the sound in the room - and reading the held claim as "the room is Music Assistant's"
// hid the station's own now-playing page, left its Stop row stopping the wrong stream, and told Home
// Assistant the radio was paused while it played. A remote that is playing is still what the room hears,
// whatever this player is doing with its own stream.
func (p *Player) Carried() bool {
	if !p.remote.playing() {
		return false
	}
	if playing, _ := p.RemotePlaying(); playing {
		return true
	}
	// A remote that is paused, or that has not said yet, is what the room hears only when this player
	// has nothing of its own playing.
	playing, _ := p.stream.Playing()
	return !playing
}

// ExternalTrack takes what a remote says it is playing, so the room can name it. An empty title means
// the remote has stopped naming anything.
func (p *Player) ExternalTrack(title, artist, album string) {
	p.extTrack.Store(remoteTrack{Title: title, Artist: artist, Album: album})
	if title != "" {
		p.lastExt.Store(lastTrack{remoteTrack: remoteTrack{Title: title, Artist: artist, Album: album}})
	} else if l, _ := p.lastExt.Load().(lastTrack); l.Title != "" && l.clearedAt.IsZero() {
		l.clearedAt = time.Now()
		p.lastExt.Store(l)
	}
	p.refresh()
}

// lastTrack is the last track a remote named, and when it stopped naming it.
type lastTrack struct {
	remoteTrack
	clearedAt time.Time
}

// lastFor is how long after a remote stops naming its track the name still stands for what it was
// playing: the stop that follows the clearing comes straight after it.
const lastFor = 10 * time.Second

// heldTrack is a remote's track paused from here, and when.
type heldTrack struct {
	remoteTrack
	at time.Time
}

// HoldRemote keeps the remote's track for the screen as it is paused from here. See held.
func (p *Player) HoldRemote() {
	t, _ := p.extTrack.Load().(remoteTrack)
	if l, _ := p.lastExt.Load().(lastTrack); t.Title == "" && time.Since(l.clearedAt) < lastFor {
		t = l.remoteTrack
	}
	if t.Title == "" {
		return
	}
	p.held.Store(heldTrack{remoteTrack: t, at: time.Now()})
}

// Held is a remote's track paused from here and still worth offering to play: until something plays,
// it is stopped, or stoppedFor goes by, as this player's own paused track is ended then.
func (p *Player) Held() (title, artist, album string, ok bool) {
	h, _ := p.held.Load().(heldTrack)
	if h.at.IsZero() || time.Since(h.at) > stoppedFor {
		return "", "", "", false
	}
	if playing, paused := p.stream.Playing(); playing || paused {
		return "", "", "", false
	}
	return h.Title, h.Artist, h.Album, true
}

// ForgetHeld lets a held track go: the music is over, whatever it was.
func (p *Player) ForgetHeld() {
	p.held.Store(heldTrack{})
	p.refresh()
}

// ScreenState is what the room's music is doing, as a screen shows it: this player's own stream, a
// remote's it is carrying, or a remote's paused from here and held.
func (p *Player) ScreenState() (playing, paused bool) {
	if p.Carried() {
		playing, paused = p.CarriedState()
		// Just after a pause the claim on the speaker is still let go of slowly, and the remote says
		// neither: the held track is what the screen should show, with play on it.
		if _, _, _, ok := p.Held(); ok && !playing && !paused {
			return false, true
		}
		return playing, paused
	}
	playing, paused = p.Playing()
	if _, _, _, ok := p.Held(); ok && !playing && !paused {
		return false, true
	}
	return playing, paused
}

// Track is what a remote last said it was playing, whether or not it still is.
func (p *Player) Track() (title, artist, album string) {
	t, _ := p.extTrack.Load().(remoteTrack)
	return t.Title, t.Artist, t.Album
}

// Playing reports what the track is doing, which is what decides whether a turn has anything to take
// the speaker from.
func (p *Player) Playing() (playing, paused bool) { return p.stream.Playing() }

// Pause leaves the track where it is, so it can be picked up again.
func (p *Player) Pause() { p.stream.Pause() }

// Resume picks a paused track up again. (Stream.Resume is something else: it gives the speaker back
// after a turn, and leaves a track the listener paused where it is.)
func (p *Player) Resume() { p.stream.Unpause() }

// Stop ends the track, as Home Assistant's stop does.
func (p *Player) Stop() { p.stream.Stop() }

// Sleep is the sleep timer: what is playing stops after a while.
func (p *Player) Sleep() *sleeper { return p.sleep }

// PlayURL starts a stream the device already knows the address of — one of its own radio stations —
// without Home Assistant resolving anything first. It is the same track as any other: a turn ducks
// it, the buttons set its level, and the media player reports it.
func (p *Player) PlayURL(url string) {
	p.ours()
	p.stream.Play(url)
}

// ours marks what is about to play as this player's own, so a play or a pause goes to its own stream
// rather than to a remote that has let go. Every path that starts local audio has to say so: remoteLast
// is what decides where a transport command goes, and one left set sends the pause to a session that is
// gone, which is a dead pause button on a phone over Bluetooth.
func (p *Player) ours() {
	p.remoteLast.Store(false)
}

// PlayReceived plays audio a remote is sending (a phone using the device as a Bluetooth speaker) as a
// track: it replaces what was playing, and a turn ducks or pauses it like anything else.
func (p *Player) PlayReceived(name string, src PCMSource, rate, channels int) {
	p.ours()
	p.stream.PlayPCM(name, src, rate, channels)
}

// Receiving names what is being played from a remote, empty when nothing is.
func (p *Player) Receiving() string { return p.stream.Receiving() }

// refresh tells Home Assistant what the player is doing. Anything that displaces the noise — a track,
// a stop, the action button — clears both entities, rather than leaving them naming a sound nobody can
// hear.
func (p *Player) refresh() {
	p.mp.SetState(p.state())

	if len(p.stream.Noise()) == 0 {
		for _, sel := range p.layers {
			sel.Set(noiseOff)
		}
	}
}

func (p *Player) state() esphome.MediaPlayerState {
	playing, paused := p.stream.Playing()

	// A stream this player is carrying is somebody else's, and what the remote says it is doing is the
	// truth about the room: this player has nothing of its own to report about it. A remote holding the
	// speaker without playing is not that, or a station playing underneath a paused one would be
	// reported to Home Assistant as paused.
	if p.Carried() {
		playing, paused = p.CarriedState()
	}

	switch {
	case playing || p.speaking.Load():
		return esphome.MediaPlayerPlaying
	case paused:
		return esphome.MediaPlayerPaused
	default:
		return esphome.MediaPlayerIdle
	}
}

// Set applies a level and remembers it.
func (p *Player) Set(step int) {
	applied := p.apply(step, true)
	if err := config.Set().Speaker().Volume(applied); err != nil {
		slog.Error("saving volume failed", "err", err)
	}
}

// apply drives the speaker and reports the step it settled on. tell is false when nothing happened that
// anyone needs to see or read about, which is a restore: the arc is a response to being turned up, not a
// readout of the current level.
func (p *Player) apply(step int, tell bool) int {
	step = max(0, min(step, VolumeSteps))
	p.step = step

	p.mp.SetVolume(float32(step) / VolumeSteps)
	speaker.Get().SetVolume(step)
	if !tell {
		return step
	}

	p.show(step)
	p.OnVolume.Emit(step)
	slog.Info("volume", "step", step, "of", VolumeSteps)
	return step
}

// Mute drops the output without losing the level it was at.
func (p *Player) Mute(muted bool) {
	p.mp.SetMuted(muted)
	if muted {
		speaker.Get().SetVolume(0)
		return
	}
	speaker.Get().SetVolume(p.step)
}

// Adjust moves the level by a step and says so, which is what the buttons and Home Assistant's own
// up and down both do.
func (p *Player) Adjust(delta int) {
	p.Set(p.step + delta)
	speaker.Sound().Chime(speaker.ToneVolume)
}

// show lights the level as a clockwise arc, the leading segment dimmed by the fraction of a segment
// the level does not fill. It takes its own claim each time and lets it expire, which is what puts
// back whatever was underneath — including a conversation that is still running.
func (p *Player) show(step int) {
	frame := led.Volume(float64(step) / VolumeSteps)
	led.Get().Claim(led.PriorityNotice).PaintFor(frame, volumeFlash)
}

// Volume is the current level in steps, 0..VolumeSteps.
func (p *Player) Volume() int { return p.step }
