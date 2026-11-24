package detect

// Sending kept wake clips to a trainer on the home network (#140).
//
// Off unless a target is chosen (config.WakeSend). The queue is the kept clips themselves: a clip is
// unsent until its sidecar says "sent_at", and a clip a trainer refused for good says "send_failed".
// Nothing is held in memory but the one clip being sent, streamed from its file. One goroutine sends
// the oldest unsent clip first, one at a time, no faster than one every sendEvery, and backs off from
// sendBackoffMin to sendBackoffMax while the trainer cannot be reached.
//
// Only clips kept after the target was set are sent (config.WakeSend.Since): what was kept before
// anybody said where clips may go stays on the device. Pruning is unchanged, so an unsent clip that is
// pruned is gone, and the disk used is what it always was. On Linux a clip of a kind that is sent waits
// in RAM rather than on eMMC, and goes once it is sent (clipsram.go).
//
// Clips go only to an address on the home network. The address is checked when it is saved and again
// before each send, and the connection itself is made only to an address that passes (sendDialer), so a
// name that starts resolving to somewhere else is never sent to.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"maps"
	"mime/multipart"
	"net"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
	"github.com/HuskerMinion/techo5/echod/internal/lib/bootreason"
)

const (
	// sendEvery is the least between one clip being sent and the next.
	sendEvery = 2 * time.Second

	// sendBackoffMin and sendBackoffMax bound the wait after a failure to reach the trainer: doubled
	// each time, back to the first after a clip goes through.
	sendBackoffMin = 5 * time.Second
	sendBackoffMax = 10 * time.Minute

	// sendPoll is how often the sender looks on its own, with nothing to wake it.
	sendPoll = 3 * time.Minute

	// sendAsk is the most one request may take.
	sendAsk = 30 * time.Second

	// sendAnswerMax is the most of an answer that is read.
	sendAnswerMax = 64 << 10
)

// Said by Detect's sensors.
const (
	statusRefused = "token refused"
	statusKeepOff = "Keep wake clips is off"
)

// An address that is not on the home network.
var errNotHome = errors.New("that address is not on your home network or your Tailscale network: clips are only sent to a private address (192.168.x.y, 10.x.y.z, 172.16-31.x.y, a Tailscale 100.64-127.x.y or fd7a:115c:a1e0:: address, another link-local or fc00::/7 address, or a name that resolves to one); an IPv6 address with a zone, like fe80::1%eth0, is not supported")

// sendItem is the clip being sent.
type sendItem struct {
	wav  string
	meta clipMeta
	name string // the file's name without .wav
	at   time.Time
}

// sender uploads kept clips. Everything it reaches for outside itself is a field, for the tests.
type sender struct {
	c *clips

	cfg     func() config.WakeSend
	device  func() string
	resolve func(ctx context.Context, host string) ([]net.IP, error)
	allow   func(net.IP) bool
	roots   *x509.CertPool // what certificates are checked against: nil is the system's
	// wait sleeps for d, or until wake fires, and says whether to go on (false once ctx is done).
	wait    func(ctx context.Context, d time.Duration, wake <-chan struct{}) bool
	now     func() time.Time
	publish func(unsent int, status string) // the sensors; nil until wired

	// nudge is a clip being written, and changed is the settings: each holds at most one wake-up.
	nudge   chan struct{}
	changed chan struct{}

	mu        sync.Mutex
	halted    string          // the settings (fingerprint) sending was stopped under, until they change
	haltedWhy string          // and why
	done      map[string]bool // clips sent whose sidecar could not be marked: not sent again this run
	markErr   string          // why one could not be
	unsent    int
	status    string
	cert      string             // see Untrusted
	abort     context.CancelFunc // cancels the upload under way, for turning Keep wake clips off
	abortKind string             // the kind of clip it is: a take is not cut short by Keep going off
}

func newSender(c *clips) *sender {
	s := &sender{
		c:       c,
		cfg:     func() config.WakeSend { return config.Get().Wake.Send },
		device:  deviceName,
		resolve: lookupIPs,
		allow:   homeAddress,
		wait:    realWait,
		now:     time.Now,
		nudge:   make(chan struct{}, 1),
		changed: make(chan struct{}, 1),
		done:    map[string]bool{},
	}
	return s
}

