// Package sealed is a connection encrypted with a key both ends already share: the Noise NNpsk0
// handshake, then every record encrypted and checked. The wrong key fails the handshake, and the key
// itself never crosses the network.
//
// On the wire, the handshake's two messages and then every record are a 4-byte big-endian length and
// that many bytes. The prologue names what the connection is for, so a key shared by two uses cannot
// open one with the other.
package sealed

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"

	"github.com/flynn/noise"
)

const (
	recordMax = 60000 // well inside Noise's 65535-byte message limit, with room for the tag
	wireMax   = recordMax + 64

	// handshakeMax is the most a handshake message may be. NNpsk0's are 48 bytes; a peer that
	// claims more, before it has shown the key, is not given the memory to say it.
	handshakeMax = 256
)

// ErrKey is a handshake the other end's key did not match.
var ErrKey = errors.New("sealed: the other end has a different key")

func suite() noise.CipherSuite {
	return noise.NewCipherSuite(noise.DH25519, noise.CipherChaChaPoly, noise.HashSHA256)
}

// Key turns a shared word into the 32 bytes Noise wants, whatever the word's length. label keeps
// one word's keys for different uses apart.
func Key(label, word string) []byte {
	sum := sha256.Sum256([]byte(label + ":" + word))
	return sum[:]
}

// Conn is a connection after the handshake: what is written is encrypted, what is read decrypted
// and checked. Writes may come from several goroutines; reads from one.
type Conn struct {
	net.Conn
	send, recv *noise.CipherState

	wmu  sync.Mutex
	rbuf []byte
}

// Client is the side that opened the connection.
func Client(c net.Conn, prologue string, key []byte) (*Conn, error) {
	hs, err := handshake(prologue, key, true)
	if err != nil {
		return nil, err
	}
	first, _, _, err := hs.WriteMessage(nil, nil)
	if err != nil {
		return nil, err
	}
	if err := writeFrame(c, first); err != nil {
		return nil, err
	}
	reply, err := readFrameMax(c, handshakeMax)
	if err != nil {
		return nil, err
	}
	_, send, recv, err := hs.ReadMessage(nil, reply)
	if err != nil {
		return nil, ErrKey
	}
	return &Conn{Conn: c, send: send, recv: recv}, nil
}

// Server is the side that took the connection.
func Server(c net.Conn, prologue string, key []byte) (*Conn, error) {
	hs, err := handshake(prologue, key, false)
	if err != nil {
		return nil, err
	}
	first, err := readFrameMax(c, handshakeMax)
	if err != nil {
		return nil, err
	}
	if _, _, _, err := hs.ReadMessage(nil, first); err != nil {
		return nil, ErrKey
	}
	reply, recv, send, err := hs.WriteMessage(nil, nil)
	if err != nil {
		return nil, err
	}
	if err := writeFrame(c, reply); err != nil {
		return nil, err
	}
	return &Conn{Conn: c, send: send, recv: recv}, nil
}

func handshake(prologue string, key []byte, initiator bool) (*noise.HandshakeState, error) {
	return noise.NewHandshakeState(noise.Config{
		CipherSuite: suite(), Random: rand.Reader, Pattern: noise.HandshakeNN, Initiator: initiator,
		Prologue: []byte(prologue), PresharedKey: key, PresharedKeyPlacement: 0,
	})
}

func (s *Conn) Write(p []byte) (int, error) {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	total := len(p)
	for len(p) > 0 {
		n := min(len(p), recordMax)
		ct, err := s.send.Encrypt(nil, nil, p[:n])
		if err != nil {
			return total - len(p), err
		}
		if err := writeFrame(s.Conn, ct); err != nil {
			return total - len(p), err
		}
		p = p[n:]
	}
	return total, nil
}

func (s *Conn) Read(p []byte) (int, error) {
	for len(s.rbuf) == 0 {
		ct, err := readFrame(s.Conn)
		if err != nil {
			return 0, err
		}
		pt, err := s.recv.Decrypt(nil, nil, ct)
		if err != nil {
			return 0, errors.New("sealed: a record that does not check out")
		}
		s.rbuf = pt
	}
	n := copy(p, s.rbuf)
	s.rbuf = s.rbuf[n:]
	return n, nil
}

func writeFrame(w io.Writer, b []byte) error {
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(b)))
	_, err := w.Write(append(hdr[:], b...))
	return err
}

func readFrame(r io.Reader) ([]byte, error) { return readFrameMax(r, wireMax) }

// readFrameMax reads one frame of at most most bytes, refusing a larger one before allocating it.
func readFrameMax(r io.Reader, most uint32) ([]byte, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n == 0 || n > most {
		return nil, fmt.Errorf("sealed: a record of %d bytes", n)
	}
	b := make([]byte, n)
	_, err := io.ReadFull(r, b)
	return b, err
}
