package dlna

import (
	"bufio"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
	"github.com/HuskerMinion/techo5/echod/internal/feature/video"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
	"github.com/HuskerMinion/techo5/echod/internal/lib/netguard"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
	"github.com/HuskerMinion/techo5/echod/internal/lib/useragent"
)

// The transport: the song a controller set, its state, and the song it queued to follow. The device
// plays the song as a received track named home.DLNAName; whatever else starts after it takes the
// speaker, and the transport then reads STOPPED.

// Transport states, as UPnP names them.
const (
	stNoMedia = "NO_MEDIA_PRESENT"
	stStopped = "STOPPED"
	stPlaying = "PLAYING"
	stPaused  = "PAUSED_PLAYBACK"
	stLoading = "TRANSITIONING"
)

// song is what a controller set: where it is and what it is. A video (isVideo) is played by
// feature/video rather than here, from asks who sent it.
type song struct {
	uri, meta                 string
	title, artist, album, art string
	dur                       time.Duration
	class, mime               string // the description's upnp:class and its res's content type
	video                     bool
	from                      string
	// start is where in the whole song or film this stream begins (startNS's start attribute on
	// res): a controller that sends a film again from a place, as one continuous stream, says so,
	// so the screen can show the film's own time. Positions and the length stay the stream's own.
	start time.Duration
}

// startNS is the namespace of TECHO5's own DIDL-Lite attributes: start, on res.
const startNS = "urn:techo5:dlna"

type renderer struct {
	f *Feature

	mu    sync.Mutex
	cur   song
	next  song
	state string

	gen       int                // which start is the current one; a start overtaken is left alone
	fetching  context.CancelFunc // ends the current start's fetch, when another takes its place
	started   time.Time          // when the song began, for the position
	pausedAt  time.Time          // when it was paused, zero while it plays
	pausedFor time.Duration
	ended     bool // the song played to its end (not stopped, not replaced)
	endedGen  int  // the start whose stream ran out on its own (the source saw the end of it)
	// playingGen is the start whose song the speaker has: a seek's start is a new one, and the song it
	// moves on from keeps the speaker (and can still run out) until the new place has come.
	playingGen int

	// Moving through the song (seek.go): how, once found out (smap; mapping while it is being, which
	// mapCancel ends and mapGen tells from one ended), and a seek asked for and not yet landed, whose
	// target the position reads as meanwhile.
	smap       *seekMap
	mapping    string
	mapCancel  context.CancelFunc
	mapGen     int
	seeking    bool
	seekTo     time.Duration
	seekTimer  *time.Timer        // the settle before a burst's last seek is carried out
	seekArm    int                // which settle timer is the armed one (settled)
	seekOnPlay bool               // a seek while paused, carried out on Play
	seekFetch  context.CancelFunc // ends a seek's fetch, when another takes its place

	// videoID is the video feature/video is playing for the current song, when it is a video.
	videoID uint64
}

// firstBytes is how long a server has to start sending the song once it has answered.
const firstBytes = 15 * time.Second

// fetchClient fetches songs and covers from the home network: quick to connect and answer, then no
// limit, since a song plays for minutes. A controller names the address, and any host on the network
// can be a controller, so the device never connects to itself (loopback), to link-local or multicast
// addresses, or to nowhere, whatever name, redirect or DNS answer leads there: refused when the
// connection is made, so redirects and rebinding are covered too. Every request names the device
// (useragent): a song can come from a server that answers Go's own name differently (#730).
var fetchClient = &http.Client{
	Transport: useragent.Transport(&http.Transport{
		DialContext:           (&net.Dialer{Timeout: 5 * time.Second, Control: refuseOwn}).DialContext,
		ResponseHeaderTimeout: 10 * time.Second,
		MaxIdleConns:          4,
		IdleConnTimeout:       30 * time.Second,
	}),
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 {
			return errors.New("too many redirects")
		}
		return nil
	},
}

// refuseOwn refuses a connection to an address that is the device itself (loopback, or one of its own
// addresses on the network, which the kernel would carry over loopback too), link-local or multicast.
func refuseOwn(network, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if ip == nil || ip.IsLoopback() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsMulticast() || ownAddress(ip) {
		return fmt.Errorf("dlna: not fetching from %s", host)
	}
	return nil
}