// CertError is a certificate that does not verify, and is not the pinned one: Fingerprint is its SHA-256
// for the owner to trust, and Changed says one was pinned before.
type CertError struct {
	Fingerprint string
	Changed     bool
}

func (e *CertError) Error() string {
	if e.Changed {
		return "certificate changed: the trainer's certificate is not the one that was trusted (SHA-256 " + e.Fingerprint + ")"
	}
	return "the trainer's certificate is not one this device trusts (SHA-256 " + e.Fingerprint + ")"
}

// certFingerprint is a certificate's SHA-256, as hex.
func certFingerprint(c *x509.Certificate) string {
	sum := sha256.Sum256(c.Raw)
	return hex.EncodeToString(sum[:])
}

// verifyFor is the check of a connection to host, the name or the IP address in the saved address: the
// certificate verifies as usual, for that very host, or its leaf is the pinned one, and nothing else. The
// host is the address's own, not what the handshake sent as its server name, which is empty for an IP
// address. InsecureSkipVerify is on only so that this runs in place of the library's check.
func (s *sender) verifyFor(host string) func(tls.ConnectionState) error {
	return func(cs tls.ConnectionState) error {
		if len(cs.PeerCertificates) == 0 {
			return errors.New("the trainer sent no certificate")
		}
		leaf := cs.PeerCertificates[0]
		inter := x509.NewCertPool()
		for _, c := range cs.PeerCertificates[1:] {
			inter.AddCert(c)
		}
		var err error
		if host == "" {
			err = errors.New("no host to check the certificate for")
		} else {
			_, err = leaf.Verify(x509.VerifyOptions{DNSName: host, Roots: s.roots, Intermediates: inter})
		}
		if err == nil {
			return nil
		}
		fp := certFingerprint(leaf)
		pin := strings.ToLower(s.cfg().Pin)
		if pin != "" && pin == fp {
			return nil
		}
		s.mu.Lock()
		s.cert = fp
		s.mu.Unlock()
		return &CertError{Fingerprint: fp, Changed: pin != ""}
	}
}

// Untrusted is the fingerprint of the last certificate that was refused for not verifying, for the page to
// offer to trust.
func (s *sender) Untrusted() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cert
}

// clientFor is the client for requests to an address: no proxy, no redirects (one could lead off the home
// network), certificates verified for that address's host, and connections made only to a home address.
func (s *sender) clientFor(raw string) *http.Client {
	host := ""
	if u, err := url.Parse(raw); err == nil {
		host = u.Hostname()
	}
	tr := &http.Transport{
		Proxy:               nil,
		DialContext:         s.dial,
		TLSHandshakeTimeout: 10 * time.Second,
		// Verification is done by verifyFor, which does what the library would and accepts one more
		// certificate: the one the owner pinned.
		TLSClientConfig:       &tls.Config{InsecureSkipVerify: true, VerifyConnection: s.verifyFor(host), MinVersion: tls.VersionTLS12},
		ResponseHeaderTimeout: sendAsk,
		DisableKeepAlives:     true,
	}
	return &http.Client{
		Transport:     tr,
		Timeout:       sendAsk,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// dial connects to host:port only if every address the host has is on the home network.
func (s *sender) dial(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	ips, err := s.homeIPs(ctx, host)
	if err != nil {
		return nil, err
	}
	d := net.Dialer{Timeout: 5 * time.Second}
	var last error
	for _, ip := range ips {
		c, err := d.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return c, nil
		}
		last = err
	}
	return nil, last
}

// homeIPs is the addresses of host, or errNotHome if it has none or any is not on the home network.
func (s *sender) homeIPs(ctx context.Context, host string) ([]net.IP, error) {
	var ips []net.IP
	if ip := net.ParseIP(strings.Trim(host, "[]")); ip != nil {
		ips = []net.IP{ip}
	} else {
		var err error
		if ips, err = s.resolve(ctx, host); err != nil {
			return nil, fmt.Errorf("could not find %s: %w", host, err)
		}
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("could not find %s", host)
	}
	for _, ip := range ips {
		if !s.allow(ip) {
			return nil, errNotHome
		}
	}
	return ips, nil
}

// cgnat is 100.64.0.0/10, the range Tailscale gives its devices. (Its IPv6 range, fd7a:115c:a1e0::/48, is
// inside fc00::/7.)
var cgnat = &net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}

