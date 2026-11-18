// Package share keeps settings in step between the devices of the house, setting by setting, with the
// devices each one is chosen for, or copies settings to other devices once.
//
//   - A linked setting is kept in step with the devices it is linked to: a change on any of them, from the
//     screen, Home Assistant or the setup page, reaches the others, passed on from one to the next. There is
//     no leader. Each change is numbered one past the highest the device making it had seen, and the
//     highest number wins (the device's name breaks a tie), so the order does not rest on any device's clock.
//     A device that refuses a change as older answers with what it has, and the sender catches up.
//   - A link is made on each device, never by another: ticking a device for a setting here links it here and
//     asks that device, which links back only when somebody accepts there. A device can always take itself
//     out of a link.
//   - A copy sends settings to other devices once, with no link: from the setup page, or Home Assistant's
//     copy_settings action. Any device of the house can copy to a device that has sharing on; a copy landing
//     on a setting linked there is passed on to its links, as a change made there would be.
//   - Any device can keep any setting its own: nothing another device sends changes it, and the sender is
//     told it was kept.
//
// Off until it is turned on, on each device: one that has not been turned on takes nothing. Only the
// settings on the list (catalog.go) travel. Messages are signed with the house word and bound to the two
// devices (feature/announce's share.go). What a device takes, and from whom, is logged.
package share

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/announce"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
)

func init() {
	component.Register(component.Network, Get(), component.Order(40))
}

const (
	// watchEvery is how often the linked settings are looked at for a change made here.
	watchEvery = time.Second

	// askAgainEvery is how often a link asked for and not accepted yet is asked again: the device may
	// have been away, or its answer lost.
	askAgainEvery = 5 * time.Minute

	// sendWithin is how long one message to one device may take.
	sendWithin = 10 * time.Second

	// peersEvery is how often the devices on the network are looked at for one that has come back, to
	// bring it up to date (catchUp).
	peersEvery = 15 * time.Second

	// maxJump is the furthest a change's number may run ahead of the one this device has: changes made
	// elsewhere while it was away number a handful, and a number far past that would end every change
	// made here after it.
	maxJump = 1 << 20

	// maxName is the longest device name taken: it is kept, with where each value came from and with
	// each request, in the settings file.
	maxName = 64
)

// Outcomes, as a device answers for each setting sent to it.
const (
	Applied     = "applied"       // taken
	Same        = "same"          // already so
	Kept        = "kept"          // kept its own
	NotLinked   = "not linked"    // a linked change from a device this one has not linked the setting with
	NotHere     = "not here"      // this device has no such setting
	NotShared   = "not shareable" // not on the list
	Older       = "older"         // this device has a later change, which it answers with
	BadValue    = "bad value"     // not a value the setting takes, or a number out of order
	NotTaking   = "not taking"    // sharing is off on that device
	Unreachable = "unreachable"   // the device could not be reached
	Linked      = "linked"        // a link in place both ways
	Asked       = "asked"         // a link request waiting to be accepted there
	Full        = "full"          // the setting is linked with as many devices as it may be
	Declined    = "declined"      // a link request declined there
)

// message is what travels between two devices.
type message struct {
	Kind  string `json:"kind"` // set, copy, link, get
	Items []item `json:"items,omitempty"`
	// Link, for a link message: request, accept or remove, for the setting ID.
	Link string `json:"link,omitempty"`
	ID   string `json:"id,omitempty"`
}

type item struct {
	ID     string `json:"id"`
	Value  string `json:"value"`
	N      uint64 `json:"n"`
	Origin string `json:"origin"` // the device the change was made on
}

type reply struct {
	Results map[string]string `json:"results"`
	// Current is, for each change refused as older, what this device has instead, for the sender to
	// catch up with.
	Current map[string]item `json:"current,omitempty"`
	// Values is, for a get, what this device has for each setting asked about that it has and shares:
	// none it keeps its own is left out, nor anything not on the list.
	Values map[string]string `json:"values,omitempty"`
}

