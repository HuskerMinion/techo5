//go:build !dot

package video

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// The guard is the fence (fence.go) for a kernel that cannot match a packet to its user: the Spot's
// has no owner match. The decoder is then given a proxy, here in the daemon on loopback, and fetches
// everything through it: every name is resolved and every connection made here, after the redirect
// that led to it and for every part of a playlist, and one to the device itself (loopback, its own
// addresses), link-local or multicast is refused. ffmpeg's http, https (through CONNECT) and HLS all
// take the proxy; the hls demuxer opens nothing but http and https, so no part of a stream leaves it.
//
// It takes only the decoder: a password made at random for this run of the daemon, in the proxy's
// address ffmpeg is given.

var (
	guardMu  sync.Mutex
	guardURL string
	guardSrv *http.Server
)

// closeGuard stops the proxy, when Video is turned off: nothing is listening for the decoder while no
// video can play. The next video starts it again, with a new password.
func closeGuard() {
	guardMu.Lock()
	defer guardMu.Unlock()
	if guardSrv != nil {
		_ = guardSrv.Close()
		guardSrv, guardURL = nil, ""
	}
}

// guardProxy is the proxy's address for ffmpeg's -http_proxy, password and all, starting it the first
// time it is wanted (and again later, if it could not be started then).
func guardProxy() (string, error) {
	guardMu.Lock()
	defer guardMu.Unlock()
	// Under the lock closeGuard takes, after the switch is saved: once Video is off, the proxy either
	// was open and is closed by it, or is not opened again here.
	if !guardAllowed() {
		return "", ErrOff
	}
	if guardURL != "" {
		return guardURL, nil
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if errors.Is(err, syscall.EADDRNOTAVAIL) {
		// An image from before boot brought loopback up (techo5-lib.sh's t5_wifi_up), or a test daemon
		// run on one: loopback is down, with no 127.0.0.1 on it. Brought up here as well, so the guard
		// works whatever the image; on a current image this is never reached.
		loopbackUp()
		ln, err = net.Listen("tcp", "127.0.0.1:0")
	}
	if err != nil {
		return "", fmt.Errorf("the decoder's proxy: %w", err)
	}
	var b [16]byte
	_, _ = rand.Read(b[:])
	pass := hex.EncodeToString(b[:])
	g := &guard{auth: "Basic " + base64.StdEncoding.EncodeToString([]byte("video:"+pass))}
	srv := &http.Server{Handler: g, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	guardSrv = srv
	guardURL = "http://video:" + pass + "@" + ln.Addr().String()
	slog.Info("video: the decoder fetches through a proxy of the daemon's (no owner match in this kernel)")
	return guardURL, nil
}

// loopbackUp brings the loopback interface up with its address; a variable for the tests.
var loopbackUp = func() {
	for _, args := range [][]string{{"link", "set", "lo", "up"}, {"addr", "add", "127.0.0.1/8", "dev", "lo"}} {
		if out, err := exec.Command("ip", args...).CombinedOutput(); err != nil {
			slog.Info("video: bringing loopback up", "ip", args, "err", err, "said", clip(string(out), 200))
		}
	}
}

// guard is the proxy.
type guard struct{ auth string }

// refused is whether the guard keeps the decoder off ip; a variable for the tests.
var refused = func(ip net.IP) bool {
	return ip == nil || ip.IsLoopback() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsMulticast() || ownIP(ip)
}

// ownIP is whether ip is one of the device's own addresses, read at each connection; not knowing them,
// the guard takes every address for its own.
func ownIP(ip net.IP) bool {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return true
	}
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.Equal(ip) {
			return true
		}
	}
	return false
}

// guardDialer makes the guard's connections, refusing one that would reach the device whatever the name
// or the DNS answer that led there: checked as the connection is made.
var guardDialer = &net.Dialer{
	Timeout: 10 * time.Second,
	Control: func(_, address string, _ syscall.RawConn) error {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return err
		}
		if refused(net.ParseIP(host)) {
			return fmt.Errorf("the decoder may not reach %s", host)
		}
		return nil
	},
}

