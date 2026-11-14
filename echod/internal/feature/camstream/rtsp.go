package camstream

import (
	"bufio"
	"crypto/md5"
	"crypto/rand"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/lib/rtpjpeg"
)

// The RTSP side: OPTIONS, DESCRIBE, SETUP over TCP, PLAY, TEARDOWN and the keep-alives, with a Digest
// login on everything but OPTIONS. Enough for ffmpeg and what is built on it, which is what records
// cameras; nothing an RTSP server does beyond one stream to a few viewers.

const (
	realm = "TECHO5 camera"

	// loginWithin is how long a connection has from being opened to logging in, whatever it sends
	// meanwhile.
	loginWithin = 10 * time.Second

	// requestIdle is how long a logged-in connection may go without a request before it is closed, and
	// playIdle the same while it is being sent the camera: viewers send keep-alives far more often.
	requestIdle = 30 * time.Second
	playIdle    = 2 * time.Minute

	// writeWithin is how long one packet may take to go out before a viewer that has stopped reading is
	// let go.
	writeWithin = 5 * time.Second

	// maxLine is the longest line a request may have, maxHeader the most a request may say in all, and
	// maxTries the logins one connection may get wrong.
	maxLine   = 4096
	maxHeader = 16 << 10
	maxTries  = 5

	// mtu is the most an RTP packet carries, comfortably inside one TCP segment's worth.
	mtu = 1400
)

var errTooLong = errors.New("a request too long")

// session is one viewer's connection.
type session struct {
	f      *Feature
	conn   net.Conn
	r      *bufio.Reader
	from   string
	opened time.Time

	writeMu sync.Mutex
	nonce   string
	id      string
	tries   int
	authed  bool

	playing bool
	stop    func()      // lets the pictures go, while playing
	ending  atomic.Bool // the session let the pictures go, rather than the camera stopping
}

// request is one RTSP request.
type request struct {
	method, uri string
	headers     map[string]string // lowercased names
}

func (f *Feature) session(conn net.Conn) {
	s := &session{f: f, conn: conn, r: bufio.NewReaderSize(conn, maxLine), from: hostOf(conn.RemoteAddr()),
		opened: time.Now(), nonce: randomHex(16)}
	defer func() {
		s.end()
		_ = conn.Close()
		f.gone(conn)
	}()
	for {
		switch {
		case !s.authed:
			_ = conn.SetReadDeadline(s.opened.Add(loginWithin))
		case s.playing:
			_ = conn.SetReadDeadline(time.Now().Add(playIdle))
		default:
			_ = conn.SetReadDeadline(time.Now().Add(requestIdle))
		}
		// A viewer sends its RTCP reports on the same connection, framed as interleaved data: read past
		// them, once it is being sent the camera. Before that there is nothing for it to report on.
		if b, err := s.r.Peek(1); err != nil {
			return
		} else if b[0] == '$' {
			if !s.playing {
				return
			}
			var head [4]byte
			if _, err := io.ReadFull(s.r, head[:]); err != nil {
				return
			}
			if _, err := s.r.Discard(int(binary.BigEndian.Uint16(head[2:]))); err != nil {
				return
			}
			continue
		}
		req, err := readRequest(s.r)
		if err != nil {
			return
		}
		if !s.handle(req) {
			return
		}
	}
}

// end stops what the session started.
func (s *session) end() {
	s.ending.Store(true)
	if s.stop != nil {
		s.stop()
		s.stop = nil
	}
	s.playing = false
}

// readLine is one line, refused when longer than maxLine.
func readLine(r *bufio.Reader) (string, error) {
	b, err := r.ReadSlice('\n')
	if errors.Is(err, bufio.ErrBufferFull) {
		return "", errTooLong
	}
	return string(b), err
}

func readRequest(r *bufio.Reader) (request, error) {
	var req request
	line, err := readLine(r)
	if err != nil {
		return req, err
	}
	total := len(line)
	parts := strings.Fields(line)
	if len(parts) != 3 || !strings.HasPrefix(parts[2], "RTSP/1.") {
		return req, fmt.Errorf("not an RTSP request")
	}
	req.method, req.uri = parts[0], parts[1]
	req.headers = map[string]string{}
	for {
		h, err := readLine(r)
		if err != nil {
			return req, err
		}
		if total += len(h); total > maxHeader || len(req.headers) > 64 {
			return req, errTooLong
		}
		h = strings.TrimRight(h, "\r\n")
		if h == "" {
			break
		}
		name, value, ok := strings.Cut(h, ":")
		if ok {
			req.headers[strings.ToLower(strings.TrimSpace(name))] = strings.TrimSpace(value)
		}
	}
	// CSeq goes back in the answer: digits only, so nothing a request says is written into it.
	if c := req.headers["cseq"]; c == "" || len(c) > 10 || strings.Trim(c, "0123456789") != "" {
		req.headers["cseq"] = "0"
	}
	if n, err := strconv.Atoi(req.headers["content-length"]); err == nil && n > 0 {
		if n > maxHeader {
			return req, errTooLong
		}
		if _, err := r.Discard(n); err != nil {
			return req, err
		}
	}
	return req, nil
}

