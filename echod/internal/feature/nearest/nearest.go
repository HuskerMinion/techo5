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
// A device that is not sure it heard its wake word asks Home Assistant, which takes most of a second
// (#685). It says so to the others as it asks, with how well it heard it: an unsure claim. A device that
// heard the word for sure, but less well than that, waits for the answer before it opens its turn: the
// unsure device's claim once Home Assistant has heard the word too, which it then loses to, or the unsure
// claim withdrawn, or unsureFor gone by, after which it answers. An unsure claim never makes a device
// stand down by itself, so a wake Home Assistant turns down costs the device somebody is talking to a
// moment's wait at most, never the answer. Its word is unsurePrefix and the wake word, which a device on
// an older release takes for another word and ignores.
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

	// unsurePrefix makes an unsure claim's word: the wake word asked about, behind it.
	unsurePrefix = "unsure:"

	// unsureFor is how long an unsure claim is waited on. It is made as the unsure device's score
	// first goes over its confirm threshold, and the question goes once the score has fallen away, up
	// to 600 ms later (detect's CandidateFor); then Home Assistant has 1.5 s to hear the word (voice's
	// confirmFor), and the claim that follows crosses the network. The microphones' history
	// (mic.Held) still reaches back to the word after it.
	unsureFor = 2200 * time.Millisecond

	// heldPoll is how often a device waiting on an unsure claim looks again.
	heldPoll = 50 * time.Millisecond
)

// claim is what a device says when it hears its wake word. Word is which wake word, so two people
// saying different ones to different devices at once are not taken for one person saying one.
//
// Withdrawn is an unsure claim taken back: Home Assistant did not hear the word, or the device gave up
// asking.
type claim struct {
	From      string  `json:"from"`
	Turn      string  `json:"turn"`
	Word      string  `json:"word,omitempty"`
	Score     float64 `json:"score"`
	Withdrawn bool    `json:"withdrawn,omitempty"`
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

	// peers is how many devices were told of it, for the log.
	peers int

	// clarity is how clearly this device heard it, before the offset and a call's lead: what Confirm
	// below nearest-device score is measured against (#503), so an offset set to lean the decision
	// doesn't make every wake faint, or none.
	clarity float64

	// answered is set once the turn opened here; elsewhere when a device that already answered said so.
	answered, elsewhere bool

	// held is when this claim began waiting on an unsure one that beat it, zero while it has not.
	held time.Time

	// lost runs when another device has the word instead (Confirmed): beaten, already answered, or a
	// newer claim of this device's own took this one's place. Nil for a wake word heard here, which
	// simply does not open.
	lost func()
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
	faint     func(slot int, at time.Time) // a faint win (won): asked about first, else woke's (#503)
	busy      func() bool
	cancel    func() // ends the turn running here (cutOff)
	canAnswer func(slot int) bool
	wakeWord  func(slot int) string
	score     func() float64
	hears     func() bool
	// onCall is whether a call is up here and lets the wake word through (phone.WakeAllowed): a wake word
	// heard during one is most likely for this device, the one somebody wants to hang up, so its claim
	// leads by onCallLead. Only then: a device on a call that does not take wake words claims as any other.
	onCall func() bool
	// interrupting is whether a wake word heard now takes a turn here over (voice.Interruptible):
	// thinking or replying, not listening, when it is part of what is being said.
	interrupting func() bool
	peers        func() []announce.Peer
	// peersHeard is when any other device was last heard on the network (announce.LastHeard), for the
	// line a wake word with nobody to ask writes.
	peersHeard func() time.Time
	fanout     func(peers []announce.Peer, mine *pending)
	tell       func(peers []announce.Peer, c claim) // an unsure claim, or its withdrawal: nothing comes back
	now        func() time.Time
	after      func(d time.Duration, f func())

	mu      sync.Mutex
	enabled bool
	others  []arrived
	mine    *pending

	// skipped is why the last wake word went to its turn at once, without a decision, and "" once one
	// has taken part since. Each of those ways was silent, so a device that answered over the one
	// somebody spoke to said nothing of why (#751); it is written when it changes, not at every wake.
	skipped string
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
		faint:     func(slot int, at time.Time) { voice.Get().WokeFaint(slot, at) },
		busy:      func() bool { return voice.Get().Busy() },
		cancel:    func() { voice.Get().Cancel() },
		canAnswer: func(slot int) bool { return voice.Get().CanAnswer(slot) },
		wakeWord: func(slot int) string {
			if ids := voice.Get().ActiveWakeWords(); slot >= 0 && slot < len(ids) {
				return ids[slot]
			}
			return ""
		},
		score:        func() float64 { return mic.Get().Standout(heard) },
		hears:        layout.HasMicrophone,
		onCall:       func() bool { return phone.Get().Busy() && phone.Get().WakeAllowed() },
		interrupting: func() bool { return voice.Get().Interruptible() },
		peers:        announce.Peers,
		peersHeard:   announce.LastHeard,
		now:          time.Now,
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
	n.tell = func(peers []announce.Peer, c claim) {
		if len(peers) > maxPeers {
			peers = peers[:maxPeers]
		}
		for _, p := range peers {
			safe.Go("nearest unsure claim", func() { post(p, c) })
		}
	}
	web.Handle(path, "", n.open, n.receive)
	voice.SetConfirmedContest(n.Confirmed)
	voice.SetAskingClaim(n.Asking)
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
		if config.Get().Home.HouseWord == "" {
			slog.Warn("nearest device answers is on, but does nothing until this device has a house word")
		}
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
func (n *Nearest) open() bool { return n.closed() == "" }

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
	over := n.busy()
	var lost func()
	if over && n.claimsOver(true) {
		lost = n.cutOff
	}
	n.contend(n.wakeWord(slot), n.now(), true,
		func() bool { return n.canAnswer(slot) },
		func(at time.Time, score float64) { n.wonOver(over, slot, at, score) },
		func() { n.wokeNow(slot) }, lost)
}

