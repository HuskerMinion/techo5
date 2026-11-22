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
)

// What the Plex server is told, which is what puts the device on the server's Now Playing page and
// keeps its position, so a film started on the Show can be finished on a phone. It is a different
// timeline from the one controllers read: parameters here rather than XML, and both key and ratingKey,
// because a report with only one of them is answered 200 and quietly does nothing.
//
// The cadence is Plex's: on every change, and every ten seconds while it plays.

// report tells the server where the device is. A report that fails is not retried: the next one, ten
// seconds on, says the same thing about a later moment.
func (p *playback) report() {
	p.mu.Lock()
	if p.req.address == "" || p.it == nil || p.session == "" {
		p.mu.Unlock()
		return
	}
	req, it, session := p.req, p.it, p.session
	pos, state := p.atLocked(time.Now()), p.state
	p.reported = time.Now()
	p.mu.Unlock()

	switch state {
	case statePlaying, statePaused, stateStopped, stateBuffering, stateError:
	default:
		state = stateStopped
	}
	q := url.Values{
		"key":        {it.key},
		"ratingKey":  {it.ratingKey},
		"state":      {state},
		"time":       {strconv.FormatInt(pos.Milliseconds(), 10)},
		"duration":   {strconv.FormatInt(it.dur.Milliseconds(), 10)},
		"identifier": {"com.plexapp.plugins.library"},
	}
	if it.queue != "" {
		q.Set("containerKey", it.queue)
	}
	if _, item := queueIDs(it.queue); item != "" {
		q.Set("playQueueItemID", item)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		req.server()+"/:/timeline?"+q.Encode(), nil)
	if err != nil {
		return
	}
	httpReq.Header.Set("X-Plex-Client-Identifier", clientIdentifier())
	httpReq.Header.Set("X-Plex-Session-Identifier", session)
	httpReq.Header.Set("X-Plex-Token", req.token)
	httpReq.Header.Set("X-Plex-Platform", platform)
	resp, err := fetchClient.Do(httpReq)
	if err != nil {
		slog.Debug("plex: the server was not told where the device is", "err", err)
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	resp.Body.Close()
}

// tick is the clock: it keeps the state true to the device's own players, pushes the timeline while
// something plays, and reports to the server every ten seconds.
func (p *playback) tick() {
	p.sync()
	p.mu.Lock()
	playing := p.state == statePlaying
	due := playing && time.Since(p.reported) >= reportEvery
	p.mu.Unlock()
	if !playing {
		return
	}
	p.f.tl.push()
	if due {
		go p.report()
	}
}