// guardTransport fetches for the decoder, never following a redirect itself: ffmpeg follows it, and its
// next request comes back here to be checked like the first.
var guardTransport = &http.Transport{
	Proxy:                 nil,
	DialContext:           guardDialer.DialContext,
	ResponseHeaderTimeout: 15 * time.Second,
	DisableCompression:    true,
	MaxIdleConnsPerHost:   2,
	IdleConnTimeout:       30 * time.Second,
}

// hopHeaders are a proxy's own, not passed on.
var hopHeaders = []string{"Proxy-Authorization", "Proxy-Connection", "Connection", "Keep-Alive", "Te", "Trailer",
	"Transfer-Encoding", "Upgrade"}

func (g *guard) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("Proxy-Authorization")), []byte(g.auth)) != 1 {
		w.Header().Set("Proxy-Authenticate", `Basic realm="techo5"`)
		http.Error(w, "proxy authorization required", http.StatusProxyAuthRequired)
		return
	}
	if r.Method == http.MethodConnect {
		g.tunnel(w, r)
		return
	}
	if r.URL.Scheme != "http" || r.URL.Host == "" {
		http.Error(w, "an absolute http address is wanted", http.StatusBadRequest)
		return
	}
	out := r.Clone(r.Context())
	out.RequestURI = ""
	for _, h := range hopHeaders {
		out.Header.Del(h)
	}
	resp, err := guardTransport.RoundTrip(out)
	if err != nil {
		slog.Info("video: the decoder's fetch was refused or failed", "host", r.URL.Hostname(), "err", err)
		http.Error(w, "refused", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	for _, h := range hopHeaders {
		resp.Header.Del(h)
	}
	for k, v := range resp.Header {
		w.Header()[k] = v
	}
	w.WriteHeader(resp.StatusCode)
	fl, _ := w.(http.Flusher)
	buf := make([]byte, 64<<10)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return
			}
			if fl != nil {
				fl.Flush()
			}
		}
		if err != nil {
			return
		}
	}
}

// tunnel is CONNECT, which is how ffmpeg reaches an https address through the guard.
func (g *guard) tunnel(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	up, err := guardDialer.DialContext(ctx, "tcp", r.Host)
	if err != nil {
		slog.Info("video: the decoder's connection was refused or failed", "to", r.Host, "err", err)
		http.Error(w, "refused", http.StatusBadGateway)
		return
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		up.Close()
		http.Error(w, "no tunnel", http.StatusInternalServerError)
		return
	}
	down, rw, err := hj.Hijack()
	if err != nil {
		up.Close()
		return
	}
	_, _ = down.Write([]byte("HTTP/1.1 200 Connection established\r\n\r\n"))
	done := make(chan struct{}, 2)
	go func() {
		// What the client sent after its CONNECT and the server read ahead of the hijack goes first.
		if n := rw.Reader.Buffered(); n > 0 {
			b, _ := rw.Reader.Peek(n)
			_, _ = up.Write(b)
		}
		_, _ = io.Copy(up, down)
		done <- struct{}{}
	}()
	go func() { _, _ = io.Copy(down, up); done <- struct{}{} }()
	<-done
	up.Close()
	down.Close()
}

// guardAllowed is whether videos may play, which the proxy is for; a variable for the tests.
var guardAllowed = func() bool { return config.Get().Video.On }

// netGuard is how the decoder's network is kept off the device for this run: "" when the kernel fences
// its user (fence.go), else the guard's address for -http_proxy. An error is neither: no decoder. A
// session stopped meanwhile, or Video turned off, gets nothing.
func netGuard(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if !guardAllowed() {
		return "", ErrOff
	}
	cred, err := decoderCred()
	if err != nil {
		return "", err
	}
	if cred == nil {
		return "", nil // the tests' stand-in
	}
	ferr := fence(cred.Uid)
	if ferr == nil {
		return "", nil
	}
	p, perr := guardProxy()
	if perr != nil {
		return "", errors.Join(ferr, perr)
	}
	return p, nil
}
