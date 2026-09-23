package sendspin

import (
	"context"
	"encoding/base64"
	"log/slog"
	"math"
	"sync/atomic"
	"time"

	"github.com/Sendspin/sendspin-go/pkg/protocol"
	ssync "github.com/Sendspin/sendspin-go/pkg/sync"
	"github.com/gorilla/websocket"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
)

// Clock sync runs in bursts, matching the reference player. A round that spent longer in the network
// says less about the offset, so the best of eight is used and the rest thrown away: measured on device,
// single rounds let one at rtt=3.09 s into the filter.
const (
	syncEvery   = 10 * time.Second
	syncBurst   = 8
	syncTimeout = 500 * time.Millisecond
)

// changeGrace is how long a stream's end is held back before the room is told nothing is playing. The
// server ends one stream and starts the next a moment later, and a skip that flashed the clock on the way
// through was worse than the wait: if the next stream starts inside this window, it takes the room, and
// the held-back release no longer matches and does nothing.
const changeGrace = 2 * time.Second

// protocolVersion: the library exports no constant and does not default it. Zero is refused.
const protocolVersion = 1

// session is one server's hold on this room. The server dials us, so there is no reconnect here.
type session struct {
	client *protocol.Client
	clock  *ssync.ClockSync
	out    *out
	bg     *speaker.Arbiter
	report func(string)

	// dec is non-nil exactly while a stream is running.
	dec    decoder
	chunks int

	// lastTS is the newest chunk's timestamp, kept to measure what a new stream overlaps.
	lastTS int64
	opened bool
	muted  bool

	// meta is the track the server last described, and what a message that only changes part of it
	// is merged onto.
	meta metadata

	// claim is this session's hold on the room's now-playing state, given back when it is done. It is
	// what stops a session that is finishing from clearing what the next one has just set.
	claim uint64

	// takes is what the server says it will take from the controller role, so a button the server
	// would ignore is not pressed. It is written once, on the read loop, and read from whoever asks.
	takes atomic.Value // map[string]bool

	// asked is the last transport this session sent, which is how a pause is told from a stop: the
	// server says "stopped" for both, and one of them was asked for by the room. It is written from
	// whoever asked - the touch driver, or Home Assistant's read loop - and read on the session's own,
	// so it is an atomic like every other field here that two goroutines can reach.
	asked atomic.Value // string

	// live is false once this connection is finished. The media player outlives the session and keeps
	// the transport hook, so anything asked after that is dropped rather than written to a dead client.
	live atomic.Bool
}

func newSession(conn *websocket.Conn, o *out, bg *speaker.Arbiter, name string, report func(string)) *session {
	offered := make([]protocol.AudioFormat, 0, len(formats()))
	for _, f := range formats() {
		offered = append(offered, protocol.AudioFormat{
			Codec: f.Codec, Channels: f.Channels, SampleRate: f.SampleRate, BitDepth: f.BitDepth,
		})
	}
	mac, _ := layout.FactoryMAC()

	client := protocol.NewClientFromConn(protocol.Config{
		Name: name,

		Version: protocolVersion,

		// Nothing is activated that is not claimed here. Metadata is claimed so the room can say what
		// it is playing, and the controller role so the room can ask for the track's own controls:
		// next, previous, play and pause are the server's to carry out, and both the screen and Home
		// Assistant reach them through this.
		SupportedRoles: []string{"player@v1", "metadata@v1", "controller@v1"},

		// The factory mac: survives a reinstall, a rename and a new address.
		ClientID: mac,
		DeviceInfo: protocol.DeviceInfo{
			ProductName:     layout.Model,
			Manufacturer:    layout.Manufacturer,
			SoftwareVersion: layout.Version,
		},
		PlayerV1Support: protocol.PlayerV1Support{
			SupportedFormats:  offered,
			BufferCapacity:    bufferCapacity,
			SupportedCommands: []string{"volume", "mute"},
		},
	}, conn)

	return &session{client: client, clock: ssync.NewClockSync(), out: o, bg: bg, report: report}
}

// bufferCapacity caps how far ahead the server may send, which is the stall the room can ride out. The
// spec counts bytes and carries one number for every format, so it is sized for the widest we offer:
// every codec then gets the same cushion, bounded by the server's 30 s cap rather than by bytes.
const bufferSeconds = 30

const bufferCapacity = bufferSeconds * speaker.Rate * speaker.Channels * speaker.Bits / 8