// cutOff is a wake word said over this device's own reply that another device heard clearly better
// (#737): the turn there is the one somebody started, so this device's reply ends, as every
// interrupting wake word ended it before such a word was claimed. Left playing, it talks over the
// other device's turn. Only while a turn is still running here: one that has finished since has
// nothing to end.
func (n *Nearest) cutOff() {
	if !n.busy() {
		return
	}
	slog.Info("nearest device: another heard the wake word over this reply better, ending the reply")
	n.cancel()
}

// WokeGated is Woke for a wake word whose turn may still be called off (detect's contest with a command
// word): the claim goes out at once, as Woke's does, and gate decides whether the turn opens once this
// device has won it, sending what was said since at from the history.
func (n *Nearest) WokeGated(slot int, at time.Time, gate func(open func())) {
	over := n.busy()
	// Lost over a reply, the reply ends as Woke's does, but through the gate too: a command word that
	// beat the wake word here means it was not a wake word after all, and the reply goes on.
	var lost func()
	if over && n.claimsOver(true) {
		lost = func() { gate(n.cutOff) }
	}
	n.contend(n.wakeWord(slot), at, true,
		func() bool { return n.canAnswer(slot) },
		func(at time.Time, score float64) { gate(func() { n.wonOver(over, slot, at, score) }) },
		func() { gate(func() { n.woke(slot, at) }) }, lost)
}

// won is a wake word this device won, having heard it with score (pending.clarity). One under Confirm below
// nearest-device score is asked about first (#503): a TV across the room can score as highly on the
// model as somebody at the device, and far lower on how clearly it stood out here, while a person in
// another room scores low here too, so it is asked about rather than dropped. Only the first wake word's:
// Home Assistant is asked about that one alone. A word with nobody to compare with never gets here.
func (n *Nearest) won(slot int, at time.Time, score float64) {
	if below := config.Get().Wake.OnHA.FaintBelow(); below > 0 && slot == 0 && score < below {
		slog.Info("nearest device: heard it faintly, asking home assistant", "score", round(score), "below", below)
		n.faint(slot, at)
		return
	}
	n.woke(slot, at)
}

