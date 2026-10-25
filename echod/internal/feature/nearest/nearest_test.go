package nearest

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/announce"
)

const word = "lantern"

var stateOnce sync.Once

// house is a device taking part, with one other device to ask and the clock and the window in the
// test's hands. What it would do to the turn is written down instead of done.
type house struct {
	n      *Nearest
	now    time.Time
	later  []func()
	opened []time.Time
	at0    int
	sent   []claim
	mine   *pending
}

func newHouse(t *testing.T) *house {
	t.Helper()
	stateOnce.Do(func() { config.Use(filepath.Join(t.TempDir(), "state.json")) })
	if err := config.Set().Home().HouseWord(word); err != nil {
		t.Fatal(err)
	}
	if err := config.Set().Nearest().WindowMs(300); err != nil {
		t.Fatal(err)
	}
	if err := config.Set().Nearest().Offset(0); err != nil {
		t.Fatal(err)
	}
	h := &house{now: time.Date(2026, 10, 17, 7, 0, 0, 0, time.UTC)}
	n := build()
	n.enabled = true
	n.busy = func() bool { return false }
	n.canAnswer = func(int) bool { return true }
	n.wakeWord = func(int) string { return "okay_nabu" }
	n.score = func() float64 { return 20 }
	n.hears = func() bool { return true }
	n.onCall = func() bool { return false }
	n.peers = func() []announce.Peer { return []announce.Peer{{Name: "Kitchen", Address: "192.0.2.1", Port: 8181}} }
	n.fanout = func(_ []announce.Peer, mine *pending) { h.sent = append(h.sent, mine.claim); h.mine = mine }
	n.now = func() time.Time { return h.now }
	n.after = func(_ time.Duration, f func()) { h.later = append(h.later, f) }
	n.woke = func(_ int, at time.Time) { h.opened = append(h.opened, at) }
	n.wokeNow = func(int) { h.at0++ }
	h.n = n
	return h
}

// wait lets the window go by.
func (h *house) wait(d time.Duration) {
	h.now = h.now.Add(d)
	for _, f := range h.later {
		f()
	}
	h.later = nil
}

// claimFrom is a claim from another device arriving now, and whether it was told the word was answered
// here.
func (h *house) claimFrom(name string, score float64) bool {
	return h.n.took(claim{From: name, Turn: "t", Word: "okay_nabu", Score: score})
}

func TestOffAnswersAtOnce(t *testing.T) {
	h := newHouse(t)
	h.n.enabled = false
	h.n.Woke(0)
	if h.at0 != 1 || len(h.later) != 0 {
		t.Errorf("off: answered at once %d times, waiting %d", h.at0, len(h.later))
	}
}

func TestNobodyToAskAnswersAtOnce(t *testing.T) {
	h := newHouse(t)
	h.n.peers = func() []announce.Peer { return nil }
	h.n.Woke(0)
	if h.at0 != 1 || len(h.sent) != 0 {
		t.Errorf("alone: answered at once %d times, sent %d claims", h.at0, len(h.sent))
	}
}

// Only devices that can hear are asked: a Skylight never claims a wake word, so a house of it alone is
// a house of one, and the turn opens at once. A device that says nothing either way is asked.
func TestADeviceThatCannotHearIsNotAsked(t *testing.T) {
	h := newHouse(t)
	h.n.peers = func() []announce.Peer {
		return []announce.Peer{{Name: "Office Skylight", Address: "192.0.2.2", NoMicrophone: true}}
	}
	h.n.Woke(0)
	if h.at0 != 1 || len(h.sent) != 0 || len(h.later) != 0 {
		t.Errorf("with only a Skylight: answered at once %d times, sent %d claims, waiting %d", h.at0, len(h.sent), len(h.later))
	}

	h = newHouse(t)
	var asked []announce.Peer
	h.n.peers = func() []announce.Peer {
		return []announce.Peer{{Name: "Kitchen", Address: "192.0.2.1"}, {Name: "Office Skylight", Address: "192.0.2.2", NoMicrophone: true}}
	}
	h.n.fanout = func(p []announce.Peer, mine *pending) { asked = p; h.sent = append(h.sent, mine.claim) }
	h.n.Woke(0)
	if h.at0 != 0 || len(asked) != 1 || asked[0].Name != "Kitchen" {
		t.Errorf("asked %v, answered at once %d times; want only the Kitchen asked", asked, h.at0)
	}
}

