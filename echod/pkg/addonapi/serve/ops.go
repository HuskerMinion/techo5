package serve

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/HuskerMinion/techo5/echod/pkg/addonapi"
)

// needs is the capability each op needs. hello needs none; command.reply needs commands, as only a
// command event asks for one.
var needs = map[string]string{
	addonapi.OpCommandReply:  addonapi.CapCommands,
	addonapi.OpEventFire:     addonapi.CapEvents,
	addonapi.OpSpeak:         addonapi.CapSpeak,
	addonapi.OpSpeakCancel:   addonapi.CapSpeak,
	addonapi.OpSoundPlay:     addonapi.CapSound,
	addonapi.OpTimerStart:    addonapi.CapTimers,
	addonapi.OpTimerCancel:   addonapi.CapTimers,
	addonapi.OpTimerList:     addonapi.CapTimers,
	addonapi.OpAwakeHold:     addonapi.CapKeepAwake,
	addonapi.OpAwakeRenew:    addonapi.CapKeepAwake,
	addonapi.OpAwakeRelease:  addonapi.CapKeepAwake,
	addonapi.OpSettingsGet:   addonapi.CapSettings,
	addonapi.OpSettingsReply: addonapi.CapSettings,
	addonapi.OpEntitySet:     addonapi.CapEntities,
	addonapi.OpMediaPlay:     addonapi.CapMedia,
	addonapi.OpMediaPause:    addonapi.CapMedia,
	addonapi.OpMediaResume:   addonapi.CapMedia,
	addonapi.OpMediaStop:     addonapi.CapMedia,
}

func bad(format string, a ...any) error {
	return addonapi.Errorf(addonapi.CodeBadRequest, format, a...)
}

// args reads a request's arguments strictly into v. Missing arguments are an empty object.
func args(req addonapi.Request, v any) error {
	b := []byte(req.Args)
	if len(b) == 0 {
		b = []byte("{}")
	}
	if err := addonapi.Unmarshal(b, v); err != nil {
		return bad("%s's arguments do not read: %v", req.Op, err)
	}
	return nil
}