// wonOver is won for a wake word claimed over a turn of this device's own (over: busy when it was
// heard), which is never asked about: how clearly it stood out was measured over this device's own
// reply, and a wake word that interrupts one has always opened at once.
func (n *Nearest) wonOver(over bool, slot int, at time.Time, score float64) {
	if over {
		n.woke(slot, at)
		return
	}
	n.won(slot, at, score)
}

// Confirmed is a wake this device was not sure of that Home Assistant then heard too (#685), heard at at
// with clarity, measured then. It takes part in the decision as a wake over the threshold does, claimed
// only now that Home Assistant has heard it: a candidate it turned down never makes the device somebody
// is talking to stand down. decide is told whether this device answers. Home Assistant has already
// confirmed it, so a faint one is not asked about again (#503).
func (n *Nearest) Confirmed(slot int, at time.Time, clarity float64, settled func(confirmed bool), decide func(answer bool)) {
	n.contendHeard(n.wakeWord(slot), at, clarity, true,
		func() bool { return n.canAnswer(slot) },
		func(time.Time, float64) { decide(true) },
		func() {
			// Answered without a claim for the word (busy, unable, nobody to ask): the unsure claim
			// is taken back, or a device that heard it for sure would wait unsureFor on it for nothing.
			if settled != nil {
				settled(false)
			}
			decide(true)
		},
		func() { decide(false) })
}

// Asking is a wake this device was not sure of, being asked about now (#685), heard with clarity. The
// others are told with an unsure claim, so one that heard it for sure but less well waits for Home
// Assistant's answer; settled, run when the question ends, takes the claim back unless Home Assistant
// heard the word (confirmed), when the claim Confirmed makes stands in for it. Nil when nobody is told.
func (n *Nearest) Asking(slot int, clarity float64) (settled func(confirmed bool)) {
	if !n.open() || !n.canAnswer(slot) {
		return nil
	}
	peers := hearing(n.peers())
	if len(peers) == 0 {
		return nil
	}
	c := config.Get()
	u := claim{From: c.Device.Name, Turn: newTurn(), Word: unsurePrefix + n.wakeWord(slot),
		Score: clarity + float64(c.Nearest.Offset) + n.lead(true)}
	n.tell(peers, u)
	return func(confirmed bool) {
		if confirmed {
			return
		}
		u.Withdrawn = true
		n.tell(peers, u)
	}
}

// Answer is the same question for something other than a wake word, the next song word: of the devices
// that heard word, only the one that heard it best acts. It acts at once when this device does not take
// part, cannot act, or has nobody to ask, and only the nearest does otherwise. What it does is act, run
// after the window, and the others hear nothing of it. Word names what was said, so it is not taken for
// a wake word or for another word.
func (n *Nearest) Answer(word string, can func() bool, act func()) {
	n.contend(word, n.now(), false, can, func(time.Time, float64) { act() }, act, nil)
}

// contend is the claim every word goes through; wake is whether it is a wake word. open runs after the
// window, with the time the word was heard and this device's score for it, if this device is the
// nearest; now runs in its place when there is nobody to ask. A turn that is already open is not
// claimed over, but for a wake word that takes it over (claimsOver). lost, if not nil, runs when another
// device has the word instead.
func (n *Nearest) contend(word string, at time.Time, wake bool, can func() bool, open func(time.Time, float64), now, lost func()) {
	if n.passes(wake, can) {
		now()
		return
	}
	n.contendHeard(word, at, n.score(), wake, can, open, now, lost)
}

// passes is whether a word goes to its turn at once, without a claim: this device does not take part,
// a turn of its own is running that the word does not take over, or it could not answer. The order is
// the one the checks always had, so can, which may ask the voice feature, is asked last.
func (n *Nearest) passes(wake bool, can func() bool) bool {
	if why := n.closed(); why != "" {
		n.skip(wake, why)
		return true
	}
	if n.busy() && !n.claimsOver(wake) {
		n.skip(wake, "a turn is running here")
		return true
	}
	if !can() {
		n.skip(wake, "this device could not answer it")
		return true
	}
	return false
}

// closed is why claims are not made here, "" when they are (open).
func (n *Nearest) closed() string {
	n.mu.Lock()
	on := n.enabled
	n.mu.Unlock()
	switch {
	case !on:
		return "nearest device answers is off"
	case !n.hears():
		return "this device has no microphones"
	case config.Get().Home.HouseWord == "":
		return "no house word"
	}
	return ""
}