type Feature struct {
	sw *esphome.Switch

	// send carries a message to a device; replaced in tests.
	send func(ctx context.Context, to string, body []byte) ([]byte, error)
	// me is this device's name; now the clock; both replaced in tests.
	me  func() string
	now func() time.Time

	wake chan struct{}

	// mu is held while settings are taken or a change here is looked for, so the two never cross. The
	// settings' commands run under it: they are quick (they save a setting and set it going), and a slow
	// one would only hold up sharing.
	mu   sync.Mutex
	last map[string]string // the linked settings' values as last seen or taken, so a change is seen once

	// asks is how asking each device for a link went (askAgain), and asking whether askAgain is running.
	// behind is, the same way, each linked setting a change of which did not reach a device: it is sent
	// again (catchUp), soon at first and then less often, until it gets there.
	askMu  sync.Mutex
	asks   map[string]*askLater
	behind map[string]*askLater
	asking atomic.Bool

	// peers is the names of the devices on the network now; replaced in tests. seen is those there at the
	// last look, only Run's.
	peers func() []string
	seen  map[string]bool

	// catching is the devices being brought up to date now (lookForPeers), by lower-case name: one that
	// comes and goes is not sent everything again while the last time is still under way.
	catchMu  sync.Mutex
	catching map[string]bool
}

var (
	once   sync.Once
	shared *Feature
)

func Get() *Feature {
	once.Do(func() {
		f := &Feature{
			send: announce.SendShare,
			me:   func() string { return config.Get().Device.Name },
			now:  time.Now,
			wake: make(chan struct{}, 1),
			last: map[string]string{},
			peers: func() []string {
				var names []string
				for _, p := range announce.Peers() {
					names = append(names, p.Name)
				}
				return names
			},
		}
		f.sw = &esphome.Switch{
			Base: esphome.Base{ObjectID: "settings_sharing", Name: "Settings sharing", Icon: "mdi:sync",
				Category: esphome.CategoryConfig},
			OnCommand: f.SetOn,
		}
		announce.ShareOpen = func() bool { return config.Get().Share.On }
		announce.ShareReceive = f.receive
		shared = f
	})
	return shared
}

func (f *Feature) Name() string { return "settings sharing" }

func (f *Feature) Entities() []esphome.Entity { return []esphome.Entity{f.sw} }

func (f *Feature) Restore(c config.Config) { f.sw.Set(c.Share.On) }

// SetOn is the switch.
func (f *Feature) SetOn(on bool) {
	if err := config.Set().Share().On(on); err != nil {
		slog.Error("saving a setting failed", "setting", "settings sharing", "err", err)
		f.sw.Set(!on)
		return
	}
	f.sw.Set(on)
	slog.Info("setting changed", "setting", "settings sharing", "using", on)
	select {
	case f.wake <- struct{}{}:
	default:
	}
}

