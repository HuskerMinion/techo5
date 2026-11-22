//go:build !dot

package plexcast

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"time"
)

// The Companion server: the port every Plex client is told, on which a casting app asks this device to
// play something and asks what it is doing. Every command is a GET with query parameters; success is
// 200 with an empty body, which is what Plex's own spec says and what its players answer.
func (f *Feature) buildMux() *http.ServeMux {
	m := http.NewServeMux()
	m.HandleFunc("/resources", f.resources)
	m.HandleFunc("/player/timeline/poll", f.poll)
	m.HandleFunc("/player/timeline/subscribe", f.subscribe)
	m.HandleFunc("/player/timeline/unsubscribe", f.unsubscribe)
	m.HandleFunc("/player/playback/playMedia", f.playMedia)
	m.HandleFunc("/player/playback/play", f.transport(transportPlay))
	m.HandleFunc("/player/playback/pause", f.transport(transportPause))
	m.HandleFunc("/player/playback/stop", f.transport(transportStop))
	m.HandleFunc("/player/playback/seekTo", f.seekTo)
	m.HandleFunc("/player/playback/stepForward", f.step(forward))
	m.HandleFunc("/player/playback/stepBack", f.step(back))
	m.HandleFunc("/player/playback/skipNext", f.skip(1))
	m.HandleFunc("/player/playback/skipPrevious", f.skip(-1))
	m.HandleFunc("/player/playback/setParameters", f.setParameters)
	m.HandleFunc("/player/playback/setStreams", f.setStreams)
	m.HandleFunc("/player/playback/refreshPlayQueue", f.refreshPlayQueue)
	// Navigation and the mirror's details page are capability names a controller may ask for and this
	// player does not offer. Answered rather than left to a 404, so a controller that asks anyway is
	// told no instead of looking at a broken player.
	for _, p := range []string{"/player/navigation/", "/player/mirror/", "/player/application/"} {
		m.HandleFunc(p, func(w http.ResponseWriter, r *http.Request) { agree(w) })
	}
	return m
}

// handler is everything the server answers, with what every request needs around it.
func (f *Feature) handler() http.Handler { return withCompanion(f.mux) }

// serve runs the Companion server until ctx ends.
func (f *Feature) serve(ctx context.Context) {
	ln, err := net.Listen("tcp", ":"+strconv.Itoa(Port))
	if err != nil {
		slog.Error("plex: the control port did not open", "port", Port, "err", err)
		<-ctx.Done()
		return
	}
	// No ReadTimeout: a timeline poll holds its request on purpose (timeline.go). The header timeout is
	// the whole defence, which is enough for commands that carry nothing but a query string.
	srv := &http.Server{Handler: f.handler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute}
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("plex: the control server stopped", "err", err)
		}
	}()
	slog.Info("plex: control server on", "port", Port)
	<-ctx.Done()
	_ = srv.Close()
}

// withCompanion is what every request needs and none of the handlers should have to remember:
//
//   - X-Plex-Target-Client-Identifier, when a controller sends one, must be this device. A command
//     meant for another player on the same network is not for us, and the spec says 404 rather than
//     403, so that is the answer;
//   - the CORS headers Plex Web needs to read an answer from a browser, including the exposed
//     X-Plex-Client-Identifier header: without it, Plex Web ignores every timeline and the cast looks
//     connected but frozen;
//   - this device's identifier on every answer, which is what a controller matches a timeline to.
func withCompanion(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "X-Plex-Client-Identifier,X-Plex-Target-Client-Identifier,X-Plex-Token,Content-Type")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,OPTIONS")
		w.Header().Set("Access-Control-Expose-Headers", "X-Plex-Client-Identifier")
		w.Header().Set("X-Plex-Client-Identifier", clientIdentifier())
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if target := r.Header.Get("X-Plex-Target-Client-Identifier"); target != "" && target != clientIdentifier() {
			http.Error(w, "not this player", http.StatusNotFound)
			return
		}
		h.ServeHTTP(w, r)
	})
}

// agree is a command carried out: 200 and nothing in it.
func agree(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
}

// fail is a command that could not be carried out, in the words a controller can show. The code is
// Plex's own (a 400 for a request that made no sense, a 500 for one that did), and the body is the
// Response element its clients parse.
func fail(w http.ResponseWriter, code int, said string) {
	w.Header().Set("Content-Type", `text/xml; charset="utf-8"`)
	w.WriteHeader(code)
	fmt.Fprintf(w, `<Response code="%d" status="%s"/>`, code, escape(said))
}

// readCommand is a command's query, and a method that can carry one. Plex's commands are GETs; a
// controller that POSTs one is answered rather than refused, since there is nothing to read in a body
// either way.
func readCommand(w http.ResponseWriter, r *http.Request) (query, bool) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		fail(w, http.StatusMethodNotAllowed, "commands are GETs")
		return query{}, false
	}
	return query{r: r}, true
}

// query is a command's parameters, with the conversions every command needs.
type query struct{ r *http.Request }

func (q query) get(name string) string { return q.r.URL.Query().Get(name) }

// millis is a parameter that is a number of milliseconds, as Plex's offsets and times are. A missing or
// unreadable one is zero, which every caller reads as "from the start".
func (q query) millis(name string) time.Duration {
	ms, err := strconv.ParseInt(q.get(name), 10, 64)
	if err != nil {
		return 0
	}
	return time.Duration(ms) * time.Millisecond
}

// number is a parameter that is a whole number, or def when it is missing or not one.
func (q query) number(name string, def int) int {
	n, err := strconv.Atoi(q.get(name))
	if err != nil {
		return def
	}
	return n
}

// resources answers what this player is. Controllers use it as the liveness probe -- a player on the
// network is one that answers it -- and Plex's own clients read the capabilities from it.
func (f *Feature) resources(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", `text/xml; charset="utf-8"`)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.WriteString(w, `<MediaContainer size="1">`+
		`<Player title="`+escape(friendlyName())+`" machineIdentifier="`+escape(clientIdentifier())+`"`+
		` product="`+product+`" version="`+escape(version())+`" platform="`+platform+`"`+
		` deviceClass="`+deviceClass+`" protocol="plex"`+
		` protocolVersion="`+strconv.Itoa(protocolVersion)+`"`+
		` protocolCapabilities="`+protocolCaps+`"/>`+
		`</MediaContainer>`)
}
