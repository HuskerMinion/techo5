// Package nearest decides which device answers a wake word that several of them heard.
//
// Home Assistant keeps whichever device reaches it first and refuses the rest, which is the fastest
// device rather than the one somebody spoke to. With this on, a device that hears its wake word tells the
// others how well it heard it, waits a moment to hear from them, and only opens its turn if none of them
// heard it better. The others go back to listening without a sound.
//
// How well is how far the word stood out from the device's own room (hardware/mic's Standout), plus an
// offset of the device's own for hardware that hears louder or quieter than the rest.
//
// A device that has already answered keeps the turn. A claim that arrives after that, however much better,
// is told so, and the device that sent it stands down: taking the turn away would leave nobody answering,
// since Home Assistant refuses the same wake word from a second device for a moment after the first.
//
// It fails open: with no house word, no other devices, a claim that does not arrive or anything else
// going wrong, a device answers exactly as it would without this. The most it can cost is the moment it
// waits, and what is said in that moment is sent from the microphone's history.
package nearest

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/announce"
	"github.com/HuskerMinion/techo5/echod/internal/feature/phone"
	"github.com/HuskerMinion/techo5/echod/internal/feature/voice"
	"github.com/HuskerMinion/techo5/echod/internal/feature/web"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/mic"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
)

func init() {
	component.Register(component.Device, Get(), component.Order(40))
}

const (
	// path is where claims arrive, on the device's web port, which the Wi-Fi firewall already lets in.
	path = "/nearest"

	// heard is how far back the loudness is read at a wake: the word, which has just ended.
	heard = 1200 * time.Millisecond

	// kept is how long a claim from another device is remembered: longer than any window, so one that
	// arrived just before this device heard the word still counts.
	kept = 3 * time.Second

	// warnEvery is how often a claim that does not check out is written to the log, so a device on the
	// network sending them cannot fill it.
	warnEvery = time.Minute

	// bodyWithin is how long a signed-looking claim has to arrive whole: one is a few hundred bytes.
	bodyWithin = 2 * time.Second

	// maxPeers bounds the devices one wake word is told to, which mDNS alone decides.
	maxPeers = 16
)

// claim is what a device says when it hears its wake word. Word is which wake word, so two people
// saying different ones to different devices at once are not taken for one person saying one.
type claim struct {
	From  string  `json:"from"`
	Turn  string  `json:"turn"`
	Word  string  `json:"word,omitempty"`
	Score float64 `json:"score"`
}

// arrived is a claim from another device and when it came.
type arrived struct {
	claim
	at time.Time
}

// pending is this device's own claim for the wake word it heard last.
type pending struct {
	claim
	at     time.Time // when the word was heard
	window time.Duration

	// answered is set once the turn opened here; elsewhere when a device that already answered said so.
	answered, elsewhere bool
}

// Nearest is the switch, its two settings and the claims.
type Nearest struct {
	on     *esphome.Switch
	window *esphome.Number
	offset *esphome.Number

	// The device, replaced in tests: which wakes open a turn, whether one could, how well this device
	// heard the word, whether it hears at all, the others, how a claim reaches them, and the clock.
	woke      func(slot int, at time.Time)
	wokeNow   func(slot int)
	busy      func() bool
	canAnswer func(slot int) bool
	wakeWord  func(slot int) string
	score     func() float64
	hears     func() bool
	// onCall is whether a call is up here and lets the wake word through (phone.WakeAllowed): a wake word
	// heard during one is most likely for this device, the one somebody wants to hang up, so its claim
	// leads by onCallLead. Only then: a device on a call that does not take wake words claims as any other.
	onCall func() bool
	peers  func() []announce.Peer
	fanout func(peers []announce.Peer, mine *pending)
	now    func() time.Time
	after  func(d time.Duration, f func())

	mu      sync.Mutex
	enabled bool
	others  []arrived
	mine    *pending
}

var (
	once   sync.Once
	shared *Nearest
)

// Get is the one.
func Get() *Nearest {
	once.Do(func() { shared = build() })
	return shared
}

