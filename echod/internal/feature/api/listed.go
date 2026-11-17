package api

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"

	esphome "github.com/ygelfand/go-esphome-device"
	"github.com/ygelfand/go-esphome-device/api"
)

// Home Assistant reads the entity list when it connects and asks for the states last, after setting up
// the entities, the device's services and its satellite. A connect of Home Assistant's that stops
// between the two (it was seen after a Home Assistant restart under heavy load) leaves a connection
// that answers its pings and shows the device connected, with every entity unavailable: Home Assistant
// writes an entity's state again only when one arrives, and none is sent to a connection that has not
// asked. It stayed that way until the daemon was restarted, which did nothing but close the connection.
//
// unsubscribed closes it the same way: a connection that listed the entities and has not asked for
// their states within subscribeWithin is dropped, so Home Assistant connects again and reads the device
// afresh. A client that never lists them (a log viewer) is left alone.
type unsubscribed struct {
	mu     sync.Mutex
	timers map[*esphome.Conn]*time.Timer
}

// subscribeWithin is how long a connection that listed the entities may go without asking for their
// states. A loaded Home Assistant takes seconds between the two; minutes is a connect that has stopped.
// A variable for the tests.
var subscribeWithin = 5 * time.Minute

func newUnsubscribed() *unsubscribed {
	return &unsubscribed{timers: map[*esphome.Conn]*time.Timer{}}
}

func (u *unsubscribed) Handle(ctx context.Context, c *esphome.Conn, msg proto.Message) error {
	switch msg.(type) {
	case *api.ListEntitiesRequest:
		u.mu.Lock()
		defer u.mu.Unlock()
		if _, ok := u.timers[c]; ok || c.StatesSubscribed() {
			return nil
		}
		u.timers[c] = time.AfterFunc(subscribeWithin, func() {
			if c.StatesSubscribed() {
				return
			}
			slog.Warn("api: home assistant listed the entities and never asked for their states; dropping the connection so it connects again",
				"client", c.ClientInfo(), "after", subscribeWithin)
			_ = c.Close()
		})
	case *api.SubscribeStatesRequest:
		u.stop(c)
	}
	return nil
}

// ConnClosed forgets a connection that has gone.
func (u *unsubscribed) ConnClosed(c *esphome.Conn) { u.stop(c) }

func (u *unsubscribed) stop(c *esphome.Conn) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if t, ok := u.timers[c]; ok {
		t.Stop()
		delete(u.timers, c)
	}
}
