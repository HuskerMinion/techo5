package hass

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	l.writeMu.Lock()
	_ = l.s.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	err := l.s.conn.WriteJSON(cmd)
	l.writeMu.Unlock()
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

// FollowEntities follows entities as they change. changed is called with each one as it arrives
// and whenever it changes, whole, and removed with any that go away; both on the connection's
// reader, so they must not block. Home Assistant sends the full state once and after that only
// what differs, which is put together here.
func (l *Live) FollowEntities(ctx context.Context, ids []string, changed func(LiveEntity), removed func(string)) error {
	var mu sync.Mutex
	have := map[string]*LiveEntity{}
	handle := func(raw json.RawMessage) {
		var ev struct {
			Added map[string]struct {
				S string         `json:"s"`
				A map[string]any `json:"a"`
			} `json:"a"`
			Changed map[string]struct {
				Plus *struct {
					S *string        `json:"s"`
					A map[string]any `json:"a"`
				} `json:"+"`
				Minus *struct {
					A []string `json:"a"`
				} `json:"-"`
			} `json:"c"`
			Removed []string `json:"r"`
		}
		if json.Unmarshal(raw, &ev) != nil {
			return
		}
		var out []LiveEntity
		mu.Lock()
		for id, a := range ev.Added {
			e := &LiveEntity{ID: id, State: a.S, Attrs: a.A}
			if e.Attrs == nil {
				e.Attrs = map[string]any{}
			}
			have[id] = e
			out = append(out, copyEntity(e))
		}
		for id, c := range ev.Changed {
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
		return r.err
	case <-ctx.Done():
		return ctx.Err()
	case <-l.done:
		return errors.New("hass: the connection ended")
	}
}

func copyEntity(e *LiveEntity) LiveEntity {
	a := make(map[string]any, len(e.Attrs))
	for k, v := range e.Attrs {
		a[k] = v
	}
	return LiveEntity{ID: e.ID, State: e.State, Attrs: a}
}
