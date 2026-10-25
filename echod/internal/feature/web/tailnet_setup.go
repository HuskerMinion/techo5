package web

import (
	"net"
	"net/http"
	"strings"
)

// RemoteSetup is whether the setup page may be reached from away over the tailnet now (feature/setup's
// Remote setup): the one exception to tailnetGate, and only for the page's own paths.
var RemoteSetup = func() bool { return false }

// remoteSetupRequest is whether a request from away is the setup page's while Remote setup is
// on. It has to ask for the device by its address: a name could be a site of somebody else's pointed at
// the device (DNS rebinding), with its script on the page once a browser was let in.
func remoteSetupRequest(r *http.Request) bool {
	return setupPath(r.URL.Path) && hostIsAddress(r.Host) && RemoteSetup()
}

// setupPath is whether a path is the setup page's own.
func setupPath(p string) bool { return p == "/setup" || strings.HasPrefix(p, "/setup/") }

// hostIsAddress is whether a Host header is an IP address, with or without a port. None at all is not:
// every browser sends one.
func hostIsAddress(host string) bool {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host, _, _ = strings.Cut(strings.Trim(host, "[]"), "%")
	return net.ParseIP(host) != nil
}
