package diag

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The switch has to reach everything the device downloads, which is everything that goes through the
// default transport — so this is the default transport, and it is in place before anything runs.
func TestTheDefaultTransportIsTheSwitch(t *testing.T) {
	if http.DefaultTransport != http.RoundTripper(transport) {
		t.Fatal("the default transport is not the one the switch works")
	}
}

// A server with a certificate nothing signed stands in for whatever the device is being pointed at
// while somebody is debugging. It is refused until the switch is on, taken while it is, and refused
// again once it is off — and the connections made while it was on do not outlive it.
func TestCertificatesAreCheckedUnlessTheSwitchIsOn(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "hello")
	}))
	defer srv.Close()

	t.Cleanup(func() { insecureTLS(false) })

	if _, err := http.Get(srv.URL); err == nil {
		t.Error("a certificate nothing signed was accepted with the switch off")
	}

	insecureTLS(true)
	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("with the switch on: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	insecureTLS(false)
	if _, err := http.Get(srv.URL); err == nil {
		t.Error("certificates were still not being checked after the switch went off")
	}
}
