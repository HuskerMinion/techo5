//go:build !dot

package plexcast

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
)

// The link: plex.tv hands the device four characters to show, somebody types them at plex.tv/link, and
// the account's token comes back on the poll. Every request here carries the device's identity, and
// the token that comes back is bound to it.

// pinFlow is the link being made, if any: the code to show, whether the poll is still running, and why
// the last one failed.
type pinFlow struct {
	cancel  context.CancelFunc
	code    string
	waiting bool
	err     string
}

// pinReply is plex.tv's answer to a pin: created with a code, and later with a token in it. The id is
// raw because Plex has written it as a number and as a string; authToken is absent until somebody
// types the code.
type pinReply struct {
	ID        json.RawMessage `json:"id"`
	Code      string          `json:"code"`
	AuthToken string          `json:"authToken"`
	ExpiresIn int             `json:"expiresIn"`
}

// userReply is who the token belongs to: the name the screens say, and the account number the cloud
// socket's address needs.
type userReply struct {
	ID       json.RawMessage `json:"id"`
	Username string          `json:"username"`
	Title    string          `json:"title"`
}

// StartPinLink asks plex.tv for a code and starts polling for the token that comes back once somebody
// has typed it at plex.tv/link. It answers with the four characters, which the screen and the setup
// page show until the account is linked or the code runs out. Asking again while a link is being made
// answers the same code rather than making a second one.
func StartPinLink() (string, error) { return Get().startPin() }

func (f *Feature) startPin() (string, error) {
	f.mu.Lock()
	if f.pin.waiting && f.pin.code != "" {
		code := f.pin.code
		f.mu.Unlock()
		return code, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	f.pin = pinFlow{cancel: cancel, waiting: true}
	f.st.Code, f.st.Waiting, f.st.Err = "", true, ""
	f.mu.Unlock()
	f.listeners()

	reply, err := createPin(ctx)
	if err != nil {
		cancel()
		f.finishPin("", err)
		return "", err
	}
	f.mu.Lock()
	f.pin.code = reply.Code
	f.st.Code = reply.Code
	f.mu.Unlock()
	f.listeners()
	slog.Info("plex: waiting for the code to be entered", "code", reply.Code)
	safe.Go("plex pin", func() { f.pollPin(ctx, idString(reply.ID)) })
	return reply.Code, nil
}

// createPin is the POST that makes a code. It is not a PIN in the lock's sense: four characters, a
// link on plex.tv's side, and nothing the device has to remember.
func createPin(ctx context.Context) (pinReply, error) {
	var out pinReply
	resp, err := request(ctx, http.MethodPost, apiURL+"/pins?strong=true", nil)
	if err != nil {
		return out, err
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(io.LimitReader(resp.Body, mostBody)).Decode(&out); err != nil {
		return out, err
	}
	if out.Code == "" || idString(out.ID) == "" {
		return out, errExpired
	}
	return out, nil
}

// pollPin asks plex.tv whether the code has been entered yet, every two seconds for five minutes.
// Anything that goes wrong in one poll is tried again: a phone's link does not fail because a packet
// did.
func (f *Feature) pollPin(ctx context.Context, id string) {
	deadline := time.Now().Add(linkFor)
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		if time.Now().After(deadline) {
			f.finishPin("", errExpired)
			return
		}
		var reply pinReply
		if err := getJSON(ctx, apiURL+"/pins/"+url.PathEscape(id), &reply); err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.Debug("plex: the pin poll failed, trying again", "err", err)
			continue
		}
		if reply.AuthToken == "" {
			continue
		}
		f.linked(ctx, reply.AuthToken)
		return
	}
}

// linked is a token in hand: who it belongs to, stored, and the servers started (the Run loop is woken
// by changed). A user plex.tv does not name is stored empty, and the pages say "Linked" for it.
func (f *Feature) linked(ctx context.Context, token string) {
	user, userID := who(ctx, token)
	if err := config.Set().Plex().Link(token, user, userID); err != nil {
		slog.Error("plex: saving the account failed", "err", err)
		f.finishPin("", err)
		return
	}
	f.mu.Lock()
	if f.pin.cancel != nil {
		f.pin.cancel()
	}
	f.pin = pinFlow{}
	f.st = Status{Linked: true, User: user}
	f.mu.Unlock()
	slog.Info("plex: linked", "user", user)
	f.changed()
}

// who asks plex.tv whose token this is. A failure is not a failed link: the device can cast without
// knowing the account's name, and only the cloud socket's address misses it.
func who(ctx context.Context, token string) (user, id string) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL+"/user", nil)
	if err != nil {
		return "", ""
	}
	for k, v := range identityHeaders() {
		req.Header.Set(k, v)
	}
	req.Header.Set("X-Plex-Token", token)
	resp, err := client.Do(req)
	if err != nil {
		return "", ""
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return "", ""
	}
	var u userReply
	if err := json.NewDecoder(io.LimitReader(resp.Body, mostBody)).Decode(&u); err != nil {
		return "", ""
	}
	if u.Username == "" {
		u.Username = u.Title
	}
	return u.Username, idString(u.ID)
}

// finishPin ends a link that did not happen, saying why in the device's words.
func (f *Feature) finishPin(code string, err error) {
	msg := ""
	switch {
	case err == nil:
	case errors.Is(err, context.Canceled):
		// The card was put away while plex.tv was answering: nobody is waiting for it, so there is
		// nothing to say and nothing to warn about.
	case err == errExpired || strings.Contains(err.Error(), "expired"):
		msg = "the code expired"
	default:
		msg = "plex.tv could not be reached"
		slog.Warn("plex: linking failed", "err", err)
	}
	f.mu.Lock()
	if f.pin.cancel != nil {
		f.pin.cancel()
	}
	f.pin = pinFlow{code: code, err: msg}
	f.st.Code, f.st.Waiting, f.st.Err = code, false, msg
	f.mu.Unlock()
	f.listeners()
}

// CancelPinLink stops a link being made: the poll ends and the code is forgotten. It is the Cancel on
// the screen's card and on the setup page.
func CancelPinLink() { Get().cancelPin() }

func (f *Feature) cancelPin() {
	f.mu.Lock()
	if f.pin.cancel != nil {
		f.pin.cancel()
	}
	f.pin = pinFlow{}
	f.st.Code, f.st.Waiting = "", false
	f.mu.Unlock()
	f.listeners()
}

// UnlinkAccount forgets the account: the token and who it belonged to, the servers (the Run loop stops
// them), what was playing, and the device's place in the account, as far as plex.tv lets go of it. The
// identifier stays, because it is the device's name rather than the account's.
func UnlinkAccount() error { return Get().unlink() }

func (f *Feature) unlink() error {
	linked := f.Linked()
	token := config.Get().Plex.Token
	f.cancelPin()
	f.pb.stop()
	f.tl.clear()
	err := config.Set().Plex().Unlink()
	f.mu.Lock()
	f.st = Status{}
	f.mu.Unlock()
	f.changed()
	if linked && token != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		f.unregister(ctx, token)
	}
	slog.Info("plex: unlinked")
	return err
}
