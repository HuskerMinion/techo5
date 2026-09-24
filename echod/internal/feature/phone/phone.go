// Package phone makes the device a telephone: it signs in to a SIP provider, places calls Home
// Assistant asks for, rings for calls that come in, and carries the call's audio through the
// microphones and the speaker.
//
// The login comes from Home Assistant only (the phone_account action), over its encrypted link, and
// is kept in its own owner-only file. Calls go over TLS with SRTP unless the account says otherwise.
//
// To Home Assistant: a status sensor, the other party, answer and hang up buttons, the phone_call,
// phone_answer and phone_hangup actions, and an esphome.techo5_phone event for each thing a call does,
// so automations can announce a caller or send a message when a call is missed. Each event carries the
// device's name, the other party, the direction and, when it ends, who ended it and how long it lasted.
package phone

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/emiago/diago"
	"github.com/emiago/sipgo/sip"
	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/web"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/led"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hook"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
	"github.com/HuskerMinion/techo5/echod/internal/service"
)

func init() {
	component.Register(component.Network, Get(), component.Order(60),
		component.Supervise(service.Restart(5*time.Second, 5*time.Minute)))
}

// Event is what Home Assistant's bus calls something a call did.
const Event = "esphome.techo5_phone"

const (
	// ringFor is how long an incoming call rings before it counts as missed. A provider usually gives
	// up sooner and sends callers to voicemail.
	ringFor = 60 * time.Second

	// dialFor bounds a call nobody answers.
	dialFor = 60 * time.Second
)

// Phase is what the phone is doing.
type Phase int

const (
	Idle Phase = iota
	Ringing
	Dialing
	Talking
)

func (p Phase) String() string {
	switch p {
	case Ringing:
		return "Ringing"
	case Dialing:
		return "Calling"
	case Talking:
		return "In call"
	}
	return "Idle"
}

// State is what the screen and Home Assistant show.
type State struct {
	Configured bool
	Registered bool
	Phase      Phase
	Peer       string // who is on the other end, or calling
	Incoming   bool
	Intercom   bool      // the call is with another device in the house, not over the phone line
	DropIn     bool      // an intercom call that connected by itself: somebody is listening in
	Since      time.Time // when the phase began
	Problem    string    // why the phone cannot be used, if it cannot
}

type Phone struct {
	status, peer     *esphome.TextSensor
	answer, hangup   *esphome.Button
	dropIn, dnd      *esphome.Switch
	ringLED, callLED *led.Claim

	// Changed fires when State does; listeners must not block.
	Changed hook.Hook[State]

	reload chan struct{}

	mu    sync.Mutex
	state State
	line  *line
	// answered is closed to answer the call that is ringing; end cancels the call in progress.
	answered chan struct{}
	end      context.CancelFunc
	say      chan []int16

	// declined is when each device's last intercom call here was turned down, so it cannot ring
	// again straight away.
	declined map[string]time.Time
}

var (
	once   sync.Once
	shared *Phone
)

func Get() *Phone {
	once.Do(func() { shared = build() })
	return shared
}

func build() *Phone {
	p := &Phone{
		status: &esphome.TextSensor{Base: esphome.Base{ObjectID: "phone", Name: "Phone", Icon: "mdi:phone"}},
		peer:   &esphome.TextSensor{Base: esphome.Base{ObjectID: "phone_peer", Name: "Phone call", Icon: "mdi:account-voice"}},
		answer: &esphome.Button{Base: esphome.Base{ObjectID: "phone_answer", Name: "Answer call", Icon: "mdi:phone-in-talk"}},
		hangup: &esphome.Button{Base: esphome.Base{ObjectID: "phone_hangup", Name: "Hang up", Icon: "mdi:phone-hangup"}},
		reload: make(chan struct{}, 1),
		say:    make(chan []int16, 16),
	}
	p.answer.OnPress = func() { p.Answer() }
	p.dropIn = &esphome.Switch{Base: esphome.Base{ObjectID: "intercom_drop_in", Name: "Allow Drop In", Icon: "mdi:phone-in-talk", Category: esphome.CategoryConfig}}
	p.dnd = &esphome.Switch{Base: esphome.Base{ObjectID: "intercom_do_not_disturb", Name: "Intercom do not disturb", Icon: "mdi:phone-cancel", Category: esphome.CategoryConfig}}
	p.dropIn.OnCommand = p.SetDropIn
	p.dnd.OnCommand = p.SetDoNotDisturb
	web.Handle(intercomPath, "", intercomOpen, p.intercomIn)
	p.hangup.OnPress = func() { p.Hangup() }
	p.ringLED = led.Get().Claim(led.PriorityAlarm)
	p.callLED = led.Get().Claim(led.PriorityTurn)
	p.status.Set("Not set up")
	p.peer.Set("")
	return p
}