// ownAddress is whether ip is one of the device's own addresses, as netguard knows them: read at each
// connection, since the address can change with the network, and on a panel from what the app reports
// (Android 11 and later refuse an app the list, and every song was refused). Not knowing them, the
// device fetches nothing rather than maybe itself.
func ownAddress(ip net.IP) bool { return netguard.OwnIP(ip) }

// coverMost bounds a cover's size.
const coverMost = 4 << 20

func (r *renderer) avTransport(name string, args map[string]string, from string) ([]kv, error) {
	if id := args["InstanceID"]; id != "" && id != "0" {
		return nil, soapError{718, "Invalid InstanceID"}
	}
	switch name {
	case "SetAVTransportURI":
		return nil, r.set(args["CurrentURI"], args["CurrentURIMetaData"], from)
	case "SetNextAVTransportURI":
		return nil, r.setNext(args["NextURI"], args["NextURIMetaData"], from)
	case "Play":
		return nil, r.play()
	case "Pause":
		return nil, r.pause()
	case "Stop":
		r.stop()
		return nil, nil
	case "Seek":
		return nil, r.seek(args["Unit"], args["Target"])
	case "Next", "Previous":
		return nil, errNoContents
	case "GetTransportInfo":
		return []kv{{"CurrentTransportState", r.sync()}, {"CurrentTransportStatus", "OK"}, {"CurrentSpeed", "1"}}, nil
	case "GetPositionInfo":
		r.sync()
		vpos, vdur, isVideo := r.videoPosition()
		r.mu.Lock()
		defer r.mu.Unlock()
		track := "0"
		if r.cur.uri != "" {
			track = "1"
		}
		pos, dur := r.positionLocked(), r.cur.dur
		if isVideo {
			pos = vpos
			if dur == 0 {
				dur = vdur
			}
			if r.seeking {
				pos = r.seekTo
			}
		}
		return []kv{{"Track", track}, {"TrackDuration", hms(dur)}, {"TrackMetaData", shownMeta(r.cur, from)}, {"TrackURI", shownURI(r.cur, from)},
			{"RelTime", hms(pos)}, {"AbsTime", "NOT_IMPLEMENTED"}, {"RelCount", "2147483647"}, {"AbsCount", "2147483647"}}, nil
	case "GetMediaInfo":
		r.sync()
		r.mu.Lock()
		defer r.mu.Unlock()
		n := "0"
		if r.cur.uri != "" {
			n = "1"
		}
		return []kv{{"NrTracks", n}, {"MediaDuration", hms(r.cur.dur)}, {"CurrentURI", shownURI(r.cur, from)}, {"CurrentURIMetaData", shownMeta(r.cur, from)},
			{"NextURI", shownURI(r.next, from)}, {"NextURIMetaData", shownMeta(r.next, from)}, {"PlayMedium", "NETWORK"},
			{"RecordMedium", "NOT_IMPLEMENTED"}, {"WriteStatus", "NOT_IMPLEMENTED"}}, nil
	case "GetDeviceCapabilities":
		return []kv{{"PlayMedia", "NETWORK"}, {"RecMedia", "NOT_IMPLEMENTED"}, {"RecQualityModes", "NOT_IMPLEMENTED"}}, nil
	case "GetTransportSettings":
		return []kv{{"PlayMode", "NORMAL"}, {"RecQualityMode", "NOT_IMPLEMENTED"}}, nil
	case "GetCurrentTransportActions":
		return []kv{{"Actions", r.actions()}}, nil
	}
	return nil, errInvalidAction
}

// set takes the song a controller chose. A song set while another plays starts at once, as every
// controller expects of SetAVTransportURI on a playing renderer.
func (r *renderer) set(uri, meta, from string) error {
	if err := checkURI(uri); err != nil {
		return errInvalidArgs
	}
	s := parseSong(uri, meta)
	s.from = from
	if s.video = isVideo(s); s.video && !video.DLNAOn() {
		// Not offered (sinkProtocols), so not taken: a controller that sends one anyway is told so.
		return errNotVideo
	}
	r.mu.Lock()
	playing := r.state == stPlaying || r.state == stLoading
	paused := r.state == stPaused
	r.cur, r.next, r.ended = s, song{}, false
	r.state = stStopped // a song set while another plays or waits paused is started afresh below
	r.clearSeekLocked()
	r.clearMapLocked()
	r.mu.Unlock()
	slog.Info("dlna: song set", "from", from, "title", s.title, "video", s.video)
	if paused && media.Get().Receiving() == home.DLNAName {
		media.Get().Stop() // the old song is not left holding the speaker, paused, behind the new one
	}
	r.stopVideo()
	if playing {
		return r.play()
	}
	r.f.e.changed()
	return nil
}