// ValidName is whether a device name is one this takes: short, and plain text.
func ValidName(s string) bool {
	if s == "" || len(s) > maxName || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// next is the number after n, which never wraps round.
func next(n uint64) uint64 {
	if n == math.MaxUint64 {
		return n
	}
	return n + 1
}

// Run watches the linked settings for a change made here, while sharing is on, and sends it on; and asks
// again, now and then, for links not accepted yet.
func (f *Feature) Run(ctx context.Context) error {
	tick := time.NewTicker(watchEvery)
	defer tick.Stop()
	askAgain := time.NewTicker(askAgainEvery)
	defer askAgain.Stop()
	peers := time.NewTicker(peersEvery)
	defer peers.Stop()
	for {
		f.watch(ctx)
		select {
		case <-ctx.Done():
			return nil
		case <-f.wake:
		case <-tick.C:
		case <-peers.C:
			f.lookForPeers(ctx)
		case <-askAgain.C:
			f.askAgain(ctx)
		}
	}
}

// lookForPeers brings each device that has come on the network since the last look up to date: it may
// have been unplugged while a linked setting changed here, or changed one itself meanwhile. The first
// look finds every device there, which brings each up to date after this one starts.
func (f *Feature) lookForPeers(ctx context.Context) {
	now := map[string]bool{}
	var back []string
	for _, n := range f.peers() {
		k := strings.ToLower(n)
		now[k] = true
		if !f.seen[k] {
			back = append(back, n)
		}
	}
	f.seen = now
	if !config.Get().Share.On {
		return
	}
	for _, d := range back {
		k := strings.ToLower(d)
		f.catchMu.Lock()
		busy := f.catching[k]
		if !busy {
			if f.catching == nil {
				f.catching = map[string]bool{}
			}
			f.catching[k] = true
		}
		f.catchMu.Unlock()
		if busy {
			continue
		}
		safe.Go("share catch up", func() {
			defer func() { f.catchMu.Lock(); delete(f.catching, k); f.catchMu.Unlock() }()
			f.catchUp(ctx, []string{d}, true)
		})
	}
}

// catchUp sends each linked setting's value here to each device in devices (every linked device, for
// none) it is linked with, all of them (every) or only those a change did not reach whose time has come
// again. Each takes it if it is later than what it has, and answers with its own if not, which is taken
// here the same way: the two end with the later.
func (f *Feature) catchUp(ctx context.Context, devices []string, every bool) {
	c := config.Get().Share
	if !c.On {
		return
	}
	now := f.now()
	for id, linked := range c.Links {
		if !Shareable(id) {
			continue
		}
		for _, d := range linked {
			if c.WaitingFor(id, d) || (devices != nil && !containsFold(devices, d)) {
				continue
			}
			if !every {
				f.askMu.Lock()
				b := f.behind[config.AskKey(id, d)]
				due := b != nil && !now.Before(b.next)
				f.askMu.Unlock()
				if !due {
					continue
				}
			}
			f.sendValue(ctx, id, d)
		}
	}
}

// reached notes, for catchUp, how sending settings to a device went: each that could not reach it is
// sent again later.
func (f *Feature) reached(to string, results map[string]string) {
	f.askMu.Lock()
	defer f.askMu.Unlock()
	if f.behind == nil {
		f.behind = map[string]*askLater{}
	}
	for id, outcome := range results {
		k := config.AskKey(id, to)
		if outcome != Unreachable {
			delete(f.behind, k)
			continue
		}
		b := f.behind[k]
		if b == nil {
			b = &askLater{}
			f.behind[k] = b
		}
		b.delay = min(max(2*b.delay, askFirst), askMost)
		b.next = f.now().Add(b.delay)
	}
}

// watch looks at every linked setting once, and sends each that changed here to the devices it is linked
// with (those that have accepted).
func (f *Feature) watch(ctx context.Context) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := config.Get().Share // after the lock: a change taken meanwhile is in it
	if !c.On {
		return
	}
	me := f.me()
	out := map[string][]item{}
	from := map[string]config.ShareFrom{}
	for id, devices := range c.Links {
		e, ok := entities(id)
		if !ok || !Shareable(id) || len(devices) == 0 {
			continue
		}
		v, ok := valueOf(e)
		if !ok {
			continue
		}
		was, seen := f.last[id]
		f.last[id] = v
		if !seen || was == v {
			continue // the first look only learns the value
		}
		it := item{ID: id, Value: v, N: next(c.From[id].N), Origin: me}
		from[id] = config.ShareFrom{Device: me, At: f.now(), N: it.N}
		for _, d := range devices {
			if !c.WaitingFor(id, d) {
				out[d] = append(out[d], it)
			}
		}
	}
	if len(from) > 0 {
		_ = config.Set().Share().Update(func(s *config.Share) {
			if s.From == nil {
				s.From = map[string]config.ShareFrom{}
			}
			for id, fr := range from {
				s.From[id] = fr
			}
		})
	}
	for d, items := range out {
		safe.Go("share", func() { f.reached(d, f.deliver(ctx, d, message{Kind: "set", Items: items})) })
	}
}

// deliver sends a message to one device, within sendWithin, and answers what it made of it. Where it
// refused a change as older, this device catches up with what it has instead.
func (f *Feature) deliver(ctx context.Context, to string, m message) map[string]string {
	body, err := json.Marshal(m)
	if err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, sendWithin)
	defer cancel()
	raw, err := f.send(ctx, to, body)
	if err != nil {
		outcome := Unreachable
		if errors.Is(err, announce.ErrShareNotTaking) {
			outcome = NotTaking
		}
		slog.Warn("share: not delivered", "to", to, "kind", m.Kind, "outcome", outcome, "err", err)
		out := map[string]string{}
		for _, it := range m.Items {
			out[it.ID] = outcome
		}
		if m.ID != "" {
			out[m.ID] = outcome
		}
		return out
	}
	var r reply
	if json.Unmarshal(raw, &r) != nil {
		return nil
	}
	// Only what was sent is answered for: anything else in the reply is no answer to this message.
	sent := map[string]bool{}
	for _, it := range m.Items {
		sent[it.ID] = true
	}
	if m.ID != "" {
		sent[m.ID] = true
	}
	results := map[string]string{}
	var behind []item
	for id, outcome := range r.Results {
		if !sent[id] {
			continue
		}
		results[id] = outcome
		switch outcome {
		case Applied, Same, Linked, Asked:
		case Older:
			if cur, ok := r.Current[id]; ok && cur.ID == id && ValidName(cur.Origin) && m.Kind != "link" {
				behind = append(behind, cur)
			}
		default:
			slog.Info("share: a device did not take a setting", "to", to, "setting", id, "outcome", outcome)
		}
	}
	if len(behind) > 0 {
		// Taken as a linked change from that device, so the order decides, as for any other.
		got := f.take(to, behind, false)
		slog.Info("share: caught up with a later change", "from", to, "results", got)
	}
	return results
}

