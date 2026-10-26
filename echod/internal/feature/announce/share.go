package announce

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/web"
)

// Settings shared between the devices (feature/share) travel here, from one device to one other, signed
// with the house word like announcements but with a key and a form of their own: keyed slowly (PBKDF2,
// as nearest's claims are), so a message heard on the network is no quick way to guess the word, and
// bound to both devices, so one heard on its way to one device cannot be sent on to another or passed
// off as from a third. Peers are found over mDNS, which anything on the network can answer: only the
// signature is trusted, never the record.
//
// This file is the only hook: announce.go's own sending and receiving are untouched.

const (
	sharePath        = "/share"
	shareAuthHeader  = "X-Techo5-Share"
	shareReplyHeader = "X-Techo5-Share-Reply"
	shareFromHeader  = "X-Techo5-Share-From"
	shareVersion     = "share-v1"

	// shareSalt and shareRounds are the slow keying; the salt is this protocol's own, so the key is no use
	// to announcements, the intercom or nearest's claims.
	shareSalt   = "techo5-share v1"
	shareRounds = 100_000

	// shareSkew is how far a sender's clock may be from this one's. A sender told its clock is out signs
	// again by this one's (timeHeader), as announcements do.
	shareSkew = 2 * time.Minute

	// shareMaxBody is more than any message: a copy of every shareable setting is a few kilobytes.
	shareMaxBody = 64 << 10

	// shareReadWithin is how long a request has to arrive whole, once its header has checked out.
	shareReadWithin = 10 * time.Second

	// shareFailures failed requests from one address in shareFailWindow and it is turned away until the
	// window ends; shareAddressesKept bounds the addresses counted.
	shareFailures      = 10
	shareFailWindow    = time.Minute
	shareAddressesKept = 1024

	// shareWait is how long one send may take.
	shareWait = 10 * time.Second
)

// ShareOpen is whether this device takes shared settings at all; feature/share sets it. The house word
// is needed too.
var ShareOpen = func() bool { return false }

// ShareReceive is feature/share's answer to a message from another device, by its name; it sets it.
var ShareReceive = func(from string, body []byte) ([]byte, error) { return nil, errors.New("not taking shared settings") }

func init() {
	web.Handle(sharePath, "", func() bool { return config.Get().Home.HouseWord != "" && ShareOpen() }, receiveShare)
}

var shareKeys struct {
	sync.Mutex
	word string
	key  []byte
}

// shareKey is the HMAC key for a house word, worked out once: it takes a moment by design.
func shareKey(word string) []byte {
	shareKeys.Lock()
	defer shareKeys.Unlock()
	if shareKeys.key != nil && shareKeys.word == word {
		return shareKeys.key
	}
	k, err := pbkdf2.Key(sha256.New, word, []byte(shareSalt), shareRounds, sha256.Size)
	if err != nil {
		k = nil // cannot happen for these sizes; a nil key signs nothing another device checks
	}
	shareKeys.word, shareKeys.key = word, k
	return k
}

// shareMAC is the signature over a message from one device to another. Names are compared without regard
// to case, as the devices find each other.
func shareMAC(word, from, to, ts, nonce string, body []byte) []byte {
	sum := sha256.Sum256(body)
	m := hmac.New(sha256.New, shareKey(word))
	for _, part := range []string{shareVersion, ts, nonce, http.MethodPost, sharePath,
		strings.ToLower(from), strings.ToLower(to), hex.EncodeToString(sum[:])} {
		m.Write([]byte(part))
		m.Write([]byte{'\n'})
	}
	return m.Sum(nil)
}

// shareSign is the header for a message about to be sent.
func shareSign(word, from, to string, body []byte, now time.Time) string {
	header, _ := shareSignNonce(word, from, to, body, now)
	return header
}

// shareSignNonce is shareSign, and the nonce in it, which the reply is signed over.
func shareSignNonce(word, from, to string, body []byte, now time.Time) (header, nonce string) {
	var n [12]byte
	_, _ = rand.Read(n[:])
	ts := strconv.FormatInt(now.Unix(), 10)
	nonce = hex.EncodeToString(n[:])
	return shareVersion + " " + ts + " " + nonce + " " + hex.EncodeToString(shareMAC(word, from, to, ts, nonce, body)), nonce
}

