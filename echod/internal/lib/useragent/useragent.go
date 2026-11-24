// Package useragent names the device to the servers it fetches media from.
//
// Go's own User-Agent ("Go-http-client/1.1") is not neutral: some station networks take it for a
// scraper and answer it differently. The regiocast/streamabc network behind most German private radio
// sends it a fixed clip of about ten seconds and then nothing, while any other name gets the live
// stream (#730). A request that names the device is not treated that way, and the name says what the
// device is to anyone reading their logs.
package useragent

import (
	"net/http"

	"github.com/HuskerMinion/techo5/echod/internal/layout"
)

// String is the User-Agent the device sends: its name, the version running, and where it comes from.
func String() string {
	return "TECHO5/" + layout.Version + " (+https://github.com/HuskerMinion/techo5)"
}

// Transport wraps base (http.DefaultTransport when nil) so every request it carries names the device,
// unless the request already says who it is. It is the client's transport, not the first request, that
// sets it, so the request for a redirect, which Go makes fresh, carries it too.
func Transport(base http.RoundTripper) http.RoundTripper {
	return &transport{base: base}
}

type transport struct{ base http.RoundTripper }

func (t *transport) next() http.RoundTripper {
	if t.base == nil {
		// Looked up at each request, not when the client was made, so a test that swaps the default
		// transport is still the one used.
		return http.DefaultTransport
	}
	return t.base
}

func (t *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Header.Get("User-Agent") != "" {
		return t.next().RoundTrip(req)
	}
	// A RoundTripper may not change the request it was given: the caller still owns it, and may send
	// it again.
	out := req.Clone(req.Context())
	out.Header.Set("User-Agent", String())
	return t.next().RoundTrip(out)
}

// CloseIdleConnections passes through, so a client's CloseIdleConnections still reaches the pool
// underneath.
func (t *transport) CloseIdleConnections() {
	if c, ok := t.next().(interface{ CloseIdleConnections() }); ok {
		c.CloseIdleConnections()
	}
}
