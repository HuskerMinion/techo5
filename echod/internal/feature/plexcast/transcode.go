//go:build !dot

package plexcast

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// capProfile is what this device's decoder takes, said to the Plex server as a profile it may add to its
// own. The server then chooses a stream that fits -- a film bigger than this is sent downscaled rather
// than in frames the device would drop. It is 720p, and 1080p on the Skylight Calendar, whose screen is
// its full size and whose cores are faster (transcode_board.go); what a decoder of a given size takes
// is written once, in capProfileAt.
var capProfile = capProfileAt(1280, 720)

// capProfileAt is that profile for a decoder that takes pictures up to w by h: H.264, with AAC, MP3 or
// AC-3 sound, in an MPEG-TS for HLS.
//
// The syntax is Plex's own ("profile augmentations"): directives joined by "+", each a verb with
// parenthesised arguments.
func capProfileAt(w, h int) string {
	return "add-transcode-target(type=videoProfile&context=streaming&protocol=hls" +
		"&container=mpegts&videoCodec=h264&audioCodec=aac,mp3,ac3&subtitleCodec=srt&replace=true)" +
		fmt.Sprintf("+add-limitation(scope=videoCodec&scopeName=*&type=upperBound&name=video.width&value=%d&replace=true)", w) +
		fmt.Sprintf("+add-limitation(scope=videoCodec&scopeName=*&type=upperBound&name=video.height&value=%d&replace=true)", h) +
		"+add-limitation(scope=videoCodec&scopeName=*&type=upperBound&name=video.bitDepth&value=8&replace=true)"
}

// videoURL is a film's stream: the server decides what to send, then it is asked for it. Both calls use
// one session id, because a start without a decision for the same session is refused by current Plex
// servers with a 400 that looks like a broken film.
//
// Subtitles are burned into the picture rather than sent as a separate track: nothing on this device
// draws a text overlay over a video, and a burned caption is the one that is legible on a screen held
// at arm's length. offset is seconds on the transcoder; Plex's own offsets are milliseconds.
func (p *playback) videoURL(ctx context.Context, req mediaRequest, it *item, offset time.Duration, subtitles bool, session string) (string, error) {
	q := url.Values{
		"path":                        {it.key},
		"mediaIndex":                  {"0"},
		"partIndex":                   {"0"},
		"protocol":                    {"hls"},
		"directPlay":                  {"0"},
		"directStream":                {"1"},
		"fastSeek":                    {"1"},
		"copyts":                      {"1"},
		"offset":                      {strconv.Itoa(int(offset / time.Second))},
		"session":                     {session},
		"X-Plex-Client-Identifier":    {clientIdentifier()},
		"X-Plex-Session-Identifier":   {session},
		"X-Plex-Platform":             {platform},
		"X-Plex-Client-Profile-Name":  {"generic"},
		"X-Plex-Client-Profile-Extra": {capProfile},
		"X-Plex-Token":                {req.token},
	}
	if a := it.part.audioStream(); a != 0 {
		q.Set("audioStreamID", strconv.Itoa(a))
	}
	if subtitles {
		q.Set("subtitles", "burn")
		q.Set("advancedSubtitles", "burn")
	}
	if s := it.part.subtitleStream(); subtitles && s != 0 {
		q.Set("subtitleStreamID", strconv.Itoa(s))
	}
	base := req.server()
	if err := decide(ctx, base+"/video/:/transcode/universal/decision?"+q.Encode(), req.token); err != nil {
		return "", err
	}
	return base + "/video/:/transcode/universal/start.m3u8?" + q.Encode(), nil
}

// decide asks the server what it will send, and is answered with a MediaContainer describing the
// decision. The answer is read and dropped: what matters is that the server recorded a decision for
// the session, which the start after it is checked against.
func decide(ctx context.Context, u, token string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Plex-Client-Identifier", clientIdentifier())
	req.Header.Set("X-Plex-Token", token)
	req.Header.Set("Accept", "application/xml")
	resp, err := fetchClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, mostMetadata))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("the server would not decide on a stream: %s", resp.Status)
	}
	return nil
}

// stopTranscode ends a stream the server is making for this device, so a film that was replaced does
// not keep a core busy on the server. Best effort: a session that has already gone is not an error
// worth saying.
func stopTranscode(ctx context.Context, req mediaRequest, session string) {
	if session == "" {
		return
	}
	q := url.Values{"session": {session}, "X-Plex-Token": {req.token}}
	u := req.server() + "/video/:/transcode/universal/stop?" + q.Encode()
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return
	}
	r.Header.Set("X-Plex-Client-Identifier", clientIdentifier())
	if resp, err := fetchClient.Do(r); err == nil {
		resp.Body.Close()
	}
}
