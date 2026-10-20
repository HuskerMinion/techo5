package camstream

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/lib/rtpjpeg"
)

// Wrong logins are answered at once in tests.
func TestMain(m *testing.M) {
	wrongDelay = 0
	os.Exit(m.Run())
}

// server is a Feature with a fake camera, listening on a free local port.
func server(t *testing.T) (*Feature, string, chan image.Image, chan bool) {
	t.Helper()
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if err := config.Set().CamStream().Login("viewer", "secret"); err != nil {
		t.Fatal(err)
	}
	pics := make(chan image.Image, 1)
	released := make(chan bool, 4)
	f := build()
	f.available = func() bool { return true }
	f.h264 = func() bool { return false }
	lazy := make(chan frame, 1)
	go func() {
		for img := range pics {
			lazy <- func() image.Image { return img }
		}
	}()
	f.frames = func() (<-chan frame, func(), error) {
		return lazy, func() { released <- true }, nil
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f.ln = ln
	t.Cleanup(f.close)
	go f.serve(ln)
	return f, ln.Addr().String(), pics, released
}

// client is a viewer.
type client struct {
	t     *testing.T
	conn  net.Conn
	r     *bufio.Reader
	url   string
	seq   int
	nonce string
	user  string
	pass  string
}

func dial(t *testing.T, addr, user, pass string) *client {
	t.Helper()
	return dialFrom(t, "127.0.0.1", addr, user, pass)
}

// dialFrom is a viewer from another loopback address, as another machine would be.
func dialFrom(t *testing.T, from, addr, user, pass string) *client {
	t.Helper()
	d := net.Dialer{LocalAddr: &net.TCPAddr{IP: net.ParseIP(from)}}
	conn, err := d.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	return &client{t: t, conn: conn, r: bufio.NewReader(conn), url: "rtsp://" + addr + Path, user: user, pass: pass}
}

type response struct {
	status  int
	headers map[string]string
	body    string
}

// do sends a request, with a Digest login once the server has asked for one, and reads the answer.
func (c *client) do(method, uri string, extra ...string) response {
	c.t.Helper()
	c.seq++
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s RTSP/1.0\r\nCSeq: %d\r\n", method, uri, c.seq)
	if c.nonce != "" {
		ha1 := md5hex(c.user + ":" + realm + ":" + c.pass)
		ha2 := md5hex(method + ":" + uri)
		fmt.Fprintf(&b, `Authorization: Digest username="%s", realm="%s", nonce="%s", uri="%s", response="%s"`+"\r\n",
			c.user, realm, c.nonce, uri, md5hex(ha1+":"+c.nonce+":"+ha2))
	}
	for _, h := range extra {
		b.WriteString(h + "\r\n")
	}
	b.WriteString("\r\n")
	if _, err := io.WriteString(c.conn, b.String()); err != nil {
		c.t.Fatal(err)
	}
	resp, err := c.read()
	if err != nil {
		c.t.Fatalf("%s: %v", method, err)
	}
	if got := resp.headers["cseq"]; got != strconv.Itoa(c.seq) {
		c.t.Fatalf("%s: CSeq %q, want %d", method, got, c.seq)
	}
	if resp.status == 401 && c.nonce == "" {
		p := digestParams(strings.TrimPrefix(resp.headers["www-authenticate"], "Digest "))
		c.nonce = p["nonce"]
		return c.do(method, uri, extra...)
	}
	return resp
}

func (c *client) read() (response, error) {
	resp := response{headers: map[string]string{}}
	line, err := c.r.ReadString('\n')
	if err != nil {
		return resp, err
	}
	parts := strings.Fields(line)
	if len(parts) < 2 || parts[0] != "RTSP/1.0" {
		return resp, fmt.Errorf("status line %q", line)
	}
	resp.status, _ = strconv.Atoi(parts[1])
	for {
		h, err := c.r.ReadString('\n')
		if err != nil {
			return resp, err
		}
		h = strings.TrimRight(h, "\r\n")
		if h == "" {
			break
		}
		name, value, _ := strings.Cut(h, ":")
		resp.headers[strings.ToLower(name)] = strings.TrimSpace(value)
	}
	if n, _ := strconv.Atoi(resp.headers["content-length"]); n > 0 {
		body := make([]byte, n)
		if _, err := io.ReadFull(c.r, body); err != nil {
			return resp, err
		}
		resp.body = string(body)
	}
	return resp, nil
}

// packet reads one interleaved packet.
func (c *client) packet() (channel byte, p []byte) {
	c.t.Helper()
	var head [4]byte
	if _, err := io.ReadFull(c.r, head[:]); err != nil {
		c.t.Fatal(err)
	}
	if head[0] != '$' {
		c.t.Fatalf("interleaved frame starts %q", head[0])
	}
	p = make([]byte, binary.BigEndian.Uint16(head[2:]))
	if _, err := io.ReadFull(c.r, p); err != nil {
		c.t.Fatal(err)
	}
	return head[1], p
}

func picture(w, h int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), 128, 255})
		}
	}
	return img
}

