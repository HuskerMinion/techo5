package media

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// station serves a station: it never ends, so a request to it lasts until the reader goes away or the
// test is over. start, if not nil, is what it sends first.
//
// The test being over is a bound of its own, and closing the server drops every connection before it
// waits for them. A handler that waited only for its reader would hold the server's Close for as long as
// the reader stays: a test that fails before stopping its stream, or a reader left over from another
// test that dialed a port this server was given after that test's own server had gone, kept Close
// waiting until the whole run timed out (v1.6.53, TestAStationStartedDuringAPausedTurnPlaysAfterIt).
func station(t *testing.T, start func(w http.ResponseWriter)) string {
	t.Helper()
	done := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if start != nil {
			start(w)
		}
		select {
		case <-r.Context().Done():
		case <-done:
		}
	}))
	t.Cleanup(func() {
		close(done)
		srv.CloseClientConnections()
		srv.Close()
	})
	return srv.URL
}