func build() *Nearest {
	n := &Nearest{
		on: &esphome.Switch{Base: esphome.Base{
			ObjectID: "nearest_device_answers", Name: "Nearest device answers", Icon: "mdi:account-voice",
			Category: esphome.CategoryConfig,
		}},
		window: &esphome.Number{
			Base: esphome.Base{ObjectID: "nearest_device_window", Name: "Nearest device window", Icon: "mdi:timer-sand",
				Category: esphome.CategoryConfig},
			Min: config.MinNearestWindowMs, Max: config.MaxNearestWindowMs, Step: 10, Unit: "ms",
			Mode: esphome.NumberBox,
		},
		offset: &esphome.Number{
			Base: esphome.Base{ObjectID: "nearest_device_offset", Name: "Nearest device offset", Icon: "mdi:tune-variant",
				Category: esphome.CategoryConfig},
			Min: config.MinNearestOffset, Max: config.MaxNearestOffset, Step: 1, Unit: "dB",
			Mode: esphome.NumberBox,
		},
		woke:      func(slot int, at time.Time) { voice.Get().WokeAt(slot, at) },
		wokeNow:   func(slot int) { voice.Get().Woke(slot) },
		busy:      func() bool { return voice.Get().Busy() },
		canAnswer: func(slot int) bool { return voice.Get().CanAnswer(slot) },
		wakeWord: func(slot int) string {
			if ids := voice.Get().ActiveWakeWords(); slot >= 0 && slot < len(ids) {
				return ids[slot]
			}
			return ""
		},
		score:  func() float64 { return mic.Get().Standout(heard) },
		hears:  layout.HasMicrophone,
		onCall: func() bool { return phone.Get().Busy() && phone.Get().WakeAllowed() },
		peers:  announce.Peers,
		now:    time.Now,
		after: func(d time.Duration, f func()) {
			time.AfterFunc(d, func() { safe.Go("nearest device decision", f) })
		},
	}
	n.fanout = func(peers []announce.Peer, mine *pending) {
		if len(peers) > maxPeers {
			peers = peers[:maxPeers]
		}
		for _, p := range peers {
			safe.Go("nearest claim", func() {
				if post(p, mine.claim) {
					n.answeredElsewhere(mine, p.Name)
				}
			})
		}
	}
	n.on.OnCommand = func(on bool) {
		n.set(on)
		if err := config.Set().Nearest().On(on); err != nil {
			slog.Error("saving nearest device answers failed", "err", err)
		}
	}
	n.window.OnCommand = func(v float32) {
		n.window.Set(v)
		if err := config.Set().Nearest().WindowMs(int(v)); err != nil {
			slog.Error("saving the nearest device window failed", "err", err)
		}
	}
	n.offset.OnCommand = func(v float32) {
		n.offset.Set(v)
		if err := config.Set().Nearest().Offset(int(v)); err != nil {
			slog.Error("saving the nearest device offset failed", "err", err)
		}
	}
	web.Handle(path, "", n.open, n.receive)
	return n
}

func (n *Nearest) Name() string { return "nearest device" }

func (n *Nearest) Entities() []esphome.Entity { return []esphome.Entity{n.on, n.window, n.offset} }

func (n *Nearest) Restore(c config.Config) {
	n.set(c.Nearest.Enabled())
	n.window.Set(float32(c.Nearest.Window() / time.Millisecond))
	n.offset.Set(float32(c.Nearest.Offset))
	slog.Info("restored", "what", n.on.ObjectID, "using", c.Nearest.Enabled())
}

// set turns taking part on or off. The loudness is measured from now on, so it has learned the room by
// the time a wake word comes; with no house word yet too, since nothing comes back here when one is set. A wake word already waiting on the others is still answered as it would
// have been: turning this off must not lose it.
func (n *Nearest) set(on bool) {
	if on {
		warm(config.Get().Home.HouseWord)
	}
	n.on.Set(on)
	mic.Get().SetStandout(on)
	n.mu.Lock()
	n.enabled = on
	if !on {
		n.others = nil
	}
	n.mu.Unlock()
	web.Wake()
}

// open is whether claims are taken: only while this device takes part, can hear a wake word at all
// (not a Skylight), and the house has a word to sign them with.
func (n *Nearest) open() bool {
	n.mu.Lock()
	on := n.enabled
	n.mu.Unlock()
	return on && n.hears() && config.Get().Home.HouseWord != ""
}

// hearing is the others that could claim a wake word: a device without microphones (a Skylight) never
// does, and waiting on it would only cost the window. A device on an older release says nothing either
// way and is asked; one that had this off, as it was by default, still costs the window until it is
// updated.
func hearing(peers []announce.Peer) []announce.Peer {
	var out []announce.Peer
	for _, p := range peers {
		if p.Hears() {
			out = append(out, p)
		}
	}
	return out
}

