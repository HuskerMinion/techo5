//go:build !dot

package dashboard

import (
	"errors"
	"net"
	"strconv"
	"strings"
)

// defaultPort is dashcast's own, from its docker-compose.yml.
const defaultPort = "9555"

// NormalizeServer is the dashcast server's address as host:port, from however it was typed. People
// paste what a browser shows - "https://dashcast.example.lan:9555/" - and that was kept as it was,
// dialed as it was, and failed with nothing on the screen to say why (techo5#26). So a scheme, a
// user and a path are dropped, the port is dashcast's own when none is given, and anything that is
// still not a host and a port is refused. Empty stays empty: it is how the server is cleared.
func NormalizeServer(addr string) (string, error) {
	s := strings.TrimSpace(addr)
	if s == "" {
		return "", nil
	}
	if _, rest, ok := strings.Cut(s, "://"); ok {
		s = rest
	}
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	if i := strings.LastIndex(s, "@"); i >= 0 {
		s = s[i+1:]
	}

	host, port, err := net.SplitHostPort(s)
	if err != nil {
		// No port: a name, an IPv4 address, or an IPv6 address in brackets.
		host, port = strings.TrimSuffix(strings.TrimPrefix(s, "["), "]"), defaultPort
		if strings.Contains(host, ":") && net.ParseIP(host) == nil {
			return "", errors.New("not an address: write it as host:port, with an IPv6 address in brackets")
		}
	}
	if !validHost(host) {
		return "", errors.New("not an address: write it as host:port, like 192.168.1.20:9555")
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return "", errors.New("not a port number: " + port)
	}
	return net.JoinHostPort(host, port), nil
}

// validHost is an IP address or a host name made of letters, digits, dots and hyphens.
func validHost(h string) bool {
	if h == "" {
		return false
	}
	if net.ParseIP(h) != nil {
		return true
	}
	for _, r := range h {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-':
		default:
			return false
		}
	}
	return !strings.HasPrefix(h, "-") && !strings.HasPrefix(h, ".")
}
