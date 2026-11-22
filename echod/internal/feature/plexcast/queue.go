//go:build !dot

package plexcast

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// The play queue: what a controller sends with playMedia as containerKey (/playQueues/12?own=1), which
// is the list the item sits in. skipNext and skipPrevious walk it, and the timeline reports its number
// so the casting app shows the queue it made rather than a device with one item.

// next is the item delta places from the one playing in its queue, described and ready to start.
func (p *playback) next(ctx context.Context, req mediaRequest, it *item, delta int) (*item, error) {
	path := it.queue
	if i := strings.Index(path, "?"); i >= 0 {
		path = path[:i]
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	u := req.server() + path + "?X-Plex-Token=" + url.QueryEscape(req.token)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Accept", "application/xml")
	httpReq.Header.Set("X-Plex-Client-Identifier", clientIdentifier())
	resp, err := fetchClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the queue could not be read: %s", resp.Status)
	}
	var c containerXML
	if err := xml.NewDecoder(io.LimitReader(resp.Body, mostMetadata)).Decode(&c); err != nil {
		return nil, err
	}
	all := append(append([]mediaXML{}, c.Videos...), c.Tracks...)
	at := -1
	for i, m := range all {
		if m.RatingKey == it.ratingKey {
			at = i
			break
		}
	}
	if at < 0 {
		return nil, errors.New("the item is not in its queue")
	}
	to := at + delta
	if to < 0 || to >= len(all) {
		return nil, errors.New("the queue has no such item")
	}
	m := all[to]
	nreq := req
	nreq.key = cmpStr(m.Key, "/library/metadata/"+m.RatingKey)
	nreq.offset = 0
	return p.f.metadata(ctx, nreq)
}

// refreshPlayQueue re-reads the queue a controller has changed, so a skip later walks the new one. The
// item playing is unchanged.
func (f *Feature) refreshPlayQueue(w http.ResponseWriter, r *http.Request) {
	q, ok := readCommand(w, r)
	if !ok {
		return
	}
	queue := q.get("playQueueID")
	if queue != "" {
		f.pb.mu.Lock()
		f.pb.queueID = queue
		f.pb.mu.Unlock()
		f.tl.push()
	}
	agree(w)
}
