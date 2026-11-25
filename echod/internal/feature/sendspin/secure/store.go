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

	// spokeSaved is when an entry for a server that spoke encrypted was last written for its own sake.
	spokeSaved time.Time
}

// now is the clock; a variable, for tests.
var now = time.Now

// clockSet is whether the clock is one to keep: these devices start in 1970, and NTP sets it a little
// after boot. A time from before that is not written, and not counted from.
func clockSet(t time.Time) bool { return t.Year() >= 2025 }

// spokeEvery is how often an entry for a server that is not paired is written: the encrypted handshake
// of every new server would otherwise be a write, and anything on the network can make one. A paired
// server's is written at once. One that is not written yet is still in memory until the next write or
// the next restart, which only means a server is taken for a new one again after a power cut.
const spokeEvery = time.Hour

type storeFile struct {
	PrivateKey []byte   `json:"private_key"`
	PairingPSK []byte   `json:"pairing_psk"`
	Records    []record `json:"records,omitempty"`

	// Encrypted is the servers this device has completed an encrypted handshake with, paired or not.
	// Firmware from before it ignores the field, and a file from before it has none.
	Encrypted []spoke `json:"encrypted,omitempty"`
}

// spoke is a server that has spoken encrypted Sendspin with the device: its address, as the device
// knows it before any handshake, and its id, so that a server on a new address takes its own entry
// along. Failing is when the server first hung up on client/init (or kept silent) since then, zero
// while it has not.
type spoke struct {
	Peer     string    `json:"peer"`
	ServerID string    `json:"server_id"`
	Seen     time.Time `json:"seen"`
	Failing  time.Time `json:"failing,omitzero"`
}

// seenRefresh is how stale a server's Seen may grow before an encrypted handshake writes it again.
// Seen orders eviction only, so a write on every reconnection would wear the flash for nothing.
const seenRefresh = 24 * time.Hour

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

// ClientIDAt is the client id of the store at path, read without opening it: nothing is made or
// written, so a look at what the device is called in Music Assistant never makes it a new identity,
// and the store the Sendspin client holds open is not raced. A missing file is fs.ErrNotExist.
func ClientIDAt(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var f storeFile
	if err := json.Unmarshal(b, &f); err != nil {
		return "", err
	}
	if len(f.PrivateKey) != keySize {
		return "", errors.New("sendspin keys: a key of the wrong size")
	}
	pub, err := publicKey(f.PrivateKey)
	if err != nil {
		return "", err
	}
	return b64.EncodeToString(pub), nil
}

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

// SpokeEncrypted records that the server at peer, whose id is serverID, has just completed an encrypted
// handshake: a hang-up from it from now on is not taken for a server from before encryption (#721).
// Its entry replaces any for the same address or the same server.
//
// At capacity, the entry seen longest ago goes, but never a paired server's, nor one seen in the last
// day: a burst of servers on the network (anything on it can do an encrypted handshake, paired or not)
// would otherwise push out the one the device actually plays from. Past twice capacity the oldest of the
// unpaired goes whatever its age, so the list stays bounded. The file is written at once for a paired
// server, and for others at most once an hour (spokeEvery).
func (s *Store) SpokeEncrypted(peer, serverID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := now().UTC()
	for _, e := range s.file.Encrypted {
		if e.Peer == peer && e.ServerID == serverID && e.Failing.IsZero() && t.Sub(e.Seen) < seenRefresh {
			return nil // nothing that matters has changed
		}
	}
	s.file.Encrypted = slices.DeleteFunc(s.file.Encrypted, func(e spoke) bool {
		return e.Peer == peer || e.ServerID == serverID
	})
	if len(s.file.Encrypted) >= capacity {
		s.evict(t, false)
	}
	if len(s.file.Encrypted) >= 2*capacity {
		s.evict(t, true)
	}
	s.file.Encrypted = append(s.file.Encrypted, spoke{Peer: peer, ServerID: serverID, Seen: t})
	if !s.pairedLocked(serverID) && !s.spokeSaved.IsZero() && t.Sub(s.spokeSaved) < spokeEvery {
		return nil
	}
	s.spokeSaved = t
	return s.save()
}

// evict drops the entry seen longest ago that may go: not a paired server's, and, unless anyway, not one
// seen in the last day.
func (s *Store) evict(t time.Time, anyway bool) {
	oldest := -1
	for i, e := range s.file.Encrypted {
		if s.pairedLocked(e.ServerID) || (!anyway && t.Sub(e.Seen) < seenRefresh) {
			continue
		}
		if oldest < 0 || e.Seen.Before(s.file.Encrypted[oldest].Seen) {
			oldest = i
		}
	}
	if oldest >= 0 {
		s.file.Encrypted = slices.Delete(s.file.Encrypted, oldest, oldest+1)
	}
}

// pairedLocked is whether the device holds a record for the server, with s.mu held.
func (s *Store) pairedLocked(serverID string) bool {
	for _, r := range s.file.Records {
		if r.ServerID == serverID {
			return true
		}
	}
	return false
}

// HungUp records that the server at peer hung up on client/init or kept silent, and reports whether it
// has spoken encrypted with the device before and, if so, since when it has been failing: the first
// such hang-up after its last encrypted handshake. Only that first one is written, so a server that
// keeps hanging up does not keep writing the file.
func (s *Store) HungUp(peer string) (failingSince time.Time, spoken bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.file.Encrypted {
		e := &s.file.Encrypted[i]
		if e.Peer != peer {
			continue
		}
		t := now().UTC()
		// On a clock not set yet, no streak starts: counted from 1970, a day would be up the moment the
		// clock came right. One kept from such a clock starts again from now, once there is a now.
		if !clockSet(t) {
			return time.Time{}, true
		}
		if e.Failing.IsZero() || !clockSet(e.Failing) {
			e.Failing = t
			_ = s.save() // a failed write only starts the count again after a restart
		}
		return e.Failing, true
	}
	return time.Time{}, false
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