// receive is a message from another device.
func (f *Feature) receive(from string, body []byte) ([]byte, error) {
	if !ValidName(from) {
		return nil, errors.New("not a device name this device takes")
	}
	var m message
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, errors.New("not a message this device reads")
	}
	if strings.EqualFold(from, f.me()) {
		return nil, errors.New("a message from this device's own name")
	}
	for _, it := range m.Items {
		if it.Origin != "" && !ValidName(it.Origin) {
			return nil, errors.New("not a device name this device takes")
		}
	}
	rep := reply{Results: map[string]string{}}
	if !config.Get().Share.On {
		for _, it := range m.Items {
			rep.Results[it.ID] = NotTaking
		}
		if m.ID != "" {
			rep.Results[m.ID] = NotTaking
		}
		return json.Marshal(rep)
	}
	switch m.Kind {
	case "set", "copy":
		rep = f.takeReply(from, m.Items, m.Kind == "copy")
	case "link":
		rep.Results[m.ID] = f.linkFrom(from, m.Link, m.ID)
	case "get":
		rep.Values = valuesOf(m.Items)
	default:
		return nil, fmt.Errorf("not a kind of message this device reads: %q", m.Kind)
	}
	return json.Marshal(rep)
}

// take is takeReply's outcomes.
func (f *Feature) take(from string, items []item, copied bool) map[string]string {
	return f.takeReply(from, items, copied).Results
}

// takeReply is settings from another device. A linked change is taken only from a device this one has
// linked the setting with, and only when it comes later in the setting's order than the one this device
// has; one that does not is answered with what this device has. A copy is taken whenever it comes, as the
// latest change. Either way a setting kept here is left, and what is taken is passed on to the setting's
// other links. Where every setting came from is written once.
func (f *Feature) takeReply(from string, items []item, copied bool) reply {
	rep := reply{Results: map[string]string{}}
	newFrom := map[string]config.ShareFrom{}
	forward := map[string][]item{}
	now := f.now()

	f.mu.Lock()
	c := config.Get().Share // after the lock: a change made here meanwhile is in it
	for _, it := range items {
		outcome, fr, write, cur := f.takeOne(from, it, copied, c, now)
		rep.Results[it.ID] = outcome
		if cur != nil {
			if rep.Current == nil {
				rep.Current = map[string]item{}
			}
			rep.Current[it.ID] = *cur
		}
		if !write {
			continue
		}
		newFrom[it.ID] = fr
		c.From = mapWith(c.From, it.ID, fr) // a later item for the same setting is ordered against this
		if outcome == Applied {
			// Passed on, numbered as taken, to every other device this one has linked it with: the number
			// stops it going round, since a device that has it already answers it is older.
			fwd := item{ID: it.ID, Value: it.Value, N: fr.N, Origin: fr.Device}
			for _, d := range c.Links[it.ID] {
				if !strings.EqualFold(d, from) && !strings.EqualFold(d, fr.Device) && !c.WaitingFor(it.ID, d) {
					forward[d] = append(forward[d], fwd)
				}
			}
		}
	}
	if len(newFrom) > 0 {
		_ = config.Set().Share().Update(func(s *config.Share) {
			for id, fr := range newFrom {
				s.From = mapWith(s.From, id, fr)
			}
		})
	}
	f.mu.Unlock()

	for d, its := range forward {
		safe.Go("share", func() { f.deliver(context.Background(), d, message{Kind: "set", Items: its}) })
	}
	return rep
}

func mapWith(m map[string]config.ShareFrom, id string, fr config.ShareFrom) map[string]config.ShareFrom {
	if m == nil {
		m = map[string]config.ShareFrom{}
	}
	m[id] = fr
	return m
}