// handle answers one request: its result, or an error, an *addonapi.Error for a refusal.
func (s *Server) handle(c *conn, req addonapi.Request) (any, error) {
	g := c.a.grant
	if req.Op != addonapi.OpHello && !c.greeted() {
		return nil, bad("the first request is hello")
	}
	if want, ok := needs[req.Op]; ok && !g.Has(want) {
		return nil, addonapi.Errorf(addonapi.CodeDenied, "%s needs the %s capability, which this add-on was not granted", req.Op, want)
	}
	switch req.Op {
	case addonapi.OpHello:
		var h addonapi.Hello
		if err := args(req, &h); err != nil {
			return nil, err
		}
		if h.API < 1 {
			return nil, bad("hello says the API level the add-on was written for, 1 or more")
		}
		c.mu.Lock()
		c.hello = true
		c.mu.Unlock()
		w := addonapi.Welcome{API: addonapi.Version, Addon: g.ID, Capabilities: slices.Clone(g.Caps), Device: s.Device.Info()}
		if g.Has(addonapi.CapSettings) {
			w.Settings = s.Device.Settings(g.ID)
		}
		return w, nil

	case addonapi.OpCommandReply:
		var r addonapi.CommandReply
		if err := args(req, &r); err != nil {
			return nil, err
		}
		if utf8.RuneCountInString(r.Text) > addonapi.MaxAnswer {
			return nil, addonapi.Errorf(addonapi.CodeTooLarge, "an answer is at most %d characters", addonapi.MaxAnswer)
		}
		if !s.answer(c.a, r.Call, false, r) {
			return nil, bad("no command is waiting for call %d", r.Call)
		}
		return nil, nil

	case addonapi.OpSettingsReply:
		var r addonapi.SettingsReply
		if err := args(req, &r); err != nil {
			return nil, err
		}
		if utf8.RuneCountInString(r.Text) > addonapi.MaxTestAnswer {
			return nil, addonapi.Errorf(addonapi.CodeTooLarge, "a test's answer is at most %d characters", addonapi.MaxTestAnswer)
		}
		if !s.answer(c.a, r.Call, true, addonapi.CommandReply{Call: r.Call, OK: r.OK, Text: r.Text}) {
			return nil, bad("no test is waiting for call %d", r.Call)
		}
		return nil, nil

	case addonapi.OpEventFire:
		var f addonapi.Fire
		if err := args(req, &f); err != nil {
			return nil, err
		}
		if !addonapi.ValidName(f.Name) {
			return nil, bad("an event's name is lowercase letters, digits and underscores, starting with a letter")
		}
		if b, _ := json.Marshal(f.Data); len(b) > addonapi.MaxEventData {
			return nil, addonapi.Errorf(addonapi.CodeTooLarge, "an event's data is at most %d bytes", addonapi.MaxEventData)
		}
		s.mu.Lock()
		ok := c.a.events.take(s.Now())
		s.mu.Unlock()
		if !ok {
			s.strike(c.a)
			return nil, addonapi.Errorf(addonapi.CodeRateLimited, "at most %d events a second", addonapi.EventsPerSecond)
		}
		if !s.Device.Connected() {
			return nil, addonapi.Errorf(addonapi.CodeUnavailable, "Home Assistant is not connected")
		}
		return nil, s.Device.Fire(g.ID, f.Name, f.Data)

	case addonapi.OpSpeak:
		var sp addonapi.Speak
		if err := args(req, &sp); err != nil {
			return nil, err
		}
		return s.speak(c.a, sp.Text)

	case addonapi.OpSpeakCancel:
		s.mu.Lock()
		n := len(c.a.speech)
		c.a.speech = nil
		s.mu.Unlock()
		return addonapi.Dropped{Dropped: n}, nil

	case addonapi.OpSoundPlay:
		var so addonapi.Sound
		if err := args(req, &so); err != nil {
			return nil, err
		}
		return nil, s.sound(c.a, so)

	case addonapi.OpTimerStart:
		var t addonapi.TimerStart
		if err := args(req, &t); err != nil {
			return nil, err
		}
		d := time.Duration(t.Seconds * float64(time.Second))
		switch {
		case t.Seconds <= 0 || d > addonapi.MaxTimer:
			return nil, bad("a timer runs for more than nothing and at most %v", addonapi.MaxTimer)
		case utf8.RuneCountInString(t.Name) > addonapi.MaxLabel || strings.ContainsFunc(t.Name, control):
			return nil, bad("a timer's name is at most %d characters, without control characters", addonapi.MaxLabel)
		}
		if err := s.timerToken(c.a); err != nil {
			return nil, err
		}
		c.a.timerMu.Lock()
		defer c.a.timerMu.Unlock()
		if len(s.Device.Timers(g.ID)) >= addonapi.MaxTimers {
			return nil, addonapi.Errorf(addonapi.CodeBusy, "an add-on has at most %d timers at once", addonapi.MaxTimers)
		}
		id, err := s.Device.StartTimer(g.ID, strings.TrimSpace(t.Name), d)
		if err != nil {
			return nil, err
		}
		return addonapi.TimerRef{ID: id}, nil

	case addonapi.OpTimerCancel:
		var t addonapi.TimerRef
		if err := args(req, &t); err != nil {
			return nil, err
		}
		if err := s.timerToken(c.a); err != nil {
			return nil, err
		}
		if !s.Device.CancelTimer(g.ID, t.ID) {
			return nil, bad("the add-on has no timer %q", t.ID)
		}
		return nil, nil

	case addonapi.OpTimerList:
		if err := args(req, &addonapi.None{}); err != nil {
			return nil, err
		}
		list := s.Device.Timers(g.ID)
		if list == nil {
			list = []addonapi.Timer{}
		}
		return addonapi.Timers{Timers: list}, nil

	case addonapi.OpAwakeHold, addonapi.OpAwakeRenew:
		var h addonapi.AwakeHold
		if err := args(req, &h); err != nil {
			return nil, err
		}
		return s.hold(c, req.Op == addonapi.OpAwakeRenew, h)

	case addonapi.OpAwakeRelease:
		var r addonapi.AwakeRelease
		if err := args(req, &r); err != nil {
			return nil, err
		}
		if !s.release(c.a, r.Handle) {
			return nil, bad("the add-on holds no %q", r.Handle)
		}
		return nil, nil

	case addonapi.OpSettingsGet:
		if err := args(req, &addonapi.None{}); err != nil {
			return nil, err
		}
		return addonapi.Values{Values: s.Device.Settings(g.ID)}, nil

	case addonapi.OpEntitySet:
		var e addonapi.EntitySet
		if err := args(req, &e); err != nil {
			return nil, err
		}
		ent, ok := addonapi.FindEntity(g.Entities, e.ID)
		if !ok {
			return nil, bad("the add-on declares no entity %q", e.ID)
		}
		if err := ent.CheckState(e.State); err != nil {
			return nil, bad("%v", err)
		}
		// A state it already has is not sent on, and costs nothing of the add-on's limits.
		state := string(mustJSON(e.State))
		s.mu.Lock()
		if c.a.shown[e.ID] == state {
			s.mu.Unlock()
			return nil, nil
		}
		b := c.a.sets[e.ID]
		if b == nil {
			b = &bucket{rate: addonapi.EntitySetsPerSecond, burst: addonapi.EntitySetsPerSecond}
			c.a.sets[e.ID] = b
		}
		now := s.Now()
		one := b.take(now)
		all := one && c.a.states.take(now)
		s.mu.Unlock()
		if !all {
			s.strike(c.a)
			return nil, addonapi.Errorf(addonapi.CodeRateLimited, "an entity is set at most %d times a second, and all of them together %d",
				addonapi.EntitySetsPerSecond, addonapi.StatesPerSecond)
		}
		if err := s.Device.SetEntity(g.ID, ent, e.State); err != nil {
			return nil, err
		}
		s.mu.Lock()
		c.a.shown[e.ID] = state
		s.mu.Unlock()
		return nil, nil

	case addonapi.OpMediaPlay:
		var m addonapi.MediaPlay
		if err := args(req, &m); err != nil {
			return nil, err
		}
		u, err := url.Parse(m.URL)
		switch {
		case m.Kind != addonapi.MediaVideo && m.Kind != addonapi.MediaAudio:
			return nil, bad("media is a %q or an %q", addonapi.MediaVideo, addonapi.MediaAudio)
		case len(m.URL) > addonapi.MaxURL || err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "":
			return nil, bad("media plays from an http or https address of at most %d characters", addonapi.MaxURL)
		case utf8.RuneCountInString(m.Title) > addonapi.MaxTitle || strings.ContainsFunc(m.Title, control):
			return nil, bad("a title is at most %d characters", addonapi.MaxTitle)
		case s.Device.Quiet():
			return nil, addonapi.Errorf(addonapi.CodeBusy, "it is quiet hours on this device")
		}
		s.mu.Lock()
		ok := c.a.plays.take(s.Now())
		s.mu.Unlock()
		if !ok {
			s.strike(c.a)
			return nil, addonapi.Errorf(addonapi.CodeRateLimited, "media.play at most once every %v", addonapi.MediaPlayEvery)
		}
		return nil, s.Device.Media(g.ID, req.Op, m)

	case addonapi.OpMediaPause, addonapi.OpMediaResume, addonapi.OpMediaStop:
		if err := args(req, &addonapi.None{}); err != nil {
			return nil, err
		}
		// Carrying on is making a sound again, which quiet hours do not allow; pausing and stopping are
		// not.
		if req.Op == addonapi.OpMediaResume && s.Device.Quiet() {
			return nil, addonapi.Errorf(addonapi.CodeBusy, "it is quiet hours on this device")
		}
		return nil, s.Device.Media(g.ID, req.Op, addonapi.MediaPlay{})
	}
	return nil, addonapi.Errorf(addonapi.CodeUnknownOp, "there is no op %q at API level %d", req.Op, addonapi.Version)
}