// run drives the connection until it closes or ctx ends.
func (s *session) run(ctx context.Context) error {
	defer s.finish()

	if err := s.client.Start(); err != nil {
		return err
	}
	s.live.Store(true)

	// The media player is a singleton and outlives this session, so the listener has to come off with
	// the connection: one left behind pins this whole session - client, socket, decoder, output - for
	// the life of the daemon, and every reconnect adds another. `live` still says whether writing to
	// this one is worth anything, because Emit copies the listener list before it runs them, so a call
	// can arrive after the cancel.
	cancelTransport := media.Get().OnTransport.Listen(s.asks)
	defer cancelTransport()

	s.reported()
	s.out.use(s.clock)
	safe.Go("sendspin clock", func() { s.synced(ctx) })

	for {
		select {
		case <-ctx.Done():
			s.client.SendGoodbye("user_request")
			return nil

		case <-s.client.Done():
			return nil

		case start, ok := <-s.client.StreamStart:
			if !ok {
				return nil
			}
			s.began(start)

		// Music Assistant ends the stream instead, but the spec has this for seeks and other servers
		// may use it.
		case <-s.client.StreamClear:
			s.cleared()

		case <-s.client.StreamEnd:
			late, dropped := s.out.misses()
			slog.Info("sendspin stream end", "queued_ms", s.out.queuedMs(), "late", late, "dropped", dropped)
			s.ended()

		case chunk, ok := <-s.client.AudioChunks:
			if !ok {
				return nil
			}
			s.heard(chunk)

		case cmd := <-s.client.ControlMsgs:
			s.told(cmd)

		case g := <-s.client.GroupUpdate:
			s.grouped(g)

		// Nothing acts on the artwork, but an undrained channel blocks the reader.
		case st := <-s.client.ServerState:
			s.noticed(st)
		case <-s.client.ArtworkChunks:
		}
	}
}

// began builds the decoder for the negotiated format and takes the speaker.
func (s *session) began(start protocol.StreamStart) {
	if start.Player == nil {
		return
	}
	p := start.Player

	// FLAC arrives as bare frames; what makes them a stream is this.
	header, err := base64.StdEncoding.DecodeString(p.CodecHeader)
	if err != nil {
		slog.Error("sendspin codec header", "codec", p.Codec, "err", err)
		return
	}

	dec, err := decoderFor(p.Codec, p.SampleRate, p.Channels, p.BitDepth, header)
	if err != nil {
		slog.Error("sendspin cannot play what was offered", "codec", p.Codec,
			"rate", p.SampleRate, "ch", p.Channels, "bits", p.BitDepth, "err", err)
		return
	}
	if err := s.out.open(p.SampleRate, p.Channels, p.BitDepth); err != nil {
		slog.Error("sendspin", "err", err)
		return
	}

	first := s.dec == nil
	if !first {
		s.dec.close()
	}
	s.dec = dec
	s.opened = true
	if first {
		s.bg.Took(s.out)
		// Somebody else's stream, which is what the screen says the room is playing rather than what
		// this player chose. Taken here rather than at the top: a stream whose codec we do not offer
		// returns above, and a claim taken for it would have the room showing a track that was never
		// heard until the server gets round to ending it.
		s.claim = media.Get().External()
		s.report(statePlaying)
	}
	slog.Info("sendspin stream", "codec", p.Codec, "rate", p.SampleRate, "ch", p.Channels, "bits", p.BitDepth)
}

// cleared drops what has not been heard, both what is still coded and what is queued. It says nothing
// about the room: a clear is a seek, and the stream carries on after it.
func (s *session) cleared() {
	drained := 0
	for {
		select {
		case <-s.client.AudioChunks:
			drained++
		default:
			slog.Info("sendspin clear", "undecoded", drained, "queued_ms", s.out.queuedMs())
			s.out.flush()
			return
		}
	}
}

// askedFor is the last transport the room asked for, empty when it has not asked anything.
func (s *session) askedFor() string {
	v, _ := s.asked.Load().(string)
	return v
}

// grouped only reports, and says what the stream is doing so the room's controls can match it. Stopping
// is stream/end's job, which the server sends on stop as well as on skip and seek. Fields are deltas, so
// an absent state means unchanged.
func (s *session) grouped(g protocol.GroupUpdate) {
	if g.PlaybackState == nil {
		return
	}
	state := *g.PlaybackState
	switch {
	case state == "playing":
		// The server is playing, so nothing is held any more.
		s.asked.Store("")
		media.Get().RemoteState(state)
	case state == "paused":
		// Paused on the server's side too: the room keeps the track and offers play.
		s.asked.Store("pause")
		media.Get().RemoteState(state)
	case state == "stopped" && s.askedFor() != "pause":
		media.Get().RemoteState(state)
		s.releaseSoon()
	}
	slog.Info("sendspin group", "state", state, "queued_ms", s.out.queuedMs())
}