// reply sends a response, and answers whether the connection goes on.
func (s *session) reply(req request, status string, headers []string, body string) bool {
	var b strings.Builder
	b.WriteString("RTSP/1.0 " + status + "\r\n")
	b.WriteString("CSeq: " + req.headers["cseq"] + "\r\n")
	b.WriteString("Server: TECHO5\r\n")
	for _, h := range headers {
		b.WriteString(h + "\r\n")
	}
	if body != "" {
		b.WriteString("Content-Length: " + strconv.Itoa(len(body)) + "\r\n")
	}
	b.WriteString("\r\n" + body)
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_ = s.conn.SetWriteDeadline(time.Now().Add(writeWithin))
	_, err := io.WriteString(s.conn, b.String())
	return err == nil
}

// handle answers a request, and answers whether the connection goes on.
func (s *session) handle(req request) bool {
	if req.method == "OPTIONS" {
		return s.reply(req, "200 OK", []string{"Public: OPTIONS, DESCRIBE, SETUP, PLAY, TEARDOWN, GET_PARAMETER, SET_PARAMETER"}, "")
	}
	if !s.login(req) {
		if s.f.guard.refused(s.from) {
			return false
		}
		s.tries++
		if s.tries > maxTries {
			return false
		}
		return s.reply(req, "401 Unauthorized", []string{fmt.Sprintf(`WWW-Authenticate: Digest realm="%s", nonce="%s"`, realm, s.nonce)}, "")
	}
	if !ourPath(req.uri) {
		return s.reply(req, "404 Not Found", nil, "")
	}
	switch req.method {
	case "DESCRIBE":
		media := "m=video 0 RTP/AVP " + strconv.Itoa(rtpjpeg.PayloadType) + "\r\na=rtpmap:" + strconv.Itoa(rtpjpeg.PayloadType) +
			" JPEG/" + strconv.Itoa(rtpjpeg.ClockRate) + "\r\n"
		if e := s.f.encoder(); e != nil {
			media = e.sdp()
		}
		sdp := "v=0\r\no=- " + randomDigits() + " 1 IN IP4 0.0.0.0\r\ns=TECHO5 camera\r\nc=IN IP4 0.0.0.0\r\nt=0 0\r\n" +
			media + "a=control:track1\r\n"
		base := strings.TrimSuffix(req.uri, "/") + "/"
		return s.reply(req, "200 OK", []string{"Content-Type: application/sdp", "Content-Base: " + base}, sdp)
	case "SETUP":
		if !strings.Contains(req.headers["transport"], "RTP/AVP/TCP") {
			// RTP over UDP would be a port per viewer through the firewall: TCP only, which every viewer
			// that matters falls back to on this answer.
			return s.reply(req, "461 Unsupported Transport", nil, "")
		}
		if s.id == "" {
			s.id = randomHex(8)
		}
		return s.reply(req, "200 OK", []string{"Transport: RTP/AVP/TCP;unicast;interleaved=0-1", "Session: " + s.id + ";timeout=60"}, "")
	case "PLAY":
		if s.id == "" || !sameSession(req.headers["session"], s.id) {
			return s.reply(req, "454 Session Not Found", nil, "")
		}
		if !s.playing {
			pics, stop, err := s.f.subscribe()
			switch {
			case errors.Is(err, errFull):
				return s.reply(req, "453 Not Enough Bandwidth", nil, "")
			case err != nil:
				slog.Warn("camera streaming: the camera could not be started", "err", err)
				return s.reply(req, "503 Service Unavailable", nil, "")
			}
			s.playing, s.stop = true, stop
			if !s.reply(req, "200 OK", []string{"Session: " + s.id, "Range: npt=0.000-"}, "") {
				return false
			}
			go s.send(pics)
			return true
		}
		return s.reply(req, "200 OK", []string{"Session: " + s.id, "Range: npt=0.000-"}, "")
	case "TEARDOWN":
		s.end()
		s.reply(req, "200 OK", []string{"Session: " + s.id}, "")
		return false
	case "GET_PARAMETER", "SET_PARAMETER":
		return s.reply(req, "200 OK", []string{"Session: " + s.id}, "")
	}
	return s.reply(req, "405 Method Not Allowed", nil, "")
}