// skip writes why a wake word went to its turn without a decision, when that is not why the last one
// did (skipped). Only for wake words: another word (Answer) is acted on at once just as quietly as it
// always was.
func (n *Nearest) skip(wake bool, why string, args ...any) {
	if !wake {
		return
	}
	n.mu.Lock()
	same := n.skipped == why
	n.skipped = why
	n.mu.Unlock()
	if !same {
		slog.Info("nearest device: answering at once, "+why, args...)
	}
}

// lastPeerHeard is peersHeard's answer, zero without one.
func (n *Nearest) lastPeerHeard() time.Time {
	if n.peersHeard == nil {
		return time.Time{}
	}
	return n.peersHeard()
}

// claimsOver is whether a word heard during a turn of this device's own is claimed all the same: a
// wake word that takes the turn over, said while it is thinking or replying (#737). Opened at once, as
// every word during a turn was, it was the one device not claiming it: the others heard no claim in
// their window, the best of them answered however faintly it heard it, and by the time this device's
// interrupted run had closed, Home Assistant had that turn and refused this one as a duplicate. A wake
// word while listening is part of what is being said, and ignored, so it is still not claimed, and
// neither is any other word.
func (n *Nearest) claimsOver(wake bool) bool {
	return wake && n.interrupting != nil && n.interrupting()
}

// contendHeard is contend with how clearly this device heard the word already measured, and lost, if
// not nil, run when another device has it instead.
func (n *Nearest) contendHeard(word string, at time.Time, clarity float64, wake bool, can func() bool, open func(time.Time, float64), now, lost func()) {
	if n.passes(wake, can) {
		now()
		return
	}
	all := n.peers()
	peers := hearing(all)
	if len(peers) == 0 {
		// The others are found over mDNS (announce): a device whose looks stop hearing them forgets
		// them all, and from then on answers every wake word at once, over the one somebody spoke to.
		last := "never"
		if t := n.lastPeerHeard(); !t.IsZero() {
			last = n.now().Sub(t).Round(time.Second).String()
		}
		n.skip(wake, "no other devices known", "devices", len(all), "last_heard_ago", last)
		now()
		return
	}

	c := config.Get()
	window := c.Nearest.Window()
	mine := &pending{
		claim: claim{From: c.Device.Name, Turn: newTurn(), Word: word,
			Score: clarity + float64(c.Nearest.Offset) + n.lead(wake)},
		at:      at,
		window:  window,
		clarity: clarity,
		peers:   len(peers),
		lost:    lost,
	}
	n.mu.Lock()
	n.mine = mine
	n.skipped = ""
	n.mu.Unlock()

	n.fanout(peers, mine)
	n.after(window, func() { n.decide(word, mine, can, open) })
}