func (p *Phone) Name() string { return "phone" }

func (p *Phone) Entities() []esphome.Entity {
	return []esphome.Entity{p.status, p.peer, p.answer, p.hangup, p.dropIn, p.dnd}
}

// Restore puts the intercom's two switches back the way they were left.
func (p *Phone) Restore(c config.Config) {
	p.dropIn.Set(c.Home.DropIn)
	p.dnd.Set(c.Home.DoNotDisturb)
}

// SetDropIn lets intercom calls connect by themselves, or has them ring again.
func (p *Phone) SetDropIn(on bool) {
	if err := config.Set().Home().DropIn(on); err != nil {
		slog.Error("phone: saving drop in", "err", err)
		return
	}
	p.dropIn.Set(on)
	slog.Info("intercom: drop in", "allowed", on)
	p.Changed.Emit(p.State())
}

// SetDoNotDisturb turns intercom calls away, or lets them ring again.
func (p *Phone) SetDoNotDisturb(on bool) {
	if err := config.Set().Home().DoNotDisturb(on); err != nil {
		slog.Error("phone: saving do not disturb", "err", err)
		return
	}
	p.dnd.Set(on)
	slog.Info("intercom: do not disturb", "on", on)
	p.Changed.Emit(p.State())
}

// State is a copy of what the phone is doing.
func (p *Phone) State() State {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.state
}

// Busy reports whether a call is ringing, being placed or in progress: the wake word and the action
// button leave the speaker and the microphones to it.
func (p *Phone) Busy() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.state.Phase != Idle
}

// Button is the action button while a call is up: it answers one that is ringing and hangs up one
// that is placed or in progress. It reports whether there was a call to act on.
func (p *Phone) Button() bool {
	switch p.State().Phase {
	case Ringing:
		p.Answer()
		return true
	case Dialing, Talking:
		p.Hangup()
		return true
	}
	return false
}

// Say sends 16 kHz speech into the call in progress, and reports whether there was one.
func (p *Phone) Say(samples []int16) bool {
	if p.State().Phase != Talking {
		return false
	}
	select {
	case p.say <- samples:
		return true
	default:
		return false
	}
}

// claim takes the line for a call, with p.mu held.
func (p *Phone) claim(phase Phase, peer string, incoming bool) {
	p.state.Phase, p.state.Peer, p.state.Incoming = phase, peer, incoming
	p.state.Since = time.Now()
}

func (p *Phone) set(f func(s *State)) {
	p.mu.Lock()
	before := p.state
	f(&p.state)
	if p.state.Phase == Idle {
		p.state.Intercom, p.state.DropIn = false, false
	}
	if p.state.Phase != before.Phase {
		p.state.Since = time.Now()
	}
	st := p.state
	p.mu.Unlock()

	p.status.Set(statusText(st))
	p.peer.Set(st.Peer)
	switch st.Phase {
	case Ringing:
		p.ringLED.Play(led.EffectHeartbeat, callColor)
		p.callLED.Clear()
	case Dialing, Talking:
		p.ringLED.Clear()
		p.callLED.Play(led.EffectPulse, callColor)
	default:
		p.ringLED.Clear()
		p.callLED.Clear()
	}
	p.Changed.Emit(st)
}

var callColor = led.Color{R: 0x00, G: 0xC8, B: 0x53}

func statusText(s State) string {
	switch {
	case s.Phase != Idle:
		return s.Phase.String()
	case !s.Configured:
		return "Not set up"
	case s.Problem != "":
		return s.Problem
	case !s.Registered:
		return "Connecting"
	}
	return "Ready"
}

func fire(event string, st State, extra ...string) {
	// device names which one this was: every device's events arrive on the one bus under one name.
	data := map[string]string{"event": event, "peer": st.Peer, "device": config.Get().Device.Name}
	data["kind"] = "phone"
	if st.Intercom {
		data["kind"] = "intercom"
	}
	if st.Incoming {
		data["direction"] = "incoming"
	} else {
		data["direction"] = "outgoing"
	}
	for i := 0; i+1 < len(extra); i += 2 {
		data[extra[i]] = extra[i+1]
	}
	component.Fire.Emit(component.Event{Name: Event, Data: data})
}