// shareReplyMAC is the signature over a reply: to the message with that nonce, from the device that
// answered to the one that asked. A reply is believed only with it, so nothing that answers for a device
// without the house word can say what a device has.
func shareReplyMAC(word, nonce, from, to string, body []byte) string {
	sum := sha256.Sum256(body)
	m := hmac.New(sha256.New, shareKey(word))
	for _, part := range []string{shareVersion + " reply", nonce, strings.ToLower(from), strings.ToLower(to),
		hex.EncodeToString(sum[:])} {
		m.Write([]byte(part))
		m.Write([]byte{'\n'})
	}
	return hex.EncodeToString(m.Sum(nil))
}

// ErrShareNotTaking is a device that answers but takes no shared settings: sharing is off there, or it
// has no house word.
var ErrShareNotTaking = errors.New("not taking shared settings")

var (
	errShareUnsigned = errors.New("not signed by this house")
	errShareStale    = errors.New("signed at a time too far from this device's clock")
)

// shareStarted is when this program started: a message signed before it is refused, so one taken after
// a restart, while the nonces remembered are gone, cannot be sent again.
var shareStarted = time.Now()

func shareStartedWall(now time.Time) time.Time {
	return now.Add(-time.Since(shareStarted)).Truncate(time.Second)
}

// shareLooksSigned checks a message's header before its body is read: its form, and a time this device
// would take.
func shareLooksSigned(header string, now time.Time) (ts, nonce string, mac []byte, err error) {
	parts := strings.Fields(header)
	if len(parts) != 4 || parts[0] != shareVersion || len(parts[3]) != 2*sha256.Size {
		return "", "", nil, errShareUnsigned
	}
	sec, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return "", "", nil, errShareUnsigned
	}
	mac, err = hex.DecodeString(parts[3])
	if err != nil {
		return "", "", nil, errShareUnsigned
	}
	at := time.Unix(sec, 0)
	if d := now.Sub(at); d > shareSkew || d < -shareSkew || at.Before(shareStartedWall(now)) {
		return "", "", nil, errShareStale
	}
	return parts[1], parts[2], mac, nil
}

var shareTries struct {
	sync.Mutex
	by map[string]*shareTally
}

type shareTally struct {
	n    int
	from time.Time
}

// shareSource is where a request came from, for counting its failures: a global IPv6 address by its /64,
// anything else by itself.
func shareSource(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if ip := net.ParseIP(host); ip != nil && ip.To4() == nil && !ip.IsLinkLocalUnicast() && !ip.IsPrivate() {
		return ip.Mask(net.CIDRMask(64, 128)).String() + "/64"
	}
	return host
}

func shareTurnedAway(addr string, now time.Time) bool {
	shareTries.Lock()
	defer shareTries.Unlock()
	t := shareTries.by[addr]
	return t != nil && now.Sub(t.from) < shareFailWindow && t.n >= shareFailures
}

func shareFailed(addr string, now time.Time) {
	shareTries.Lock()
	defer shareTries.Unlock()
	if shareTries.by == nil {
		shareTries.by = map[string]*shareTally{}
	}
	t := shareTries.by[addr]
	if t == nil || now.Sub(t.from) >= shareFailWindow {
		if len(shareTries.by) >= shareAddressesKept {
			for k, old := range shareTries.by {
				if now.Sub(old.from) >= shareFailWindow {
					delete(shareTries.by, k)
				}
			}
		}
		if len(shareTries.by) >= shareAddressesKept {
			return
		}
		t = &shareTally{from: now}
		shareTries.by[addr] = t
	}
	t.n++
}

