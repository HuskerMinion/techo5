// The connection between a phone and this device is encrypted with Noise, keyed by a pairing key: the
// NNpsk0 handshake, as the dashboard stream's is. This file is the device's copy of wire/secure.go in
// the techo5-cast repository, whose docs/protocol.md is the specification; the two have to match on
// the wire.
package cast

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
	prologue  = "techo5-cast/1"
	recordMax = 60000 // well inside Noise's 65535-byte message limit, with room for the tag
	wireMax   = recordMax + 64

	// handshakeMax is the most a handshake message may be. NNpsk0's are 48 bytes; a connection that
	// claims more, before it has shown the key, is not given the memory to say it.
	handshakeMax = 256
)

func suite() noise.CipherSuite {
	return noise.NewCipherSuite(noise.DH25519, noise.CipherChaChaPoly, noise.HashSHA256)
}

// psk is the key as Noise wants it: 32 bytes, whatever the key's length.
func psk(key string) []byte {
	sum := sha256.Sum256([]byte("techo5-cast psk:" + key))
	return sum[:]
}

// secure is a connection after the handshake: what is written is encrypted, what is read decrypted
// and checked.
type secure struct {
	net.Conn
	send, recv *noise.CipherState

	wmu  sync.Mutex
	rbuf []byte
}

func writeFrame(w io.Writer, b []byte) error {
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(b)))
	_, err := w.Write(append(hdr[:], b...))
	return err
}

func readFrame(r io.Reader, most uint32) ([]byte, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n == 0 || n > most {
		return nil, fmt.Errorf("secure: a record of %d bytes", n)
	}
	b := make([]byte, n)
	_, err := io.ReadFull(r, b)
	return b, err
}

// dial is the phone's side, here for the tests.
func dial(c net.Conn, key string) (*secure, error) {
	hs, err := noise.NewHandshakeState(noise.Config{
		CipherSuite: suite(), Random: rand.Reader, Pattern: noise.HandshakeNN, Initiator: true,
		Prologue: []byte(prologue), PresharedKey: psk(key), PresharedKeyPlacement: 0,
	})
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
	reply, err := readFrame(c, handshakeMax)
	if err != nil {
		return nil, err
	}
	_, toDevice, fromDevice, err := hs.ReadMessage(nil, reply)
	if err != nil {
		return nil, errors.New("secure: the device's key is not this one")
	}
	return &secure{Conn: c, send: toDevice, recv: fromDevice}, nil
}

// accept is the device's side. It fails when the phone's key is not this device's.
func accept(c net.Conn, key string) (*secure, error) {
	hs, err := noise.NewHandshakeState(noise.Config{
		CipherSuite: suite(), Random: rand.Reader, Pattern: noise.HandshakeNN, Initiator: false,
		Prologue: []byte(prologue), PresharedKey: psk(key), PresharedKeyPlacement: 0,
	})
	if err != nil {
		return nil, err
	}
	first, err := readFrame(c, handshakeMax)
	if err != nil {
		return nil, err
	}
	if _, _, _, err := hs.ReadMessage(nil, first); err != nil {
		return nil, errors.New("secure: the sender's key is not this device's")
	}
	reply, fromSender, toSender, err := hs.WriteMessage(nil, nil)
	if err != nil {
		return nil, err
	}
	if err := writeFrame(c, reply); err != nil {
		return nil, err
	}
	return &secure{Conn: c, send: toSender, recv: fromSender}, nil
}

func (s *secure) Write(p []byte) (int, error) {
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

// Read is only ever called from one goroutine, the one reading the connection.
func (s *secure) Read(p []byte) (int, error) {
	for len(s.rbuf) == 0 {
		ct, err := readFrame(s.Conn, wireMax)
		if err != nil {
			return 0, err
		}
		pt, err := s.recv.Decrypt(nil, nil, ct)
		if err != nil {
			return 0, errors.New("secure: a record that does not check out")
		}
		s.rbuf = pt
	}
	n := copy(p, s.rbuf)
	s.rbuf = s.rbuf[n:]
	return n, nil
}