// A device without microphones takes no claims: it has no wake word to weigh them against.
func TestADeviceWithoutMicrophonesTakesNoClaims(t *testing.T) {
	h := newHouse(t)
	if !h.n.open() {
		t.Fatal("not open with a house word and the switch on")
	}
	h.n.hears = func() bool { return false }
	if h.n.open() {
		t.Error("a Skylight takes claims")
	}
}

func TestNoHouseWordAnswersAtOnce(t *testing.T) {
	h := newHouse(t)
	if err := config.Set().Home().HouseWord(""); err != nil {
		t.Fatal(err)
	}
	h.n.Woke(0)
	if h.at0 != 1 {
		t.Error("with no house word to sign with, the device did not answer at once")
	}
}

func TestBusyAnswersAsBefore(t *testing.T) {
	h := newHouse(t)
	h.n.busy = func() bool { return true }
	h.n.Woke(0)
	if h.at0 != 1 || len(h.sent) != 0 {
		t.Error("in the middle of a turn, the wake word was claimed")
	}
}

// A device that could not answer does not claim, so it cannot silence one that could.
func TestADeviceThatCannotAnswerDoesNotClaim(t *testing.T) {
	h := newHouse(t)
	h.n.canAnswer = func(int) bool { return false }
	h.n.Woke(0)
	if len(h.sent) != 0 || h.at0 != 1 {
		t.Errorf("sent %d claims, passed on %d times; want none sent and passed on as before", len(h.sent), h.at0)
	}
}

func TestNoAnswerFromTheOthersAnswersAfterTheWindow(t *testing.T) {
	h := newHouse(t)
	heard := h.now
	h.n.Woke(0)
	if len(h.sent) != 1 || h.sent[0].Score != 20 {
		t.Fatalf("claims sent: %+v", h.sent)
	}
	if len(h.opened) != 0 {
		t.Fatal("the turn opened before the window ended")
	}
	h.wait(300 * time.Millisecond)
	if len(h.opened) != 1 || !h.opened[0].Equal(heard) {
		t.Errorf("after the window with nothing heard: opened %v, want once, at the wake", h.opened)
	}
}

func TestABetterClaimInTheWindowStandsThisOneDown(t *testing.T) {
	h := newHouse(t)
	h.n.Woke(0)
	h.now = h.now.Add(120 * time.Millisecond)
	h.claimFrom("Kitchen", 26)
	h.wait(180 * time.Millisecond)
	if len(h.opened) != 0 {
		t.Error("answered though another device heard it better")
	}
}

func TestAWorseClaimDoesNot(t *testing.T) {
	h := newHouse(t)
	h.n.Woke(0)
	h.claimFrom("Kitchen", 12)
	h.wait(300 * time.Millisecond)
	if len(h.opened) != 1 {
		t.Error("a device that heard it worse kept this one from answering")
	}
}

// A device that heard the word first and better has already claimed it by the time this one hears it.
func TestABetterClaimJustBeforeCounts(t *testing.T) {
	h := newHouse(t)
	h.claimFrom("Kitchen", 30)
	h.now = h.now.Add(200 * time.Millisecond)
	h.n.Woke(0)
	h.wait(300 * time.Millisecond)
	if len(h.opened) != 0 {
		t.Error("a better claim from just before this wake did not count")
	}
}

func TestAnOldClaimDoesNotCount(t *testing.T) {
	h := newHouse(t)
	h.claimFrom("Kitchen", 30)
	h.now = h.now.Add(2 * time.Second)
	h.n.Woke(0)
	h.wait(300 * time.Millisecond)
	if len(h.opened) != 1 {
		t.Error("a claim from an earlier wake word kept this one from answering")
	}
}

func TestATieGoesToTheNameThatSortsFirst(t *testing.T) {
	if !better(claim{From: "Den", Score: 20}, claim{From: "Office", Score: 20}) {
		t.Error("Den should win a tie with Office")
	}
	if better(claim{From: "Office", Score: 20}, claim{From: "Den", Score: 20}) {
		t.Error("Office should lose a tie with Den")
	}
}

func TestTheOffsetIsAdded(t *testing.T) {
	h := newHouse(t)
	if err := config.Set().Nearest().Offset(-8); err != nil {
		t.Fatal(err)
	}
	h.n.Woke(0)
	if len(h.sent) != 1 || h.sent[0].Score != 12 {
		t.Errorf("claimed %+v, want 20 - 8 = 12", h.sent)
	}
}