// ended drops what is held: the spec has stream/end stop output and clear buffers, and the server sends
// it on stop, skip and seek. A track running into the next one keeps the stream and says nothing.
func (s *session) ended() {
	if s.dec == nil {
		s.releaseSoon()
		return
	}
	s.dec.close()
	s.dec = nil
	s.cleared()
	s.out.close()
	s.bg.Gave(s.out)
	s.report(stateJoined)
	s.releaseSoon()
}

// releaseSoon gives the room back when a stream ends inside a live session. A stream's end is what a
// skip and a pause both look like from here, so the release is held back far enough that a skip does not
// flash the clock on the way through.
func (s *session) releaseSoon() { s.release(false) }

// giveBack gives the room up because the connection is going. Nothing else from this session is coming
// to take it, so there is nothing to hold it against - including a pause the room asked for, which would
// otherwise keep the room for the life of the daemon.
func (s *session) giveBack() { s.release(true) }

// released is what an end gives up: the claim, and how long the release is held for. Zero means nothing
// goes back. A pause the room asked for keeps the room - the track stays on the screen with play
// offered, which is how a stop has always been kept here, and the server has no word for a pause that
// keeps the track - and only the connection going overrides that. It touches nothing, so what a skip, a
// pause and a disconnect each mean is decided in one place rather than in whichever way the connection
// happened to end.
func (s *session) released(now bool) (claim uint64, after time.Duration) {
	if s.claim == 0 {
		return 0, 0
	}
	if !now && s.askedFor() == "pause" {
		return 0, 0
	}
	if now {
		return s.claim, 0
	}
	return s.claim, changeGrace
}

// release acts on it. The claim goes either way, so a newer claim that arrived in between is the one that
// stands: LetGo only gives back what the caller still holds.
func (s *session) release(now bool) {
	c, after := s.released(now)
	if c == 0 {
		return
	}
	s.claim = 0
	if after == 0 {
		media.Get().LetGo(c)
		return
	}
	time.AfterFunc(after, func() { media.Get().LetGo(c) })
}

// heard plays a chunk as it arrives.
func (s *session) heard(chunk protocol.AudioChunk) {
	if s.dec == nil {
		return
	}

	pcm, err := s.dec.decode(chunk.Data)
	if err != nil {
		slog.Warn("sendspin decode", "err", err)
		return
	}
	// A frame can span chunks, and the one that did not finish it carries no audio of its own.
	if len(pcm) == 0 {
		return
	}
	s.out.write(chunk.Timestamp, pcm)

	// A new stream's first chunk against the outgoing one's last says whether the server means it to
	// follow on or to replace what is still queued. Both look the same at stream/end.
	if s.opened {
		s.opened = false
		slog.Info("sendspin stream first chunk",
			"lead_ms", (chunk.Timestamp-s.clock.ServerMicrosNow())/1000,
			"prev_last_lead_ms", (s.lastTS-s.clock.ServerMicrosNow())/1000,
			"overlap_ms", (s.lastTS-chunk.Timestamp)/1000,
			"queued_ms", s.out.queuedMs())
	}
	s.lastTS = chunk.Timestamp

	// lead_ms is when the server wanted this played, against now. We play on arrival instead, so it is
	// also how far behind the intended point the room is running.
	if s.chunks++; s.chunks%250 == 0 {
		late, dropped := s.out.misses()
		drift, corrected := s.out.drifting()
		slog.Info("sendspin ahead",
			"queued_ms", s.out.queuedMs(),
			"undecoded", len(s.client.AudioChunks),
			"lead_ms", (chunk.Timestamp-s.clock.ServerMicrosNow())/1000,
			"late", late,
			"dropped", dropped,
			"drift_ms", drift*1000/speaker.Rate,
			"corrected", corrected)
	}
}

// noticed takes what the server says about the track, and which commands the controller role may send.
// Only the player, metadata and controller roles are claimed, so the rest of the message is not this
// device's to act on.
func (s *session) noticed(st protocol.ServerStateMessage) {
	if st.Controller != nil {
		s.took(st.Controller)
	}
	if st.Metadata == nil {
		return
	}
	if !s.meta.merge(st.Metadata) {
		return
	}
	media.Get().ExternalTrack(s.meta.title, s.meta.artist, s.meta.album)
	slog.Info("sendspin now playing",
		"title", s.meta.title, "artist", s.meta.artist, "album", s.meta.album)
}

// took records what the controller role may ask the server for. The server decides whether to act on a
// command, so a command it does not list is a button that would do nothing.
func (s *session) took(c *protocol.ControllerState) {
	takes := make(map[string]bool, len(c.SupportedCommands))
	for _, name := range c.SupportedCommands {
		takes[name] = true
	}
	s.takes.Store(takes)
	slog.Info("sendspin controller", "commands", c.SupportedCommands)
}