// takeOne is one setting; with f.mu held. It answers the outcome; where the setting now came from, and
// whether that is to be written; and, for a change refused as older, what this device has instead.
func (f *Feature) takeOne(from string, it item, copied bool, c config.Share, now time.Time) (string, config.ShareFrom, bool, *item) {
	none := config.ShareFrom{}
	if !Shareable(it.ID) {
		return NotShared, none, false, nil
	}
	e, ok := entities(it.ID)
	if !ok {
		return NotHere, none, false, nil
	}
	if c.Kept(it.ID) {
		slog.Info("share: kept this device's own", "setting", it.ID, "from", from)
		return Kept, none, false, nil
	}
	have := c.From[it.ID]
	if it.N > have.N && it.N-have.N > maxJump {
		return BadValue, none, false, nil // a number from nowhere, which would end every change after it
	}
	was, _ := valueOf(e)
	// A copy is numbered here, so it is this device's change in the order, not the sender's: put down
	// as the sender's, it held a number the sender never had, and the sender's next change, numbered
	// the same, was a second value under one number and one name, which neither device would give way
	// to once the two were linked.
	origin := it.Origin
	if origin == "" {
		origin = from
	}
	via := ""
	if copied {
		origin, via = f.me(), from
	}
	var n uint64
	if copied {
		if was == it.Value {
			f.last[it.ID] = was
			return Same, none, false, nil // a copy of what is here already changes nothing, not even the order
		}
		n = max(have.N, it.N)
		if n == math.MaxUint64 {
			return BadValue, none, false, nil
		}
		n++ // a copy is the latest change, wherever the sender's own order stands
	} else {
		if !c.LinkedWith(it.ID, from) || c.WaitingFor(it.ID, from) {
			return NotLinked, none, false, nil
		}
		n = it.N
		if n < have.N || (n == have.N && strings.ToLower(origin) <= strings.ToLower(have.Device)) {
			holder := have.Device
			if holder == "" {
				holder = f.me()
			}
			return Older, none, false, &item{ID: it.ID, Value: was, N: have.N, Origin: holder}
		}
	}
	fr := config.ShareFrom{Device: origin, At: now, N: n, Via: via}
	if was == it.Value {
		f.last[it.ID] = was
		return Same, fr, true, nil // the same value, later in the order: only where it came from is new
	}
	// What this applies is no change made here: the watch is told the value applied, so it does not send
	// it back, even if the command sets it going a moment later than it returns.
	f.last[it.ID] = it.Value
	if err := apply(e, it.Value); err != nil {
		f.last[it.ID] = was
		return BadValue, none, false, nil
	}
	slog.Info("share: setting changed by another device", "setting", it.ID, "from", from, "origin", origin,
		"was", was, "now", it.Value, "copied", copied)
	return Applied, fr, true, nil
}

// linkFrom is another device asking about a link. It can ask (kept as a request here until somebody
// accepts or declines it), accept one this device asked for, or take itself out of one: never link this
// device by itself. Nothing is written for a message that changes nothing.
func (f *Feature) linkFrom(from, op, id string) string {
	if !Shareable(id) {
		return NotShared
	}
	if _, ok := entities(id); !ok && op != "remove" {
		return NotHere
	}
	c := config.Get().Share
	linked, waiting := c.LinkedWith(id, from), c.WaitingFor(id, from)
	requested := false
	for _, r := range c.Requests {
		if r.ID == id && strings.EqualFold(r.Device, from) {
			requested = true
		}
	}
	var outcome string
	var change func(s *config.Share)
	switch op {
	case "request":
		switch {
		case c.WasDeclined(id, from) && !linked:
			outcome = Declined // declined here before: not shown again
		case linked:
			outcome = Linked
			if waiting {
				change = func(s *config.Share) { config.SetWaiting(s, id, from, false, time.Time{}) }
			}
		case requested:
			outcome = Asked
		default:
			outcome = Asked
			change = func(s *config.Share) { config.AddRequest(s, id, from, f.now()) }
		}
	case "accept":
		// Only a link this device asked for: anything else is no link.
		switch {
		case linked && waiting:
			outcome = Linked
			change = func(s *config.Share) { config.SetWaiting(s, id, from, false, time.Time{}) }
		case linked:
			outcome = Linked
		default:
			outcome = NotLinked
		}
	case "remove":
		outcome = Applied
		if linked || waiting || requested {
			change = func(s *config.Share) { config.RemoveLink(s, id, from) }
		}
	default:
		return BadValue
	}
	if change != nil {
		if err := config.Set().Share().Update(change); err != nil {
			return BadValue
		}
		if op != "request" || outcome == Linked {
			f.forget(id, from) // accepted or taken out: nothing waits on it here any more
		}
	}
	slog.Info("share: link message", "setting", id, "from", from, "op", op, "outcome", outcome)
	if op == "accept" && linked && waiting || op == "request" && linked && waiting {
		// The link is in place both ways now: the two settle on the later of what they have.
		safe.Go("share", func() { f.sendValue(context.Background(), id, from) })
	}
	return outcome
}