// Woke is a wake word heard here, in place of voice's Woke. It opens the turn at once unless this device
// takes part, could answer, and has somebody to ask who could hear it too; otherwise it claims the word, and opens the turn
// after the window only if no device claimed it better or had already answered it.
func (n *Nearest) Woke(slot int) {
	at := n.now()
	if !n.open() || n.busy() || !n.canAnswer(slot) {
		n.wokeNow(slot)
		return
	}
	peers := hearing(n.peers())
	if len(peers) == 0 {
		n.wokeNow(slot)
		return
	}

	c := config.Get()
	window := c.Nearest.Window()
	mine := &pending{
		claim: claim{From: c.Device.Name, Turn: newTurn(), Word: n.wakeWord(slot),
			Score: n.score() + float64(c.Nearest.Offset) + n.callBonus()},
		at:     at,
		window: window,
	}
	n.mu.Lock()
	n.mine = mine
	n.mu.Unlock()

	n.fanout(peers, mine)
	n.after(window, func() { n.decide(slot, mine) })
}

// decide is the end of the window: the turn opens unless a claim heard since a window before the word
// beat this one, or a device had already answered it.
//
// Whether this device can still answer is asked again here: the window is long enough for Home Assistant
// to go, or a call to start. One that cannot does not claim the word as answered, so a later device that
// can is not told to stand down for it, and it is passed on all the same, to say so in the usual way.
func (n *Nearest) decide(slot int, mine *pending) {
	defer func() {
		// Whatever went wrong here, the wake word is still somebody talking to a device.
		if r := recover(); r != nil {
			slog.Error("nearest device: deciding failed, answering", "panic", r)
			n.woke(slot, mine.at)
		}
	}()

	can := n.canAnswer(slot)
	var best arrived
	var beaten, elsewhere, current bool
	func() {
		n.mu.Lock()
		defer n.mu.Unlock()
		if current = n.mine == mine; !current {
			return
		}
		best, beaten = n.bestSince(mine.at.Add(-mine.window), mine.claim)
		elsewhere = mine.elsewhere
		mine.answered = !beaten && !elsewhere && can
	}()
	if !current {
		return
	}

	switch {
	case elsewhere:
		slog.Info("nearest device: another had already answered", "slot", slot+1, "score", round(mine.Score))
	case beaten:
		slog.Info("nearest device: another heard it better", "slot", slot+1, "mine", round(mine.Score),
			"theirs", round(best.Score), "by", best.From)
	case !can:
		slog.Info("nearest device: could not answer after all, passing it on", "slot", slot+1)
		n.woke(slot, mine.at)
	default:
		slog.Info("nearest device: answering", "slot", slot+1, "score", round(mine.Score),
			"waited", n.now().Sub(mine.at).Round(time.Millisecond))
		n.woke(slot, mine.at)
	}
}

// onCallLead is added to a claim from a device on a call, in the same dB of standout the claims are
// made of. Enough to win a word another device heard about as well, the call being what somebody most
// likely meant; not so much that a word somebody said to a device in another room, and heard faintly
// here, goes to the call.
const onCallLead = 6

func (n *Nearest) callBonus() float64 {
	if n.onCall != nil && n.onCall() {
		return onCallLead
	}
	return 0
}

// bestSince is the best claim from another device since from, and whether it beats mine. Called with
// the lock held.
func (n *Nearest) bestSince(from time.Time, mine claim) (arrived, bool) {
	var best arrived
	found := false
	for _, a := range n.others {
		if a.at.Before(from) || a.Word != mine.Word {
			continue
		}
		if !found || better(a.claim, best.claim) {
			best, found = a, true
		}
	}
	return best, found && better(best.claim, mine)
}

// better is whether a heard the word better than b. A tie goes to the name that sorts first, so two
// devices that tie still agree on one.
func better(a, b claim) bool {
	if a.Score != b.Score {
		return a.Score > b.Score
	}
	return a.From < b.From
}

// answeredElsewhere is a device that had already answered telling this one so, for the claim mine.
func (n *Nearest) answeredElsewhere(mine *pending, by string) {
	n.mu.Lock()
	mine.elsewhere = true
	n.mu.Unlock()
	slog.Debug("nearest device: told the word was already answered", "by", by)
}