func control(r rune) bool { return r < 0x20 || r == 0x7f }

// speak queues words to be said, and starts saying them if nothing of the add-on's is being said.
func (s *Server) speak(a *addon, text string) (any, error) {
	text = strings.TrimSpace(text)
	switch {
	case text == "":
		return nil, bad("speak says something")
	case utf8.RuneCountInString(text) > addonapi.MaxSpeak:
		return nil, addonapi.Errorf(addonapi.CodeTooLarge, "speak says at most %d characters", addonapi.MaxSpeak)
	case s.Device.Quiet():
		return nil, addonapi.Errorf(addonapi.CodeBusy, "it is quiet hours on this device")
	case !s.Device.Connected():
		return nil, addonapi.Errorf(addonapi.CodeUnavailable, "Home Assistant is not connected, and it is what speaks")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(a.speech) >= addonapi.MaxSpeakQueue {
		return nil, addonapi.Errorf(addonapi.CodeBusy, "%d of the add-on's are waiting to be said already", addonapi.MaxSpeakQueue)
	}
	a.speech = append(a.speech, text)
	n := len(a.speech)
	if a.speaking {
		n++ // the one being said
	} else {
		a.speaking = true
		go s.sayAll(a)
	}
	return addonapi.Queued{Queued: n}, nil
}

// sayAll says the add-on's words one after the other, until none are left or it stops.
func (s *Server) sayAll(a *addon) {
	for {
		s.mu.Lock()
		if len(a.speech) == 0 || a.ctx.Err() != nil {
			a.speaking = false
			s.mu.Unlock()
			return
		}
		text := a.speech[0]
		a.speech = a.speech[1:]
		s.mu.Unlock()
		// Quiet hours may have begun while it waited.
		if s.Device.Quiet() {
			continue
		}
		if err := s.Device.Say(a.ctx, a.grant.ID, text); err != nil && a.ctx.Err() == nil {
			s.warn(a.grant.ID, "unsaid", "addonapi: words of an add-on's were not said", "err", err)
		}
	}
}

// sound plays one of the fixed sounds, or a WAV of the add-on's own files.
func (s *Server) sound(a *addon, so addonapi.Sound) error {
	g := a.grant
	if (so.Name == "") == (so.File == "") {
		return bad("sound.play names one of %s, or one of the add-on's files", strings.Join(addonapi.Sounds, ", "))
	}
	if s.Device.Quiet() {
		return addonapi.Errorf(addonapi.CodeBusy, "it is quiet hours on this device")
	}
	var wav *addonapi.WAV
	if so.Name != "" && !slices.Contains(addonapi.Sounds, so.Name) {
		return bad("there is no sound %q; there are %s", so.Name, strings.Join(addonapi.Sounds, ", "))
	}
	if so.File != "" {
		// Only a name its addon.json lists, which the catalog pinned and which is in its bin folder: a
		// path, or a file it wrote itself, is never read.
		if !slices.Contains(g.Files, so.File) || !strings.HasSuffix(so.File, ".wav") || filepath.Base(so.File) != so.File {
			return bad("%q is not a .wav among the add-on's extra files", so.File)
		}
		w, err := readWAV(filepath.Join(g.BinDir, so.File))
		if err != nil {
			return bad("%v", err)
		}
		wav = &w
	}
	now := s.Now()
	s.mu.Lock()
	tooSoon := now.Sub(a.soundAt) < time.Second/addonapi.SoundsPerSecond
	if !tooSoon {
		a.soundAt = now
	}
	s.mu.Unlock()
	if tooSoon {
		s.strike(a)
		return addonapi.Errorf(addonapi.CodeRateLimited, "at most %d sound a second", addonapi.SoundsPerSecond)
	}
	return s.Device.PlaySound(g.ID, so.Name, wav)
}

// readWAV reads a sound, never more of it than a sound may be.
func readWAV(path string) (addonapi.WAV, error) {
	f, err := os.Open(path)
	if err != nil {
		return addonapi.WAV{}, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, addonapi.MaxWAV+1))
	if err != nil {
		return addonapi.WAV{}, err
	}
	return addonapi.ParseWAV(b)
}

