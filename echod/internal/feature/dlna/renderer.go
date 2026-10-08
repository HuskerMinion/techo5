package dlna

import (
	"bufio"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log/slog"
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
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
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
}

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

	// videoID is the video feature/video is playing for the current song, when it is a video.
	videoID uint64
}

// firstBytes is how long a server has to start sending the song once it has answered.
const firstBytes = 15 * time.Second

// fetchClient fetches songs and covers from the home network: quick to connect and answer, then no
// limit, since a song plays for minutes. A controller names the address, and any host on the network
// can be a controller, so the device never connects to itself (loopback), to link-local or multicast
// addresses, or to nowhere, whatever name, redirect or DNS answer leads there: refused when the
// connection is made, so redirects and rebinding are covered too.
var fetchClient = &http.Client{
	Transport: &http.Transport{
		DialContext:           (&net.Dialer{Timeout: 5 * time.Second, Control: refuseOwn}).DialContext,
		ResponseHeaderTimeout: 10 * time.Second,
		MaxIdleConns:          4,
		IdleConnTimeout:       30 * time.Second,
	},
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

// ownAddress is whether ip is one of the device's own interface addresses. Read at each connection:
// the address can change with the network.
func ownAddress(ip net.IP) bool {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return true // not knowing its own addresses, the device fetches nothing rather than maybe itself
	}
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.Equal(ip) {
			return true
		}
	}
	return false
}

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
		return nil, errNotSupported
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
		}
		return []kv{{"Track", track}, {"TrackDuration", hms(dur)}, {"TrackMetaData", r.cur.meta}, {"TrackURI", r.cur.uri},
			{"RelTime", hms(pos)}, {"AbsTime", "NOT_IMPLEMENTED"}, {"RelCount", "2147483647"}, {"AbsCount", "2147483647"}}, nil
	case "GetMediaInfo":
		r.sync()
		r.mu.Lock()
		defer r.mu.Unlock()
		n := "0"
		if r.cur.uri != "" {
			n = "1"
		}
		return []kv{{"NrTracks", n}, {"MediaDuration", hms(r.cur.dur)}, {"CurrentURI", r.cur.uri}, {"CurrentURIMetaData", r.cur.meta},
			{"NextURI", r.next.uri}, {"NextURIMetaData", r.next.meta}, {"PlayMedium", "NETWORK"},
			{"RecordMedium", "NOT_IMPLEMENTED"}, {"WriteStatus", "NOT_IMPLEMENTED"}}, nil
	case "GetDeviceCapabilities":
		return []kv{{"PlayMedia", "NETWORK"}, {"RecMedia", "NOT_IMPLEMENTED"}, {"RecQualityModes", "NOT_IMPLEMENTED"}}, nil
	case "GetTransportSettings":
		return []kv{{"PlayMode", "NORMAL"}, {"RecQualityMode", "NOT_IMPLEMENTED"}}, nil
	case "GetCurrentTransportActions":
		actions := "Play"
		switch r.sync() {
		case stPlaying:
			actions = "Pause,Stop"
		case stPaused:
			actions = "Play,Stop"
		}
		return []kv{{"Actions", actions}}, nil
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
	if r.state == stPlaying {
		r.mu.Unlock()
		return nil
	}
	r.gen++
	gen, s := r.gen, r.cur
	r.state, r.ended = stLoading, false
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
	safe.Go("dlna song", func() { r.start(ctx, gen, s) })
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

// start fetches the song and hands it to the speaker.
func (r *renderer) start(ctx context.Context, gen int, s song) {
	// A server that answers and then sends nothing is given up on, rather than left TRANSITIONING.
	ctx, giveUp := context.WithCancel(ctx)
	slow := time.AfterFunc(firstBytes, giveUp)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.uri, nil)
	if err != nil {
		slow.Stop()
		giveUp()
		return
	}
	resp, err := fetchClient.Do(req)
	if err == nil && resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		err = fmt.Errorf("the server answered %s", resp.Status)
	}
	var pcm io.Reader
	if err == nil {
		pcm, err = media.DecodeStream(bufio.NewReaderSize(resp.Body, 64<<10), resp.Header.Get("Content-Type"))
		if err != nil {
			resp.Body.Close()
		}
	}
	slow.Stop() // the song is coming (or has failed): no more waiting on its first bytes
	r.mu.Lock()
	if r.gen != gen {
		r.mu.Unlock()
		if err == nil {
			resp.Body.Close()
		}
		return
	}
	if err != nil {
		r.state = stStopped
		r.mu.Unlock()
		slog.Warn("dlna: the song could not be played", "title", s.title, "err", err)
		r.f.e.changed()
		return
	}
	r.mu.Unlock()

	// The song's picture is cleared until the new one arrives, so the last song's is not left on it.
	home.ReceivedPicture(home.DLNAName, nil)
	src := newSource(pcm, resp.Body, func() {
		r.mu.Lock()
		if r.gen == gen {
			r.endedGen = gen
		}
		r.mu.Unlock()
	})
	media.Get().PlayReceived(home.DLNAName, src, speaker.Rate, 2)
	media.Get().SetReceivedTrack(home.DLNAName, s.title, s.artist, s.album)

	r.mu.Lock()
	if r.gen != gen {
		// Stopped or replaced while it was being started: it does not get to keep the speaker.
		r.mu.Unlock()
		if media.Get().Receiving() == home.DLNAName {
			media.Get().Stop()
		}
		return
	}
	r.state, r.started, r.pausedAt, r.pausedFor = stPlaying, time.Now(), time.Time{}, 0
	r.publishPositionLocked(1)
	r.mu.Unlock()
	safe.Go("dlna cover", func() { r.cover(gen, s.art) })
	r.f.e.changed()
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
	if r.sync() != stPlaying {
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
	r.ended = false
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
	r.mu.Unlock()
}

// sync brings the state in line with the speaker: a song that is no longer the one playing ended, or
// was replaced; one paused or resumed on the device's own screen is paused or playing. It returns the
// state.
func (r *renderer) sync() string {
	ours := media.Get().Receiving() == home.DLNAName
	_, paused := media.Get().ScreenState()
	vs := video.Current()
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
	case r.state == stPlaying && ours && paused:
		r.state, r.pausedAt = stPaused, time.Now()
		r.publishPositionLocked(0)
	case r.state == stPaused && ours && !paused:
		r.pausedFor += time.Since(r.pausedAt)
		r.state, r.pausedAt = stPlaying, time.Time{}
		r.publishPositionLocked(1)
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
	vs := video.Current()
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

// positionLocked is how far into the song it is, wants r.mu.
func (r *renderer) positionLocked() time.Duration {
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

// parseHMS reads H:MM:SS with or without a fraction.
func parseHMS(v string) time.Duration {
	parts := strings.Split(strings.TrimSpace(v), ":")
	if len(parts) != 3 {
		return 0
	}
	h, err1 := strconv.Atoi(parts[0])
	m, err2 := strconv.Atoi(parts[1])
	sec, err3 := strconv.ParseFloat(parts[2], 64)
	if err1 != nil || err2 != nil || err3 != nil || h < 0 || m < 0 || sec < 0 {
		return 0
	}
	return time.Duration(h)*time.Hour + time.Duration(m)*time.Minute + time.Duration(sec*float64(time.Second))
}

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