// sendValue sends this device's value for a setting to one linked device, numbered as it is here: the
// other takes it if it is later than what it has, and answers with its own if not.
func (f *Feature) sendValue(ctx context.Context, id, to string) {
	e, ok := entities(id)
	if !ok {
		return
	}
	f.mu.Lock()
	v, ok := valueOf(e)
	if !ok {
		f.mu.Unlock()
		return
	}
	have := config.Get().Share.From[id]
	origin := have.Device
	if origin == "" {
		origin = f.me()
	}
	f.last[id] = v
	f.mu.Unlock()
	f.reached(to, f.deliver(ctx, to, message{Kind: "set", Items: []item{{ID: id, Value: v, N: have.N, Origin: origin}}}))
}

// Link sets the devices a setting is linked with, from here. Devices added are linked here and asked; each
// links back once it is accepted there. Devices still not accepted are asked again. Devices taken out are
// unlinked here and told. Answers, by device, what each made of it.
func (f *Feature) Link(ctx context.Context, id string, devices []string) (map[string]string, error) {
	if !Shareable(id) {
		return nil, fmt.Errorf("%s is not a setting that can be shared", id)
	}
	me := f.me()
	var want []string
	for _, d := range devices {
		d = strings.TrimSpace(d)
		if !ValidName(d) {
			continue
		}
		if !strings.EqualFold(d, me) && !containsFold(want, d) {
			want = append(want, d)
		}
	}
	if len(want) > config.MaxLinked {
		return nil, fmt.Errorf("a setting is linked with %d devices at most", config.MaxLinked)
	}
	c := config.Get().Share
	var add, again, removed []string
	for _, d := range want {
		switch {
		case !c.LinkedWith(id, d):
			add = append(add, d)
		case c.WaitingFor(id, d):
			again = append(again, d) // asked before, not accepted yet: asked again, with nothing to write
		}
	}
	for _, d := range c.Linked(id) {
		if !containsFold(want, d) {
			removed = append(removed, d)
		}
	}
	results := map[string]string{}
	if len(add) > 0 || len(removed) > 0 {
		now := f.now()
		if err := config.Set().Share().Update(func(s *config.Share) {
			for _, d := range removed {
				config.RemoveLink(s, id, d)
			}
			var asked []string
			for _, d := range add {
				config.Undecline(s, id, d) // asked from here: whatever was declined before is not held against it
				if config.AddLink(s, id, d) {
					config.SetWaiting(s, id, d, true, now)
					asked = append(asked, d)
				} else {
					results[d] = Full
				}
			}
			add = asked
		}); err != nil {
			return nil, err
		}
	}

	// Linked anew or taken out: how asking went before is no guide.
	for _, d := range append(add, removed...) {
		f.forget(id, d)
	}

	var mu sync.Mutex
	var wg sync.WaitGroup
	tell := func(d, op string) {
		defer wg.Done()
		var outcome string
		if op == "request" {
			outcome = f.ask(ctx, id, d)
		} else {
			outcome = f.deliver(ctx, d, message{Kind: "link", Link: op, ID: id})[id]
		}
		mu.Lock()
		results[d] = outcome
		mu.Unlock()
	}
	for _, d := range append(add, again...) {
		wg.Add(1)
		go tell(d, "request")
	}
	for _, d := range removed {
		wg.Add(1)
		go tell(d, "remove")
	}
	wg.Wait()
	if len(add) > 0 || len(removed) > 0 {
		slog.Info("share: links set here", "setting", id, "asked", add, "removed", removed)
	}
	return results, nil
}

