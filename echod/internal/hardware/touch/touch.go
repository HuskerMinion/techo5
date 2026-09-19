//go:build !dot

// Package touch owns the touchscreen and says what a finger did: a tap, or a swipe as it travels,
// and on a device that wants them (the Echo Spot's ring menu) a hold, the drag that follows it and
// the release. It does not know what any of it means; the display decides.
//
// The Goodix controllers speak multitouch protocol B (slots and tracking ids). Coordinates are
// reported in the frame the screen draws in: on the Show the panel's portrait frame turned a quarter
// turn into landscape, as hardware/screen does; on the Spot the panel's own 480x480 (device_*.go).
package touch

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hook"
	"github.com/HuskerMinion/techo5/echod/internal/lib/input"
	"github.com/HuskerMinion/techo5/echod/internal/service"
)

func init() {
	component.Register(component.Hardware, Get(), component.Order(20),
		component.Supervise(service.Restart(time.Second, 30*time.Second)))
}

const (
	btnTouch        = 0x14a
	absMTSlot       = 0x2f
	absMTPositionX  = 0x35
	absMTPositionY  = 0x36
	absMTTrackingID = 0x39
	synReport       = 0

	// tapHold is how long a tap may stay down; how far it may wander is the device's tapMove.
	tapHold = 500 * time.Millisecond

	// notch (the device's) is how far a vertical swipe travels per step it reports, so a slow drag turns the volume
	// a step at a time and a flick several.

	// swipeMin is how far a horizontal movement has to go to be a swipe at release.
	swipeMin = 120

	// holdAfter is how long a finger stays put before it is a hold, where holds are reported.
	holdAfter = 450 * time.Millisecond

	// followMove is how far a finger moves before it is followed, in follow mode (SetFollow).
	followMove = 12
)

// Kind is what the finger did.
type Kind string

const (
	Tap        Kind = "tap"
	SwipeUp    Kind = "swipe_up"   // reported per notch while the finger moves
	SwipeDown  Kind = "swipe_down" // likewise
	SwipeLeft  Kind = "swipe_left" // reported once, at release
	SwipeRight Kind = "swipe_right"

	// Hold, Drag and Release follow one finger that stayed put for holdAfter, on a device with
	// holdGestures: Hold once, Drag as it moves, Release where it lifts. No tap or swipe comes from
	// that finger.
	Hold    Kind = "hold"
	Drag    Kind = "drag"
	Release Kind = "release"
)

// Gesture is one thing the finger did, with where it started in the screen's frame (for Drag and
// Release: where the finger is).
type Gesture struct {
	Kind Kind
	X, Y int
}

func (g Gesture) String() string { return fmt.Sprintf("%s at %d,%d", g.Kind, g.X, g.Y) }

type Screen struct {
	// Gestures fires as they happen, on the reader's goroutine: listeners must not block.
	Gestures hook.Hook[Gesture]

	dev  *input.Device
	rawW int // panel x range, exclusive
	rawH int // panel y range, exclusive

	mu     sync.Mutex
	down   bool // a finger is on the panel
	follow bool // every moving finger is followed (Hold, Drag, Release) rather than swiped
}

// SetFollow turns follow mode on or off: while on, a finger that moves is reported as Hold where it
// started, Drag as it goes and Release where it lifts, at once and without waiting to be held. A dial
// on the screen wants that; a finger that does not move is still a tap.
func (s *Screen) SetFollow(on bool) {
	s.mu.Lock()
	s.follow = on
	s.mu.Unlock()
}

var (
	once   sync.Once
	shared *Screen
)

func Get() *Screen {
	once.Do(func() { shared = &Screen{} })
	return shared
}

func (s *Screen) Name() string { return "touchscreen" }

// Touched reports whether a finger is on the panel now.
func (s *Screen) Touched() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.down
}

// Start opens the controller's node and reads its coordinate ranges.
func (s *Screen) Start(context.Context) error {
	dev, err := input.Find(deviceName)
	if err != nil && layout.Board == "cronos" {
		dev, err = input.Find("Goodix Capacitive TouchScreen")
	}
	if err != nil {
		return fmt.Errorf("touch: %w", err)
	}
	x, errX := dev.Abs(absMTPositionX)
	y, errY := dev.Abs(absMTPositionY)
	if errX != nil || errY != nil || x.Max <= x.Min || y.Max <= y.Min {
		// The panel's raw axes, from the device's hardware notes, when the controller will not say.
		x.Min, x.Max, y.Min, y.Max = 0, int32(rawFallbackW-1), 0, int32(rawFallbackH-1)
		slog.Warn("touch: using the documented ranges", "errX", errX, "errY", errY)
	}
	s.dev = dev
	s.rawW = int(x.Max-x.Min) + 1
	s.rawH = int(y.Max-y.Min) + 1
	slog.Info("touchscreen open", "device", dev.Path, "raw", fmt.Sprintf("%dx%d", s.rawW, s.rawH))
	return nil
}

