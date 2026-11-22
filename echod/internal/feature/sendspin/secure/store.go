package secure

import (
	"encoding/base32"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// capacity is how many servers the device remembers. The specification asks for at least five, and a
// house has one Music Assistant, perhaps a second being tried: eight leaves room without a file that
// grows. The device holds one connection at a time, so the record in use is never the one evicted.
const capacity = 8

// Store is the device's Sendspin identity and the servers it has paired with, kept in one file.
//
// The private key and the pairing PSK are made once, on first use, and kept: the key is the device's
// client_id, which is how Music Assistant knows it across reconnections, and the pairing PSK is what
// the pairing token carries, so a new one would make a token already given out useless.
type Store struct {
	path string

	mu   sync.Mutex
	file storeFile
	pub  []byte

	// gone is a store Discard removed: nothing is written back to its file.
	gone bool
}

type storeFile struct {
	PrivateKey []byte   `json:"private_key"`
	PairingPSK []byte   `json:"pairing_psk"`
	Records    []record `json:"records,omitempty"`
}

// record is one paired server: its id and the long-term PSK the two agreed on.
type record struct {
	ServerID string    `json:"server_id"`
	PSK      []byte    `json:"psk"`
	Used     time.Time `json:"used"`
}

// Open reads the store at path, making the identity and pairing PSK if this is the first time. A file
// that cannot be read is an error rather than a new identity: replacing it would make the device a new
// player to every server it knows.
func Open(path string) (*Store, error) {
	s := &Store{path: path}
	b, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if s.file.PrivateKey, err = random(); err != nil {
			return nil, err
		}
		if s.file.PairingPSK, err = random(); err != nil {
			return nil, err
		}
		if err := s.save(); err != nil {
			return nil, err
		}
	case err != nil:
		return nil, err
	default:
		if err := json.Unmarshal(b, &s.file); err != nil {
			return nil, err
		}
		if len(s.file.PrivateKey) != keySize || len(s.file.PairingPSK) != keySize {
			return nil, errors.New("sendspin keys: a key of the wrong size")
		}
	}
	if s.pub, err = publicKey(s.file.PrivateKey); err != nil {
		return nil, err
	}
	return s, nil
}

// ClientID is the device's id on the wire: its public key.
func (s *Store) ClientID() string { return b64.EncodeToString(s.pub) }

// Token is the pairing token for this device, which the operator enters into a server to pair with it
// using the pairing PSK. It is a credential: whoever holds it can pair a server with the device.
func (s *Store) Token() string {
	s.mu.Lock()
	payload := append(slices.Clone(s.pub), s.file.PairingPSK...)
	s.mu.Unlock()
	return "SP:0" + strings.ReplaceAll(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(payload), "2", "9")
}

// Paired says whether the device holds a record for any server.
func (s *Store) Paired() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.file.Records) > 0
}

func (s *Store) private() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.file.PrivateKey
}

// resolved is a PSK found for a psk_id, with the server it was agreed with when it is long-term.
type resolved struct {
	psk      []byte
	category Category
	serverID string
}

// resolve finds the PSK the server named, held under the category it declared, or reports a miss.
func (s *Store) resolve(id string, c Category) (resolved, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch c {
	case Sentinel:
		if id == pskID(sentinelPSK) {
			return resolved{psk: sentinelPSK, category: Sentinel}, true
		}
	case Pairing:
		if id == pskID(s.file.PairingPSK) {
			return resolved{psk: s.file.PairingPSK, category: Pairing}, true
		}
	case LongTerm:
		for _, r := range s.file.Records {
			if pskID(r.PSK) == id {
				return resolved{psk: r.PSK, category: LongTerm, serverID: r.ServerID}, true
			}
		}
	}
	return resolved{}, false
}

// remember keeps a new pairing, replacing any record for the same server and, at capacity, evicting
// the one used longest ago.
func (s *Store) remember(serverID string, psk []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.file.Records = slices.DeleteFunc(s.file.Records, func(r record) bool { return r.ServerID == serverID })
	if len(s.file.Records) >= capacity {
		oldest := 0
		for i, r := range s.file.Records {
			if r.Used.Before(s.file.Records[oldest].Used) {
				oldest = i
			}
		}
		s.file.Records = slices.Delete(s.file.Records, oldest, oldest+1)
	}
	s.file.Records = append(s.file.Records, record{ServerID: serverID, PSK: psk, Used: time.Now().UTC()})
	return s.save()
}

// used marks a server's record as just used, for eviction. A failed write costs only the order.
func (s *Store) used(serverID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.file.Records {
		if s.file.Records[i].ServerID == serverID {
			s.file.Records[i].Used = time.Now().UTC()
			_ = s.save()
			return
		}
	}
}

// forget drops a server's record, as server/unpair asks.
func (s *Store) forget(serverID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := len(s.file.Records)
	s.file.Records = slices.DeleteFunc(s.file.Records, func(r record) bool { return r.ServerID == serverID })
	if len(s.file.Records) == n {
		return nil
	}
	return s.save()
}

// Discard removes the store's file for good: the identity, the pairing token and every server paired
// with, for a device leaving its owner (feature/api's Leave Home Assistant). The store goes on
// answering until the restart that follows, but writes nothing back, so a pairing meanwhile cannot put
// the old identity back; the next Open makes a new one.
func (s *Store) Discard() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gone = true
	if err := os.Remove(s.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// save writes the file whole and renames it into place, so a power cut leaves the old one or the new
// one and never half of either. Only the owner may read it: it holds the device's private key.
func (s *Store) save() error {
	if s.gone {
		return nil
	}
	b, err := json.Marshal(s.file)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".sendspin-keys-")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.path)
}
