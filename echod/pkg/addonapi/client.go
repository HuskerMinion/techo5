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

	// media, once WatchMedia has made it, is where media.state events go instead of events.
	media chan MediaState
	// screen, once WatchScreen has made it, is where touch, swipe, back and screen.state go.
	screen chan ScreenEvent
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
		// Under mu, with err set, so a WatchMedia after this finds the connection over and closes its
		// own.
		if c.media != nil {
			close(c.media)
		}
		if c.screen != nil {
			close(c.screen)
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
			c.mu.Lock()
			media, screen := c.media, c.screen
			c.mu.Unlock()
			if media != nil && f.Event == EventMediaState {
				var st MediaState
				if err := json.Unmarshal(f.Data, &st); err == nil {
					media <- st
					continue
				}
			}
			if screen != nil {
				if ev, ok := screenEvent(f.Event, f.Data); ok {
					screen <- ev
					continue
				}
			}
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
	_, err := c.StartMedia(ctx, kind, url, title)
	return err
}

// StartMedia is PlayMedia, answering the id the media.state events of what it started carry.
func (c *Client) StartMedia(ctx context.Context, kind, url, title string) (string, error) {
	var m MediaStarted
	err := c.Call(ctx, OpMediaPlay, MediaPlay{URL: url, Kind: kind, Title: title}, &m)
	return m.Media, err
}

// MediaStatus is what the add-on's latest media is doing now: State MediaIdle when it has started none.
func (c *Client) MediaStatus(ctx context.Context) (MediaState, error) {
	var m MediaState
	err := c.Call(ctx, OpMediaStatus, None{}, &m)
	return m, err
}

// WatchMedia has media.state events, which say what the media the add-on started is doing, arrive on
// the channel it returns rather than on Events, decoded; it is closed when the connection ends. Like
// Events, an add-on that leaves it unread holds up everything the daemon sends, answers included, and
// is disconnected once the daemon's queue for it is full. Asked again, it returns the same channel.
func (c *Client) WatchMedia() <-chan MediaState {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.media == nil {
		c.media = make(chan MediaState, 16)
		if c.err != nil {
			close(c.media) // ended already, and nothing else would close it
		}
	}
	return c.media
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

// The screen (stage 2), with the screen capability.

// SetScene sets the whole scene of the add-on's page or card (OnPage, OnCard). It is checked here
// first as the device checks it (ValidScene), so a scene the device would refuse is refused without
// sending it; the device checks again, and also that a progress's timer is the add-on's. The device
// draws the latest scene at most SceneUpdatesPerSecond times a second, so setting it faster costs
// nothing but the requests.
func (c *Client) SetScene(ctx context.Context, on string, root Block) error {
	if err := ValidScene(on, root, nil); err != nil {
		return err
	}
	return c.Call(ctx, OpSceneSet, SceneSet{On: on, Scene: root}, nil)
}

// PatchScene replaces the block with this id in the page's or the card's scene, keeping the id.
func (c *Client) PatchScene(ctx context.Context, on, id string, with Block) error {
	return c.Call(ctx, OpScenePatch, ScenePatch{On: on, ID: id, Block: with}, nil)
}

// OpenScreen asks for the add-on's page to be brought up. It waits behind whatever is more pressing on
// the device, and screen.state says when it is in view.
func (c *Client) OpenScreen(ctx context.Context) error {
	return c.Call(ctx, OpScreenOpen, None{}, nil)
}

// CloseScreen asks for the add-on's page to be put away.
func (c *Client) CloseScreen(ctx context.Context) error {
	return c.Call(ctx, OpScreenClose, None{}, nil)
}

// ClearCard takes the add-on's card away from over the clock.
func (c *Client) ClearCard(ctx context.Context) error {
	return c.Call(ctx, OpCardClear, None{}, nil)
}

// PutImage keeps a JPEG or a PNG on the device under name, for an image block of that name
// (ImageNamed) to show; its header is checked here first as the device checks it. It answers how much
// of the add-on's MaxImageBudget its images take now. A put that would take more than the budget is
// refused (too_large): drop one first (DropImage).
func (c *Client) PutImage(ctx context.Context, name string, b []byte) (ImageBudget, error) {
	var out ImageBudget
	if !ValidName(name) {
		return out, Errorf(CodeBadRequest, "an image's name %q is not a name", name)
	}
	if err := ImageBytes(b); err != nil {
		return out, err
	}
	err := c.Call(ctx, OpImagePut, ImagePut{Name: name, Data: b}, &out)
	return out, err
}

// DropImage lets the image of that name go.
func (c *Client) DropImage(ctx context.Context, name string) (ImageBudget, error) {
	var out ImageBudget
	err := c.Call(ctx, OpImageDrop, ImageDrop{Name: name}, &out)
	return out, err
}

// ScreenEvent is one of the screen's events, decoded: Event says which, and the field of that name is
// set (Touch for touch, Change for change, Swipe for swipe, State for screen.state; back carries nothing).
type ScreenEvent struct {
	Event  string
	Touch  TouchEvent
	Change ChangeEvent
	Swipe  SwipeEvent
	State  ScreenStateEvent
}

// screenEvent decodes a screen event, when it is one.
func screenEvent(name string, data json.RawMessage) (ScreenEvent, bool) {
	ev := ScreenEvent{Event: name}
	var err error
	switch name {
	case EventTouch:
		err = json.Unmarshal(data, &ev.Touch)
	case EventChange:
		err = json.Unmarshal(data, &ev.Change)
	case EventSwipe:
		err = json.Unmarshal(data, &ev.Swipe)
	case EventScreenState:
		err = json.Unmarshal(data, &ev.State)
	case EventBack:
	default:
		return ev, false
	}
	return ev, err == nil
}

// WatchScreen has touch, change, swipe, back and screen.state events arrive on the channel it returns rather
// than on Events, decoded; it is closed when the connection ends. As with WatchMedia, an add-on that
// leaves it unread holds up everything the daemon sends and is disconnected once the daemon's queue for
// it is full. Asked again, it returns the same channel.
func (c *Client) WatchScreen() <-chan ScreenEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.screen == nil {
		c.screen = make(chan ScreenEvent, 16)
		if c.err != nil {
			close(c.screen)
		}
	}
	return c.screen
}