func (s *Screen) Close() error {
	if s.dev == nil {
		return nil
	}
	err := s.dev.Close()
	s.dev = nil
	return err
}

// finger is the one contact being followed: the first slot that went down, until it lifts.
type finger struct {
	slot         int
	id           int32
	x, y         int // raw, current
	sx, sy       int // raw, where it started
	at           time.Time
	notched      int  // steps already reported along the vertical travel
	swiped       bool // a notch went out, so this is not a tap
	seenX, seenY bool
	held         bool        // it became a hold: Drag and Release follow
	holdTimer    *time.Timer // pending hold, stopped by movement or a lift
}

// Run reads until ctx is cancelled; the node is closed from the side to end the blocking read.
func (s *Screen) Run(ctx context.Context) error {
	dev := s.dev
	stop := context.AfterFunc(ctx, func() { _ = dev.Close() })
	defer stop()

	slot := 0
	var f *finger
	// per-slot positions arrive before the slot's tracking id is known to be ours, so keep them all
	type pos struct {
		x, y         int32
		seenX, seenY bool
	}
	slots := map[int]*pos{}

	for {
		e, err := dev.Read()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("touch: reading %s: %w", dev.Path, err)
		}
		switch e.Type {
		case input.EvAbs:
			p := slots[slot]
			if p == nil {
				p = &pos{}
				slots[slot] = p
			}
			switch e.Code {
			case absMTSlot:
				slot = int(e.Value)
			case absMTPositionX:
				p.x, p.seenX = e.Value, true
				if f != nil && f.slot == slot {
					s.mu.Lock()
					f.x, f.seenX = int(e.Value), true
					s.mu.Unlock()
				}
			case absMTPositionY:
				p.y, p.seenY = e.Value, true
				if f != nil && f.slot == slot {
					s.mu.Lock()
					f.y, f.seenY = int(e.Value), true
					s.mu.Unlock()
				}
			case absMTTrackingID:
				switch {
				case e.Value >= 0 && f == nil:
					f = &finger{slot: slot, id: e.Value, at: time.Now(), sx: -1}
					s.setDown(true)
					if holdGestures {
						nf := f
						s.mu.Lock()
						nf.holdTimer = time.AfterFunc(holdAfter, func() { s.holdFired(nf) })
						s.mu.Unlock()
					}
				case e.Value < 0 && f != nil && f.slot == slot:
					s.mu.Lock()
					if f.holdTimer != nil {
						f.holdTimer.Stop()
					}
					s.mu.Unlock()
					s.lift(f)
					f = nil
					s.setDown(false)
				}
			}
		case input.EvKey:
			// BTN_TOUCH is the only lift some controllers send: the Spot's mtk-tpd reports multitouch
			// protocol A, with no slots and no tracking id of -1. On the Show it arrives beside the
			// tracking id and whichever comes second finds nothing to do.
			if e.Code != btnTouch {
				continue
			}
			switch {
			case e.Value == 1 && f == nil:
				f = &finger{slot: slot, at: time.Now(), sx: -1}
				s.setDown(true)
				if holdGestures {
					nf := f
					s.mu.Lock()
					nf.holdTimer = time.AfterFunc(holdAfter, func() { s.holdFired(nf) })
					s.mu.Unlock()
				}
			case e.Value == 0 && f != nil:
				s.mu.Lock()
				if f.holdTimer != nil {
					f.holdTimer.Stop()
				}
				s.mu.Unlock()
				s.lift(f)
				f = nil
				s.setDown(false)
			}
		case input.EvSyn:
			if e.Code != synReport || f == nil {
				continue
			}
			s.mu.Lock()
			// Evdev only sends coordinates that changed. A new contact may reuse
			// either axis from the slot's previous contact.
			if p := slots[f.slot]; p != nil {
				if !f.seenX && p.seenX {
					f.x, f.seenX = int(p.x), true
				}
				if !f.seenY && p.seenY {
					f.y, f.seenY = int(p.y), true
				}
			}
			if f.sx < 0 && f.seenX && f.seenY {
				f.sx, f.sy = f.x, f.y
			}
			s.mu.Unlock()
			if f.sx >= 0 {
				s.moved(f)
			}
		}
	}
}