// took is a claim that arrived from another device. It answers whether this device has already
// answered the same wake word: a claim that comes after that, however much better, is too late, and the
// device that sent it stands down.
func (n *Nearest) took(c claim) (answeredHere bool) {
	now := n.now()
	n.mu.Lock()
	defer n.mu.Unlock()
	fresh := n.others[:0]
	for _, a := range n.others {
		if now.Sub(a.at) < kept {
			fresh = append(fresh, a)
		}
	}
	n.others = append(fresh, arrived{claim: c, at: now})
	mine := n.mine
	return mine != nil && mine.answered && mine.Word == c.Word && now.Sub(mine.at) <= 2*mine.window
}

func round(v float64) float64 { return float64(int(v*10+0.5)) / 10 }

func newTurn() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// receive takes a claim from another device of this house.
func (n *Nearest) receive(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		web.Refuse(w, "post a claim", http.StatusMethodNotAllowed)
		return
	}
	// The header first, and the body only with a deadline: nothing that has not signed is waited on.
	if err := looksSigned(r.Header.Get(authHeader), time.Now()); err != nil {
		warn(n.now(), "nearest device: a claim that does not check out", "from", r.RemoteAddr, "err", err)
		web.Refuse(w, "not this house", http.StatusForbidden)
		return
	}
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(bodyWithin))
	body, err := readBody(r)
	if err != nil {
		web.Refuse(w, "not a claim", http.StatusBadRequest)
		return
	}
	c := config.Get()
	if err := verify(c.Home.HouseWord, c.Device.Name, r.Header.Get(authHeader), body, time.Now()); err != nil {
		warn(n.now(), "nearest device: a claim that does not check out", "from", r.RemoteAddr, "err", err)
		web.Refuse(w, "not this house", http.StatusForbidden)
		return
	}
	var cl claim
	if err := json.Unmarshal(body, &cl); err != nil || cl.From == "" {
		web.Refuse(w, "not a claim", http.StatusBadRequest)
		return
	}
	if cl.From == c.Device.Name {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if n.took(cl) {
		w.Header().Set(answeredHeader, answeredMAC(c.Home.HouseWord, nonceOf(r.Header.Get(authHeader)), cl.From, c.Device.Name))
		http.Error(w, "answered here already", http.StatusConflict)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// warned is when a warning about another device's claims or answers was last written, for warn.
var warned struct {
	sync.Mutex
	at time.Time
}

// warn logs, at most once every warnEvery: whatever is sending bad claims or unproven refusals may send
// them as fast as it likes.
func warn(now time.Time, msg string, args ...any) {
	warned.Lock()
	quiet := !warned.at.IsZero() && now.Sub(warned.at) < warnEvery
	if !quiet {
		warned.at = now
	}
	warned.Unlock()
	if !quiet {
		slog.Warn(msg, args...)
	}
}

// client sends claims. Connections are kept between wakes, so the next claim does not wait on a new
// one; the timeout is the longest window, after which a claim is of no use to anybody.
var client = &http.Client{
	Timeout: time.Duration(config.MaxNearestWindowMs) * time.Millisecond,
	Transport: &http.Transport{
		DialContext:         (&net.Dialer{Timeout: time.Duration(config.MaxNearestWindowMs) * time.Millisecond}).DialContext,
		MaxIdleConnsPerHost: 1,
		IdleConnTimeout:     10 * time.Minute,
	},
}

// post sends a claim to one other device, and answers whether that device said, with proof, that it had
// already answered the word. Anything else, failing to arrive included, says so in the log at most: the
// device answers without it, as it would have anyway.
func post(p announce.Peer, c claim) (answeredThere bool) {
	body, err := json.Marshal(c)
	if err != nil {
		slog.Warn("nearest device: a claim that could not be written", "err", err)
		return false
	}
	word := config.Get().Home.HouseWord
	ctx, cancel := context.WithTimeout(context.Background(), client.Timeout)
	defer cancel()
	url := "http://" + net.JoinHostPort(p.Address, fmt.Sprint(p.Port)) + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return false
	}
	header := sign(word, p.Name, body, time.Now())
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(authHeader, header)
	resp, err := client.Do(req)
	if err != nil {
		slog.Debug("nearest device: claim not delivered", "to", p.Name, "err", err)
		return false
	}
	_ = resp.Body.Close()
	if resp.StatusCode == http.StatusConflict {
		if answeredBy(word, header, resp.Header.Get(answeredHeader), c.From, p.Name) {
			return true
		}
		warn(time.Now(), "nearest device: told the word was answered, without proof; answering anyway", "by", p.Name)
		return false
	}
	if resp.StatusCode >= 300 {
		slog.Debug("nearest device: claim refused", "to", p.Name, "status", resp.Status)
	}
	return false
}