// Building blocks, for a scene written in Go. Each is a Block with its type set; set the rest of its
// fields as wanted (an id to patch it by, a weight in a row).

// Column is children top to bottom.
func Column(children ...Block) Block { return Block{Type: BlockColumn, Children: children} }

// Row is children start to end.
func Row(children ...Block) Block { return Block{Type: BlockRow, Children: children} }

// Card is children on a card, marked with one of the theme's Accents, or none with "".
func Card(accent string, children ...Block) Block {
	return Block{Type: BlockCard, Accent: accent, Children: children}
}

// List is sections that scroll.
func List(sections ...Section) Block { return Block{Type: BlockList, Sections: sections} }

// Text is words in the body size.
func Text(words string) Block { return Block{Type: BlockText, Text: words} }

// Title is words in the title size.
func Title(words string) Block { return Block{Type: BlockText, Text: words, Size: SizeTitle} }

// Button is a button with an id, a label and an icon ("mdi:play", or "" for none).
func Button(id, label, icon string) Block {
	return Block{Type: BlockButton, ID: id, Label: label, Icon: icon}
}

// Chip is a chip with an id, a label and an icon ("" for none).
func Chip(id, label, icon string) Block {
	return Block{Type: BlockChip, ID: id, Label: label, Icon: icon}
}

// Toggle is an on and off switch beside its label, flipped by a tap (the change event).
func Toggle(id, label string, on bool) Block {
	return Block{Type: BlockToggle, ID: id, Label: label, On: on}
}

// Stepper is a stepper: a number from min to max in steps of step, its label and unit beside it (either
// may be ""), changed by its − and + (the change event).
func Stepper(id, label string, value, min, max, step float64, unit string) Block {
	return Block{Type: BlockStepper, ID: id, Label: label, Value: &value, Min: &min, Max: &max, Step: &step, Unit: unit}
}

// Progress is a bar at value, from 0 to 1.
func Progress(value float64) Block { return Block{Type: BlockProgress, Value: &value} }

// Countdown is a bar tied to one of the add-on's timers, by timer.start's id.
func Countdown(timer string) Block { return Block{Type: BlockProgress, Timer: timer} }

// Image is an image fetched from an http(s) address on the LAN or Home Assistant.
func Image(url string) Block { return Block{Type: BlockImage, URL: url} }

// ImageData is an image of these bytes, a JPEG or a PNG.
func ImageData(b []byte) Block { return Block{Type: BlockImage, Data: b} }

// ImageNamed is the image the add-on put under name (PutImage).
func ImageNamed(name string) Block { return Block{Type: BlockImage, Name: name} }

// Divider is a rule between blocks; Spacer is room.
func Divider() Block { return Block{Type: BlockDivider} }
func Spacer() Block  { return Block{Type: BlockSpacer} }
