//go:build !dot

package plexcast

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
)

// The two commands that change something rather than move it: the volume and the shuffle, and which
// sound and which subtitles.
//
// The volume is the device's own -- the phone's slider and the speaker's buttons are one control, as
// they are for Spotify Connect (#312) -- and a stream choice is remembered by the Plex server as well
// as acted on here: an audio track cannot be switched inside an HLS stream, so the film is restarted at
// the same second with the other track, which is what Plex's own players do.

// setParameters is the volume, shuffle and repeat.
func (f *Feature) setParameters(w http.ResponseWriter, r *http.Request) {
	q, ok := readCommand(w, r)
	if !ok {
		return
	}
	if v := q.get("volume"); v != "" {
		pct, err := strconv.Atoi(v)
		if err != nil || pct < 0 || pct > 100 {
			fail(w, http.StatusBadRequest, "volume is 0-100")
			return
		}
		if err := config.Set().Speaker().Volume(volumeSteps(pct)); err != nil {
			slog.Warn("plex: saving the volume failed", "err", err)
		}
	}
	shuffle, repeat := q.number("shuffle", -1), q.number("repeat", -1)
	if shuffle >= 0 || repeat >= 0 {
		f.pb.mu.Lock()
		if shuffle >= 0 {
			f.pb.shuffle = min(max(shuffle, 0), 1)
		}
		if repeat >= 0 {
			f.pb.repeat = min(max(repeat, 0), 2)
		}
		f.pb.mu.Unlock()
		f.tl.push()
	}
	agree(w)
}

// setStreams chooses the sound and the subtitles, -1 for "as it is" and 0 for none of that kind.
func (f *Feature) setStreams(w http.ResponseWriter, r *http.Request) {
	q, ok := readCommand(w, r)
	if !ok {
		return
	}
	if err := f.pb.setStreams(q.number("audioStreamID", -1), q.number("subtitleStreamID", -1)); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	agree(w)
}

func (p *playback) setStreams(audio, subtitle int) error {
	p.mu.Lock()
	req, it := p.req, p.it
	if it == nil {
		p.mu.Unlock()
		return errNothingPlaying
	}
	// Under the lock: the start below reads the same streams to ask for the other track.
	if audio >= 0 {
		it.part.selectStream(streamAudio, audio)
	}
	if subtitle >= 0 {
		it.part.selectStream(streamSubtitle, subtitle)
	}
	p.mu.Unlock()
	// The server records the choice for this user, so a resume starts with what was picked. It is done
	// on its own goroutine: the restart below is what the controller waits for.
	if it.part.id != 0 {
		partID := it.part.id
		safe.Go("plex stream choice", func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			putPart(ctx, req, partID, audio, subtitle)
		})
	}
	req.offset = p.at()
	return p.start(req, it)
}

// selectStream marks one stream of a kind chosen and the others not, which is how the transcode asks
// for the right one. 0 chooses none of that kind (subtitles off).
func (p *part) selectStream(kind, id int) {
	for i := range p.streams {
		if p.streams[i].kind != kind {
			continue
		}
		p.streams[i].selected = id != 0 && p.streams[i].id == id
	}
}

// putPart tells the Plex server which streams were chosen. allParts is on, as Plex's own clients send
// it: a library item can have more than one file, and the choice belongs to the whole item.
func putPart(ctx context.Context, req mediaRequest, partID, audio, subtitle int) {
	q := url.Values{"allParts": {"1"}, "X-Plex-Token": {req.token}}
	if audio >= 0 {
		q.Set("audioStreamID", strconv.Itoa(audio))
	}
	if subtitle >= 0 {
		q.Set("subtitleStreamID", strconv.Itoa(subtitle))
	}
	u := req.server() + "/library/parts/" + strconv.Itoa(partID) + "?" + q.Encode()
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPut, u, nil)
	if err != nil {
		return
	}
	httpReq.Header.Set("X-Plex-Client-Identifier", clientIdentifier())
	httpReq.Header.Set("X-Plex-Token", req.token)
	resp, err := fetchClient.Do(httpReq)
	if err != nil {
		slog.Debug("plex: the server was not told about the stream choice", "err", err)
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		slog.Debug("plex: the server refused the stream choice", "status", resp.Status)
	}
}
