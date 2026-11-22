package sendspin

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/sendspin/secure"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
)

// Every connection is encrypted, as the Sendspin 1.0 specification has it (#679). Music Assistant
// from before encryption knows only the unencrypted wire, and the device still plays to it: a server
// that does not answer client/init with server/init is remembered, and the next connection from it is
// unencrypted, as every connection was before.

// keysPath is the device's Sendspin identity and its pairings. A variable for the tests.
var keysPath = layout.StateDir + "/sendspin-keys.json"

// keys is the store, opened on first use. A store that cannot be opened leaves the device on the
// unencrypted wire, which is how it played before, rather than without music, and is tried again on
// the next use, at most once every keysRetry: early in boot the state directory may not be ready.
func keys() (*secure.Store, error) {
	keyStore.Lock()
	defer keyStore.Unlock()
	if keyStore.discarded {
		return nil, errDiscarded
	}
	if keyStore.st != nil {
		return keyStore.st, nil
	}
	if keyStore.err != nil && time.Since(keyStore.tried) < keysRetry {
		return nil, keyStore.err
	}
	st, err := secure.Open(keysPath)
	keyStore.tried = time.Now()
	if err != nil {
		slog.Error("sendspin keys", "err", err)
		keyStore.err = err
		return nil, err
	}
	keyStore.st, keyStore.err = st, nil
	return st, nil
}

// keysRetry is the least time between two attempts to open a store that failed to open.
const keysRetry = 30 * time.Second

var keyStore struct {
	sync.Mutex
	st    *secure.Store
	err   error
	tried time.Time

	// discarded is the identity forgotten (Discard): until the restart there is none, so no token
	// goes out, not even to the Home Assistant connection still open.
	discarded bool
}

var errDiscarded = errors.New("sendspin keys: forgotten, a new identity comes with the restart")

// maxFrame is the most one WebSocket frame from a server may hold, as the player library allows on the
// unencrypted wire. An encrypted frame is one Noise message, 64 KiB at most; a longer message comes in
// fragments.
const maxFrame = 1 << 20

// unencryptedFor is how long a server that did not speak encrypted Sendspin is taken to be one from
// before it. Music Assistant updated in the meantime is tried encrypted again after that.
const unencryptedFor = 24 * time.Hour

// unencrypted is the servers known to speak only the unencrypted wire, by address, and until when.
var unencrypted = struct {
	sync.Mutex
	until map[string]time.Time
}{until: map[string]time.Time{}}

func isUnencrypted(peer string) bool {
	unencrypted.Lock()
	defer unencrypted.Unlock()
	return time.Now().Before(unencrypted.until[peer])
}

func markUnencrypted(peer string, yes bool) {
	unencrypted.Lock()
	defer unencrypted.Unlock()
	if yes {
		unencrypted.until[peer] = time.Now().Add(unencryptedFor)
	} else {
		delete(unencrypted.until, peer)
	}
}

// errTryUnencrypted is a connection spent on a server that does not speak encrypted Sendspin. It has
// read client/init as a broken hello, so this connection is over; the next is made unencrypted.
var errTryUnencrypted = errors.New("sendspin: the server does not speak encrypted Sendspin; the next connection is unencrypted")

// secured runs the encrypted handshake on conn, with peer the server's address, and returns the
// WebSocket the session is to run over: conn itself when the server speaks only the unencrypted wire.
// plain reports which it is.
func secured(ctx context.Context, conn *websocket.Conn, peer string) (ws *websocket.Conn, plain bool, err error) {
	// The library caps what it reads at maxFrame, but on an encrypted connection it reads a pipe: the
	// socket itself is read here and by the bridge, and one frame of any size from any host on the
	// network would be held whole in memory before anything checked it.
	conn.SetReadLimit(maxFrame)
	st, err := keys()
	if err != nil {
		return conn, true, nil
	}
	// A paired device is not talked out of encryption: only an unpaired one plays to a server it took
	// for one from before encryption.
	if isUnencrypted(peer) && !st.Paired() {
		return conn, true, nil
	}
	c, err := secure.Handshake(conn, st)
	if errors.Is(err, secure.ErrNotEncrypted) {
		markUnencrypted(peer, true)
		return nil, false, errTryUnencrypted
	}
	if err != nil {
		return nil, false, err
	}
	slog.Info("sendspin encrypted", "psk", c.Category, "credential_mismatch", c.Mismatch)
	mac, _ := layout.FactoryMAC()
	lib, _, err := secure.Bridge(ctx, c, conn, secure.Options{Unpaired: unpairedAccess, MAC: strings.ToLower(mac)})
	if err != nil {
		return nil, false, err
	}
	return lib, false, nil
}

// unstarted takes back the mark on a server thought to speak only the unencrypted wire when an
// unencrypted session with it would not start: it was the network, or a server that has since been
// updated, and the next connection is encrypted again.
func unstarted(plain bool, peer string, err error) {
	if plain && errors.Is(err, errNotStarted) {
		markUnencrypted(peer, false)
	}
}

// unpairedAccess lets a server the device has not paired with play to it, once its operator has
// approved the device there. A device in a house is set up by the house: what pairing adds is that
// no one else on the network can pass for that house's Music Assistant.
const unpairedAccess = true

// Discard forgets the device's Sendspin identity, its pairing token and the servers paired with it, for
// a device leaving its owner (feature/api's Leave Home Assistant): the token went to the old owner's
// Home Assistant and Music Assistant, and with it they could pair again. The next start makes a new
// identity, so the device comes up as a new player to whatever Music Assistant it meets next.
func Discard() error {
	keyStore.Lock()
	defer keyStore.Unlock()
	keyStore.discarded = true
	if st := keyStore.st; st != nil {
		keyStore.st = nil
		return st.Discard() // stops a pairing in a session still open from writing it back
	}
	// Never opened, or not openable: the file alone goes.
	if err := os.Remove(keysPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// PairingToken is the token that pairs a server with this device, for the setup page, or empty when
// the device has no keys.
func PairingToken() string {
	st, err := keys()
	if err != nil {
		return ""
	}
	return st.Token()
}

// Pairing is what Home Assistant hands Music Assistant so that it pairs with the device on its own:
// the client id and the pairing token, both empty when the device has no keys, and whether the player
// is switched on. Home Assistant reads it over the native API (feature/api), which hands the token
// out only over a connection encrypted with the device's key.
func Pairing() (clientID, token string, on bool) {
	on = config.Get().Sendspin.Enabled
	st, err := keys()
	if err != nil {
		return "", "", on
	}
	return st.ClientID(), st.Token(), on
}