// A device that has answered keeps the turn: a claim that comes after, however much better, is told the
// word was answered here, and that device stands down.
func TestAnAnsweredTurnIsKept(t *testing.T) {
	h := newHouse(t)
	h.n.Woke(0)
	h.wait(300 * time.Millisecond)
	h.now = h.now.Add(100 * time.Millisecond)
	if !h.claimFrom("Kitchen", 25) {
		t.Error("a late claim was not told the word was answered here")
	}
	if len(h.opened) != 1 {
		t.Errorf("opened %v, want the one turn, kept", h.opened)
	}

	// Long after, a claim is about another wake word.
	h.now = h.now.Add(time.Second)
	if h.claimFrom("Kitchen", 25) {
		t.Error("a claim long after the wake was told it was answered here")
	}

	// Before answering, a claim is only a claim.
	h2 := newHouse(t)
	h2.n.Woke(0)
	if h2.claimFrom("Kitchen", 10) {
		t.Error("a claim inside the window was told the word was answered")
	}
}

// The late device, told so, stands down.
func TestToldItWasAnsweredStandsDown(t *testing.T) {
	h := newHouse(t)
	h.n.Woke(0)
	h.n.answeredElsewhere(h.mine, "Kitchen")
	h.wait(300 * time.Millisecond)
	if len(h.opened) != 0 {
		t.Error("answered though another device already had")
	}
}

// Turning it off while a wake word waits still answers that wake word.
func TestTurningOffDoesNotLoseAWakeWord(t *testing.T) {
	h := newHouse(t)
	h.n.Woke(0)
	h.n.mu.Lock()
	h.n.enabled = false
	h.n.others = nil
	h.n.mu.Unlock()
	h.wait(300 * time.Millisecond)
	if len(h.opened) != 1 {
		t.Error("a wake word waiting when it was turned off was lost")
	}
}

// A failure deciding still answers.
func TestAPanicDecidingStillAnswers(t *testing.T) {
	h := newHouse(t)
	h.n.Woke(0)
	answered := 0
	h.n.woke = func(int, time.Time) {
		answered++
		if answered == 1 {
			panic("boom")
		}
	}
	h.wait(300 * time.Millisecond)
	if answered != 2 {
		t.Errorf("answered %d times; the panic should be followed by an answer", answered)
	}
}

func TestSigning(t *testing.T) {
	now := time.Now()
	body := []byte(`{"from":"Kitchen","turn":"a","score":20}`)
	h := sign(word, "Office", body, now)
	if err := verify(word, "office", h, body, now); err != nil {
		t.Fatalf("a good claim: %v", err)
	}
	if err := verify(word, "Office", h, body, now); err != errReplayed {
		t.Errorf("the same claim again: %v, want replayed", err)
	}
	if err := verify(word, "Den", sign(word, "Office", body, now), body, now); err != errUnsigned {
		t.Errorf("a claim for another device: %v", err)
	}
	if err := verify("other", "Office", sign(word, "Office", body, now), body, now); err != errUnsigned {
		t.Errorf("another house: %v", err)
	}
	if err := verify(word, "Office", sign(word, "Office", body, now), []byte(`{"from":"Kitchen","turn":"a","score":99}`), now); err != errUnsigned {
		t.Errorf("a changed claim: %v", err)
	}
	if err := verify(word, "Office", sign(word, "Office", body, now.Add(-10*time.Second)), body, now); err != errStale {
		t.Errorf("an old claim: %v", err)
	}
	if err := verify("", "Office", sign("", "Office", body, now), body, now); err != errUnsigned {
		t.Errorf("no house word: %v", err)
	}
}