// Run keeps the account signed in, and signs in again whenever the login changes.
func (p *Phone) Run(ctx context.Context) error {
	for {
		acct, err := loadAccount()
		if err != nil {
			slog.Error("phone: reading the account", "err", err)
		}
		p.set(func(s *State) { s.Configured, s.Registered, s.Problem = acct.valid(), false, "" })
		if !acct.valid() {
			select {
			case <-ctx.Done():
				return nil
			case <-p.reload:
				continue
			}
		}

		lctx, cancel := context.WithCancel(ctx)
		errc := make(chan error, 1)
		safe.Go("phone: line", func() { errc <- p.serve(lctx, acct) })

		select {
		case <-ctx.Done():
			cancel()
			<-errc
			return nil
		case <-p.reload:
			slog.Info("phone: account changed, signing in again")
			cancel()
			<-errc
		case err := <-errc:
			cancel()
			if refused(err) {
				// A login the provider refuses is not tried again until it changes: a wrong password
				// tried over and over gets the whole home's address blocked.
				slog.Error("phone: the provider refused the login; waiting for a new one", "err", err)
				p.set(func(s *State) { s.Registered, s.Problem = false, "Login refused" })
				select {
				case <-ctx.Done():
					return nil
				case <-p.reload:
					continue
				}
			}
			p.set(func(s *State) { s.Registered, s.Problem = false, "Cannot sign in" })
			// Anything else (the network, the provider down) goes back to the supervisor, which waits
			// longer each time.
			return fmt.Errorf("phone: %w", err)
		}
	}
}

// serve signs one account in and keeps it there until ctx ends.
func (p *Phone) serve(ctx context.Context, acct Account) error {
	l, err := open(ctx, acct, p.incoming)
	if err != nil {
		return err
	}
	defer l.close()

	p.mu.Lock()
	p.line = l
	p.mu.Unlock()
	defer func() {
		// The line going - a reload of the account, a failure - ends a call on it, and only that:
		// an intercom call between two rooms has nothing to do with the provider.
		p.mu.Lock()
		intercom := p.state.Intercom
		p.mu.Unlock()
		if !intercom {
			p.Hangup()
		}
		p.mu.Lock()
		p.line = nil
		p.mu.Unlock()
	}()

	first := true
	err = l.register(ctx, func() {
		if first {
			slog.Info("phone: signed in", "server", acct.Server, "secure", !acct.Plain)
			first = false
		}
		p.set(func(s *State) { s.Registered, s.Problem = true, "" })
	})
	if ctx.Err() != nil {
		return nil
	}
	return err
}

// refused reports whether the provider turned the login down, as opposed to not being reached.
func refused(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, " 401 ") || strings.Contains(msg, " 403 ") || strings.Contains(msg, " 407 ")
}

// Call places a call to a number or an extension. It returns once the call is under way; what happens
// to it is reported through State and the Home Assistant event.
func (p *Phone) Call(number string) error {
	number = dialable(number)
	if number == "" {
		return errors.New("phone: no number to call")
	}
	ctx, cancel := context.WithCancel(context.Background())
	// The line is checked idle and claimed in one step: checked and claimed apart, a call coming in
	// between the two was let through as well, and one of the two was left ringing with nobody able
	// to answer it.
	p.mu.Lock()
	l := p.line
	var err error
	switch {
	case l == nil:
		err = errors.New("phone: not set up")
	case p.state.Phase != Idle:
		err = errors.New("phone: already on a call")
	case !p.state.Registered:
		err = errors.New("phone: not signed in yet")
	default:
		p.end = cancel
		p.claim(Dialing, number, false)
	}
	p.mu.Unlock()
	if err != nil {
		cancel()
		return err
	}
	p.set(func(*State) {}) // tell Home Assistant and the lights
	slog.Info("phone: calling", "number", number)
	fire("dialing", p.State())

	safe.Go("phone: call", func() {
		defer cancel()
		// The call's media lives on the context it was placed with, so that context must outlast the
		// call: a timeout on it would cut the audio the moment the call was answered. Nobody answering
		// is limited by canceling the whole call instead.
		unanswered := time.AfterFunc(dialFor, cancel)
		d, err := l.dial(ctx, number)
		unanswered.Stop()
		if err != nil {
			reason := "failed"
			if ctx.Err() != nil {
				reason = "cancelled"
			}
			slog.Info("phone: call not answered", "number", number, "err", err)
			fire("not_answered", p.State(), "reason", reason)
			p.set(func(s *State) { s.Phase, s.Peer = Idle, "" })
			return
		}
		defer d.Close()
		p.talk(ctx, d.Context(), func(tctx context.Context) error { return talk(tctx, &d.DialogMedia, p.say) },
			func(hctx context.Context) error { return d.Hangup(hctx) })
	})
	return nil
}

