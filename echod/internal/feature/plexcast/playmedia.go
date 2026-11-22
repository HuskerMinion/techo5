//go:build !dot

package plexcast

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// playMedia is how a casting app starts something: where the item is on a Plex server, what it is, and
// where in it to start. The device fetches nothing until it has asked the server what it should be
// given (transcode.go), because what plays well on a Show is not what a server sends by default.

// mediaRequest is a controller's playMedia, and the server's coordinates for every later call about the
// item: the token is the transient one Plex hands a controller for exactly this, so it is used for the
// description, the stream and the reports alike.
type mediaRequest struct {
	protocol  string
	address   string
	port      string
	key       string
	token     string
	machine   string
	container string
	typ       string
	offset    time.Duration
}

// server is the Plex server as an address: what every request about this item is made against.
func (m mediaRequest) server() string {
	return m.protocol + "://" + net.JoinHostPort(m.address, m.port)
}

// check is whether a controller's request is one this device may act on. The address is a host or an
// address and nothing else, the protocol is one that can be fetched, and the key is a path on that
// server: a request that says otherwise is refused before anything connects anywhere.
func (m mediaRequest) check() error {
	if m.protocol != "http" && m.protocol != "https" {
		return errors.New("the server's protocol is not http or https")
	}
	if m.address == "" || strings.ContainsAny(m.address, "/?#@\\ \t\r\n") {
		return errors.New("the server has no address")
	}
	if port, err := strconv.Atoi(m.port); err != nil || port < 1 || port > 65535 {
		return errors.New("the server has no port")
	}
	if !strings.HasPrefix(m.key, "/") || strings.ContainsAny(m.key, " \t\r\n") {
		return errors.New("the item has no path")
	}
	if m.machine == "" {
		return errors.New("the server is not named")
	}
	return nil
}

// playMedia is the handler: read the request, describe the item, and start it. The answer is 200 as
// soon as it is accepted; what the device is doing is the timeline's job, which is where Plex's own
// players put it too.
func (f *Feature) playMedia(w http.ResponseWriter, r *http.Request) {
	q, ok := readCommand(w, r)
	if !ok {
		return
	}
	req := mediaRequest{
		protocol:  cmpStr(q.get("protocol"), "http"),
		address:   q.get("address"),
		port:      cmpStr(q.get("port"), "32400"),
		key:       q.get("key"),
		token:     q.get("token"),
		machine:   q.get("machineIdentifier"),
		container: q.get("containerKey"),
		typ:       q.get("type"),
		offset:    q.millis("offset"),
	}
	if err := req.check(); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	it, err := f.metadata(ctx, req)
	if err != nil {
		slog.Warn("plex: the item could not be described", "key", req.key, "err", err)
		fail(w, http.StatusInternalServerError, "the server could not describe this item")
		return
	}
	if err := f.pb.start(req, it); err != nil {
		slog.Warn("plex: the item could not be started", "title", it.title, "err", err)
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	slog.Info("plex: playing", "title", it.title, "type", it.typ, "offset", req.offset)
	agree(w)
}

// audioURL is where a song is fetched from. A file the device decodes as it is comes straight from the
// server; anything else is asked for as MP3, which the same decoder plays, rather than leaving a
// library of AAC or ALAC silent.
func (p *playback) audioURL(req mediaRequest, it *item, offset time.Duration, session string) string {
	token := url.QueryEscape(req.token)
	if it.part.playable() && it.part.key != "" {
		return req.server() + it.part.key + "?X-Plex-Token=" + token
	}
	q := url.Values{
		"path":                      {it.key},
		"protocol":                  {"http"},
		"offset":                    {strconv.Itoa(int(offset / time.Second))},
		"session":                   {session},
		"X-Plex-Client-Identifier":  {clientIdentifier()},
		"X-Plex-Session-Identifier": {session},
		"X-Plex-Platform":           {platform},
		"X-Plex-Token":              {req.token},
	}
	return req.server() + "/music/:/transcode/universal/start.mp3?" + q.Encode()
}
