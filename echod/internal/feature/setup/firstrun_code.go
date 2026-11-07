package setup

import (
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/firstrun"
)

// The one-time setup code (docs/first-run-plan.md): the phone that has just given a Dot its Wi-Fi over
// Improv (#355) is handed /setup?code=..., and that browser comes into first run with no press, since
// it was within Bluetooth range of the device moments before. MintCode is what Improv calls; a code is
// good for one browser, for codeLife, and only while first run is still to do. Nothing mints one yet.

const codeLife = 10 * time.Minute

var codes struct {
	mu    sync.Mutex
	code  string
	until time.Time
}

// MintCode opens the page and makes a new code, replacing any earlier one. It refuses once first run
// is done: from then on the press is the only way in.
func (f *Feature) MintCode() (string, bool) {
	if !firstrun.Pending() {
		return "", false
	}
	c := newToken()
	codes.mu.Lock()
	codes.code, codes.until = c, time.Now().Add(codeLife)
	codes.mu.Unlock()
	f.Open()
	return c, true
}

// takeCode is whether code is the one minted, still good, with first run still to do: it is used up.
func takeCode(code string) bool {
	if code == "" || !firstrun.Pending() {
		return false
	}
	codes.mu.Lock()
	defer codes.mu.Unlock()
	if codes.code == "" || time.Now().After(codes.until) || !sameToken(code, codes.code) {
		return false
	}
	codes.code, codes.until = "", time.Time{}
	return true
}

// enterWithCode lets the browser bringing a good code in, as a press would, and gives it the cookie.
func (f *Feature) enterWithCode(w http.ResponseWriter, r *http.Request) bool {
	if !takeCode(r.URL.Query().Get("code")) {
		return false
	}
	token := newToken()
	now := time.Now()
	f.mu.Lock()
	if !f.on(now) {
		f.mu.Unlock()
		return false
	}
	f.live[token] = now
	f.mu.Unlock()
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: token, Path: "/",
		HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: int(life / time.Second),
	})
	slog.Info("setup page: a browser was let in by its one-time code")
	return true
}