// homeAddress is whether ip is private (RFC 1918, fc00::/7), link-local, or on a Tailscale network.
func homeAddress(ip net.IP) bool {
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	return ip.IsPrivate() || ip.IsLinkLocalUnicast() || cgnat.Contains(ip)
}

func lookupIPs(ctx context.Context, host string) ([]net.IP, error) {
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	out := make([]net.IP, 0, len(addrs))
	for _, a := range addrs {
		out = append(out, a.IP)
	}
	return out, err
}

func realWait(ctx context.Context, d time.Duration, wake <-chan struct{}) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-wake:
		return ctx.Err() == nil
	case <-t.C:
		return ctx.Err() == nil
	}
}

// deviceName is the device as Home Assistant and a trainer's folders know it: its ESPHome node name, the
// <device> in esphome.<device>_wake_clips (layout.NodeID, as the API advertises the node), not the name
// shown on the screen.
func deviceName() string {
	mac, _ := layout.FactoryMAC()
	return layout.NodeID(config.Get().Device.Name, mac)
}

// CheckSendURL is the address as it is kept: an http or https address with a host, with nothing but a
// path after it, and no login in it.
func CheckSendURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return "", errors.New("the address is http:// or https:// and the trainer's name or address, like http://192.168.1.20:8789")
	}
	if u.User != nil {
		return "", errors.New("the address has no login in it: a token is entered on its own")
	}
	if strings.Contains(u.Hostname(), "%") {
		return "", errors.New("an IPv6 address with a zone, like fe80::1%eth0, is not supported: use its name or another address")
	}
	if u.RawQuery != "" || u.Fragment != "" || strings.Contains(raw, "?") || strings.Contains(raw, "#") {
		return "", errors.New("the address is the trainer's base: nothing after a ? or #")
	}
	if len(raw) > 300 {
		return "", errors.New("that address is too long")
	}
	// The same address however it was typed: a name's final dot ("192.168.1.20." or "trainer.lan.") and
	// the endpoint a receiver shows ("/api/clips", "/api/clips/ping") come off, so the address is the
	// trainer's base, as the sender wants it, and a token kept for it stays.
	if h := strings.TrimSuffix(u.Hostname(), "."); h != u.Hostname() {
		if p := u.Port(); p != "" {
			u.Host = net.JoinHostPort(h, p)
		} else {
			u.Host = h
		}
	}
	u.Host = strings.ToLower(u.Host)
	path := strings.TrimRight(u.Path, "/")
	path = strings.TrimSuffix(path, "/api/clips/ping")
	path = strings.TrimSuffix(path, "/api/clips")
	u.Path, u.RawPath = path, ""
	return strings.TrimRight(u.String(), "/"), nil
}

// CheckHome is whether the address's host is on the home network, looked up now.
func (s *sender) CheckHome(ctx context.Context, raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return errors.New("that is not an address")
	}
	_, err = s.homeIPs(ctx, u.Hostname())
	return err
}

// fingerprint is the part of the settings that a refused token is about.
func fingerprint(w config.WakeSend) string { return w.Target + "\x00" + w.URL + "\x00" + w.Token }

// settingsChanged wakes the sender, and lets it try again after a refused token.
func (s *sender) settingsChanged() {
	// Saving again is asking it to try again.
	s.mu.Lock()
	s.halted, s.haltedWhy = "", ""
	s.mu.Unlock()
	select {
	case s.changed <- struct{}{}:
	default:
	}
	s.wake()
}

// wake is a clip written, or anything else that makes the queue worth looking at.
func (s *sender) wake() {
	select {
	case s.nudge <- struct{}{}:
	default:
	}
}

// run sends until ctx is done.
func (s *sender) run(ctx context.Context) {
	poll := time.NewTicker(sendPoll)
	defer poll.Stop()
	// As the daemon stops, what still waits in RAM goes to eMMC, so a reboot does not lose it; not for a
	// reset, whose next start deletes the clips with the rest.
	defer func() {
		if !bootreason.IsReset() {
			s.c.spill(func(string, time.Time) bool { return true })
		}
	}()
	for {
		s.drain(ctx)
		select {
		case <-ctx.Done():
			return
		case <-s.nudge:
		case <-s.changed:
		case <-poll.C:
		}
	}
}

// sendOutcome is what became of one clip.
type sendOutcome int