func (r *renderer) setNext(uri, meta, from string) error {
	if uri == "" {
		r.mu.Lock()
		r.next = song{}
		r.mu.Unlock()
		return nil
	}
	if err := checkURI(uri); err != nil {
		return errInvalidArgs
	}
	next := parseSong(uri, meta)
	next.from = from
	if next.video = isVideo(next); next.video && !video.DLNAOn() {
		return errNotVideo
	}
	r.mu.Lock()
	r.next = next
	if r.state != stStopped {
		r.ended = false
	}
	r.mu.Unlock()
	r.f.e.changed()
	return nil
}

// checkURI accepts a song at an http address: what a controller hands a renderer.
func checkURI(uri string) error {
	u, err := url.Parse(uri)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return errors.New("not an http address")
	}
	return nil
}

// play starts the song set, or picks a paused one up again.
func (r *renderer) play() error {
	r.sync()
	r.mu.Lock()
	if r.cur.uri == "" {
		r.mu.Unlock()
		return errNoContents
	}
	if r.state == stPaused && r.seekOnPlay && !r.cur.video {
		// Moved while paused: the song is fetched from the new place, in place of carrying on from the
		// old one.
		r.startSeekLocked(r.seekTo)
		r.mu.Unlock()
		r.f.e.changed()
		return nil
	}
	if r.state == stPaused {
		r.pausedFor += time.Since(r.pausedAt)
		r.pausedAt = time.Time{}
		r.state = stPlaying
		r.publishPositionLocked(1)
		isVideo := r.cur.video
		r.mu.Unlock()
		if isVideo {
			video.Resume()
		} else {
			media.Get().Resume()
		}
		r.f.e.changed()
		return nil
	}
	if r.state == stPlaying || (r.state == stLoading && r.seekFetch != nil) || (r.cur.video && r.state == stLoading && r.videoID != 0) {
		// Playing, or moving to a new place to play from (a video the player has is TRANSITIONING
		// while it starts, and again while a seek starts its decoder there): a Play after a Seek, as
		// many controllers send, is not a start from the beginning.
		r.mu.Unlock()
		return nil
	}
	r.gen++
	gen, s := r.gen, r.cur
	r.state, r.ended, r.playingGen = stLoading, false, 0
	if r.fetching != nil {
		r.fetching() // a start still connecting, overtaken: it goes, rather than run to its timeout
	}
	ctx, cancel := context.WithCancel(context.Background())
	r.fetching = cancel
	r.mu.Unlock()
	if s.video {
		cancel()
		return r.startVideo(gen, s)
	}
	r.f.e.changed()
	safe.Go("dlna song", func() { r.start(ctx, gen, s, nil) })
	return nil
}

// startVideo hands the song to the video player, which asks on the screen first for an address it
// has not been told to allow. The transport reads TRANSITIONING until the picture comes.
func (r *renderer) startVideo(gen int, s song) error {
	id, err := playVideo(video.Request{URL: s.uri, Title: s.title, Origin: video.FromDLNA, From: s.from})
	r.mu.Lock()
	if r.gen != gen {
		r.mu.Unlock()
		if err == nil {
			stopVideoID(id)
		}
		return nil
	}
	if err != nil {
		// Refused (off, busy, Not now): the song this renderer was playing plays on.
		r.state = stStopped
		r.mu.Unlock()
		slog.Info("dlna: the video was not played", "title", s.title, "err", err)
		r.f.e.changed()
		return errNoContents
	}
	r.videoID, r.state, r.started, r.pausedAt, r.pausedFor = id, stLoading, time.Now(), time.Time{}, 0
	r.mu.Unlock()
	// Taken: the song this renderer was playing goes. Left playing, it would be nobody's to stop, since
	// the transport is the video's now.
	stopOurSong()
	r.f.e.changed()
	return nil
}

// The video player and the speaker, as startVideo uses them; tests put their own in.
var (
	playVideo   = video.Play
	stopVideoID = video.StopID
	stopOurSong = func() {
		if media.Get().Receiving() == home.DLNAName {
			media.ClearPosition()
			media.Get().Stop()
		}
	}
)

// stopVideo stops the video the renderer started, if it is still the one playing.
func (r *renderer) stopVideo() {
	r.mu.Lock()
	id := r.videoID
	r.videoID = 0
	r.mu.Unlock()
	if id != 0 {
		video.StopID(id)
	}
}