func TestReceive(t *testing.T) {
	h := newHouse(t)
	post := func(header string, body []byte) int {
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
		if header != "" {
			req.Header.Set(authHeader, header)
		}
		w := httptest.NewRecorder()
		h.n.receive(w, req)
		return w.Code
	}
	me := config.Get().Device.Name
	body, _ := json.Marshal(claim{From: "Kitchen", Turn: "x", Score: 22})
	if code := post(sign(word, me, body, time.Now()), body); code != http.StatusNoContent {
		t.Fatalf("a good claim: %d", code)
	}
	if len(h.n.others) != 1 || h.n.others[0].From != "Kitchen" {
		t.Errorf("held %+v", h.n.others)
	}
	if code := post("", body); code != http.StatusForbidden {
		t.Errorf("an unsigned claim: %d", code)
	}
	if code := post(sign("other", me, body, time.Now()), body); code != http.StatusForbidden {
		t.Errorf("another house's claim: %d", code)
	}
	if code := post(sign(word, me+"-not", body, time.Now()), body); code != http.StatusForbidden {
		t.Errorf("a claim for another device: %d", code)
	}
	bad := []byte(`not a claim`)
	if code := post(sign(word, me, bad, time.Now()), bad); code != http.StatusBadRequest {
		t.Errorf("a signed body that is not a claim: %d", code)
	}
}

// A claim to a device that has just answered is told so.
func TestReceiveSaysWhenAnsweredHere(t *testing.T) {
	h := newHouse(t)
	h.n.Woke(0)
	h.wait(300 * time.Millisecond)
	body, _ := json.Marshal(claim{From: "Kitchen", Turn: "y", Word: "okay_nabu", Score: 40})
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	req.Header.Set(authHeader, sign(word, config.Get().Device.Name, body, time.Now()))
	w := httptest.NewRecorder()
	h.n.receive(w, req)
	if w.Code != http.StatusConflict {
		t.Errorf("a late claim was answered %d, want %d", w.Code, http.StatusConflict)
	}
}

// A device sending bad claims is written down once a minute, not once a claim.
func TestBadClaimsAreLoggedSparingly(t *testing.T) {
	now := time.Date(2026, 10, 17, 7, 0, 0, 0, time.UTC)
	warned.Lock()
	warned.at = time.Time{}
	warned.Unlock()
	n := 0
	for i := range 3 {
		warned.Lock()
		quiet := !warned.at.IsZero() && now.Sub(warned.at) < warnEvery
		warned.Unlock()
		if !quiet {
			n++
		}
		warn(now.Add(time.Duration(i)*time.Second), "test")
	}
	if n != 1 {
		t.Errorf("three bad claims in a moment logged %d times, want 1", n)
	}
}

// Two people saying different wake words to two devices at once both get an answer.
func TestADifferentWakeWordIsAnotherWake(t *testing.T) {
	h := newHouse(t)
	h.n.Woke(0)
	h.n.took(claim{From: "Kitchen", Turn: "t", Word: "hey_jarvis", Score: 40})
	h.wait(300 * time.Millisecond)
	if len(h.opened) != 1 {
		t.Error("a louder claim for another wake word kept this one from answering")
	}
	if h.n.took(claim{From: "Kitchen", Turn: "u", Word: "hey_jarvis", Score: 40}) {
		t.Error("a claim for another wake word was told this one was answered")
	}
}

// A device that can no longer answer by the end of the window does not claim the word as answered, and
// passes it on all the same.
func TestCannotAnswerByTheEndOfTheWindow(t *testing.T) {
	h := newHouse(t)
	h.n.Woke(0)
	h.n.canAnswer = func(int) bool { return false }
	h.wait(300 * time.Millisecond)
	if len(h.opened) != 1 {
		t.Error("the wake word was not passed on")
	}
	if h.claimFrom("Kitchen", 10) {
		t.Error("a device that could not answer told another it had")
	}
}

// A refusal saying the word was answered is believed only with the house's proof on it.
func TestOnlyAProvenRefusalStandsDown(t *testing.T) {
	body := []byte(`{"from":"Office","turn":"a","score":20}`)
	header := sign(word, "Kitchen", body, time.Now())
	good := answeredMAC(word, nonceOf(header), "Office", "Kitchen")
	if !answeredBy(word, header, good, "Office", "Kitchen") {
		t.Error("the house's own proof did not check out")
	}
	if answeredBy(word, header, "", "Office", "Kitchen") {
		t.Error("a refusal with no proof was believed")
	}
	if answeredBy(word, header, answeredMAC("other", nonceOf(header), "Office", "Kitchen"), "Office", "Kitchen") {
		t.Error("another house's proof was believed")
	}
	other := sign(word, "Kitchen", body, time.Now())
	if answeredBy(word, other, good, "Office", "Kitchen") {
		t.Error("a proof for another claim was believed")
	}
}