// send sends the pictures until they end or a write fails. A failed write, or the camera stopping by
// itself, closes the connection, which ends the session; the session letting them go does not, as its
// answer to TEARDOWN may still be on the way.
func (s *session) send(pics <-chan sendable) {
	defer func() {
		if !s.ending.Load() {
			_ = s.conn.Close()
		}
	}()
	ssrc := binary.BigEndian.Uint32(randomBytes(4))
	seq := binary.BigEndian.Uint16(randomBytes(2))
	start := time.Now()
	var clock keyClock // an encoder's pictures are timed from the first keyframe sent (encoder.go)
	for pic := range pics {
		ts, ok := clock.stamp(pic, uint32(time.Since(start).Seconds()*rtpjpeg.ClockRate))
		if !ok {
			continue // a viewer starts at a keyframe
		}
		for _, p := range pic.Packets(&seq, ts, ssrc, mtu) {
			if !s.write(p) {
				_ = s.conn.Close()
				return
			}
		}
	}
}

// write sends one RTP packet, framed for the RTSP connection.
func (s *session) write(p []byte) bool {
	head := [4]byte{'$', 0, 0, 0}
	binary.BigEndian.PutUint16(head[2:], uint16(len(p)))
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_ = s.conn.SetWriteDeadline(time.Now().Add(writeWithin))
	if _, err := s.conn.Write(head[:]); err != nil {
		return false
	}
	_, err := s.conn.Write(p)
	return err == nil
}

// pathOf is an address's path, whether it was given whole or as a path.
func pathOf(uri string) string {
	u, err := url.Parse(uri)
	if err != nil {
		return ""
	}
	return strings.TrimSuffix(u.Path, "/")
}

// ourPath is whether a request's address is the camera's (or its track's).
func ourPath(uri string) bool {
	p := pathOf(uri)
	return p == Path || p == Path+"/track1"
}

func sameSession(header, id string) bool {
	got, _, _ := strings.Cut(header, ";")
	return strings.TrimSpace(got) == id
}

// login is whether a request is logged in, counting the connection in on the first that is. A request
// with a wrong login is counted against its address; one with none is asked for one.
func (s *session) login(req request) bool {
	h, given := req.headers["authorization"]
	if !given {
		return false
	}
	ok, done := s.f.guard.attempt(s.from)
	if !ok {
		return false
	}
	good := false
	defer func() { done(!good) }() // counted however this ends
	good = authorized(req, h, s.nonce)
	if !good {
		return false
	}
	if !s.authed {
		s.authed = true
		s.f.loggedIn(s.conn)
	}
	return true
}

// authorized is whether an Authorization header is a Digest login for the camera's user and password,
// against this connection's nonce, for the address the request is for.
func authorized(req request, h, nonce string) bool {
	user, pass := config.Get().CamStream.Login()
	if pass == "" {
		return false
	}
	rest, ok := strings.CutPrefix(h, "Digest ")
	if !ok {
		return false
	}
	p := digestParams(rest)
	if p["realm"] != realm || p["nonce"] != nonce || p["uri"] == "" {
		return false
	}
	if p["uri"] != req.uri && pathOf(p["uri"]) != pathOf(req.uri) {
		return false
	}
	ha1 := md5hex(user + ":" + realm + ":" + pass)
	ha2 := md5hex(req.method + ":" + p["uri"])
	want := md5hex(ha1 + ":" + nonce + ":" + ha2)
	userOK := subtle.ConstantTimeCompare([]byte(user), []byte(p["username"]))
	return userOK&subtle.ConstantTimeCompare([]byte(want), []byte(p["response"])) == 1
}

// digestParams is a Digest header's name="value" pairs.
func digestParams(s string) map[string]string {
	out := map[string]string{}
	for s != "" {
		s = strings.TrimLeft(s, " ,")
		name, rest, ok := strings.Cut(s, "=")
		if !ok {
			break
		}
		name = strings.ToLower(strings.TrimSpace(name))
		var value string
		if strings.HasPrefix(rest, `"`) {
			end := strings.Index(rest[1:], `"`)
			if end < 0 {
				break
			}
			value, s = rest[1:1+end], rest[2+end:]
		} else {
			value, s, _ = strings.Cut(rest, ",")
		}
		out[name] = strings.TrimSpace(value)
	}
	return out
}

func md5hex(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

func randomBytes(n int) []byte {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return b
}

func randomHex(n int) string { return hex.EncodeToString(randomBytes(n)) }

func randomDigits() string {
	return strconv.FormatUint(uint64(binary.BigEndian.Uint32(randomBytes(4))), 10)
}