// start fetches the song and hands it to the speaker: from its start, or for a seek (sk) from the
// place sk asks for, in place of the same song from where it was.
func (r *renderer) start(ctx context.Context, gen int, s song, sk *seekReq) {
	// A server that answers and then sends nothing is given up on, rather than left TRANSITIONING.
	ctx, giveUp := context.WithCancel(ctx)
	slow := time.AfterFunc(firstBytes, giveUp)
	var (
		pcm    io.Reader
		body   io.ReadCloser
		at     time.Duration
		length int64
		ranges bool
		err    error
	)
	if sk == nil {
		pcm, body, length, ranges, err = fetchSong(ctx, s.uri)
	} else {
		pcm, body, at, err = sk.m.open(ctx, fetchClient, sk.to)
	}
	slow.Stop() // the song is coming (or has failed): no more waiting on its first bytes
	ours, _ := speakerState()
	r.mu.Lock()
	if r.gen != gen {
		r.mu.Unlock()
		if err == nil {
			body.Close()
		}
		return
	}
	if err == nil && sk != nil && !ours {
		// Something else took the speaker while the place was being fetched: the seek does not take it
		// back.
		body.Close()
		r.state = stStopped
		r.clearSeekLocked()
		r.mu.Unlock()
		r.f.e.changed()
		return
	}
	if err != nil {
		resume := false
		if sk != nil {
			// The song plays on from where it was (paused, it is carried on: Play was what asked, and the
			// time it was paused is not counted in where it is).
			slog.Warn("dlna: the song could not be moved", "title", s.title, "to", sk.to.Round(time.Second), "err", quietErr(err))
			r.state, r.seekFetch = stPlaying, nil
			if !r.pausedAt.IsZero() {
				r.pausedFor += time.Since(r.pausedAt)
				r.pausedAt = time.Time{}
			}
			if r.seekTimer == nil {
				r.seeking = false
			}
			r.publishPositionLocked(1)
			resume = true
		} else {
			r.state = stStopped
			r.clearSeekLocked()
			slog.Warn("dlna: the song could not be played", "title", s.title, "err", quietErr(err))
		}
		r.mu.Unlock()
		if resume && media.Get().Receiving() == home.DLNAName {
			media.Get().Resume()
		}
		r.f.e.changed()
		return
	}
	// From here the speaker is the new start's: the song it replaces running out now is not this
	// one's end.
	r.playingGen = gen
	if sk != nil {
		// The fetch is about to be the speaker's: a seek that overtakes this one from now on must not
		// end it, or the sound would end with it. It is handed on below, with the source it goes into.
		r.seekFetch = handedOver
	}
	r.mu.Unlock()

	if sk == nil {
		// The song's picture is cleared until the new one arrives, so the last song's is not left on it.
		// A seek is the same song, and keeps it.
		home.ReceivedPicture(home.DLNAName, nil)
	}
	src := newSource(pcm, body, func() {
		r.mu.Lock()
		if r.playingGen == gen {
			r.endedGen = r.gen
		}
		r.mu.Unlock()
	})
	playOnSpeaker(src, s)

	r.mu.Lock()
	if r.gen != gen {
		// Stopped or replaced while it was being started: it does not get to keep the speaker. A seek
		// overtaken by another seek of the same song does: the next takes over from it.
		keep := sk != nil && r.cur.uri == s.uri && (r.state == stPlaying || r.state == stPaused || r.state == stLoading)
		var old context.CancelFunc
		hold := false
		if keep {
			// Its fetch is the song's now, until the next lands and replaces it (or fails, and this one
			// plays on); the one before it went with the source this replaced.
			old, r.fetching = r.fetching, sk.cancel
			// The position is this one's too until then: a seek that overtakes it and fails carries
			// on from where this one is, not from where the song was before it.
			r.started, r.pausedFor = time.Now().Add(-at), 0
			r.pausedAt = time.Time{}
			if r.state == stPaused {
				// Paused while its place was going to the speaker: the pause reached the source this
				// one replaced, so this one is paused too, or the song would play on under a
				// transport that says it is paused.
				r.pausedAt, hold = time.Now(), true
			}
		}
		r.mu.Unlock()
		if old != nil {
			old()
		}
		if hold {
			pauseSpeaker()
		}
		if !keep {
			if sk != nil {
				sk.cancel()
			}
			if media.Get().Receiving() == home.DLNAName {
				media.Get().Stop()
			}
		}
		return
	}
	r.state, r.started, r.pausedAt, r.pausedFor = stPlaying, time.Now().Add(-at), time.Time{}, 0
	var old context.CancelFunc
	if sk != nil {
		// The fetch is the song's now; the old one's is closed with the source it replaced.
		old, r.fetching, r.seekFetch = r.fetching, sk.cancel, nil
		if r.seekTimer == nil {
			r.seeking = false
		}
	}
	r.publishPositionLocked(1)
	mapped := r.smap != nil && r.smap.uri == s.uri
	mapIt := sk == nil && length > 0 && ranges && !mapped && r.mapping != s.uri
	var (
		mapCtx    context.Context
		mapCancel context.CancelFunc
		mapGen    int
	)
	if mapIt {
		// Found out once, however often the song is started meanwhile, and ended with the song.
		r.stopMappingLocked()
		mapCtx, mapCancel = context.WithTimeout(context.Background(), 20*time.Second)
		r.mapping, r.mapCancel, mapGen = s.uri, mapCancel, r.mapGen
	}
	// A seek asked for while the song was starting: carried out now if the song is mapped, once it is
	// if it is being, and forgotten if it cannot be (no length, or a server that sends no parts).
	seekNow := false
	if sk == nil && r.seeking && r.seekTimer == nil {
		switch {
		case mapped:
			seekNow = true
		case r.mapping != s.uri:
			r.seeking = false
		}
	}
	r.mu.Unlock()
	if old != nil {
		old()
	}
	if sk == nil {
		safe.Go("dlna cover", func() { r.cover(gen, s.art) })
	}
	if mapIt {
		safe.Go("dlna seek map", func() { r.mapSong(mapCtx, mapCancel, mapGen, s, length) })
	}
	if seekNow {
		r.seekNow()
	}
	r.f.e.changed()
}