// receiveShare takes a message from another device and hands it to feature/share.
func receiveShare(w http.ResponseWriter, r *http.Request) {
	// Settings are shared within the house, never with a device from away over the tailnet (web.AwayRequest).
	// The web port's own gate turns it away first; this holds whatever the gate lets through.
	if web.AwayRequest(r) {
		slog.Warn("share: refused, from away", "from", r.RemoteAddr)
		http.NotFound(w, r)
		return
	}
	now := receiveNow()
	src := shareSource(r)
	if r.Method != http.MethodPost {
		web.Refuse(w, "post a message", http.StatusMethodNotAllowed)
		return
	}
	if shareTurnedAway(src, now) {
		web.Refuse(w, "too many that did not check out", http.StatusTooManyRequests)
		return
	}
	ts, nonce, mac, err := shareLooksSigned(r.Header.Get(shareAuthHeader), now)
	if errors.Is(err, errShareStale) {
		w.Header().Set(timeHeader, strconv.FormatInt(now.Unix(), 10))
		web.Refuse(w, "our clocks disagree", http.StatusConflict)
		return
	}
	if err != nil {
		shareFailed(src, now)
		web.Refuse(w, "not this house", http.StatusForbidden)
		return
	}
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(shareReadWithin))
	body, err := io.ReadAll(io.LimitReader(r.Body, shareMaxBody+1))
	if err != nil || len(body) > shareMaxBody {
		shareFailed(src, now)
		web.Refuse(w, "too long, or cut short", http.StatusBadRequest)
		return
	}
	from, _ := url.QueryUnescape(r.Header.Get(shareFromHeader))
	word, me := config.Get().Home.HouseWord, config.Get().Device.Name
	if from == "" || word == "" || !hmac.Equal(mac, shareMAC(word, from, me, ts, nonce, body)) {
		shareFailed(src, now)
		web.Refuse(w, "not this house", http.StatusForbidden)
		return
	}
	if !fresh("share "+nonce, now) {
		web.Refuse(w, "heard this one already", http.StatusForbidden)
		return
	}
	reply, err := ShareReceive(from, body)
	if err != nil {
		web.Refuse(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set(shareReplyHeader, shareReplyMAC(word, nonce, me, from, reply))
	_, _ = w.Write(reply)
}

// SendShare sends a message to the device of that name, answering its reply.
func SendShare(ctx context.Context, to string, body []byte) ([]byte, error) {
	signed := false // the other device answered signed: a clock it gave is kept
	word := config.Get().Home.HouseWord
	if word == "" {
		return nil, errors.New("this house has no word set")
	}
	all := Peers()
	i := -1
	for k, p := range all {
		if strings.EqualFold(p.Name, to) {
			i = k
			break
		}
	}
	if i < 0 {
		return nil, fmt.Errorf("%s is not on the network now", to)
	}
	p := all[i]
	me := config.Get().Device.Name
	ctx, cancel := context.WithTimeout(ctx, shareWait)
	defer cancel()
	addr := net.JoinHostPort(p.Address, strconv.Itoa(p.Port))
	// Twice at most: the second time signed by the other device's clock, when it says ours is out.
	for try := 0; ; try++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+addr+sharePath, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(shareFromHeader, url.QueryEscape(me))
		header, nonce := shareSignNonce(word, me, p.Name, body, time.Now().Add(offsetFor(addr)))
		req.Header.Set(shareAuthHeader, header)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, err
		}
		reply, _ := io.ReadAll(io.LimitReader(resp.Body, shareMaxBody))
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusConflict && try == 0 {
			if theirs, err := strconv.ParseInt(resp.Header.Get(timeHeader), 10, 64); err == nil {
				// The time came unsigned: it is kept only once the other device answers signed, by it.
				was := offsetFor(addr)
				off := setOffset(addr, time.Until(time.Unix(theirs, 0)), time.Now())
				defer func() {
					if !signed {
						setOffset(addr, was, time.Now())
					}
				}()
				slog.Info("share: signing by another device's clock", "device", p.Name, "off_by", off.Round(time.Second))
				continue
			}
		}
		if resp.StatusCode == http.StatusNotFound {
			return nil, ErrShareNotTaking
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("%s said %s", p.Name, resp.Status)
		}
		want := shareReplyMAC(word, nonce, p.Name, me, reply)
		if !hmac.Equal([]byte(want), []byte(resp.Header.Get(shareReplyHeader))) {
			return nil, fmt.Errorf("%s answered without the house's signature", p.Name)
		}
		signed = true
		return reply, nil
	}
}