// incoming is a call offered to the device.
func (p *Phone) incoming(d *diago.DialogServerSession) {
	caller := callerOf(d.InviteRequest)
	p.mu.Lock()
	busy := p.state.Phase != Idle
	answered := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	if !busy {
		p.answered, p.end = answered, cancel
		p.claim(Ringing, caller, true) // in the same step as the check, as Call does
	}
	p.mu.Unlock()
	defer cancel()

	if busy {
		slog.Info("phone: call refused, already on one", "from", caller)
		_ = d.Respond(sip.StatusBusyHere, "Busy Here", nil)
		return
	}
	if err := d.Ringing(); err != nil {
		slog.Warn("phone: ringing", "err", err)
	}
	p.set(func(*State) {})
	slog.Info("phone: ringing", "from", caller)
	fire("ringing", p.State())

	rctx, stopRing := context.WithCancel(ctx)
	safe.Go("phone: ring", func() { ring(rctx) })

	timeout := time.NewTimer(ringFor)
	defer timeout.Stop()
	select {
	case <-answered:
		stopRing()
		if err := d.Answer(); err != nil {
			slog.Warn("phone: answering", "err", err)
			fire("ended", p.State(), "reason", "answer failed")
			p.set(func(s *State) { s.Phase, s.Peer = Idle, "" })
			return
		}
		p.talk(ctx, d.Context(), func(tctx context.Context) error { return talk(tctx, &d.DialogMedia, p.say) },
			func(hctx context.Context) error { return d.Hangup(hctx) })
	case <-ctx.Done():
		stopRing()
		_ = d.Respond(603, "Decline", nil)
		fire("declined", p.State())
		p.set(func(s *State) { s.Phase, s.Peer = Idle, "" })
	case <-d.Context().Done():
		stopRing()
		slog.Info("phone: missed call", "from", caller)
		fire("missed", p.State())
		p.set(func(s *State) { s.Phase, s.Peer = Idle, "" })
	case <-timeout.C:
		stopRing()
		_ = d.Respond(sip.StatusTemporarilyUnavailable, "No Answer", nil)
		slog.Info("phone: missed call", "from", caller)
		fire("missed", p.State())
		p.set(func(s *State) { s.Phase, s.Peer = Idle, "" })
	}
}

// talk runs an answered call until either end hangs up. audio carries the call's sound until it is
// told to stop, over whichever line the call is on.
func (p *Phone) talk(ctx, callCtx context.Context, audio func(context.Context) error, hangup func(context.Context) error) {
	p.set(func(s *State) { s.Phase = Talking })
	fire("answered", p.State())
	slog.Info("phone: call up", "peer", p.State().Peer)

	// Anything said before the call was up is not for this call.
	for len(p.say) > 0 {
		<-p.say
	}

	tctx, cancel := context.WithCancel(ctx)
	safe.Go("phone: far end", func() {
		select {
		case <-callCtx.Done():
		case <-tctx.Done():
		}
		cancel()
	})
	if err := audio(tctx); err != nil {
		slog.Warn("phone: call audio", "err", err)
	}
	cancel()

	who := "far end"
	if ctx.Err() != nil && callCtx.Err() == nil {
		who = "device"
		hctx, hcancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := hangup(hctx); err != nil {
			slog.Warn("phone: hanging up", "err", err)
		}
		hcancel()
	}
	st := p.State()
	slog.Info("phone: call ended", "peer", st.Peer, "by", who, "after", time.Since(st.Since).Round(time.Second))
	fire("ended", st, "by", who, "seconds", fmt.Sprint(int(time.Since(st.Since).Seconds())))
	p.set(func(s *State) { s.Phase, s.Peer = Idle, "" })
}