const (
	outSent   sendOutcome = iota
	outFailed             // this clip will never be taken: marked, and the rest go on
	outSkip               // this clip is gone or sending was cut short: nothing is counted or marked
	outStop               // the address or the settings are wrong: sending stops until they change
	outRetry
)

// sendFailuresMax is how many clips in a row the trainer may refuse for good before sending stops: it is
// then more likely the settings than the clips.
const sendFailuresMax = 5

// drain sends what is waiting, one clip at a time, and returns when there is nothing more to send, when
// sending is off or stopped, or when ctx is done.
func (s *sender) drain(ctx context.Context) {
	backoff := time.Duration(0)
	failures := 0
	var last string
	for ctx.Err() == nil {
		cfg := s.cfg()
		if fp := fingerprint(cfg); fp != last {
			last, backoff, failures = fp, 0, 0
		}
		s.spillWaiting(cfg)
		if !cfg.On() {
			s.report(0, "")
			return
		}
		// One look at the clips for each clip sent: it is both what to send and how many are waiting.
		// With Keep wake clips off only takes are in it (sendable), and with none of those it is said.
		queue := s.queue(cfg)
		n := len(queue)
		if why, ok := s.isHalted(cfg); ok {
			s.report(n, why)
			return
		}
		if n == 0 {
			if !s.c.kept() {
				s.report(0, statusKeepOff)
			} else {
				s.report(0, "")
			}
			return
		}
		item := queue[0]
		// The address is looked at again before each send: a name can come to point somewhere else.
		var out sendOutcome
		var why string
		if err := s.CheckHome(ctx, cfg.URL); err != nil {
			out, why = outRetry, err.Error()
		} else {
			out, why = s.send(ctx, cfg, item)
		}
		switch out {
		case outSent:
			backoff, failures = 0, 0
			if s.c.inRAM(item.wav) {
				// Only waiting in RAM until the trainer had it (clipsram.go): nothing is marked, it goes.
				if !s.c.dropSent(item.wav, item.meta.Kind) {
					// Still in the queue: not sent again this run, as a clip whose sidecar could not be
					// marked is not (settle).
					s.mu.Lock()
					s.done[item.wav] = true
					s.mu.Unlock()
				}
				s.report(n-1, "")
			} else {
				s.settle(item, func(m *clipMeta) { m.SentAt = s.now().UTC().Format(time.RFC3339) }, n-1)
			}
			if item.meta.Kind == takeKind {
				s.c.dropTake(item.wav) // kept only until it was sent
			}
			// Paced: not woken by anything, so a trainer is never asked faster than this.
			if !s.wait(ctx, sendEvery, nil) {
				return
			}
		case outSkip:
			// Looked at again from the top: the clip is no longer in the queue, or sending is off.
		case outFailed:
			// A take refused for good says nothing about the settings (an older wakesmith refuses every
			// take): it never counts toward stopping the wake clips.
			if item.meta.Kind != takeKind {
				failures++
			}
			slog.Warn("wake clip: the trainer refused it", "clip", item.meta.Kind+"/"+item.name, "why", why)
			s.settle(item, func(m *clipMeta) { m.SendFailed = failedStatus(why) }, n-1)
			if item.meta.Kind == takeKind {
				s.c.dropTake(item.wav) // a take wakesmith will never take is not kept either
			}
			if failures >= sendFailuresMax {
				why = fmt.Sprintf("the trainer refused %d clips in a row (%s): check the address and settings", failures, why)
				s.halt(cfg, why)
				slog.Warn("wake clip: sending stopped", "why", why)
				s.report(n-1, why)
				return
			}
			if !s.wait(ctx, sendEvery, nil) {
				return
			}
		case outStop:
			s.halt(cfg, why)
			slog.Warn("wake clip: sending stopped until the settings change", "why", why)
			s.report(n, why)
			return
		case outRetry:
			if backoff == 0 {
				backoff = sendBackoffMin
			} else {
				backoff = min(backoff*2, sendBackoffMax)
			}
			slog.Info("wake clip: not sent, trying again", "why", why, "in", backoff)
			s.report(n, fmt.Sprintf("%s (trying again in %s)", why, backoff))
			if !s.wait(ctx, backoff, s.changed) {
				return
			}
		}
	}
}

