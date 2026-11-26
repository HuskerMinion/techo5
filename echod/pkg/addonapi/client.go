package addonapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"time"
)

// Client is an add-on's connection to the daemon. It is safe for use from several goroutines: requests
// may be in flight at once, and events arrive on Events in the order they were sent.
type Client struct {
	conn    net.Conn
	welcome Welcome

	wmu sync.Mutex // one frame written at a time

	mu      sync.Mutex
	next    uint64
	pending map[uint64]chan Response
	err     error // why the connection ended, once it has

	events chan Event
	done   chan struct{}
}

// ErrClosed is a request on a connection that has ended.
var ErrClosed = errors.New("addonapi: the connection to the daemon has ended")

// Dial connects to the daemon at path, or at TECHO5_ADDON_API when path is empty, and says hello.
func Dial(ctx context.Context, path string) (*Client, error) {
	if path == "" {
		path = os.Getenv(EnvSocket)
	}
	if path == "" {
		return nil, fmt.Errorf("addonapi: %s is not set: the add-on was not started with the API (its addon.json says \"api\": 1?)", EnvSocket)
	}
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", path)
	if err != nil {
		return nil, err
	}
	c := &Client{conn: conn, pending: map[uint64]chan Response{}, events: make(chan Event, 64), done: make(chan struct{})}
	go c.read()
	if err := c.Call(ctx, OpHello, Hello{API: Version}, &c.welcome); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

// Welcome is what the daemon answered to hello: its API level, the capabilities granted, the device,
// and the add-on's settings.
func (c *Client) Welcome() Welcome { return c.welcome }

// Events are what the daemon sends of its own accord. The channel is closed when the connection ends.
// An add-on that does not read them is disconnected once the daemon's queue for it is full.
func (c *Client) Events() <-chan Event { return c.events }

// Done is closed when the connection has ended; Err then says why.
func (c *Client) Done() <-chan struct{} { return c.done }

// Err is why the connection ended, or nil while it is up.
func (c *Client) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

// Close ends the connection.
func (c *Client) Close() error { return c.conn.Close() }

// frame is anything the daemon sends: a response has an id, an event a name.
type frame struct {
	ID     uint64          `json:"id"`
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result"`
	Error  *Error          `json:"error"`
	Event  string          `json:"event"`
	Data   json.RawMessage `json:"data"`
}

func (c *Client) read() {
	var err error
	defer func() {
		c.mu.Lock()
		c.err = err
		for id, ch := range c.pending {
			close(ch)
			delete(c.pending, id)
		}
		c.mu.Unlock()
		close(c.events)
		close(c.done)
	}()
	for {
		var b []byte
		if b, _, err = ReadFrame(c.conn, MaxMessage); err != nil {
			return
		}
		var f frame
		if err = Unmarshal(b, &f); err != nil {
			err = fmt.Errorf("addonapi: the daemon sent what does not read: %w", err)
			return
		}
		if f.Event != "" {
			c.events <- Event{Event: f.Event, Data: f.Data}
			continue
		}
		c.mu.Lock()
		ch, ok := c.pending[f.ID]
		delete(c.pending, f.ID)
		c.mu.Unlock()
		if ok {
			ch <- Response{ID: f.ID, OK: f.OK, Result: f.Result, Error: f.Error}
		}
	}
}

// Call sends one request and waits for its answer, which is decoded into result when it is not nil.
// A refusal comes back as an *Error with its code (CodeOf).
func (c *Client) Call(ctx context.Context, op string, args, result any) error {
	var raw json.RawMessage
	if args != nil {
		b, err := Marshal(args)
		if err != nil {
			return err
		}
		raw = b
	}
	ch := make(chan Response, 1)
	c.mu.Lock()
	if c.err != nil {
		c.mu.Unlock()
		return ErrClosed
	}
	c.next++
	id := c.next
	c.pending[id] = ch
	c.mu.Unlock()

	b, err := Marshal(Request{ID: id, Op: op, Args: raw})
	if err != nil {
		return err
	}
	c.wmu.Lock()
	_ = c.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	err = WriteFrame(c.conn, b)
	c.wmu.Unlock()
	if err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return err
	}
	select {
	case r, ok := <-ch:
		if !ok {
			return ErrClosed
		}
		if !r.OK {
			if r.Error == nil {
				return Errorf(CodeInternal, "refused without a reason")
			}
			return r.Error
		}
		if result != nil && len(r.Result) > 0 {
			return json.Unmarshal(r.Result, result)
		}
		return nil
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return ctx.Err()
	}
}

// The ops, typed.