// decide is the end of the window: the turn opens unless a claim heard since a window before the word
// beat this one, or a device had already answered it.
//
// Whether this device can still answer is asked again here: the window is long enough for Home Assistant
// to go, or a call to start. One that cannot does not claim the word as answered, so a later device that
// can is not told to stand down for it, and it is passed on all the same, to say so in the usual way.
func (n *Nearest) decide(word string, mine *pending, can func() bool, open func(time.Time, float64)) {
	defer func() {
		// Whatever went wrong here, the wake word is still somebody talking to a device.
		if r := recover(); r != nil {
			slog.Error("nearest device: deciding failed, answering", "panic", r)
			open(mine.at, mine.clarity)
		}
	}()

	able := can()
	now := n.now()
	var best, unsure arrived
	var beaten, elsewhere, current, hold, first bool
	func() {
		n.mu.Lock()
		defer n.mu.Unlock()
		if current = n.mine == mine; !current {
			return
		}
		best, beaten = n.bestSince(mine.at.Add(-mine.window), mine.claim)
		elsewhere = mine.elsewhere
		if !beaten && !elsewhere && able {
			unsure, hold = n.unsureSince(mine.at.Add(-mine.window), mine.claim, now)
			// However many unsure claims come, the wake word is held unsureFor at most in all: each new
			// one would otherwise start the wait again, and a device that kept sending them (a broken
			// one, or one that has lost track of its turns) would keep this one from ever answering.
			if hold && !mine.held.IsZero() && now.Sub(mine.held) >= unsureFor {
				hold = false
			}
			if hold {
				first = mine.held.IsZero()
				if first {
					mine.held = now
				}
			}
		}
		mine.answered = !beaten && !elsewhere && able && !hold
	}()
	if current && hold {
		if first {
			slog.Info("nearest device: waiting on a device asking home assistant", "word", word,
				"mine", round(mine.Score), "theirs", round(unsure.Score), "by", unsure.From)
		}
		n.after(heldPoll, func() { n.decide(word, mine, can, open) })
		return
	}
	if !current {
		if mine.lost != nil {
			mine.lost()
		}
		return
	}

	switch {
	case elsewhere:
		slog.Info("nearest device: another had already answered", "word", word, "score", round(mine.Score))
		if mine.lost != nil {
			mine.lost()
		}
	case beaten:
		slog.Info("nearest device: another heard it better", "word", word, "mine", round(mine.Score),
			"theirs", round(best.Score), "by", best.From)
		if mine.lost != nil {
			mine.lost()
		}
	case !able:
		slog.Info("nearest device: could not answer after all, passing it on", "word", word)
		open(mine.at, mine.clarity)
	default:
		slog.Info("nearest device: answering", "word", word, "score", round(mine.Score), "clarity", round(mine.clarity),
			"waited", now.Sub(mine.at).Round(time.Millisecond), "held", !mine.held.IsZero(), "peers", mine.peers)
		open(mine.at, mine.clarity)
	}
}

// onCallLead is added to a claim from a device on a call, in the same dB of standout the claims are
// made of. Enough to win a word another device heard about as well, the call being what somebody most
// likely meant; not so much that a word somebody said to a device in another room, and heard faintly
// here, goes to the call.
const onCallLead = 6

// lead is what this device's claim for a word leads by: onCallLead on a call, and as much for a wake
// word said over a turn of its own (claimsOver), which is most likely meant for the device that is
// talking, as one said during a call is for the call. Its clarity is measured over its own reply too,
// which buries the word. Both count once: the lead is to win a word heard about as well elsewhere,
// never one heard clearly better. It goes into the claim's Score, so a device on an older release
// weighs it as this one does, and every device still agrees on who answers.
func (n *Nearest) lead(wake bool) float64 {
	if n.onCall != nil && n.onCall() || n.claimsOver(wake) {
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

// unsureSince is the best unsure claim from another device since from that beats mine and is still
// waited on: neither withdrawn nor followed by that device's claim for the word itself, and younger
// than unsureFor. Called with the lock held.
func (n *Nearest) unsureSince(from time.Time, mine claim, now time.Time) (arrived, bool) {
	want := unsurePrefix + mine.Word
	var best arrived
	found := false
	for i, a := range n.others {
		if a.at.Before(from) || a.Word != want || a.Withdrawn || now.Sub(a.at) >= unsureFor {
			continue
		}
		settled := false
		for _, b := range n.others[i+1:] {
			if b.From == a.From && (b.Word == mine.Word || b.Word == want && b.Withdrawn && b.Turn == a.Turn) {
				settled = true
				break
			}
		}
		if settled {
			continue
		}
		c := a.claim
		c.Word = mine.Word
		if better(c, mine) && (!found || better(a.claim, best.claim)) {
			best, found = a, true
		}
	}
	return best, found
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
//
// A detector can fire again on the same words a second or two later (#661), so the turn this device
// opened keeps being claimed for as long as it lasts, up to kept: past the window alone, another device's
// repeat found nobody to tell it the word was taken, and opened a second turn.
func (n *Nearest) took(c claim) (answeredHere bool) {
	busy := n.busy()
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
	if mine == nil || !mine.answered || mine.Word != c.Word {
		return false
	}
	since := now.Sub(mine.at)
	return since <= 2*mine.window || busy && since < kept
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
	// A claim is this house's only: never one from away, over the tailnet.
	if web.AwayRequest(r) {
		http.NotFound(w, r)
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