// fetchSong fetches the song from its start, decoded: the speaker's samples and the body to close,
// with the song's length in bytes and whether the server says it sends parts of it (a seek's Range
// request), which is found out for sure after (mapSong).
func fetchSong(ctx context.Context, uri string) (io.Reader, io.ReadCloser, int64, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, uri, nil)
	if err != nil {
		return nil, nil, 0, false, err
	}
	resp, err := fetchClient.Do(req)
	if err != nil {
		return nil, nil, 0, false, quietErr(err) // it names the address, token and all
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, nil, 0, false, fmt.Errorf("the server answered %s", resp.Status)
	}
	pcm, err := media.DecodeStream(bufio.NewReaderSize(resp.Body, 64<<10), resp.Header.Get("Content-Type"))
	if err != nil {
		resp.Body.Close()
		return nil, nil, 0, false, err
	}
	// A server that says nothing of ranges may still send them: asked, it says (mapSong). One that
	// says it does not is believed.
	ranges := !strings.EqualFold(strings.TrimSpace(resp.Header.Get("Accept-Ranges")), "none")
	return pcm, resp.Body, resp.ContentLength, ranges, nil
}

// speakerState is whether the speaker has the renderer's song, and whether it is paused there; tests
// put their own in.
var speakerState = func() (ours, paused bool) {
	_, paused = media.Get().ScreenState()
	return media.Get().Receiving() == home.DLNAName, paused
}

// pauseSpeaker pauses the renderer's song on the speaker; tests put their own in.
var pauseSpeaker = func() { media.Get().Pause() }

// playOnSpeaker hands the song to the speaker as the renderer's received track; tests put their own in.
var playOnSpeaker = func(src *source, s song) {
	media.Get().PlayReceived(home.DLNAName, src, speaker.Rate, 2)
	media.Get().SetReceivedTrack(home.DLNAName, s.title, s.artist, s.album)
}

// publishPositionLocked tells what is shown with the music (the lyrics) where the song is, moving at
// rate (1 playing, 0 paused). Wants r.mu. A video has no lyrics.
func (r *renderer) publishPositionLocked(rate float64) {
	if r.cur.dur <= 0 || r.cur.video {
		return
	}
	media.SetPosition(media.Position{Title: r.cur.title, At: time.Now(), Pos: r.positionLocked(), Dur: r.cur.dur, Rate: rate})
}

