//go:build !dot

package plexcast

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
)

// Plex's own addresses, in one place: the account side (pins, the user, the device registration), the
// socket a phone's commands arrive on, and the page the code is typed at (said to the owner, never
// fetched).
const (
	apiURL    = "https://plex.tv/api/v2"
	pubsubURL = "wss://pubsub.plex.tv/sub/websockets"
	linkURL   = "https://plex.tv/link"
)

// client is what talks to plex.tv: the internet, so a timeout and a body cap, unlike the LAN clients
// (which refuse the device itself instead).
var client = &http.Client{Timeout: 20 * time.Second}

// mostBody bounds what plex.tv may answer with: a pin and an account are small, and a settings page
// that asked for one must not be held by an answer that never ends.
const mostBody = 1 << 20

// clientIdentifier is the name Plex knows this device by: made at random the first time it is needed
// and kept, because plex.tv binds the account's token to it. A new one would silently invalidate the
// link, and every controller dedupes players on it. DLNA's udn is the same idea for a different
// audience.
func clientIdentifier() string {
	if id := config.Get().Plex.ClientID; id != "" {
		return id
	}
	id := uuid.NewString()
	if err := config.Set().Plex().ClientID(id); err != nil {
		// Not fatal: this run works with the id it made, and the next makes another only if the file
		// still could not take it -- which is the same failure every other setting has.
		return id
	}
	return id
}

// friendlyName is what Plex lists the device as: the name the owner gave it, or the device's default.
func friendlyName() string {
	if n := config.Get().Device.Name; n != "" {
		return n
	}
	return layout.DefaultName
}

// version is what the device says it runs. A development build ranks below every release rather than
// being a string no Plex client can order, as layout.Version is stamped for.
func version() string {
	if layout.Version == "" {
		return "1.0.0"
	}
	return layout.Version
}

// identityHeaders are on every call to Plex: what this device is, and the identifier the account is
// bound to. X-Plex-Provides says what it can be asked to do, which is what puts a "player" in the
// account's list.
func identityHeaders() map[string]string {
	return map[string]string{
		"Accept":                   "application/json",
		"X-Plex-Product":           product,
		"X-Plex-Version":           version(),
		"X-Plex-Platform":          platform,
		"X-Plex-Device":            device,
		"X-Plex-Device-Name":       friendlyName(),
		"X-Plex-Client-Identifier": clientIdentifier(),
		"X-Plex-Provides":          "client,player,pubsub-player",
		"X-Plex-Model":             "TECHO5",
	}
}

// request is a call to Plex with the identity on it. form, where given, is sent as a form body; the
// answer's body is the caller's to close.
// statusError is plex.tv answering something other than 2xx. The code is kept because a caller has to
// tell an answer it can act on -- a pin that is gone, 404 -- from one it can only report, such as a 500
// it should try again after.
type statusError struct {
	code   int
	reason string
}

func (e statusError) Error() string { return e.reason }

func request(ctx context.Context, method, u string, form url.Values) (*http.Response, error) {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, err
	}
	for k, v := range identityHeaders() {
		req.Header.Set(k, v)
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		defer resp.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, statusError{resp.StatusCode,
			fmt.Sprintf("plex.tv answered %s: %s", resp.Status, strings.TrimSpace(string(b)))}
	}
	return resp, nil
}

// getJSON reads an answer into v. An answer that is not JSON is an error the caller can say in its own
// words rather than a decoding panic somewhere deeper.
func getJSON(ctx context.Context, u string, v any) error {
	resp, err := request(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(io.LimitReader(resp.Body, mostBody)).Decode(v); err != nil {
		return fmt.Errorf("plex.tv's answer was not readable: %w", err)
	}
	return nil
}

// idString is a JSON id as a string. Plex has written these as numbers and as strings across versions,
// and a device that only understood one of them would fail to link on the other.
func idString(raw json.RawMessage) string {
	return strings.Trim(strings.TrimSpace(string(raw)), `"`)
}

// errExpired is the PIN running out: five minutes is worth saying in the device's own words.
var errExpired = errors.New("the code expired before it was entered")

// wait is the pause between polls of a PIN. Plex's own clients use a second or two; two is kinder to a
// house's connection and still quick enough that the screen changes while somebody is looking at it.
const wait = 2 * time.Second

// linkFor is how long a PIN is polled for. Four characters are good for about fifteen minutes; five is
// the brief's number and leaves the wait recognizably short.
const linkFor = 5 * time.Minute