// The whole way: a peer that answers 409 without proof is not believed, one with it is.
func TestPostBelievesOnlyAProvenRefusal(t *testing.T) {
	newHouse(t)
	me := "Office"
	for _, proven := range []bool{false, true} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if proven {
				w.Header().Set(answeredHeader, answeredMAC(word, nonceOf(r.Header.Get(authHeader)), me, "Kitchen"))
			}
			w.WriteHeader(http.StatusConflict)
		}))
		addr := srv.Listener.Addr().(*net.TCPAddr)
		got := post(announce.Peer{Name: "Kitchen", Address: addr.IP.String(), Port: addr.Port}, claim{From: me, Turn: "t", Score: 1})
		srv.Close()
		if got != proven {
			t.Errorf("a 409 with proof %v: believed %v", proven, got)
		}
	}
}

// A claim in the old form, or signed before this program started, is refused: the first keyed the word
// in one hash, the second could be one replayed across a restart.
func TestOldFormAndBeforeTheStartAreRefused(t *testing.T) {
	now := time.Now()
	body := []byte(`{"from":"Kitchen","turn":"a","score":20}`)
	old := "v1" + sign(word, "Office", body, now)[len(version):]
	if err := verify(word, "Office", old, body, now); err != errUnsigned {
		t.Errorf("a v1 claim: %v", err)
	}
	was := started
	started = now.Add(3 * time.Second)
	t.Cleanup(func() { started = was })
	if err := verify(word, "Office", sign(word, "Office", body, now), body, now.Add(3*time.Second)); err != errStale {
		t.Errorf("a claim from before the start: %v", err)
	}
	// The clock set back an hour after the start: a claim signed by it is taken.
	started = now
	if back := now.Add(-time.Hour); verify(word, "Office", sign(word, "Office", body, back), body, back) != nil {
		t.Error("a clock set back after the start refuses every claim")
	}
}

// A claim whose body never arrives whole is answered once its time is up, and its connection closed:
// nothing that has not signed holds the web port.
func TestASlowClaimIsAnsweredAndLetGo(t *testing.T) {
	h := newHouse(t)
	closed := make(chan struct{}, 4)
	srv := httptest.NewUnstartedServer(http.HandlerFunc(h.n.receive))
	srv.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		if s == http.StateClosed {
			closed <- struct{}{}
		}
	}
	srv.Start()
	t.Cleanup(srv.Close)
	for _, header := range []string{"", sign(word, config.Get().Device.Name, []byte("x"), time.Now())} {
		c, err := net.Dial("tcp", srv.Listener.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		req := "POST " + path + " HTTP/1.1\r\nHost: x\r\nContent-Length: 1000\r\n"
		if header != "" {
			req += authHeader + ": " + header + "\r\n"
		}
		if _, err := c.Write([]byte(req + "\r\nab")); err != nil {
			t.Fatal(err)
		}
		_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
		buf := make([]byte, 64)
		n, _ := c.Read(buf)
		if !bytes.HasPrefix(buf[:n], []byte("HTTP/1.1 4")) {
			t.Errorf("signed %v: answered %q, want a refusal", header != "", buf[:n])
		}
		select {
		case <-closed:
		case <-time.After(5 * time.Second):
			t.Errorf("signed %v: the connection was kept", header != "")
		}
		c.Close()
	}
}

// A device on a call leads by onCallLead: it wins a wake word another device heard about as well, the
// call being what somebody most likely meant, but not one another device heard clearly better, which
// was somebody in that room talking to that device.
func TestOnCallLeadsByAMargin(t *testing.T) {
	for _, c := range []struct {
		name   string
		theirs float64
		opened bool
	}{
		{"heard about as well elsewhere", 20 + onCallLead - 1, true},
		{"heard clearly better elsewhere", 20 + onCallLead + 1, false},
	} {
		h := newHouse(t)
		h.n.onCall = func() bool { return true }
		h.n.Woke(0)
		if len(h.sent) != 1 || h.sent[0].Score != 20+onCallLead {
			t.Fatalf("%s: claims sent %+v, want one at %v", c.name, h.sent, 20+onCallLead)
		}
		h.claimFrom("Kitchen", c.theirs)
		h.wait(300 * time.Millisecond)
		if got := len(h.opened) == 1; got != c.opened {
			t.Errorf("%s: opened %v, want %v", c.name, got, c.opened)
		}
	}
}