// ask sends a link request to a device and acts on the answer: linked there already, the wait is over and
// the two settle on the later value; declined there, the link here goes. Anything else changes nothing
// here.
func (f *Feature) ask(ctx context.Context, id, d string) string {
	outcome := f.deliver(ctx, d, message{Kind: "link", Link: "request", ID: id})[id]
	switch outcome {
	case Linked:
		_ = config.Set().Share().Update(func(s *config.Share) { config.SetWaiting(s, id, d, false, time.Time{}) })
		f.sendValue(ctx, id, d)
	case Declined:
		_ = config.Set().Share().Update(func(s *config.Share) { config.RemoveLink(s, id, d) })
		slog.Info("share: a link was declined", "setting", id, "by", d)
	}
	f.asked(id, d, outcome)
	return outcome
}

// askLater is when a link request not delivered is tried again: soon at first, then less and less often,
// up to once a day; after giveUpAfter from the first time it was asked, not at all, and the setup page
// says there was no answer.
type askLater struct {
	next      time.Time
	delay     time.Duration
	delivered bool // it reached the device, which keeps it until somebody answers there
}

const (
	askFirst    = askAgainEvery
	askMost     = 24 * time.Hour
	giveUpAfter = 7 * 24 * time.Hour
)

// asked notes how asking a device went, for askAgain.
func (f *Feature) asked(id, d, outcome string) {
	f.askMu.Lock()
	defer f.askMu.Unlock()
	if f.asks == nil {
		f.asks = map[string]*askLater{}
	}
	k := config.AskKey(id, d)
	a := f.asks[k]
	if a == nil {
		a = &askLater{}
		f.asks[k] = a
	}
	switch outcome {
	case Linked, Declined:
		delete(f.asks, k) // settled: nothing waits on it any more
	case Asked, NotShared, NotHere:
		a.delivered = true // it got there and is kept there: nothing gained by asking again
	default:
		// Not delivered, this time, whatever happened before: tried again later.
		a.delivered = false
		a.delay = min(max(2*a.delay, askFirst), askMost)
		a.next = f.now().Add(a.delay)
	}
}

// forget drops how asking a device about a setting went: the link was made, taken out, or settled.
func (f *Feature) forget(id, d string) {
	f.askMu.Lock()
	delete(f.asks, config.AskKey(id, d))
	f.askMu.Unlock()
}

// NoAnswer is whether a device asked to link a setting has not answered in giveUpAfter: it is not asked
// again.
func NoAnswer(s config.Share, id, d string, now time.Time) bool {
	at, ok := s.AskedAt[config.AskKey(id, d)]
	return ok && s.WaitingFor(id, d) && now.Sub(at) > giveUpAfter
}

// askAgain asks again, on its own goroutine, every device a link is waiting on whose request did not get
// through, when its time comes. It writes nothing unless an answer changes something.
func (f *Feature) askAgain(ctx context.Context) {
	if !f.asking.CompareAndSwap(false, true) {
		return
	}
	safe.Go("share ask again", func() {
		defer f.asking.Store(false)
		f.askDue(ctx)
	})
}

// askDue is askAgain's work, here and now.
func (f *Feature) askDue(ctx context.Context) {
	c := config.Get().Share
	if !c.On {
		return
	}
	now := f.now()
	for id, devices := range c.Waiting {
		if !Shareable(id) {
			continue
		}
		for _, d := range devices {
			if NoAnswer(c, id, d, now) {
				continue
			}
			f.askMu.Lock()
			a := f.asks[config.AskKey(id, d)]
			due := a == nil || (!a.delivered && !now.Before(a.next))
			f.askMu.Unlock()
			if due {
				f.ask(ctx, id, d)
			}
		}
	}
	f.catchUp(ctx, nil, false)
}

// Accept accepts another device's request to link a setting: linked here, and the device is told. Once
// it has heard, the two settle on the later value.
func (f *Feature) Accept(ctx context.Context, id, device string) (string, error) {
	if !Shareable(id) || !ValidName(device) {
		return "", fmt.Errorf("%s is not a setting that can be shared", id)
	}
	ok := false
	if err := config.Set().Share().Update(func(s *config.Share) { ok = config.AddLink(s, id, device) }); err != nil {
		return "", err
	}
	if !ok {
		return Full, fmt.Errorf("%s is linked with %d devices already", id, config.MaxLinked)
	}
	return f.deliver(ctx, device, message{Kind: "link", Link: "accept", ID: id})[id], nil
}