// asks carries a track's own controls to the server. It runs on whatever asked — a tap on the screen, a
// button in Home Assistant — so it does no more than write one message.
func (s *session) asks(t media.Transport) {
	if !s.live.Load() {
		return
	}
	name := t.Command()
	if name == "" {
		return
	}
	if takes, ok := s.takes.Load().(map[string]bool); ok && !takes[name] {
		// A server that will not take a stop will take a pause, and a pause is the same request read
		// kindly: somebody standing at the screen pressing Stop wants the room quiet, and a pause is
		// the closest this server offers to that. Asking for nothing at all is what the row used to do.
		if name == "stop" && takes["pause"] {
			name = "pause"
		} else {
			slog.Debug("sendspin transport", "command", name, "taken", false)
			return
		}
	}
	s.asked.Store(name)
	switch name {
	case "pause":
		// The server reports a pause as a stop, so what the room shows is settled here instead: the
		// track stays, and the button offers play.
		media.Get().RemoteState("paused")
	case "play", "next", "previous":
		media.Get().RemoteState("playing")
	case "stop":
		// The opposite of a pause: the room is not playing, and saying so here keeps the screen from
		// offering play for a track that is on its way out while the release is held back.
		media.Get().RemoteState("stopped")
	}
	payload := map[string]any{
		"controller": map[string]any{"command": name},
	}
	if err := s.client.Send("client/command", payload); err != nil {
		slog.Warn("sendspin transport", "command", name, "err", err)
		return
	}
	slog.Info("sendspin transport", "command", name)
}

func (s *session) told(cmd protocol.PlayerCommand) {
	switch cmd.Command {
	case "volume":
		s.out.setVolume(cmd.Volume)
	case "mute":
		s.muted = cmd.Mute
		s.out.setMuted(cmd.Mute)
	}
	s.reported()
}

// reported echoes what took effect. The server has no other way to learn a command landed, and the
// protocol carries no position, so this is the whole of what we say back.
func (s *session) reported() {
	if err := s.client.SendState(protocol.PlayerState{
		State:  "synchronized",
		Volume: config.Get().Speaker.Volume * 100 / speaker.VolumeSteps,
		Muted:  s.muted,
	}); err != nil {
		slog.Debug("sendspin client state", "err", err)
	}
}

// synced keeps the clock filter fed. It owns TimeSyncResp: nothing else may read that channel, or the
// burst would lose rounds to whoever got there first.
func (s *session) synced(ctx context.Context) {
	ticker := time.NewTicker(syncEvery)
	defer ticker.Stop()

	for {
		s.measure(ctx)

		select {
		case <-ctx.Done():
			return
		case <-s.client.Done():
			return
		case <-ticker.C:
		}
	}
}

// measure runs one burst and feeds the filter the round that spent least time in the network.
func (s *session) measure(ctx context.Context) {
drain:
	for {
		select {
		case <-s.client.TimeSyncResp:
		default:
			break drain
		}
	}

	var best protocol.ServerTime
	var arrived int64
	least := int64(math.MaxInt64)

	for range syncBurst {
		sent := micros()
		if err := s.client.SendTimeSync(sent); err != nil {
			slog.Debug("sendspin time sync", "err", err)
			return
		}

		timeout := time.NewTimer(syncTimeout)
		select {
		case <-ctx.Done():
			timeout.Stop()
			return
		case reply := <-s.client.TimeSyncResp:
			timeout.Stop()
			back := micros()
			rtt := (back - reply.ClientTransmitted) - (reply.ServerTransmitted - reply.ServerReceived)
			if rtt < least {
				least, best, arrived = rtt, reply, back
			}
		case <-timeout.C:
		}
	}

	if least == math.MaxInt64 {
		slog.Debug("sendspin time sync", "err", "no reply in the burst")
		return
	}
	s.clock.ProcessSyncResponse(best.ClientTransmitted, best.ServerReceived, best.ServerTransmitted, arrived)
}

func (s *session) finish() {
	s.live.Store(false)
	// No hold outlives the connection: whatever the room was showing for this server goes with it. Given
	// back before the teardown rather than after it, because ended() releases what is left of a stream
	// and with the claim still there that release is the held-back one a skip needs - two seconds late
	// for a connection that has already gone.
	s.giveBack()
	s.asked.Store("")
	s.ended()
	s.client.Close()
	// And what the room is left with is its own: the remote is not there to be asked, and the listener
	// that would have carried a command to it has gone with the connection, so a play or a pause belongs
	// to this player's stream again.
	media.Get().RemoteGone()
}

func micros() int64 { return time.Now().UnixMicro() }