// hold is one keep-awake hold: its connection, and the timer that ends it.
type hold struct {
	conn  *conn
	until time.Time
	timer *time.Timer
	gen   int // renewals, so a timer a renewal replaced does nothing
}

// expireAfter ends the hold after d, unless it was renewed since.
func (s *Server) expireAfter(a *addon, handle string, gen int, d time.Duration) *time.Timer {
	return time.AfterFunc(d, func() {
		s.mu.Lock()
		ho, ok := a.holds[handle]
		current := ok && ho.gen == gen
		s.mu.Unlock()
		if current {
			s.release(a, handle)
		}
	})
}

// hold takes a new keep-awake hold, or renews one of the add-on's.
func (s *Server) hold(c *conn, renew bool, h addonapi.AwakeHold) (any, error) {
	d := time.Duration(h.Seconds * float64(time.Second))
	if h.Seconds <= 0 || d > addonapi.MaxAwake {
		return nil, bad("a hold lasts more than nothing and at most %v", addonapi.MaxAwake)
	}
	if !renew && h.Handle != "" {
		return nil, bad("awake.hold makes a handle of its own; awake.renew takes one")
	}
	a := c.a
	until := s.Now().Add(d)
	s.mu.Lock()
	// A connection closing now releases its holds under this lock (releaseConn), after it has marked
	// itself gone: one taken here once it is gone would be released by nobody, so none is.
	select {
	case <-c.gone:
		s.mu.Unlock()
		return nil, addonapi.Errorf(addonapi.CodeUnavailable, "the connection has closed")
	default:
	}
	if a.ctx.Err() != nil {
		s.mu.Unlock()
		return nil, addonapi.Errorf(addonapi.CodeUnavailable, "the add-on has stopped")
	}
	if renew {
		ho, ok := a.holds[h.Handle]
		if !ok {
			s.mu.Unlock()
			return nil, bad("the add-on holds no %q", h.Handle)
		}
		// A new timer, not a Reset: the old one may be firing now, and its expire then finds a newer
		// generation and leaves the hold alone.
		ho.timer.Stop()
		ho.gen++
		ho.until = until
		ho.timer = s.expireAfter(a, h.Handle, ho.gen, d)
		s.mu.Unlock()
		return addonapi.Awake{Handle: h.Handle, Until: until}, nil
	}
	if len(a.holds) >= addonapi.MaxAwakeHolds {
		s.mu.Unlock()
		return nil, addonapi.Errorf(addonapi.CodeBusy, "an add-on has at most %d holds at once", addonapi.MaxAwakeHolds)
	}
	var raw [8]byte
	_, _ = rand.Read(raw[:])
	handle := hex.EncodeToString(raw[:])
	ho := &hold{conn: c, until: until}
	ho.timer = s.expireAfter(a, handle, 0, d)
	a.holds[handle] = ho
	s.holds++
	first := s.holds == 1
	s.mu.Unlock()
	if first {
		s.Device.SetAwake(true)
	}
	return addonapi.Awake{Handle: handle, Until: until}, nil
}

