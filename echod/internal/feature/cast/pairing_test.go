package cast

import (
	"net"
	"net/url"
	"strings"
	"testing"
)

func TestPairingURLRoundTrips(t *testing.T) {
	key := "a&b=c d"
	u, err := url.Parse(PairingURL("192.168.1.20", 8940, key, "Kitchen Show"))
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if u.Scheme != "techo5cast" || u.Host != "pair" {
		t.Fatalf("got %s://%s", u.Scheme, u.Host)
	}
	if q.Get("host") != "192.168.1.20" || q.Get("port") != "8940" || q.Get("key") != key || q.Get("name") != "Kitchen Show" {
		t.Fatalf("query %v", q)
	}
}

func TestNewKeyIsLongEnoughAndDiffers(t *testing.T) {
	a, b := NewKey(), NewKey()
	if len(a) != 16 || a == b {
		t.Fatalf("keys %q %q", a, b)
	}
	if strings.Trim(a, "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567") != "" {
		t.Fatalf("key %q has characters a person cannot read off a screen", a)
	}
}

func TestHostOfPrefersIPv4(t *testing.T) {
	got := hostOf([]net.IP{net.ParseIP("fe80::1"), net.ParseIP("10.0.0.7")})
	if got != "10.0.0.7" {
		t.Fatalf("got %q", got)
	}
	if hostOf([]net.IP{net.ParseIP("fe80::1")}) != "" {
		t.Fatal("an IPv6-only device has no address a phone can use here")
	}
}