// spillWaiting moves to eMMC the clips in RAM that are not to wait there any longer: every one when
// the folder is full, those of a kind no longer sent (or all, with sending off), and any that has
// waited ramWait (clipsram.go).
func (s *sender) spillWaiting(cfg config.WakeSend) {
	if s.c.ram == "" {
		return
	}
	now := s.now()
	s.c.disk.Lock()
	full := s.c.ramUsed()+clipBytes > s.c.ramCap
	s.c.disk.Unlock()
	s.c.spill(func(kind string, at time.Time) bool {
		return full || !cfg.On() || !cfg.Sends(kind) || now.Sub(at) >= ramWait
	})
}

// settle records what became of a clip in its sidecar. When that cannot be written, the clip is not sent
// again this run, and the status says so.
func (s *sender) settle(it *sendItem, change func(*clipMeta), left int) {
	err := s.mark(it, change)
	s.mu.Lock()
	if err != nil {
		if len(s.done) > 4000 {
			s.done = map[string]bool{}
		}
		s.done[it.wav] = true
		s.markErr = "can't record what was sent: " + err.Error()
	} else {
		s.markErr = ""
	}
	s.mu.Unlock()
	if err != nil {
		slog.Warn("wake clip: could not record what became of it", "err", err)
	}
	s.report(left, "")
}

// failedStatus is the HTTP status in a failure's words ("HTTP 400"), -1 when there is none.
func failedStatus(why string) int {
	var n int
	if _, err := fmt.Sscanf(why, "HTTP %d", &n); err == nil && n > 0 {
		return n
	}
	return -1
}

// isHalted is whether sending was stopped under these settings, and why.
func (s *sender) isHalted(cfg config.WakeSend) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.haltedWhy, s.halted != "" && s.halted == fingerprint(cfg)
}

// halt stops sending until the settings change.
func (s *sender) halt(cfg config.WakeSend, why string) {
	s.mu.Lock()
	s.halted, s.haltedWhy = fingerprint(cfg), why
	s.mu.Unlock()
}

// report tells the sensors, when something changed.
func (s *sender) report(unsent int, status string) {
	s.mu.Lock()
	if status == "" {
		status = s.markErr
	}
	same := s.unsent == unsent && s.status == status
	s.unsent, s.status = unsent, status
	s.mu.Unlock()
	if !same && s.publish != nil {
		s.publish(unsent, status)
	}
}

// Unsent is how many kept clips are waiting to be sent, and the last thing that went wrong.
func (s *sender) Unsent() (int, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.unsent, s.status
}

// queue is the clips to send, oldest first. A clip is to be sent when its kind is, its sidecar has no
// sent_at and no send_failed, it was kept at or after the moment the target was set, and it was not sent
// already this run with no way to say so.
func (s *sender) queue(cfg config.WakeSend) []*sendItem {
	since, err := time.Parse(time.RFC3339, cfg.Since)
	if err != nil {
		return nil // no moment to count from: nothing is old enough to be sent
	}
	s.mu.Lock()
	done := maps.Clone(s.done)
	s.mu.Unlock()
	var out []*sendItem
	for _, kind := range sendKinds(cfg) {
		if !cfg.Sends(kind) || !s.c.sendable(kind) {
			continue
		}
		for _, f := range s.c.glob(kind, "*.json") {
			blob, err := os.ReadFile(f)
			if err != nil {
				continue
			}
			var m clipMeta
			if json.Unmarshal(blob, &m) != nil || m.SentAt != "" || m.SendFailed != 0 {
				continue
			}
			at, err := time.Parse(time.RFC3339, m.At)
			if err != nil || at.Before(since) {
				continue
			}
			wav := strings.TrimSuffix(f, ".json") + ".wav"
			if done[wav] {
				continue
			}
			if _, err := os.Stat(wav); err != nil {
				continue
			}
			m.Kind = kind
			out = append(out, &sendItem{wav: wav, meta: m, name: strings.TrimSuffix(filepath.Base(f), ".json"), at: at})
		}
	}
	slices.SortFunc(out, func(a, b *sendItem) int {
		if c := a.at.Compare(b.at); c != 0 {
			return c
		}
		return strings.Compare(a.name, b.name)
	})
	return out
}

// sendKinds is the folders a target is sent from: the wake clips, and a Record wake word run's takes
// for wakesmith alone, the one trainer that knows what a take is.
func sendKinds(cfg config.WakeSend) []string {
	if cfg.Target == config.SendWakesmith {
		return append(slices.Clone(clipKinds), takeKind)
	}
	return clipKinds
}