// cover fetches the song's picture for Now Playing, unless another song has started since.
func (r *renderer) cover(gen int, u string) {
	if checkURI(u) != nil {
		return // cleared as the song started
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return
	}
	resp, err := fetchClient.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, coverMost+1))
	if err != nil || len(b) > coverMost {
		return
	}
	r.mu.Lock()
	current := r.gen == gen
	r.mu.Unlock()
	if current {
		home.ReceivedPicture(home.DLNAName, b)
	}
}

func (r *renderer) pause() error {
	st := r.sync()
	r.mu.Lock()
	if st == stLoading && r.seekFetch != nil && !r.cur.video {
		// Paused while moving: the fetch goes, the song is held where it was, and the new place is
		// fetched on Play, as a seek while paused is.
		r.seekFetch()
		r.seekFetch = nil
		r.gen++
		r.state, r.pausedAt, r.seeking, r.seekOnPlay = stPaused, time.Now(), true, true
		r.mu.Unlock()
		pauseSpeaker()
		r.f.e.changed()
		return nil
	}
	r.mu.Unlock()
	if st != stPlaying {
		return errNoContents
	}
	r.mu.Lock()
	r.state, r.pausedAt = stPaused, time.Now()
	r.publishPositionLocked(0)
	isVideo := r.cur.video
	r.mu.Unlock()
	if isVideo {
		video.Pause()
	} else {
		media.Get().Pause()
	}
	r.f.e.changed()
	return nil
}

func (r *renderer) stop() {
	r.mu.Lock()
	r.gen++
	r.ended, r.playingGen = false, 0
	r.clearSeekLocked()
	if r.fetching != nil {
		r.fetching()
		r.fetching = nil
	}
	was := r.state
	if r.cur.uri != "" {
		r.state = stStopped
	} else {
		r.state = stNoMedia
	}
	r.mu.Unlock()
	if (was == stPlaying || was == stPaused) && media.Get().Receiving() == home.DLNAName {
		// Only while the speaker is still the song's: a source that has just taken it over keeps
		// the position it has published.
		media.ClearPosition()
		media.Get().Stop()
	}
	r.stopVideo()
	r.f.e.changed()
}

// off is the switch going off: a song of the renderer's stops, and nothing is left set.
func (r *renderer) off() {
	r.stop()
	r.mu.Lock()
	r.cur, r.next, r.state = song{}, song{}, stNoMedia
	r.clearMapLocked()
	r.mu.Unlock()
}

// sync brings the state in line with the speaker: a song that is no longer the one playing ended, or
// was replaced; one paused or resumed on the device's own screen is paused or playing. It returns the
// state.
func (r *renderer) sync() string {
	ours, paused := speakerState()
	vs := currentVideo()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.state == "" {
		r.state = stNoMedia
	}
	if r.cur.video {
		r.syncVideoLocked(vs)
		return r.state
	}
	switch {
	case (r.state == stPlaying || r.state == stPaused) && !ours:
		// Over: it ran out on its own only if its stream said so (the source saw the end). Stopped
		// on the screen, by voice or Home Assistant, or replaced by a station, it is not an end to
		// go on to the next song from.
		r.ended = r.endedGen == r.gen
		r.state = stStopped
		// A seek asked for and not yet carried out was the song's: left, it would move the next one
		// (the song queued after it, or this one played again) to its place.
		r.clearSeekLocked()
	case r.state == stPlaying && ours && paused:
		r.state, r.pausedAt = stPaused, time.Now()
		r.publishPositionLocked(0)
	case r.state == stPaused && ours && !paused:
		r.pausedFor += time.Since(r.pausedAt)
		r.state, r.pausedAt = stPlaying, time.Time{}
		r.publishPositionLocked(1)
		if r.seekOnPlay {
			// Carried on from the screen after a seek while paused: it goes to the new place now.
			r.startSeekLocked(r.seekTo)
		}
	case r.state == stLoading && r.seekFetch != nil && !ours:
		// Moving through the song when something else took the speaker: the song is over, by itself
		// only if the song the seek moves on from ran out. The seek's start is overtaken (gen), so its
		// fetch failing now does not carry the song on, nor its landing take the speaker back.
		r.ended = r.endedGen == r.gen
		r.gen++
		r.state = stStopped
		r.clearSeekLocked()
	}
	return r.state
}

