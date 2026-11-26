package hass

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"
)

// Live is a websocket connection to Home Assistant kept open while something needs it: commands,
// service calls, and entities followed as they change. A dashboard on the screen is what it is for:
// the ESPHome link can follow entities too, but only a list fixed for the whole connection.
type Live struct {
	s *wsSession

	mu      sync.Mutex
	next    int
	waiting map[int]chan liveResult
	subs    map[int]func(json.RawMessage)
	err     error
	done    chan struct{}
	writeMu sync.Mutex
}

type liveResult struct {
	raw json.RawMessage
	err error
}

// LiveEntity is one entity's state and attributes, as Home Assistant has them now.
type LiveEntity struct {
	ID    string
	State string
	Attrs map[string]any
}

// OpenLive connects. It is closed with Close, or when ctx ends.
func (c *Client) OpenLive(ctx context.Context) (*Live, error) {
	s, err := c.wsOpen(ctx)
	if err != nil {
		return nil, err
	}
	// Reads wait for as long as nothing happens: a quiet house sends nothing for minutes.
	_ = s.conn.SetReadDeadline(time.Time{})
	_ = s.conn.SetWriteDeadline(time.Time{})
	l := &Live{s: s, waiting: map[int]chan liveResult{}, subs: map[int]func(json.RawMessage){}, done: make(chan struct{})}
	go l.read()
	go func() {
		select {
		case <-ctx.Done():
			l.Close()
		case <-l.done:
		}
	}()
	return l, nil
}

// Close ends the connection.
func (l *Live) Close() { l.s.conn.Close() }

// Done is closed once the connection has ended, for whatever reason; Err says which.
func (l *Live) Done() <-chan struct{} { return l.done }

func (l *Live) Err() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.err
}

