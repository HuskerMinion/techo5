package sendspin

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/HuskerMinion/techo5/echod/internal/feature/sendspin/secure"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
)

// Every connection is encrypted, as the Sendspin 1.0 specification has it (#679). Music Assistant
// from before encryption knows only the unencrypted wire, and the device still plays to it: a server
// that does not answer client/init with server/init is remembered, and the next connection from it is
// unencrypted, as every connection was before.

// keysPath is the device's Sendspin identity and its pairings. A variable for the tests.
var keysPath = layout.StateDir + "/sendspin-keys.json"

// keys opens the store once. A store that cannot be opened leaves the device on the unencrypted wire,
// which is how it played before, rather than without music.
var keys = sync.OnceValues(func() (*secure.Store, error) {
	st, err := secure.Open(keysPath)
	if err != nil {
		slog.Error("sendspin keys", "err", err)
	}
	return st, err
})

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
	st, err := keys()
	if err != nil || isUnencrypted(peer) {
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
	lib, _, err := secure.Bridge(ctx, c, conn, secure.Options{Unpaired: unpairedAccess})
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

// PairingToken is the token that pairs a server with this device, for the setup page, or empty when
// the device has no keys.
func PairingToken() string {
	st, err := keys()
	if err != nil {
		return ""
	}
	return st.Token()
}
