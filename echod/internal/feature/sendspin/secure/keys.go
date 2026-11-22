// Package secure is the encrypted Sendspin connection: the Noise KKpsk2 handshake with the server as
// initiator and this device as responder, the transport that follows it, and what the device keeps to
// recognize a server it has paired with.
//
// The wire is the Sendspin 1.0 specification's: cleartext client/init and server/init, the two
// noise/handshake messages, then every message a binary WebSocket frame holding one Noise transport
// message. Inside, the first byte says what follows: 0 is a JSON message, 1 a fragment of a message too
// long for one Noise message, and anything else a role's binary message as it would travel unencrypted.
package secure

import (
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
)

// keySize is a Curve25519 key's size and a PSK's: both are 32 bytes.
const keySize = 32

// b64 is the encoding every key, PSK id and handshake message on the wire uses.
var b64 = base64.RawURLEncoding

// Category is what a PSK is used as, as the server's first handshake message declares it.
type Category string

const (
	LongTerm Category = "lt"
	Pairing  Category = "pr"
	Sentinel Category = "sn"
)

func (c Category) valid() bool { return c == LongTerm || c == Pairing || c == Sentinel }

// sentinelPSK is the published PSK used whenever no other applies. It authenticates nothing: its value
// is public. A server that has not paired with this device keys the handshake with it.
var sentinelPSK = func() []byte {
	h := sha256.Sum256([]byte("sendspin-sentinel-psk-v1"))
	return h[:]
}()

// pskID is how the server names a PSK without revealing it.
func pskID(psk []byte) string {
	h := sha256.Sum256(append([]byte("sendspin-psk-id-v1"), psk...))
	return b64.EncodeToString(h[:])
}

// random is 32 bytes from the system's CSPRNG: a private key or a PSK.
func random() ([]byte, error) {
	b := make([]byte, keySize)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	return b, nil
}

// publicKey is the Curve25519 public key for a private one.
func publicKey(priv []byte) ([]byte, error) {
	k, err := ecdh.X25519().NewPrivateKey(priv)
	if err != nil {
		return nil, err
	}
	return k.PublicKey().Bytes(), nil
}

// peerKey decodes a client_id or server_id: 43 characters of base64url, a 32-byte public key.
func peerKey(id string) ([]byte, error) {
	if len(id) != 43 {
		return nil, fmt.Errorf("an id is 43 characters, this one is %d", len(id))
	}
	b, err := b64.DecodeString(id)
	if err != nil {
		return nil, err
	}
	if len(b) != keySize {
		return nil, errors.New("an id is a 32-byte key")
	}
	// The canonical encoding only: two spellings of one key would be two servers to a record lookup.
	if b64.EncodeToString(b) != id {
		return nil, errors.New("an id that is not canonical base64url")
	}
	return b, nil
}