// release ends one hold of the add-on's, and says whether there was one.
func (s *Server) release(a *addon, handle string) bool {
	s.mu.Lock()
	ho, ok := a.holds[handle]
	if ok {
		ho.timer.Stop()
		delete(a.holds, handle)
		s.holds--
	}
	last := ok && s.holds == 0
	s.mu.Unlock()
	if last {
		s.Device.SetAwake(false)
	}
	return ok
}

// releaseConn ends every hold a connection took: one that drops leaves nothing on.
func (s *Server) releaseConn(c *conn) {
	s.mu.Lock()
	var mine []string
	for h, ho := range c.a.holds {
		if ho.conn == c {
			mine = append(mine, h)
		}
	}
	s.mu.Unlock()
	for _, h := range mine {
		s.release(c.a, h)
	}
}

// releaseAll ends every hold of an add-on's, and fails every call waiting on it.
func (s *Server) releaseAll(a *addon) {
	s.mu.Lock()
	var all []string
	for h := range a.holds {
		all = append(all, h)
	}
	calls := a.calls
	a.calls = map[uint64]*pending{}
	s.mu.Unlock()
	for _, h := range all {
		s.release(a, h)
	}
	for _, p := range calls {
		close(p.ch)
	}
}

// Awake is whether any add-on holds the screen awake now.
func (s *Server) Awake() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.holds > 0
}

// answer hands a reply to the call waiting for it, when it is a call of that kind: a command's reply
// cannot answer a settings test, which is held to a shorter answer, nor the other way round.
func (s *Server) answer(a *addon, call uint64, test bool, r addonapi.CommandReply) bool {
	s.mu.Lock()
	p, ok := a.calls[call]
	ok = ok && p.test == test
	if ok {
		delete(a.calls, call)
	}
	s.mu.Unlock()
	if ok {
		p.ch <- r
	}
	return ok
}

// timerToken takes one of the add-on's timer.start and timer.cancel tokens: each writes nothing to
// the flash now, but a timer that rings is a sound and a line on the screen, and churning them is
// never what an add-on means to do.
func (s *Server) timerToken(a *addon) error {
	s.mu.Lock()
	ok := a.timerOp.take(s.Now())
	s.mu.Unlock()
	if !ok {
		s.strike(a)
		return addonapi.Errorf(addonapi.CodeRateLimited, "timers are started and canceled %d at once, then one every %v",
			addonapi.TimerOpBurst, addonapi.TimerOpEvery)
	}
	return nil
}

// The daemon's half: what Home Assistant, the setup page and the device send an add-on.

// errNotRunning is an add-on that is not connected to hear it.
func errNotRunning(id string) error {
	return addonapi.Errorf(addonapi.CodeUnavailable, "the add-on %s is not running, or not connected to the device", id)
}

