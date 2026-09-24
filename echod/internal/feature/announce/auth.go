package announce

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// How an announcement proves it comes from the house. It used to carry the house word itself in a
// plain header, readable by anything that could watch the network - and the word is also what the
// intercom is keyed by. Now it carries a signature instead: an HMAC, keyed from the word, over when
// it was sent, a nonce, where it is going and everything it says. The word never leaves the device,
// a changed announcement does not check out, and one heard on the network cannot be sent again.

const (
	authHeader = "X-Techo5-Auth"
	authLabel  = "techo5-announce hmac"

	// authSkew is how far a sender's clock may be from this one's. The devices keep time from the
	// network, so a few minutes is generous; the nonces seen within it are remembered.
	authSkew = 5 * time.Minute

	// noncesKept bounds the nonces remembered. A house sends a handful of announcements a day.
	noncesKept = 4096
)

func authKey(word string) []byte {
	sum := sha256.Sum256([]byte(authLabel + ":" + word))
	return sum[:]
}

// signed is what the signature covers, one field a line: the version, the time and nonce, the method
// and path, the announcement's headers as they travel, and the body's hash.
func signed(ts, nonce, method, path string, get func(string) string, body []byte) []byte {
	sum := sha256.Sum256(body)
	var b bytes.Buffer
	for _, part := range []string{"v1", ts, nonce, method, path,
		get(fromHeader), get(textHeader), get(kindHeader), get(idHeader), hex.EncodeToString(sum[:])} {
		b.WriteString(part)
		b.WriteByte('\n')
	}
	return b.Bytes()
}

// sign is the X-Techo5-Auth value for an announcement about to be sent.
func sign(word, method, path string, headers map[string]string, body []byte, now time.Time) string {
	var n [12]byte
	_, _ = rand.Read(n[:])
	ts, nonce := strconv.FormatInt(now.Unix(), 10), hex.EncodeToString(n[:])
	mac := hmac.New(sha256.New, authKey(word))
	mac.Write(signed(ts, nonce, method, path, func(k string) string { return headers[k] }, body))
	return "v1 " + ts + " " + nonce + " " + hex.EncodeToString(mac.Sum(nil))
}

var errNotThisHouse = errors.New("announce: not signed by this house")

// nonces are the ones seen lately, so a signed announcement heard on the network cannot be replayed.
var nonces struct {
	sync.Mutex
	seen map[string]time.Time
}

// fresh records a nonce, reporting false if it was seen already.
func fresh(nonce string, now time.Time) bool {
	nonces.Lock()
	defer nonces.Unlock()
	if nonces.seen == nil {
		nonces.seen = map[string]time.Time{}
	}
	if _, ok := nonces.seen[nonce]; ok {
		return false
	}
	if len(nonces.seen) >= noncesKept {
		for n, at := range nonces.seen {
			if now.Sub(at) > 2*authSkew {
				delete(nonces.seen, n)
			}
		}
		if len(nonces.seen) >= noncesKept {
			// Every one of them is recent: something is sending far more than a house does. Refused
			// rather than let the list grow.
			return false
		}
	}
	nonces.seen[nonce] = now
	return true
}

// verify checks that r, with this body, was signed with the house word. legacy is an older device
// that still sends the word itself: taken while a house updates, and said in the log.
func verify(r *http.Request, word string, body []byte, now time.Time) (legacy bool, err error) {
	if word == "" {
		return false, errNotThisHouse
	}
	auth := r.Header.Get(authHeader)
	if auth == "" {
		if old := r.Header.Get(header); old != "" && subtle.ConstantTimeCompare([]byte(old), []byte(word)) == 1 {
			return true, nil
		}
		return false, errNotThisHouse
	}
	parts := strings.Fields(auth)
	if len(parts) != 4 || parts[0] != "v1" {
		return false, errNotThisHouse
	}
	ts, nonce, got := parts[1], parts[2], parts[3]
	sec, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return false, errNotThisHouse
	}
	if d := now.Sub(time.Unix(sec, 0)); d > authSkew || d < -authSkew {
		return false, errors.New("announce: sent too long ago, or a clock is wrong")
	}
	want := hmac.New(sha256.New, authKey(word))
	want.Write(signed(ts, nonce, r.Method, r.URL.Path, r.Header.Get, body))
	sum, err := hex.DecodeString(got)
	if err != nil || !hmac.Equal(sum, want.Sum(nil)) {
		return false, errNotThisHouse
	}
	if !fresh(nonce, now) {
		return false, errors.New("announce: heard this one already")
	}
	return false, nil
}

// legacySeen keeps the older-device notice to once an hour per device.
var legacySeen struct {
	sync.Mutex
	at map[string]time.Time
}

func noteLegacy(r *http.Request) {
	from, _, _ := net.SplitHostPort(r.RemoteAddr)
	legacySeen.Lock()
	defer legacySeen.Unlock()
	if legacySeen.at == nil {
		legacySeen.at = map[string]time.Time{}
	}
	if time.Since(legacySeen.at[from]) < time.Hour {
		return
	}
	legacySeen.at[from] = time.Now()
	slog.Warn("announcement from a device that still sends the house word itself: update it", "from", from)
}