// Decline declines another device's request to link a setting, remembers it so the same request is
// answered as declined rather than shown again, and tells the device.
func (f *Feature) Decline(ctx context.Context, id, device string) {
	_ = config.Set().Share().Update(func(s *config.Share) { config.Decline(s, id, device, f.now()) })
	f.forget(id, device)
	f.deliver(ctx, device, message{Kind: "link", Link: "remove", ID: id})
}

// Copy sends settings to devices once, with no link: answers, by device, what each made of each setting.
func (f *Feature) Copy(ctx context.Context, ids, devices []string) map[string]map[string]string {
	me := f.me()
	var items []item
	for _, id := range ids {
		if !Shareable(id) {
			continue
		}
		e, ok := entities(id)
		if !ok {
			continue
		}
		if v, ok := valueOf(e); ok {
			items = append(items, item{ID: id, Value: v, N: config.Get().Share.From[id].N, Origin: me})
		}
	}
	out := map[string]map[string]string{}
	if len(items) == 0 {
		return out
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, d := range devices {
		if strings.EqualFold(d, me) || !ValidName(d) {
			continue
		}
		wg.Add(1)
		go func(d string) {
			defer wg.Done()
			r := f.deliver(ctx, d, message{Kind: "copy", Items: items})
			mu.Lock()
			out[d] = r
			mu.Unlock()
		}(d)
	}
	wg.Wait()
	slog.Info("share: settings copied", "settings", len(items), "to", len(out))
	return out
}

// valuesOf is what this device has for the settings a get asks about: only those on the list, and none it
// keeps its own, which another device has no say over and so no need to see.
func valuesOf(items []item) map[string]string {
	c := config.Get().Share
	out := map[string]string{}
	for i, it := range items {
		if i >= len(shareable) {
			break // each setting once at most: more is not a get the page sends
		}
		if !Shareable(it.ID) || c.Kept(it.ID) {
			continue
		}
		if e, ok := entities(it.ID); ok {
			if v, ok := valueOf(e); ok {
				out[it.ID] = v
			}
		}
	}
	return out
}

// Here is what this device has for settings on the list, by setting.
func Here(ids []string) map[string]string {
	out := map[string]string{}
	for _, id := range ids {
		if !Shareable(id) {
			continue
		}
		if e, ok := entities(id); ok {
			if v, ok := valueOf(e); ok {
				out[id] = v
			}
		}
	}
	return out
}

// Values asks devices what they have for settings, for the setup page to show beside each one: answers,
// by device, each setting's value there. A device with sharing off is there with nil, for the page to say
// so; one that does not answer in time, or is on a release from before it could be asked, is left out.
func (f *Feature) Values(ctx context.Context, ids, devices []string) map[string]map[string]string {
	var items []item
	for _, id := range ids {
		if Shareable(id) {
			items = append(items, item{ID: id})
		}
	}
	out := map[string]map[string]string{}
	if len(items) == 0 {
		return out
	}
	body, err := json.Marshal(message{Kind: "get", Items: items})
	if err != nil {
		return out
	}
	me := f.me()
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, d := range devices {
		if strings.EqualFold(d, me) || !ValidName(d) {
			continue
		}
		wg.Add(1)
		go func(d string) {
			defer wg.Done()
			raw, err := f.send(ctx, d, body)
			if errors.Is(err, announce.ErrShareNotTaking) {
				mu.Lock()
				out[d] = nil
				mu.Unlock()
				return
			}
			if err != nil {
				return
			}
			var r reply
			if json.Unmarshal(raw, &r) != nil {
				return
			}
			for _, outcome := range r.Results {
				if outcome == NotTaking {
					mu.Lock()
					out[d] = nil
					mu.Unlock()
					return
				}
			}
			// An answer with no values (left out when empty) is a device that keeps every one asked about
			// its own, or has none of them: it answered. One on a release that cannot be asked answers
			// with an error instead, and was left out above.
			got := map[string]string{}
			for _, it := range items {
				if v, ok := r.Values[it.ID]; ok && utf8.ValidString(v) && len(v) <= 256 {
					got[it.ID] = v
				}
			}
			mu.Lock()
			out[d] = got
			mu.Unlock()
		}(d)
	}
	wg.Wait()
	return out
}

func containsFold(list []string, s string) bool {
	for _, x := range list {
		if strings.EqualFold(x, s) {
			return true
		}
	}
	return false
}