// next is the oldest clip to send, and how many there are.
func (s *sender) next(cfg config.WakeSend) (*sendItem, int) {
	q := s.queue(cfg)
	if len(q) == 0 {
		return nil, 0
	}
	return q[0], len(q)
}

// mark changes a clip's sidecar, if the clip is still there: written whole beside it and renamed over
// it, so a power cut leaves the old or the new one.
func (s *sender) mark(it *sendItem, change func(*clipMeta)) error {
	s.c.disk.Lock()
	defer s.c.disk.Unlock()
	if _, err := os.Stat(it.wav); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil // pruned or purged while it was being sent
		}
		return err
	}
	side := sidecar(it.wav)
	blob, err := os.ReadFile(side)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	var m clipMeta
	if err := json.Unmarshal(blob, &m); err != nil {
		return err
	}
	change(&m)
	out, _ := json.Marshal(m)
	tmp := side + ".tmp"
	if err := os.WriteFile(tmp, out, 0o600); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, side); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// send uploads one clip and says what came of it, and why when it did not go.
func (s *sender) send(ctx context.Context, cfg config.WakeSend, it *sendItem) (sendOutcome, string) {
	// Registered before the clip is opened: turning wake clips off cancels it, or is seen just below.
	ctx, cancel := context.WithTimeout(ctx, sendAsk)
	defer cancel()
	s.mu.Lock()
	s.abort, s.abortKind = cancel, it.meta.Kind
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.abort, s.abortKind = nil, ""
		s.mu.Unlock()
	}()
	f, err := os.Open(it.wav)
	if errors.Is(err, fs.ErrNotExist) {
		return outSkip, "" // pruned or purged between the look and now: not the trainer's refusal
	}
	if err != nil {
		return outRetry, err.Error()
	}
	defer f.Close()
	if !s.c.sendable(it.meta.Kind) {
		return outSkip, "" // not kept any more while it was being opened
	}
	st, err := f.Stat()
	if err != nil {
		return outRetry, err.Error()
	}

	var req *http.Request
	if cfg.Target == config.SendTater {
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, cfg.URL+"/api/upload_captured_audio_raw", f)
		if err == nil {
			req.ContentLength = st.Size()
			s.taterHeaders(req, it)
		}
	} else {
		req, err = s.multipartRequest(ctx, cfg, it, f)
	}
	if err != nil {
		return outRetry, err.Error()
	}
	resp, err := s.clientFor(cfg.URL).Do(req)
	if err != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return outSkip, "" // cut short: wake clips were turned off, or the sender is stopping
		}
		var ce *CertError
		if errors.As(err, &ce) {
			return outRetry, ce.Error()
		}
		return outRetry, "could not reach the trainer: " + cleanErr(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, sendAnswerMax))
	return judge(cfg.Target, resp.StatusCode, body)
}

// judge is what an answer means: sent, never going to be accepted, a token that is refused, or worth
// trying again.
func judge(target string, status int, body []byte) (sendOutcome, string) {
	switch {
	case status >= 200 && status < 300:
		if target == config.SendTater {
			var a struct {
				OK bool `json:"ok"`
			}
			if json.Unmarshal(body, &a) != nil || !a.OK {
				return outStop, "that address answered, but not as Tater Trainer"
			}
		}
		return outSent, ""
	case status == http.StatusUnauthorized:
		return outStop, statusRefused
	case status == http.StatusRequestTimeout || status == http.StatusTooManyRequests || status >= 500:
		return outRetry, fmt.Sprintf("the trainer answered %d%s", status, answerText(body))
	case status == http.StatusBadRequest || status == http.StatusUnsupportedMediaType || status == http.StatusUnprocessableEntity:
		// This clip will never be taken; the next may be.
		return outFailed, fmt.Sprintf("HTTP %d%s", status, answerText(body))
	default:
		// A redirect, 403, 404, 405, 413 or any other refusal is about the address or the settings, not this
		// clip: sending stops, and the clips are kept, until they change.
		return outStop, fmt.Sprintf("the trainer answered %d%s: check the address and settings", status, answerText(body))
	}
}

// answerText is the "error" a receiver answered with ({"ok": false, "error": "..."}), shortened, as ": text".
func answerText(body []byte) string {
	var a struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(bytes.TrimSpace(body), &a) != nil || strings.TrimSpace(a.Error) == "" {
		return ""
	}
	t := headerText(strings.TrimSpace(a.Error))
	if len(t) > 160 {
		t = t[:160]
	}
	return ": " + t
}

