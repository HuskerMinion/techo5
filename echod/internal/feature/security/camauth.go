package security

import (
	"crypto/md5"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"unicode"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// The camera's login: the user name and password the stream takes (config.CamStream: the setup page or
// Home Assistant's camera_credentials action sets it, feature/camstream). Once a password is set, the web
// port's camera pages take it too (feature/camera), by HTTP Digest: a recorder that found the camera by
// ONVIF fetches its snapshot with the same login.

// CameraLogin is the camera's user name and password; ok is false while no password is set.
func CameraLogin() (user, pass string, ok bool) {
	user, pass = config.Get().CamStream.Login()
	return user, pass, pass != ""
}

// Encrypted is whether Home Assistant's link is encrypted with the device's key, which a login may be
// sent over.
func Encrypted() bool { return encrypted() }

// CheckLogin is what a camera login may be: printable, no spaces, colons, @, / or quotes (the
// rtsp://user:password@ form and the digests split on them), the password at least 12 long, as the
// setup page asks.
func CheckLogin(user, pass string) error {
	ok := func(s string) bool {
		for _, r := range s {
			if !unicode.IsPrint(r) || unicode.IsSpace(r) || r == ':' || r == '@' || r == '/' || r == '"' {
				return false
			}
		}
		return s != ""
	}
	if !ok(user) || !ok(pass) {
		return errors.New("a user name and password with no spaces, colons, @, / or quotes")
	}
	if len(pass) < 12 {
		return errors.New("a password of at least 12 characters")
	}
	return nil
}

// ---- HTTP Digest (RFC 7616 with MD5, as RTSP 1.0 and every camera client have it) ----

// Digest checks requests against the camera's login: Challenge for the WWW-Authenticate header,
// Check for a request's Authorization header.
type Digest struct {
	Realm string
	mu    sync.Mutex
	nonce string
}

// Challenge is the value of the WWW-Authenticate header asking for the login.
func (d *Digest) Challenge() string {
	return fmt.Sprintf(`Digest realm="%s", nonce="%s", algorithm=MD5`, d.Realm, d.current())
}

func (d *Digest) current() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.nonce == "" {
		b := make([]byte, 16)
		_, _ = rand.Read(b)
		d.nonce = hex.EncodeToString(b)
	}
	return d.nonce
}

// Check is whether authorization (an Authorization header) carries the camera's login for method
// and uri. Basic is not taken: over a link that is not encrypted it would hand the password over.
func (d *Digest) Check(authorization, method string) bool {
	user, pass, ok := CameraLogin()
	if !ok {
		return false
	}
	p, ok := parseDigest(authorization)
	if !ok || p["username"] != user || p["realm"] != d.Realm || p["nonce"] != d.current() {
		return false
	}
	h := func(s string) string { sum := md5.Sum([]byte(s)); return hex.EncodeToString(sum[:]) }
	ha1 := h(user + ":" + d.Realm + ":" + pass)
	ha2 := h(method + ":" + p["uri"])
	var want string
	if q := p["qop"]; q != "" {
		want = h(ha1 + ":" + p["nonce"] + ":" + p["nc"] + ":" + p["cnonce"] + ":" + q + ":" + ha2)
	} else {
		want = h(ha1 + ":" + p["nonce"] + ":" + ha2)
	}
	return subtle.ConstantTimeCompare([]byte(want), []byte(p["response"])) == 1
}

// parseDigest takes a Digest Authorization header apart.
func parseDigest(h string) (map[string]string, bool) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(h), "Digest ")
	if !ok {
		return nil, false
	}
	out := map[string]string{}
	for len(rest) > 0 {
		rest = strings.TrimLeft(rest, " ,")
		k, v, found := strings.Cut(rest, "=")
		if !found {
			break
		}
		k = strings.TrimSpace(k)
		if strings.HasPrefix(v, `"`) {
			end := strings.Index(v[1:], `"`)
			if end < 0 {
				return nil, false
			}
			out[k], rest = v[1:end+1], v[end+2:]
		} else {
			val, after, _ := strings.Cut(v, ",")
			out[k], rest = strings.TrimSpace(val), after
		}
	}
	return out, out["response"] != ""
}
