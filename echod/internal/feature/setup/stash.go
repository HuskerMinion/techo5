package setup

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"
	"sync"
	"time"
)

// A save the device refuses used to come back with the form as it was saved before, and everything
// typed into it gone: one wrong box in a form of thirteen, or a deck page of two dozen buttons, and all
// of it was typed again. What was sent is kept now, for the browser that sent it and a short while, and
// the page it comes back to puts it back into the form (pageScript). Never a secret: a password, key,
// token or PIN is left out before it is kept, and so is a private address and anything with a login in
// it, so they are typed again, as they always were. Kept in memory and on the device only: it is never
// written into an address, and only into the page of the session that sent it.

// stashFor is how long a refused form is kept for the page it comes back to.
const stashFor = 10 * time.Minute

// stashSecret are the fields never kept, whatever form they are in: the secrets (secret.go; every one of
// them is checked to be here, TestEveryPasswordFieldIsLeftOutOfTheStash), the page's own token, and the
// boxes that say a danger is understood, which are ticked again on purpose.
var stashSecret = map[string]bool{
	"token": true, "what": true, "understood": true, "nocheck": true,
	"key": true, "pass": true, "password": true, "passphrase": true, "pin": true, "matoken": true, "mcp": true,
}

// stashSecretIn are fields that are secret in one form only: a calendar's address is a private link to
// it, and the page never shows it again once it is saved.
var stashSecretIn = map[string]map[string]bool{
	"calendar": {"url": true},
	"house":    {"word": true}, // the key the house's devices sign with
}

// stashed is a refused form: what it saves, the fields it sent, and when.
type stashed struct {
	what   string
	fields url.Values
	at     time.Time
}

// rejectedForms are the refused forms, one for each session at most: the last.
type rejectedForms struct {
	mu   sync.Mutex
	byID map[string]stashed
}

// keep keeps a refused form for the session token, without its secrets. A form that is not one of the
// page's is not kept.
func (s *rejectedForms) keep(token, what string, form url.Values) {
	if token == "" || sectionNamed(what) == "" {
		return
	}
	fields := url.Values{}
	for name, values := range form {
		if stashSecret[name] || stashSecretIn[what][name] || hasLogin(values) {
			continue
		}
		fields[name] = append([]string(nil), values...)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.byID == nil {
		s.byID = map[string]stashed{}
	}
	now := time.Now()
	for id, f := range s.byID {
		if now.Sub(f.at) >= stashFor {
			delete(s.byID, id)
		}
	}
	s.byID[token] = stashed{what: what, fields: fields, at: now}
}

// take is the session's refused form, when it is fresh and its form is on the tab being drawn. It is
// taken once: a reload of the page after it shows the saved settings again.
func (s *rejectedForms) take(token, tab string) (stashed, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.byID[token]
	if !ok {
		return stashed{}, false
	}
	if time.Since(f.at) >= stashFor {
		delete(s.byID, token)
		return stashed{}, false
	}
	if formTab(f.what) != tab {
		return stashed{}, false
	}
	delete(s.byID, token)
	return f, true
}

// drop forgets one session's form, or every session's with "".
func (s *rejectedForms) drop(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if token == "" {
		clear(s.byID)
		return
	}
	delete(s.byID, token)
}

// hasLogin is whether any of the values is an address with a login in it, as a go2rtc address can be.
func hasLogin(values []string) bool {
	for _, v := range values {
		if !strings.Contains(v, "@") {
			continue
		}
		if u, err := url.Parse(strings.TrimSpace(v)); err == nil && u.User != nil {
			return true
		}
		if strings.Contains(v, "://") {
			return true // not an address url reads, with an @ after a scheme: taken as one with a login
		}
		if at := strings.LastIndex(v, "@"); strings.Contains(v[:at], ":") && !strings.ContainsAny(v[:at], " /") {
			return true // user:password@host, typed without a scheme
		}
	}
	return false
}

// writeStash puts a refused form into the page for its script to put back (pageScript). JSON, which
// escapes <, > and & so that nothing in it can end the script element it is in.
func writeStash(w io.Writer, f stashed) {
	b, err := json.Marshal(struct {
		What   string     `json:"what"`
		Fields url.Values `json:"fields"`
	}{f.what, f.fields})
	if err != nil {
		return
	}
	fmt.Fprintf(w, `<script type="application/json" id="refill">%s</script>`, b)
}