// syncVideoLocked is sync for a video: the video player's state is the transport's. A video that is
// no longer the player's is over: by itself only if the player says it played to its end. Wants r.mu.
func (r *renderer) syncVideoLocked(vs video.State) {
	if r.state != stPlaying && r.state != stPaused && r.state != stLoading {
		return
	}
	if r.videoID != 0 && vs.AskID == r.videoID {
		// Asked about on the screen while another video plays: still on its way.
		r.state = stLoading
		return
	}
	if r.videoID == 0 || vs.ID != r.videoID || !vs.Active() {
		r.ended = r.videoID != 0 && vs.Ended == r.videoID
		r.state, r.videoID = stStopped, 0
		r.clearSeekLocked() // the video's, as a song's is (sync)
		return
	}
	switch vs.Phase {
	case video.Asking, video.Loading:
		r.state = stLoading
	case video.Playing:
		if r.state == stPaused {
			r.pausedFor += time.Since(r.pausedAt)
			r.pausedAt = time.Time{}
		}
		r.state = stPlaying
	case video.Paused:
		if r.state != stPaused {
			r.pausedAt = time.Now()
		}
		r.state = stPaused
	}
}

// videoPosition is how far into the current song's video the player is, and how long it runs, when
// the song is a video the player has.
func (r *renderer) videoPosition() (pos, dur time.Duration, ok bool) {
	r.mu.Lock()
	id, isVideo := r.videoID, r.cur.video
	r.mu.Unlock()
	if !isVideo || id == 0 {
		return 0, 0, false
	}
	vs := currentVideo()
	if vs.ID != id {
		return 0, 0, false
	}
	return vs.Pos, vs.Dur, true
}

// watch notices a song ending and starts the one queued after it, while the renderer is on.
func (r *renderer) watch(ctx context.Context) {
	last := ""
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
		st := r.sync()
		r.mu.Lock()
		advance := st == stStopped && r.ended && r.next.uri != ""
		if advance {
			r.cur, r.next, r.ended = r.next, song{}, false
			r.clearSeekLocked() // the song before's: the next starts at its own start
			r.clearMapLocked()
		}
		r.mu.Unlock()
		if advance {
			_ = r.play()
			continue
		}
		if st != last {
			last = st
			r.f.e.changed()
		}
	}
}

// positionLocked is how far into the song it is, wants r.mu. A seek not yet landed reads as where it
// is going.
func (r *renderer) positionLocked() time.Duration {
	if r.seeking && (r.state == stPlaying || r.state == stPaused || r.state == stLoading) {
		return r.seekTo
	}
	if r.started.IsZero() || (r.state != stPlaying && r.state != stPaused) {
		return 0
	}
	end := time.Now()
	if !r.pausedAt.IsZero() {
		end = r.pausedAt
	}
	p := end.Sub(r.started) - r.pausedFor
	if r.cur.dur > 0 && p > r.cur.dur {
		p = r.cur.dur
	}
	return max(p, 0)
}

// renderingControl is the volume: UPnP's 0 to 100 on the device's own steps.
func renderingControl(name string, args map[string]string) ([]kv, error) {
	switch name {
	case "GetVolume":
		return []kv{{"CurrentVolume", strconv.Itoa(media.Get().Volume() * 100 / config.VolumeSteps)}}, nil
	case "SetVolume":
		v, err := strconv.Atoi(strings.TrimSpace(args["DesiredVolume"]))
		if err != nil || v < 0 || v > 100 {
			return nil, errInvalidArgs
		}
		media.Get().Set((v*config.VolumeSteps + 50) / 100)
		return nil, nil
	case "GetMute":
		return []kv{{"CurrentMute", "0"}}, nil
	case "SetMute":
		return nil, nil // the device's mute is its microphones'; the music is turned down instead
	case "ListPresets":
		return []kv{{"CurrentPresetNameList", "FactoryDefaults"}}, nil
	case "SelectPreset":
		return nil, nil
	}
	return nil, errInvalidAction
}