// ReplyCommand answers a command event that waits for one.
func (c *Client) ReplyCommand(ctx context.Context, call uint64, ok bool, text string) error {
	return c.Call(ctx, OpCommandReply, CommandReply{Call: call, OK: ok, Text: text}, nil)
}

// Fire puts esphome.techo5_addon on Home Assistant's bus.
func (c *Client) Fire(ctx context.Context, name string, data map[string]string) error {
	return c.Call(ctx, OpEventFire, Fire{Name: name, Data: data}, nil)
}

// Speak asks for words to be said on the device, and answers how many of the add-on's wait to be.
func (c *Client) Speak(ctx context.Context, text string) (int, error) {
	var q Queued
	err := c.Call(ctx, OpSpeak, Speak{Text: text}, &q)
	return q.Queued, err
}

// CancelSpeech drops the add-on's words not yet said.
func (c *Client) CancelSpeech(ctx context.Context) (int, error) {
	var d Dropped
	err := c.Call(ctx, OpSpeakCancel, None{}, &d)
	return d.Dropped, err
}

// PlaySound plays one of Sounds.
func (c *Client) PlaySound(ctx context.Context, name string) error {
	return c.Call(ctx, OpSoundPlay, Sound{Name: name}, nil)
}

// PlayFile plays a WAV among the add-on's extra files.
func (c *Client) PlayFile(ctx context.Context, file string) error {
	return c.Call(ctx, OpSoundPlay, Sound{File: file}, nil)
}

// StartTimer sets a timer on the device, and answers its id.
func (c *Client) StartTimer(ctx context.Context, name string, d time.Duration) (string, error) {
	var r TimerRef
	err := c.Call(ctx, OpTimerStart, TimerStart{Name: name, Seconds: d.Seconds()}, &r)
	return r.ID, err
}

// CancelTimer cancels one of the add-on's timers.
func (c *Client) CancelTimer(ctx context.Context, id string) error {
	return c.Call(ctx, OpTimerCancel, TimerRef{ID: id}, nil)
}

// Timers lists the add-on's timers.
func (c *Client) Timers(ctx context.Context) ([]Timer, error) {
	var t Timers
	err := c.Call(ctx, OpTimerList, None{}, &t)
	return t.Timers, err
}

// HoldAwake keeps the screen on for d, and answers the hold.
func (c *Client) HoldAwake(ctx context.Context, d time.Duration) (Awake, error) {
	var a Awake
	err := c.Call(ctx, OpAwakeHold, AwakeHold{Seconds: d.Seconds()}, &a)
	return a, err
}

// RenewAwake makes a hold last d from now.
func (c *Client) RenewAwake(ctx context.Context, handle string, d time.Duration) (Awake, error) {
	var a Awake
	err := c.Call(ctx, OpAwakeRenew, AwakeHold{Handle: handle, Seconds: d.Seconds()}, &a)
	return a, err
}

// ReleaseAwake ends a hold.
func (c *Client) ReleaseAwake(ctx context.Context, handle string) error {
	return c.Call(ctx, OpAwakeRelease, AwakeRelease{Handle: handle}, nil)
}

// Settings is the add-on's settings as they are now.
func (c *Client) Settings(ctx context.Context) (map[string]any, error) {
	var v Values
	err := c.Call(ctx, OpSettingsGet, None{}, &v)
	return v.Values, err
}

// ReplyTest answers a settings.test event.
func (c *Client) ReplyTest(ctx context.Context, call uint64, ok bool, text string) error {
	return c.Call(ctx, OpSettingsReply, SettingsReply{Call: call, OK: ok, Text: text}, nil)
}

// SetEntity sets the state of one of the add-on's entities.
func (c *Client) SetEntity(ctx context.Context, id string, state any) error {
	return c.Call(ctx, OpEntitySet, EntitySet{ID: id, State: state}, nil)
}

// PlayMedia plays a video (MediaVideo) or audio (MediaAudio) from an http(s) address.
func (c *Client) PlayMedia(ctx context.Context, kind, url, title string) error {
	return c.Call(ctx, OpMediaPlay, MediaPlay{URL: url, Kind: kind, Title: title}, nil)
}

// PauseMedia, ResumeMedia and StopMedia act on what the add-on is playing, and on nothing else.
func (c *Client) PauseMedia(ctx context.Context) error {
	return c.Call(ctx, OpMediaPause, None{}, nil)
}

func (c *Client) ResumeMedia(ctx context.Context) error {
	return c.Call(ctx, OpMediaResume, None{}, nil)
}

func (c *Client) StopMedia(ctx context.Context) error {
	return c.Call(ctx, OpMediaStop, None{}, nil)
}

// Decode reads an event's data into v (a CommandEvent for a command, say).
func (e Event) Decode(v any) error { return json.Unmarshal(e.Data, v) }
