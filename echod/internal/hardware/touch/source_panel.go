//go:build panel

package touch

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/HuskerMinion/techo5/echod/internal/lib/input"
	"github.com/HuskerMinion/techo5/echod/internal/lib/panelscreen"
)

// conn is the app's screen connection while the touchscreen is started.
var conn *panelscreen.Conn

// Start takes the app's connection, which says how big the canvas is.
func (s *Screen) Start(context.Context) error {
	c, err := panelscreen.Get()
	if err != nil {
		return fmt.Errorf("touch: %w", err)
	}
	conn = c
	scaleTo(c.W, c.H)
	s.rawW, s.rawH = c.W, c.H
	slog.Info("touchscreen open", "device", "panel app", "frame", fmt.Sprintf("%dx%d", c.W, c.H))
	return nil
}

// Close forgets the connection without closing it: the screen shares it. A connection that failed
// was dropped where it failed.
func (s *Screen) Close() error {
	conn = nil
	return nil
}

// Run turns the app's touches into the events a multitouch controller sends (protocol B: slot,
// tracking id, position, report), so track reads them as it reads a Goodix and every gesture is the
// Show's own. The connection is closed from the side to end the blocking read.
func (s *Screen) Run(ctx context.Context) error {
	c := conn
	if c == nil {
		return fmt.Errorf("touch: not started")
	}
	stop := context.AfterFunc(ctx, func() { _ = c.Close() })
	defer stop()

	var pending []input.Event
	ids := int32(0)
	read := func() (input.Event, error) {
		for len(pending) == 0 {
			t, err := c.ReadTouch()
			if err != nil {
				panelscreen.Drop(c)
				return input.Event{}, err
			}
			abs := func(code uint16, v int) input.Event {
				return input.Event{Type: input.EvAbs, Code: code, Value: int32(v)}
			}
			x, y := min(max(t.X, 0), Width-1), min(max(t.Y, 0), Height-1)
			pending = append(pending, abs(absMTSlot, t.Pointer))
			switch t.Action {
			case panelscreen.Down:
				ids++
				pending = append(pending, abs(absMTPositionX, x), abs(absMTPositionY, y),
					input.Event{Type: input.EvAbs, Code: absMTTrackingID, Value: ids})
			case panelscreen.Move:
				pending = append(pending, abs(absMTPositionX, x), abs(absMTPositionY, y))
			case panelscreen.Up:
				pending = append(pending, abs(absMTTrackingID, -1))
			}
			pending = append(pending, input.Event{Type: input.EvSyn, Code: synReport})
		}
		e := pending[0]
		pending = pending[1:]
		return e, nil
	}
	return s.track(ctx, panelscreen.Socket, read)
}