func (l *Live) read() {
	defer close(l.done)
	for {
		var msg struct {
			ID      int             `json:"id"`
			Type    string          `json:"type"`
			Success bool            `json:"success"`
			Result  json.RawMessage `json:"result"`
			Event   json.RawMessage `json:"event"`
			Error   struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := l.s.conn.ReadJSON(&msg); err != nil {
			l.mu.Lock()
			l.err = err
			for id, ch := range l.waiting {
				ch <- liveResult{err: err}
				delete(l.waiting, id)
			}
			l.mu.Unlock()
			return
		}
		switch msg.Type {
		case "result":
			l.mu.Lock()
			ch, ok := l.waiting[msg.ID]
			delete(l.waiting, msg.ID)
			l.mu.Unlock()
			if !ok {
				continue
			}
			if !msg.Success {
				ch <- liveResult{err: fmt.Errorf("hass: %s %s", msg.Error.Code, msg.Error.Message)}
			} else {
				ch <- liveResult{raw: msg.Result}
			}
		case "event":
			l.mu.Lock()
			fn := l.subs[msg.ID]
			l.mu.Unlock()
			if fn != nil {
				fn(msg.Event)
			}
		}
	}
}

// send writes a command with the next id, noting who waits for its result.
func (l *Live) send(cmd map[string]any, sub func(json.RawMessage)) (int, chan liveResult, error) {
	// Home Assistant refuses an id lower than one it has seen ("id_reuse"), so the id is given and the
	// command written in one go: two sends at once, a slider's volume and a tap, went out the wrong way
	// round and the second was refused.
	l.writeMu.Lock()
	defer l.writeMu.Unlock()
	l.mu.Lock()
	if l.err != nil {
		err := l.err
		l.mu.Unlock()
		return 0, nil, err
	}
	l.next++
	id := l.next
	ch := make(chan liveResult, 1)
	l.waiting[id] = ch
	if sub != nil {
		l.subs[id] = sub
	}
	l.mu.Unlock()

	cmd["id"] = id
	_ = l.s.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	err := l.s.conn.WriteJSON(cmd)
	if err != nil {
		l.mu.Lock()
		delete(l.waiting, id)
		delete(l.subs, id)
		l.mu.Unlock()
		return 0, nil, err
	}
	return id, ch, nil
}

// Call sends a command and waits for its result.
func (l *Live) Call(ctx context.Context, cmd map[string]any) (json.RawMessage, error) {
	_, ch, err := l.send(cmd, nil)
	if err != nil {
		return nil, err
	}
	select {
	case r := <-ch:
		return r.raw, r.err
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-l.done:
		return nil, errors.New("hass: the connection ended")
	}
}

// CallService runs a service, as a tile's tap does: light.toggle on an entity, say.
func (l *Live) CallService(ctx context.Context, domain, service string, data map[string]any) error {
	_, err := l.Call(ctx, map[string]any{"type": "call_service", "domain": domain, "service": service, "service_data": data})
	return err
}

// firstStatesWait is how long a subscription's first event, the one with every state, is waited for
// after its result, before the states are read another way. Home Assistant sends it straight after
// the result; a session whose first event never came drew every entity as unknown for as long as it
// lasted, and healed only for those whose state changed (#740).
var firstStatesWait = 5 * time.Second

// FollowEntities follows entities as they change. changed is called with each one as it arrives
// and whenever it changes, whole, and removed with any that go away; on the connection's reader, or
// for states read after a first event that never came, on another goroutine: they must not block,
// and changed must be safe to call from both. Home Assistant sends the full state once and after that
// only what differs, which is put together here.
func (l *Live) FollowEntities(ctx context.Context, ids []string, changed func(LiveEntity), removed func(string)) error {
	var mu sync.Mutex
	have := map[string]*LiveEntity{}
	first := make(chan struct{})
	var firstOnce sync.Once
	handle := func(raw json.RawMessage) {
		// Each entity is read on its own: one entity with something in it that can't be read here
		// dropped the whole event, and with it, for the first event, every other entity's state.
		var ev struct {
			Added   map[string]json.RawMessage `json:"a"`
			Changed map[string]json.RawMessage `json:"c"`
			Removed []string                   `json:"r"`
		}
		if err := json.Unmarshal(raw, &ev); err != nil {
			slog.Warn("hass: an entity event could not be read", "err", err, "bytes", len(raw))
			return
		}
		var out []LiveEntity
		var bad []string
		mu.Lock()
		for id, r := range ev.Added {
			var a struct {
				S string         `json:"s"`
				A map[string]any `json:"a"`
			}
			if err := json.Unmarshal(r, &a); err != nil {
				bad = append(bad, id)
				continue
			}
			e := &LiveEntity{ID: id, State: a.S, Attrs: a.A}
			if e.Attrs == nil {
				e.Attrs = map[string]any{}
			}
			have[id] = e
			out = append(out, copyEntity(e))
		}
		for id, r := range ev.Changed {
			var c struct {
				Plus *struct {
					S *string        `json:"s"`
					A map[string]any `json:"a"`
				} `json:"+"`
				Minus *struct {
					A []string `json:"a"`
				} `json:"-"`
			}
			if err := json.Unmarshal(r, &c); err != nil {
				bad = append(bad, id)
				continue
			}
			e := have[id]
			if e == nil {
				e = &LiveEntity{ID: id, Attrs: map[string]any{}}
				have[id] = e
			}
			if c.Plus != nil {
				if c.Plus.S != nil {
					e.State = *c.Plus.S
				}
				for k, v := range c.Plus.A {
					e.Attrs[k] = v
				}
			}
			if c.Minus != nil {
				for _, k := range c.Minus.A {
					delete(e.Attrs, k)
				}
			}
			out = append(out, copyEntity(e))
		}
		for _, id := range ev.Removed {
			delete(have, id)
		}
		mu.Unlock()
		if len(bad) > 0 {
			slog.Warn("hass: entities in an event could not be read", "entities", bad)
		}
		firstOnce.Do(func() {
			slog.Info("hass: first states of followed entities", "asked", len(ids), "got", len(ev.Added))
			close(first)
		})
		for _, e := range out {
			changed(e)
		}
		for _, id := range ev.Removed {
			removed(id)
		}
	}
	// The handler is in place before the command goes, since the first states follow its result
	// at once.
	_, ch, err := l.send(map[string]any{"type": "subscribe_entities", "entity_ids": ids}, handle)
	if err != nil {
		return err
	}
	select {
	case r := <-ch:
		if r.err != nil {
			return r.err
		}
	case <-ctx.Done():
		return ctx.Err()
	case <-l.done:
		return errors.New("hass: the connection ended")
	}
	go l.firstStatesOrRead(ctx, first, firstStatesWait, ids, func(list []LiveEntity) {
		// Only what no event has brought in the meantime, since what an event brought is newer. changed
		// is called with mu held, so a change taken after this is passed on after it.
		mu.Lock()
		defer mu.Unlock()
		for _, e := range list {
			if have[e.ID] != nil {
				continue
			}
			have[e.ID] = &e
			changed(copyEntity(&e))
		}
	})
	return nil
}

// firstStatesOrRead waits for a subscription's first event, and if it has not come after
// wait, reads the states of ids once and gives them to fill. ctx is the one FollowEntities was given:
// a follower that gave up in the meantime (a page closed as it opened) has nobody left to fill, and
// reading every state in the house for it would be work for nothing, with fill run after it has gone.
func (l *Live) firstStatesOrRead(ctx context.Context, first <-chan struct{}, wait time.Duration, ids []string, fill func([]LiveEntity)) {
	select {
	case <-first:
		return
	case <-l.done:
		return
	case <-ctx.Done():
		return
	case <-time.After(wait):
	}
	slog.Warn("hass: no first states came for the followed entities; reading them", "entities", len(ids))
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	raw, err := l.Call(ctx, map[string]any{"type": "get_states"})
	if err != nil {
		slog.Warn("hass: reading the followed entities' states", "err", err)
		return
	}
	var all []json.RawMessage
	if err := json.Unmarshal(raw, &all); err != nil {
		slog.Warn("hass: reading the followed entities' states", "err", err)
		return
	}
	want := make(map[string]bool, len(ids))
	for _, id := range ids {
		want[id] = true
	}
	var list []LiveEntity
	for _, r := range all {
		var st struct {
			ID    string         `json:"entity_id"`
			State string         `json:"state"`
			Attrs map[string]any `json:"attributes"`
		}
		if json.Unmarshal(r, &st) != nil || !want[st.ID] {
			continue
		}
		if st.Attrs == nil {
			st.Attrs = map[string]any{}
		}
		list = append(list, LiveEntity{ID: st.ID, State: st.State, Attrs: st.Attrs})
	}
	slog.Info("hass: read the followed entities' states", "asked", len(ids), "got", len(list))
	fill(list)
}

func copyEntity(e *LiveEntity) LiveEntity {
	a := make(map[string]any, len(e.Attrs))
	for k, v := range e.Attrs {
		a[k] = v
	}
	return LiveEntity{ID: e.ID, State: e.State, Attrs: a}
}

// RenderTemplate has Home Assistant render a template, and render it again whenever anything it
// reads changes: got is called with each result, on the connection's reader, so it must not block.
func (l *Live) RenderTemplate(ctx context.Context, text string, vars map[string]any, got func(string)) error {
	handle := func(raw json.RawMessage) {
		var ev struct {
			Result any `json:"result"`
		}
		if json.Unmarshal(raw, &ev) != nil || ev.Result == nil {
			return
		}
		switch v := ev.Result.(type) {
		case string:
			got(v)
		case float64:
			got(strconv.FormatFloat(v, 'f', -1, 64))
		default:
			b, _ := json.Marshal(v)
			got(string(b))
		}
	}
	cmd := map[string]any{"type": "render_template", "template": text}
	if len(vars) > 0 {
		cmd["variables"] = vars
	}
	_, ch, err := l.send(cmd, handle)
	if err != nil {
		return err
	}
	select {
	case r := <-ch:
		return r.err
	case <-ctx.Done():
		return ctx.Err()
	case <-l.done:
		return errors.New("hass: the connection ended")
	}
}

// SubscribeEvents calls got with the data of each event of a type fired on Home Assistant's bus, on
// the connection's reader, so it must not block.
func (l *Live) SubscribeEvents(ctx context.Context, eventType string, got func(data map[string]any)) error {
	handle := func(raw json.RawMessage) {
		var ev struct {
			Data map[string]any `json:"data"`
		}
		if json.Unmarshal(raw, &ev) == nil {
			got(ev.Data)
		}
	}
	_, ch, err := l.send(map[string]any{"type": "subscribe_events", "event_type": eventType}, handle)
	if err != nil {
		return err
	}
	select {
	case r := <-ch:
		return r.err
	case <-ctx.Done():
		return ctx.Err()
	case <-l.done:
		return errors.New("hass: the connection ended")
	}
}