// first is the add-on and its first connection that said hello, for something that goes to one of them.
func (s *Server) first(id string) (*addon, *conn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.addons[id]
	if a == nil {
		return nil, nil
	}
	var best *conn
	for c := range a.conns {
		if c.greeted() && (best == nil || c.id < best.id) {
			best = c
		}
	}
	return a, best
}

// ask sends an event to the add-on's first connection and waits for its reply, as a command or a test
// does, until ctx ends.
func (s *Server) ask(ctx context.Context, id, event string, test bool, data func(call uint64) any) (addonapi.CommandReply, error) {
	a, c := s.first(id)
	if c == nil {
		return addonapi.CommandReply{}, errNotRunning(id)
	}
	ch := make(chan addonapi.CommandReply, 1)
	s.mu.Lock()
	a.nextCall++
	call := a.nextCall
	a.calls[call] = &pending{ch: ch, test: test}
	s.mu.Unlock()
	c.send(addonapi.Event{Event: event, Data: mustJSON(data(call))})
	select {
	case r, ok := <-ch:
		if !ok {
			return r, errNotRunning(id)
		}
		return r, nil
	case <-ctx.Done():
		s.mu.Lock()
		delete(a.calls, call)
		s.mu.Unlock()
		return addonapi.CommandReply{}, addonapi.Errorf(addonapi.CodeUnavailable, "the add-on %s did not answer in time", id)
	}
}

func mustJSON(v any) json.RawMessage {
	b, err := addonapi.Marshal(v)
	if err != nil {
		return nil
	}
	return b
}

// Command calls one of an add-on's commands, checked against its declaration. With wait it waits,
// until ctx ends, for the add-on's answer, which it returns; without, it returns once the command is
// sent. from is "action" or "button".
func (s *Server) Command(ctx context.Context, id, name string, args map[string]any, from string, wait bool) (string, error) {
	g, ok := s.Grant(id)
	if !ok {
		return "", errNotRunning(id)
	}
	if !g.Has(addonapi.CapCommands) {
		return "", addonapi.Errorf(addonapi.CodeDenied, "the add-on %s has no commands", id)
	}
	cmd, ok := addonapi.FindCommand(g.Commands, name)
	if !ok {
		return "", bad("the add-on %s has no command %q", id, name)
	}
	if err := cmd.CheckArgs(args); err != nil {
		return "", bad("%v", err)
	}
	if !wait {
		_, c := s.first(id)
		if c == nil {
			return "", errNotRunning(id)
		}
		c.send(addonapi.Event{Event: addonapi.EventCommand, Data: mustJSON(addonapi.CommandEvent{Command: name, Args: args, From: from})})
		return "", nil
	}
	r, err := s.ask(ctx, id, addonapi.EventCommand, false, func(call uint64) any {
		return addonapi.CommandEvent{Call: call, Command: name, Args: args, From: from}
	})
	if err != nil {
		return "", err
	}
	if !r.OK {
		why := r.Text
		if why == "" {
			why = "it did not work"
		}
		return "", fmt.Errorf("%s: %s", id, why)
	}
	return r.Text, nil
}

// Test asks an add-on whether the settings in the form work: ok, or why not.
func (s *Server) Test(ctx context.Context, id string, values map[string]any) (bool, string, error) {
	g, ok := s.Grant(id)
	if !ok {
		return false, "", errNotRunning(id)
	}
	if !g.Has(addonapi.CapSettings) || g.Settings == nil || !g.Settings.Test {
		return false, "", addonapi.Errorf(addonapi.CodeDenied, "the add-on %s has no settings test", id)
	}
	r, err := s.ask(ctx, id, addonapi.EventSettingsTest, true, func(call uint64) any {
		return addonapi.SettingsTest{Call: call, Values: values}
	})
	if err != nil {
		return false, "", err
	}
	return r.OK, r.Text, nil
}

// Emit sends an event to every connection of the add-on that has said hello.
func (s *Server) Emit(id, event string, data any) {
	s.mu.Lock()
	a := s.addons[id]
	var conns []*conn
	if a != nil {
		for c := range a.conns {
			if c.greeted() {
				conns = append(conns, c)
			}
		}
	}
	s.mu.Unlock()
	if len(conns) == 0 {
		return
	}
	ev := addonapi.Event{Event: event, Data: mustJSON(data)}
	for _, c := range conns {
		c.send(ev)
	}
}