func TestStreamEndToEnd(t *testing.T) {
	_, addr, pics, released := server(t)
	c := dial(t, addr, "viewer", "secret")

	if r := c.do("OPTIONS", c.url); r.status != 200 || !strings.Contains(r.headers["public"], "PLAY") {
		t.Fatalf("OPTIONS: %d %v", r.status, r.headers)
	}
	r := c.do("DESCRIBE", c.url, "Accept: application/sdp")
	if r.status != 200 || !strings.Contains(r.body, "m=video 0 RTP/AVP 26") || !strings.Contains(r.body, "JPEG/90000") {
		t.Fatalf("DESCRIBE: %d %q", r.status, r.body)
	}
	if c.nonce == "" {
		t.Fatal("DESCRIBE was let through without a login")
	}
	track := r.headers["content-base"] + "track1"
	r = c.do("SETUP", track, "Transport: RTP/AVP/TCP;unicast;interleaved=0-1")
	if r.status != 200 || !strings.Contains(r.headers["transport"], "interleaved=0-1") {
		t.Fatalf("SETUP: %d %v", r.status, r.headers)
	}
	id, _, _ := strings.Cut(r.headers["session"], ";")
	if r = c.do("PLAY", c.url, "Session: "+id); r.status != 200 {
		t.Fatalf("PLAY: %d", r.status)
	}

	// 61 by 45 goes out as 56 by 40: RTP/JPEG says its size in eighths.
	pics <- picture(61, 45)
	var marked bool
	for i := 0; i < 100 && !marked; i++ {
		ch, p := c.packet()
		if ch != 0 {
			t.Fatalf("channel %d", ch)
		}
		if pt := p[1] & 0x7f; pt != rtpjpeg.PayloadType {
			t.Fatalf("payload type %d", pt)
		}
		if i == 0 {
			if w, h := int(p[12+6])*8, int(p[12+7])*8; w != 56 || h != 40 {
				t.Fatalf("size %dx%d", w, h)
			}
		}
		marked = p[1]&0x80 != 0
	}
	if !marked {
		t.Fatal("no packet ended the picture")
	}

	// The viewer's own reports come in on the same connection, and are read past.
	if _, err := c.conn.Write([]byte{'$', 1, 0, 3, 1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	if r = c.do("GET_PARAMETER", c.url, "Session: "+id); r.status != 200 {
		t.Fatalf("GET_PARAMETER: %d", r.status)
	}
	if r = c.do("TEARDOWN", c.url, "Session: "+id); r.status != 200 {
		t.Fatalf("TEARDOWN: %d", r.status)
	}
	select {
	case <-released:
	case <-time.After(5 * time.Second):
		t.Fatal("the camera was not let go after TEARDOWN")
	}
}

func TestWrongLoginRefused(t *testing.T) {
	_, addr, _, _ := server(t)
	c := dial(t, addr, "viewer", "guess")
	if r := c.do("DESCRIBE", c.url); r.status != 401 {
		t.Fatalf("a wrong password: %d", r.status)
	}
	c = dial(t, addr, "someone", "secret")
	if r := c.do("DESCRIBE", c.url); r.status != 401 {
		t.Fatalf("a wrong user: %d", r.status)
	}
	// Enough wrong answers and the connection is closed.
	for i := 0; i < maxTries+2; i++ {
		c.seq++
		fmt.Fprintf(c.conn, "DESCRIBE %s RTSP/1.0\r\nCSeq: %d\r\n\r\n", c.url, c.seq)
		if _, err := c.read(); err != nil {
			return
		}
	}
	t.Fatal("the connection stayed open through wrong logins")
}

func TestNoPasswordNoLogin(t *testing.T) {
	_, addr, _, _ := server(t)
	config.Use(filepath.Join(t.TempDir(), "fresh.json")) // a device never given a password
	c := dial(t, addr, config.DefaultCamStreamUser, "")
	if r := c.do("DESCRIBE", c.url); r.status != 401 {
		t.Fatalf("an empty password: %d", r.status)
	}
}

func TestUDPRefused(t *testing.T) {
	_, addr, _, _ := server(t)
	c := dial(t, addr, "viewer", "secret")
	r := c.do("SETUP", c.url+"/track1", "Transport: RTP/AVP;unicast;client_port=5000-5001")
	if r.status != 461 {
		t.Fatalf("SETUP over UDP: %d", r.status)
	}
}

func TestOtherPathNotFound(t *testing.T) {
	_, addr, _, _ := server(t)
	c := dial(t, addr, "viewer", "secret")
	if r := c.do("DESCRIBE", "rtsp://"+addr+"/elsewhere"); r.status != 404 {
		t.Fatalf("another path: %d", r.status)
	}
}

func TestPlayNeedsSetup(t *testing.T) {
	_, addr, _, _ := server(t)
	c := dial(t, addr, "viewer", "secret")
	if r := c.do("PLAY", c.url, "Session: 1234"); r.status != 454 {
		t.Fatalf("PLAY before SETUP: %d", r.status)
	}
}

func TestDigestParams(t *testing.T) {
	p := digestParams(`username="a, b", realm="TECHO5 camera",nonce="n1", uri="rtsp://x/camera", response="r", algorithm=MD5`)
	want := map[string]string{"username": "a, b", "realm": "TECHO5 camera", "nonce": "n1", "uri": "rtsp://x/camera", "response": "r", "algorithm": "MD5"}
	for k, v := range want {
		if p[k] != v {
			t.Errorf("%s = %q, want %q", k, p[k], v)
		}
	}
}

func TestEighths(t *testing.T) {
	if b := eighths(picture(64, 48)).Bounds(); b.Dx() != 64 || b.Dy() != 48 {
		t.Fatalf("%v", b)
	}
	if b := eighths(picture(70, 50)).Bounds(); b.Dx() != 64 || b.Dy() != 48 {
		t.Fatalf("%v", b)
	}
}

func TestNoCameraNoSwitch(t *testing.T) {
	f := build()
	f.available = func() bool { return false }
	if len(f.Entities()) != 0 {
		t.Fatal("a switch on a device with no camera")
	}
}
