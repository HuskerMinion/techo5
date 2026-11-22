//go:build !dot

package plexcast

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/lib/netguard"
)

// What a Plex server says a piece of media is: the item's names (for Now Playing), how long it runs,
// and -- for a film -- its parts and the streams in them, which is what the transcode is asked about
// and what a controller's stream choice rewrites on the server.

// item is one piece of media, as far as this player needs it.
type item struct {
	ratingKey string
	key       string // the metadata path, /library/metadata/1234
	typ       string // movie, episode, track
	title     string
	artist    string // the artist of a song, or the show of an episode
	album     string
	thumb     string // cover art's path on the server
	dur       time.Duration
	part      part
	video     bool

	// queue is the play queue the controller sent with the item, when it sent one: /playQueues/12?own=1.
	queue string
}

// part is the file the item plays from: its id (which a stream choice rewrites), its path (which a
// direct play fetches), and its streams.
type part struct {
	id         int
	key        string
	container  string
	videoCodec string
	audioCodec string
	dur        time.Duration
	streams    []stream
}

// stream is an audio, video or subtitle track in a part. Plex numbers the kinds: 1 video, 2 audio,
// 3 subtitle.
type stream struct {
	id       int
	kind     int
	codec    string
	language string
	selected bool
}

const (
	streamVideo    = 1
	streamAudio    = 2
	streamSubtitle = 3
)

// audioStream and subtitleStream are the ones selected, 0 for none. A film with subtitles picked in the
// casting app has them burned into its picture (transcode.go); a song has neither.
func (p part) audioStream() int {
	for _, s := range p.streams {
		if s.kind == streamAudio && s.selected {
			return s.id
		}
	}
	return 0
}

func (p part) subtitleStream() int {
	for _, s := range p.streams {
		if s.kind == streamSubtitle && s.selected {
			return s.id
		}
	}
	return 0
}

// playable is whether the device's decoder can be handed the file as it is: the three formats
// feature/media decodes from a stream. Anything else is transcoded to MP3 by the server.
func (p part) playable() bool {
	switch strings.ToLower(p.audioCodec) {
	case "mp3", "flac", "wav", "pcm":
		return true
	}
	switch strings.ToLower(p.container) {
	case "mp3", "flac", "wav":
		return true
	}
	return false
}

// The XML the server answers /library/metadata with. Only what this player reads is named; everything
// else in an answer is ignored, which is what keeps a server's new fields from breaking the device.
type containerXML struct {
	Videos []mediaXML `xml:"Video"`
	Tracks []mediaXML `xml:"Track"`
}

type mediaXML struct {
	RatingKey        string `xml:"ratingKey,attr"`
	Key              string `xml:"key,attr"`
	Type             string `xml:"type,attr"`
	Title            string `xml:"title,attr"`
	GrandparentTitle string `xml:"grandparentTitle,attr"`
	ParentTitle      string `xml:"parentTitle,attr"`
	Thumb            string `xml:"thumb,attr"`
	Duration         int64  `xml:"duration,attr"`
	Media            []struct {
		ID         int       `xml:"id,attr"`
		Duration   int64     `xml:"duration,attr"`
		Container  string    `xml:"container,attr"`
		VideoCodec string    `xml:"videoCodec,attr"`
		AudioCodec string    `xml:"audioCodec,attr"`
		Parts      []partXML `xml:"Part"`
	} `xml:"Media"`
}

type partXML struct {
	ID        int         `xml:"id,attr"`
	Key       string      `xml:"key,attr"`
	Container string      `xml:"container,attr"`
	Duration  int64       `xml:"duration,attr"`
	Streams   []streamXML `xml:"Stream"`
}

type streamXML struct {
	ID         int    `xml:"id,attr"`
	StreamType int    `xml:"streamType,attr"`
	Codec      string `xml:"codec,attr"`
	Language   string `xml:"language,attr"`
	Selected   bool   `xml:"selected,attr"`
}

// fetchClient fetches from the Plex server a controller named: quick to connect and answer, then no
// limit, since a film arrives over minutes. A controller is anybody on the network, so the device
// never connects to itself, to link-local or multicast addresses, or to nowhere, whatever name or
// redirect leads there -- the same rule DLNA's renderer has, for the same reason.
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

// refuseOwn refuses a connection to an address that is the device itself, link-local or multicast.
func refuseOwn(network, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if ip == nil || ip.IsLoopback() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsMulticast() || netguard.OwnIP(ip) {
		return fmt.Errorf("plex: not fetching from %s", host)
	}
	return nil
}

// metadata fetches an item's description: its names, its length, and its parts.
func (f *Feature) metadata(ctx context.Context, req mediaRequest) (*item, error) {
	u := req.server() + req.key + "?X-Plex-Token=" + url.QueryEscape(req.token)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Accept", "application/xml")
	httpReq.Header.Set("X-Plex-Client-Identifier", clientIdentifier())
	httpReq.Header.Set("X-Plex-Token", req.token)
	resp, err := fetchClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the server answered %s", resp.Status)
	}
	var c containerXML
	if err := xml.NewDecoder(io.LimitReader(resp.Body, mostMetadata)).Decode(&c); err != nil {
		return nil, fmt.Errorf("the server's description was not readable: %w", err)
	}
	all := append(append([]mediaXML{}, c.Videos...), c.Tracks...)
	if len(all) == 0 {
		return nil, errors.New("the server described nothing")
	}
	m := all[0]
	it := &item{
		ratingKey: m.RatingKey,
		key:       cmpStr(m.Key, req.key),
		typ:       m.Type,
		title:     m.Title,
		artist:    cmpStr(m.GrandparentTitle, m.ParentTitle),
		album:     m.ParentTitle,
		thumb:     m.Thumb,
		dur:       time.Duration(m.Duration) * time.Millisecond,
		queue:     req.container,
		video:     len(c.Videos) > 0,
	}
	if it.artist == it.album {
		it.artist = m.GrandparentTitle
	}
	if len(m.Media) > 0 {
		md := m.Media[0]
		it.part.container = md.Container
		it.part.videoCodec = md.VideoCodec
		it.part.audioCodec = md.AudioCodec
		it.part.dur = time.Duration(md.Duration) * time.Millisecond
		if len(md.Parts) > 0 {
			p := md.Parts[0]
			it.part.id = p.ID
			it.part.key = p.Key
			if p.Container != "" {
				it.part.container = p.Container
			}
			if p.Duration > 0 {
				it.part.dur = time.Duration(p.Duration) * time.Millisecond
			}
			for _, s := range p.Streams {
				it.part.streams = append(it.part.streams, stream{
					id: s.ID, kind: s.StreamType, codec: s.Codec, language: s.Language, selected: s.Selected,
				})
			}
		}
	}
	if it.dur == 0 {
		it.dur = it.part.dur
	}
	return it, nil
}

// mostMetadata bounds an item's description: a few kilobytes of names and parts.
const mostMetadata = 1 << 20

// cmpStr is a when it is set, and b otherwise.
func cmpStr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// escape is text for an XML attribute or element body.
func escape(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}