// cleanErr is an error without the address in it, which the log and the sensors do not need twice.
func cleanErr(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	var de *net.DNSError
	if errors.As(err, &de) {
		return "can't find the trainer: " + de.Err
	}
	var oe *net.OpError
	if errors.As(err, &oe) && oe.Err != nil {
		why := strings.TrimPrefix(strings.TrimPrefix(oe.Err.Error(), oe.Op+": "), "connect: ")
		if oe.Op == "dial" {
			return "can't connect to the trainer: " + why
		}
		return "the connection to the trainer failed: " + why
	}
	return addrInText.ReplaceAllString(err.Error(), "the trainer")
}

// addrInText is an IPv4 address, with a port when it has one, in an error's text.
var addrInText = regexp.MustCompile(`\b\d{1,3}(\.\d{1,3}){3}(:\d+)?\b`)

// abortSend cuts short the upload under way, if there is one of a kind which says yes to: a clip's as the
// clips are purged, a take's as the takes are let go of.
func (s *sender) abortSend(which func(kind string) bool) {
	s.mu.Lock()
	cancel := s.abort
	if !which(s.abortKind) {
		cancel = nil
	}
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// taterWordSuffix is what a model's id carries after its word: a version.
var taterWordSuffix = regexp.MustCompile(`[_-]v?\d+(\.\d+)*$`)

// taterWord is the wake word as Tater Trainer names it. It files a clip under the word it is told, when
// it has trained or been given one by that name (its safe name: lower case, spaces as underscores, no
// other punctuation), and under "unassigned" for review when it has not. A model's id is its file name,
// "hey_jarvis_v2", so a version is taken off the end and the rest written the way Tater writes a word.
func taterWord(model string) string {
	m := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(model)), ".tflite")
	m = strings.TrimSuffix(m, ".onnx")
	m = taterWordSuffix.ReplaceAllString(m, "")
	m = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
			return r
		case r == ' ' || r == '-':
			return '_'
		}
		return -1
	}, m)
	return strings.Trim(m, "_")
}

// taterEvent is what Tater calls what happened.
func taterEvent(kind string) string {
	switch kind {
	case "wake":
		return "wake"
	case "nearmiss":
		return "close_miss"
	}
	return "false_trigger"
}

// headerText is s as a header value: printable ASCII only.
func headerText(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r > 0x7e {
			return '?'
		}
		return r
	}, s)
}

// byteScale is a score or a cutoff on Tater's scale: ESPHome's microWakeWord one, a whole number from 0
// to 255 (its probability history is a list of those, and it reads the cutoff as a whole number).
func byteScale(v float64) string { return strconv.Itoa(int(min(max(v, 0), 1)*255 + 0.5)) }

func (s *sender) taterHeaders(req *http.Request, it *sendItem) {
	h := req.Header
	h.Set("Content-Type", "audio/wav")
	h.Set("X-Audio-Format", "wav")
	h.Set("X-Original-Name", it.name+".wav")
	h.Set("X-Source-Device", headerText(s.device()))
	if w := taterWord(it.meta.Model); w != "" {
		h.Set("X-Wake-Word", w)
	}
	h.Set("X-Event-Type", taterEvent(it.meta.Kind))
	h.Set("X-Captured-At", it.meta.At)
	// Tater reads the cutoff as a whole number, on microWakeWord's 0 to 255 scale; the peak goes on the
	// same scale so the two compare.
	h.Set("X-Max-Probability", byteScale(it.meta.Score))
	if it.meta.Cutoff > 0 {
		h.Set("X-Probability-Cutoff", byteScale(it.meta.Cutoff))
	}
	h.Set("X-Notes", fmt.Sprintf("techo5 %s, score %.3f, cutoff %.3f", it.meta.Kind, it.meta.Score, it.meta.Cutoff))
}

// sendMeta is the meta part of an upload to wakesmith or a custom receiver: the sidecar, and the name
// the clip goes by and the device it came from.
type sendMeta struct {
	Name   string  `json:"name"`
	Device string  `json:"device"`
	Kind   string  `json:"kind"`
	At     string  `json:"at"`
	Slot   int     `json:"slot"`
	Model  string  `json:"model"`
	Score  float64 `json:"score"`
	Cutoff float64 `json:"cutoff"`
}