// parseSong reads the song's description (DIDL-Lite) for what Now Playing shows.
func parseSong(uri, meta string) song {
	s := song{uri: uri, meta: meta}
	if meta == "" {
		return s
	}
	d := xml.NewDecoder(strings.NewReader(meta))
	var cur string
	for {
		tok, err := d.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			cur = t.Name.Local
			if cur == "res" {
				for _, a := range t.Attr {
					switch a.Name.Local {
					case "duration":
						if s.dur == 0 {
							s.dur = parseHMS(a.Value)
						}
					case "start":
						if a.Name.Space == startNS && s.start == 0 {
							s.start = parseHMS(a.Value)
						}
					case "protocolInfo":
						// http-get:*:video/mp4:*, the third of four.
						if parts := strings.Split(a.Value, ":"); len(parts) >= 3 && s.mime == "" {
							s.mime = strings.ToLower(parts[2])
						}
					}
				}
			}
		case xml.CharData:
			v := strings.TrimSpace(string(t))
			if v == "" {
				continue
			}
			switch cur {
			case "title":
				if s.title == "" {
					s.title = v
				}
			case "artist", "creator":
				if s.artist == "" {
					s.artist = v
				}
			case "album":
				if s.album == "" {
					s.album = v
				}
			case "albumArtURI":
				if s.art == "" {
					s.art = v
				}
			case "class":
				if s.class == "" {
					s.class = v
				}
			}
		case xml.EndElement:
			cur = ""
		}
	}
	return s
}

// isVideo is whether a song is a video: what its description calls it, else its content type, else
// what its address ends in.
func isVideo(s song) bool {
	switch c := strings.ToLower(s.class); {
	case strings.HasPrefix(c, "object.item.videoitem"):
		return true
	case strings.HasPrefix(c, "object.item.audioitem"), strings.HasPrefix(c, "object.item.imageitem"):
		return false
	}
	switch m := s.mime; {
	case strings.HasPrefix(m, "video/"), m == "application/vnd.apple.mpegurl", m == "application/x-mpegurl":
		return true
	case m != "" && m != "*":
		return false
	}
	u, err := url.Parse(s.uri)
	if err != nil {
		return false
	}
	switch strings.ToLower(path.Ext(u.Path)) {
	case ".mp4", ".m4v", ".mkv", ".mov", ".ts", ".m2ts", ".m3u8":
		return true
	}
	return false
}

// hms is a time as UPnP writes it: H:MM:SS.
func hms(d time.Duration) string {
	s := int(d / time.Second)
	return fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60)
}

// parseHMS reads H:MM:SS with or without a fraction, 0 for what is not one. A controller writes it,
// so a value that could overflow a time once multiplied out (hours past hmsMostHours, minutes or
// seconds of more than two digits, seconds that are no number: "Inf", "NaN", "1e300") is not one. A
// 75 for the minutes is taken as it always was: that is a controller's slip, not an overflow.
func parseHMS(v string) time.Duration {
	parts := strings.Split(strings.TrimSpace(v), ":")
	if len(parts) != 3 || len(parts[0]) > 6 || len(parts[1]) > 2 || len(parts[2]) > 16 {
		return 0
	}
	h, err1 := strconv.Atoi(parts[0])
	m, err2 := strconv.Atoi(parts[1])
	sec, err3 := strconv.ParseFloat(parts[2], 64)
	if err1 != nil || err2 != nil || err3 != nil || h < 0 || h > hmsMostHours || m < 0 ||
		math.IsNaN(sec) || math.IsInf(sec, 0) || sec < 0 || sec >= 100 {
		return 0
	}
	return time.Duration(h)*time.Hour + time.Duration(m)*time.Minute + time.Duration(sec*float64(time.Second))
}

// hmsMostHours bounds the hours parseHMS takes: longer than any song or film.
const hmsMostHours = 1000

func remoteHost(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// source is the decoded song as the speaker reads a received track: a deadline on a read closes the
// connection when it passes, which is how a server that stops sending ends the song. The deadline is for
// that read only: the speaker stops reading while it is paused or a reply is spoken, and a pause of any
// length must not end the song. The stream running out on its own is told to ended.
type source struct {
	io.Reader
	body  io.Closer
	ended func()

	mu    sync.Mutex
	timer *time.Timer
}

func newSource(pcm io.Reader, body io.Closer, ended func()) *source {
	return &source{Reader: pcm, body: body, ended: ended}
}

func (s *source) Read(p []byte) (int, error) {
	n, err := s.Reader.Read(p)
	s.mu.Lock()
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	s.mu.Unlock()
	if err == io.EOF && s.ended != nil {
		s.ended()
	}
	return n, err
}

func (s *source) Close() error {
	s.mu.Lock()
	if s.timer != nil {
		s.timer.Stop()
	}
	s.mu.Unlock()
	return s.body.Close()
}

func (s *source) SetReadDeadline(t time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	if !t.IsZero() {
		s.timer = time.AfterFunc(time.Until(t), func() { _ = s.body.Close() })
	}
	return nil
}
