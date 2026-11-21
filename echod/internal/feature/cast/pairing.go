package cast

import (
	"crypto/rand"
	"encoding/base32"
	"net"
	"net/url"
	"strconv"
)

// PairingURL is what the pairing QR code holds: where the device is, its key and its name, in one link a
// phone's app reads to fill in the Add a Show form. The key is in it, so the code is only ever drawn
// while somebody has asked for it and is standing at the screen.
func PairingURL(host string, port int, key, name string) string {
	q := url.Values{}
	q.Set("host", host)
	q.Set("port", strconv.Itoa(port))
	q.Set("key", key)
	if name != "" {
		q.Set("name", name)
	}
	return (&url.URL{Scheme: "techo5cast", Host: "pair", RawQuery: q.Encode()}).String()
}

var keyEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewKey is a fresh pairing key: 16 characters, 80 bits, from letters and digits a person can read
// off a screen if the QR code will not scan.
func NewKey() string {
	var b [10]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("cast: no randomness: " + err.Error())
	}
	return keyEncoding.EncodeToString(b[:])
}

// hostOf is the first IPv4 address of ips as text, the one a phone on the same network can use; empty if
// there is none.
func hostOf(ips []net.IP) string {
	for _, ip := range ips {
		if v4 := ip.To4(); v4 != nil {
			return v4.String()
		}
	}
	return ""
}