// takeMeta is the meta part of a take's upload (wakesmith 0.4.12): the word in place of the model, and no
// score, which a take has none of. Word is the model id the wake clips send as "model", which wakesmith
// matches against the word's model-name filter; Run keeps a run's takes together, Index is which of it.
type takeMeta struct {
	Name   string `json:"name"`
	Device string `json:"device"`
	Kind   string `json:"kind"`
	At     string `json:"at"`
	Word   string `json:"word"`
	Index  int    `json:"index"`
	Run    string `json:"run"`
	Frame  string `json:"frame"`
	Front  string `json:"front,omitempty"`
}

// multipartRequest is the upload to wakesmith or a custom receiver, the clip streamed through a pipe
// from its file.
func (s *sender) multipartRequest(ctx context.Context, cfg config.WakeSend, it *sendItem, f io.Reader) (*http.Request, error) {
	model := it.meta.Model
	if it.meta.Kind == takeKind {
		model = it.meta.Word
	}
	if model == "" {
		model = "unknown" // wakesmith wants one
	}
	meta, _ := json.Marshal(sendMeta{Name: it.name, Device: s.device(), Kind: it.meta.Kind, At: it.meta.At,
		Slot: it.meta.Slot, Model: model, Score: it.meta.Score, Cutoff: it.meta.Cutoff})
	if it.meta.Kind == takeKind {
		meta, _ = json.Marshal(takeMeta{Name: it.name, Device: s.device(), Kind: takeKind, At: it.meta.At,
			Word: model, Index: it.meta.Index, Run: it.meta.Run, Frame: it.meta.Frame, Front: it.meta.Front})
	}
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() {
		err := func() error {
			mh := textproto.MIMEHeader{}
			mh.Set("Content-Disposition", `form-data; name="meta"`)
			mh.Set("Content-Type", "application/json")
			p, err := mw.CreatePart(mh)
			if err != nil {
				return err
			}
			if _, err := p.Write(meta); err != nil {
				return err
			}
			ah := textproto.MIMEHeader{}
			ah.Set("Content-Disposition", fmt.Sprintf(`form-data; name="audio"; filename="%s.wav"`, it.name))
			ah.Set("Content-Type", "audio/wav")
			p, err = mw.CreatePart(ah)
			if err != nil {
				return err
			}
			if _, err := io.Copy(p, f); err != nil {
				return err
			}
			return mw.Close()
		}()
		pw.CloseWithError(err)
	}()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.URL+"/api/clips", pr)
	if err != nil {
		pr.Close()
		return nil, err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if cfg.Token != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.Token)
	}
	return req, nil
}

// Ping asks the trainer whether it is there, and says what it found. Tater Trainer is asked for its
// session, which only it answers like this; wakesmith and a custom receiver for /api/clips/ping with
// the token.
func (s *sender) Ping(ctx context.Context, cfg config.WakeSend) (string, error) {
	return s.pingWith(ctx, cfg)
}

// pingWith is Ping. The pin is the one in the saved settings, which is what the client checks against.
func (s *sender) pingWith(ctx context.Context, cfg config.WakeSend) (string, error) {
	if err := s.CheckHome(ctx, cfg.URL); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	path := "/api/clips/ping"
	if cfg.Target == config.SendTater {
		path = "/api/session"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.URL+path, nil)
	if err != nil {
		return "", err
	}
	if cfg.Target != config.SendTater && cfg.Token != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.Token)
	}
	resp, err := s.clientFor(cfg.URL).Do(req)
	if err != nil {
		var ce *CertError
		if errors.As(err, &ce) {
			return "", ce
		}
		return "", errors.New("could not reach it: " + cleanErr(err))
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, sendAnswerMax))
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return "", errors.New("it answered, and refused the token" + answerText(body))
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		return "", fmt.Errorf("it answered %d, which is not what a trainer says%s", resp.StatusCode, answerText(body))
	}
	if cfg.Target == config.SendTater {
		var a map[string]any
		if json.Unmarshal(bytes.TrimSpace(body), &a) != nil || a["ok"] != true || a["safe_word"] == nil {
			return "", errors.New("something answered, but not Tater Trainer")
		}
		return "Tater Trainer answered", nil
	}
	return "it answered and accepted the token", nil
}
