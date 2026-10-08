// Package deckwire is how a Show and the TECHO5 Deck agent on a computer talk: one request and one
// answer at a time over TCP, encrypted with Noise NNpsk0 keyed by the pairing key the agent shows.
// Both ends mix the key into the handshake, so a wrong key simply fails it and the key itself never
// crosses the network; everything after the handshake is encrypted and authenticated.
//
// On the wire, the handshake's two messages and then every record are a 4-byte big-endian length and
// that many bytes. A record is one JSON message.
package deckwire

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"github.com/flynn/noise"
)

const (
	// Port is where the agent listens unless told otherwise.
	Port = 4470
	// Service is what the agent advertises over mDNS.
	Service = "_techo5-deck._tcp"

	prologue  = "techo5-deck/1"
	recordMax = 64 << 10
	// Timeout bounds a request with its answer, and HandshakeTimeout the handshake: a connection that
	// says nothing is dropped quickly, so a few of them can't hold the agent's few slots for long.
	Timeout          = 5 * time.Second
	HandshakeTimeout = 3 * time.Second
)

// The things a Show can ask of an agent.
const (
	OpHello   = "hello"   // who the agent is and what it can do; Data is a Hello
	OpKeys    = "keys"    // press a combination: Arg like "ctrl+shift+m" or "media_play_pause"
	OpType    = "type"    // type Arg as text
	OpOpen    = "open"    // open Arg: an http(s) address, or an app by a name from Hello.Apps
	OpRun     = "run"     // run the script named Arg, from the agent's own list (Hello.Scripts)
	OpRefresh = "refresh" // read the app and script lists again, then answer as hello
)

// Request is what a Show sends.
type Request struct {
	Op  string `json:"op"`
	Arg string `json:"arg,omitempty"`
}

// Reply is what the agent answers. Error is set when it could not do it.
type Reply struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	Hello *Hello `json:"hello,omitempty"`
}

// Hello is the agent describing itself.
type Hello struct {
	Name    string   `json:"name"`              // the computer's name
	OS      string   `json:"os"`                // windows, darwin, linux
	Version string   `json:"version,omitempty"` // the agent's
	Keys    bool     `json:"keys"`              // whether it can press keys here (macOS without permission can't)
	Apps    []string `json:"apps,omitempty"`    // apps it can open by name
	Scripts []string `json:"scripts,omitempty"` // the names in its script list
}

// Limits on what is carried, so neither end can be made to hold much.
const (
	MaxArg   = 4096
	MaxNames = 2000
)

// NewKey is a fresh pairing key: 20 random bytes as 32 letters and digits in groups of four, easy
// to read off one screen and type on another.
func NewKey() (string, error) {
	var b [20]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	s := base32.StdEncoding.EncodeToString(b[:])
	var g []string
	for i := 0; i < len(s); i += 4 {
		g = append(g, s[i:i+4])
	}
	return strings.Join(g, "-"), nil
}

// NormalizeKey takes a key as somebody typed it: case, spaces and dashes don't matter.
func NormalizeKey(k string) string {
	k = strings.ToUpper(k)
	return strings.NewReplacer("-", "", " ", "", "\t", "").Replace(k)
}

func suite() noise.CipherSuite {
	return noise.NewCipherSuite(noise.DH25519, noise.CipherChaChaPoly, noise.HashSHA256)
}

func psk(key string) []byte {
	sum := sha256.Sum256([]byte("techo5-deck psk:" + NormalizeKey(key)))
	return sum[:]
}

// Conn is a connection after the handshake.
type Conn struct {
	c          net.Conn
	send, recv *noise.CipherState
}

func writeFrame(w io.Writer, b []byte) error {
	buf := make([]byte, 4+len(b))
	binary.BigEndian.PutUint32(buf, uint32(len(b)))
	copy(buf[4:], b)
	_, err := w.Write(buf)
	return err
}

func readFrame(r io.Reader) ([]byte, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n == 0 || n > recordMax+64 {
		return nil, fmt.Errorf("deckwire: a record of %d bytes", n)
	}
	b := make([]byte, n)
	_, err := io.ReadFull(r, b)
	return b, err
}