// Answer picks up the call that is ringing.
func (p *Phone) Answer() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.state.Phase != Ringing || p.answered == nil {
		return
	}
	close(p.answered)
	p.answered = nil
}

// answerIf answers only the call whose answer channel this is. Drop In answers by itself after its
// chime, and a hangup in between lets the line go: a call that has claimed it since is not this
// one's to pick up.
func (p *Phone) answerIf(answered chan struct{}) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.state.Phase != Ringing || p.answered == nil || p.answered != answered {
		return
	}
	close(p.answered)
	p.answered = nil
}

// Hangup ends the call in progress, stops one being placed, or declines one that is ringing.
func (p *Phone) Hangup() {
	p.mu.Lock()
	end := p.end
	p.end = nil
	p.answered = nil
	p.mu.Unlock()
	if end != nil {
		end()
	}
}

// Actions: phone_account signs the device in (an empty username signs it out); phone_call places a
// call; intercom_call calls another device in the house; phone_contacts sets who a screen offers to
// call; phone_answer and phone_hangup act on the call that is up, whichever line it is on.
func (p *Phone) Actions() []*esphome.Action {
	return []*esphome.Action{
		{
			Name: "phone_account",
			Args: []esphome.Arg{
				{Name: "server", Type: esphome.ArgString},
				{Name: "username", Type: esphome.ArgString},
				{Name: "password", Type: esphome.ArgString},
			},
			Run: func(c esphome.Call) (any, error) {
				if !encrypted() {
					return nil, errors.New("phone: set an API encryption key first; the password would otherwise cross the network in the clear")
				}
				acct := Account{Server: c.String("server"), Username: c.String("username"), Password: c.String("password")}
				if err := saveAccount(acct); err != nil {
					return nil, err
				}
				slog.Info("phone: account replaced", "server", strings.TrimSpace(acct.Server), "signed_out", strings.TrimSpace(acct.Username) == "")
				select {
				case p.reload <- struct{}{}:
				default:
				}
				return nil, nil
			},
		},
		{
			Name: "phone_call",
			Args: []esphome.Arg{{Name: "number", Type: esphome.ArgString}},
			Run:  func(c esphome.Call) (any, error) { return nil, p.Call(c.String("number")) },
		},
		{
			Name: "phone_contacts",
			Args: []esphome.Arg{{Name: "contacts", Type: esphome.ArgString}},
			Run: func(c esphome.Call) (any, error) {
				list, err := parseContacts(c.String("contacts"))
				if err != nil {
					return nil, err
				}
				if err := saveContacts(list); err != nil {
					return nil, err
				}
				slog.Info("phone: contacts replaced", "count", len(list))
				p.Changed.Emit(p.State())
				return nil, nil
			},
		},
		{
			Name: "intercom_call",
			Args: []esphome.Arg{{Name: "device", Type: esphome.ArgString}},
			Run:  func(c esphome.Call) (any, error) { return nil, p.CallDevice(c.String("device")) },
		},
		{Name: "phone_answer", Run: func(esphome.Call) (any, error) { p.Answer(); return nil, nil }},
		{Name: "phone_hangup", Run: func(esphome.Call) (any, error) { p.Hangup(); return nil, nil }},
	}
}

// dialable keeps what a phone can dial: digits, and a leading + dropped (the provider takes 1 and ten
// digits for North America). Anything else, such as spaces, dashes and brackets, is removed.
func dialable(number string) string {
	var b strings.Builder
	for _, r := range strings.TrimPrefix(strings.TrimSpace(number), "+") {
		if unicode.IsDigit(r) || r == '*' || r == '#' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// callerOf is who is calling: the display name if the network passed one on, and the number.
func callerOf(req *sip.Request) string {
	if req == nil {
		return "Unknown"
	}
	from := req.From()
	if from == nil {
		return "Unknown"
	}
	name := strings.Trim(from.DisplayName, `" `)
	number := from.Address.User
	switch {
	case name != "" && number != "" && name != number:
		return name + " (" + number + ")"
	case number != "":
		return number
	case name != "":
		return name
	}
	return "Unknown"
}

// encrypted reports whether the Home Assistant link has a key of its own rather than the reserved
// all-zeros one an unprovisioned device answers with.
func encrypted() bool {
	b, err := os.ReadFile(layout.KeyPath)
	if err != nil {
		return false
	}
	k, err := esphome.ParsePSK(strings.TrimSpace(string(b)))
	return err == nil && !k.IsZero()
}