func (s *Screen) setDown(v bool) {
	s.mu.Lock()
	s.down = v
	s.mu.Unlock()
}

// landscape turns a raw panel point into the frame the screen draws in (device_*.go).
func (s *Screen) landscape(rx, ry int) (x, y int) {
	x, y = toFrame(s.rawW, s.rawH, rx, ry)
	return min(max(x, 0), Width-1), min(max(y, 0), Height-1)
}

// holdFired is the hold timer: the finger is a hold if it is still down, has a position and has not
// moved or swiped.
func (s *Screen) holdFired(f *finger) {
	s.mu.Lock()
	if !s.down || f.sx < 0 || f.swiped || f.held {
		s.mu.Unlock()
		return
	}
	x0, y0 := s.landscape(f.sx, f.sy)
	x1, y1 := s.landscape(f.x, f.y)
	if abs(x1-x0) > tapMove || abs(y1-y0) > tapMove {
		s.mu.Unlock()
		return
	}
	f.held = true
	s.mu.Unlock()
	s.Gestures.Emit(Gesture{Kind: Hold, X: x0, Y: y0})
}

// moved reports vertical travel a notch at a time while the finger is down. "Up" on the landscape
// screen is towards smaller landscape y.
func (s *Screen) moved(f *finger) {
	s.mu.Lock()
	if f.held {
		x, y := s.landscape(f.x, f.y)
		s.mu.Unlock()
		s.Gestures.Emit(Gesture{Kind: Drag, X: x, Y: y})
		return
	}
	x0, y0 := s.landscape(f.sx, f.sy)
	x1, y1 := s.landscape(f.x, f.y)
	if s.follow && !f.swiped && (abs(x1-x0) > followMove || abs(y1-y0) > followMove) {
		f.held = true
		if f.holdTimer != nil {
			f.holdTimer.Stop()
		}
		s.mu.Unlock()
		s.Gestures.Emit(Gesture{Kind: Hold, X: x0, Y: y0})
		s.Gestures.Emit(Gesture{Kind: Drag, X: x1, Y: y1})
		return
	}
	if f.holdTimer != nil && (abs(x1-x0) > tapMove || abs(y1-y0) > tapMove) {
		f.holdTimer.Stop()
	}
	s.mu.Unlock()
	if verticalOnly && abs(x1-x0) > abs(y1-y0) {
		// Sideways as much as up or down: not a volume swipe, and it may yet be a sideways one.
		return
	}
	steps := (y0 - y1) / notch // positive: finger moved up
	for f.notched < steps {
		f.notched++
		f.swiped = true
		x, y := s.landscape(f.sx, f.sy)
		s.Gestures.Emit(Gesture{Kind: SwipeUp, X: x, Y: y})
	}
	for f.notched > steps {
		f.notched--
		f.swiped = true
		x, y := s.landscape(f.sx, f.sy)
		s.Gestures.Emit(Gesture{Kind: SwipeDown, X: x, Y: y})
	}
}

// lift is the finger leaving: a tap if it barely moved and did not stay, a horizontal swipe if it
// travelled sideways, nothing otherwise (its vertical notches already went out).
func (s *Screen) lift(f *finger) {
	if f.sx < 0 {
		return
	}
	s.mu.Lock()
	isHold := f.held
	s.mu.Unlock()
	if isHold {
		x, y := s.landscape(f.x, f.y)
		s.Gestures.Emit(Gesture{Kind: Release, X: x, Y: y})
		return
	}
	x0, y0 := s.landscape(f.sx, f.sy)
	x1, y1 := s.landscape(f.x, f.y)
	dx, dy := x1-x0, y1-y0
	held := time.Since(f.at)
	switch {
	case !f.swiped && abs(dx) <= tapMove && abs(dy) <= tapMove && held <= tapHold:
		s.Gestures.Emit(Gesture{Kind: Tap, X: x0, Y: y0})
	case !f.swiped && abs(dx) >= swipeMin && abs(dx) > 2*abs(dy):
		k := SwipeRight
		if dx < 0 {
			k = SwipeLeft
		}
		s.Gestures.Emit(Gesture{Kind: k, X: x0, Y: y0})
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