func handshake(c net.Conn, key string, initiator bool) (*Conn, error) {
	hs, err := noise.NewHandshakeState(noise.Config{
		CipherSuite: suite(), Random: rand.Reader, Pattern: noise.HandshakeNN, Initiator: initiator,
		Prologue: []byte(prologue), PresharedKey: psk(key), PresharedKeyPlacement: 0,
	})
	if err != nil {
		return nil, err
	}
	_ = c.SetDeadline(time.Now().Add(HandshakeTimeout))
	defer c.SetDeadline(time.Time{})
	if initiator {
		first, _, _, err := hs.WriteMessage(nil, nil)
		if err != nil {
			return nil, err
		}
		if err := writeFrame(c, first); err != nil {
			return nil, err
		}
		reply, err := readFrame(c)
		if err != nil {
			return nil, err
		}
		if string(reply) == wrongKey {
			return nil, ErrKey
		}
		_, send, recv, err := hs.ReadMessage(nil, reply)
		if err != nil {
			return nil, ErrKey
		}
		return &Conn{c: c, send: send, recv: recv}, nil
	}
	first, err := readFrame(c)
	if err != nil {
		return nil, err
	}
	if _, _, _, err := hs.ReadMessage(nil, first); err != nil {
		// Said in the clear before hanging up, so the Show can tell a wrong key from a computer that
		// isn't there. It says nothing about the key itself.
		_ = writeFrame(c, []byte(wrongKey))
		return nil, ErrKey
	}
	reply, recv, send, err := hs.WriteMessage(nil, nil)
	if err != nil {
		return nil, err
	}
	if err := writeFrame(c, reply); err != nil {
		return nil, err
	}
	return &Conn{c: c, send: send, recv: recv}, nil
}

// wrongKey is the agent's answer to a handshake made with another key.
const wrongKey = "deckwire: wrong key"

// ErrKey is a handshake that failed: the two ends hold different keys.
var ErrKey = errors.New("deckwire: the pairing key doesn't match")

// Dial connects to an agent at addr (host or host:port) with key.
func Dial(addr, key string) (*Conn, error) {
	if _, _, err := net.SplitHostPort(addr); err != nil {
		addr = net.JoinHostPort(addr, fmt.Sprint(Port))
	}
	c, err := net.DialTimeout("tcp", addr, Timeout)
	if err != nil {
		return nil, err
	}
	conn, err := handshake(c, key, true)
	if err != nil {
		c.Close()
		return nil, err
	}
	return conn, nil
}

// Accept is the agent's side of a connection that just arrived.
func Accept(c net.Conn, key string) (*Conn, error) {
	return handshake(c, key, false)
}

func (s *Conn) Close() error { return s.c.Close() }

// RemoteAddr is who is on the other end.
func (s *Conn) RemoteAddr() net.Addr { return s.c.RemoteAddr() }

// Send writes one message.
func (s *Conn) Send(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if len(b) > recordMax {
		return fmt.Errorf("deckwire: a message of %d bytes", len(b))
	}
	ct, err := s.send.Encrypt(nil, nil, b)
	if err != nil {
		return err
	}
	_ = s.c.SetWriteDeadline(time.Now().Add(Timeout))
	return writeFrame(s.c, ct)
}

// Receive reads one message into v, waiting up to wait (zero waits forever).
func (s *Conn) Receive(v any, wait time.Duration) error {
	if wait > 0 {
		_ = s.c.SetReadDeadline(time.Now().Add(wait))
	} else {
		_ = s.c.SetReadDeadline(time.Time{})
	}
	ct, err := readFrame(s.c)
	if err != nil {
		return err
	}
	pt, err := s.recv.Decrypt(nil, nil, ct)
	if err != nil {
		return errors.New("deckwire: a record that does not check out")
	}
	return json.Unmarshal(pt, v)
}

// Ask sends a request and waits for the answer; a refusal comes back as an error.
func (s *Conn) Ask(req Request) (Reply, error) {
	if err := s.Send(req); err != nil {
		return Reply{}, err
	}
	var r Reply
	if err := s.Receive(&r, Timeout); err != nil {
		return Reply{}, err
	}
	if !r.OK {
		if r.Error == "" {
			r.Error = "refused"
		}
		return r, errors.New(r.Error)
	}
	return r, nil
}

// Do dials, asks once and hangs up: what a deck button does.
func Do(addr, key string, req Request) (Reply, error) {
	c, err := Dial(addr, key)
	if err != nil {
		return Reply{}, err
	}
	defer c.Close()
	return c.Ask(req)
}
