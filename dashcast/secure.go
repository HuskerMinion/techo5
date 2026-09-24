package main

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

// The connection between a device and dashcast is encrypted with Noise, keyed by DASHCAST_KEY: the
// NNpsk0 handshake, the same family the device's link to Home Assistant uses. Both ends mix the key
// into the handshake, so a device with the wrong key simply fails it; the key itself never crosses
// the network, and everything after the handshake - the device's hello, the pictures, the touches -
// is encrypted and authenticated.
//
// On the wire, the handshake's two messages and then every record are a 4-byte big-endian length and
// that many bytes. A record holds at most recordMax bytes of the stream it carries, so a picture of
// any size is as many records as it takes. The echod side is echod/internal/feature/dashboard/secure.go,
// which has to match this one.

const (
	prologue  = "techo5-dashcast/1"
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
	sum := sha256.Sum256([]byte("techo5-dashcast psk:" + key))
	return sum[:]
}

// secureConn is a connection after the handshake: what is written is encrypted, what is read
// decrypted and checked.
type secureConn struct {
	net.Conn
	send, recv *noise.CipherState

	wmu  sync.Mutex
	rbuf []byte
}

func writeFrame(w io.Writer, b []byte) error {
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(b)))
	if _, err := w.Write(append(hdr[:], b...)); err != nil {
		return err
	}
	return nil
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
		return nil, fmt.Errorf("secure: a record of %d bytes", n)
	}
	b := make([]byte, n)
	_, err := io.ReadFull(r, b)
	return b, err
}

// serverHandshake answers a device's handshake. It fails when the device's key is not this one.
func serverHandshake(c net.Conn, key string) (*secureConn, error) {
	hs, err := noise.NewHandshakeState(noise.Config{
		CipherSuite: suite(), Random: rand.Reader, Pattern: noise.HandshakeNN, Initiator: false,
		Prologue: []byte(prologue), PresharedKey: psk(key), PresharedKeyPlacement: 0,
	})
	if err != nil {
		return nil, err
	}
	first, err := readFrameMax(c, handshakeMax)
	if err != nil {
		return nil, err
	}
	if _, _, _, err := hs.ReadMessage(nil, first); err != nil {
		return nil, errors.New("secure: the device's key is not this server's")
	}
	reply, fromDevice, toDevice, err := hs.WriteMessage(nil, nil)
	if err != nil {
		return nil, err
	}
	if err := writeFrame(c, reply); err != nil {
		return nil, err
	}
	// The first cipher state carries what the initiator (the device) sends, the second the reply.
	return &secureConn{Conn: c, send: toDevice, recv: fromDevice}, nil
}

// clientHandshake is a device's side, as echod does it; here for the tests.
func clientHandshake(c net.Conn, key string) (*secureConn, error) {
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
	reply, err := readFrameMax(c, handshakeMax)
	if err != nil {
		return nil, err
	}
	_, toServer, fromServer, err := hs.ReadMessage(nil, reply)
	if err != nil {
		return nil, err
	}
	return &secureConn{Conn: c, send: toServer, recv: fromServer}, nil
}

func (s *secureConn) Write(p []byte) (int, error) {
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
func (s *secureConn) Read(p []byte) (int, error) {
	for len(s.rbuf) == 0 {
		ct, err := readFrame(s.Conn)
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
