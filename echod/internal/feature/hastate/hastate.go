// Package hastate follows entities in Home Assistant. The ESPHome protocol lets a device ask for
// the states of entities it names, and Home Assistant then sends each one whenever it changes —
// which is how the screen knows the weather and the radio's station list without a browser.
//
// What is wanted is declared before a client connects (Want), since Home Assistant asks once per
// connection what the device would like; a change to the list takes a reconnect.
package hastate

import (
	"context"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"google.golang.org/protobuf/proto"

	esphome "github.com/ygelfand/go-esphome-device"
	"github.com/ygelfand/go-esphome-device/api"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hook"
)

func init() {
	component.Register(component.Device, Get(), component.Order(35))
}

// Key names one thing followed: an entity's state (Attribute empty) or one attribute of it.
type Key struct {
	Entity    string
	Attribute string
}

// Update is one value arriving.
type Update struct {
	Key
	Value string
	// First is the value's first arrival since start: what it already was, not a change.
	First bool
}

type Tracker struct {
	// Changed fires on every value that arrives; listeners must not block.
	Changed hook.Hook[Update]

	// subscribed is Home Assistant having asked what to follow: it is connected and listening.
	subscribed atomic.Bool

	mu     sync.Mutex
	wanted map[string][]Key // by owner, so one feature rebuilding its list leaves the others alone
	values map[Key]string
}

var (
	once   sync.Once
	shared *Tracker
)

func Get() *Tracker {
	once.Do(func() { shared = &Tracker{wanted: map[string][]Key{}, values: map[Key]string{}} })
	return shared
}

// Subscribed is whether Home Assistant has connected and asked what to follow since start.
func (t *Tracker) Subscribed() bool { return t.subscribed.Load() }

func (t *Tracker) Name() string { return "home assistant states" }

// Follow replaces what owner follows: entity states (Attribute empty) and attributes. Home Assistant
// is told at its next subscription, so a changed list wants a reconnect.
func (t *Tracker) Follow(owner string, keys ...Key) {
	var clean []Key
	for _, k := range keys {
		k.Entity = strings.TrimSpace(k.Entity)
		if k.Entity != "" && !slices.Contains(clean, k) {
			clean = append(clean, k)
		}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(clean) == 0 {
		delete(t.wanted, owner)
		return
	}
	t.wanted[owner] = clean
}

// all is every key any owner follows, each once, in a stable order.
func (t *Tracker) all() []Key {
	t.mu.Lock()
	defer t.mu.Unlock()
	owners := make([]string, 0, len(t.wanted))
	for o := range t.wanted {
		owners = append(owners, o)
	}
	slices.Sort(owners)
	var out []Key
	for _, o := range owners {
		for _, k := range t.wanted[o] {
			if !slices.Contains(out, k) {
				out = append(out, k)
			}
		}
	}
	return out
}

// Value is the last value seen for a key, if any has arrived.
func (t *Tracker) Value(entity, attribute string) (string, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	v, ok := t.values[Key{Entity: entity, Attribute: attribute}]
	return v, ok
}

// State is the entity's state, empty when unknown.
func (t *Tracker) State(entity string) string {
	v, _ := t.Value(entity, "")
	return v
}

// Handle answers Home Assistant's question about what to follow, and takes the values as they
// come. Other messages are not ours and pass through.
func (t *Tracker) Handle(ctx context.Context, c *esphome.Conn, msg proto.Message) error {
	switch m := msg.(type) {
	case *api.SubscribeHomeAssistantStatesRequest:
		wanted := t.all()
		for _, k := range wanted {
			if err := c.Send(&api.SubscribeHomeAssistantStateResponse{EntityId: k.Entity, Attribute: k.Attribute}); err != nil {
				return err
			}
		}
		slog.Info("following home assistant entities", "count", len(wanted))
		t.subscribed.Store(true)
	case *api.HomeAssistantStateResponse:
		k := Key{Entity: m.EntityId, Attribute: m.Attribute}
		t.mu.Lock()
		old, had := t.values[k]
		t.values[k] = m.State
		t.mu.Unlock()
		if !had || old != m.State {
			t.Changed.Emit(Update{Key: k, Value: m.State, First: !had})
		}
	}
	return nil
}

// Options parses the string Home Assistant sends for a list attribute — Python's repr of a list,
// like ['101.1 WXYZ', "Kitchen Echo"] — into its items. Anything else comes back as one item.
func Options(s string) []string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "[") || !strings.HasSuffix(s, "]") {
		if s == "" {
			return nil
		}
		return []string{s}
	}
	var out []string
	body := s[1 : len(s)-1]
	for i := 0; i < len(body); {
		for i < len(body) && (body[i] == ' ' || body[i] == ',') {
			i++
		}
		if i >= len(body) {
			break
		}
		q := body[i]
		if q != '\'' && q != '"' {
			// Not quoted: up to the next comma.
			j := strings.IndexByte(body[i:], ',')
			if j < 0 {
				j = len(body) - i
			}
			out = append(out, strings.TrimSpace(body[i:i+j]))
			i += j
			continue
		}
		i++
		var b strings.Builder
		for i < len(body) && body[i] != q {
			if body[i] == '\\' && i+1 < len(body) {
				i++
			}
			b.WriteByte(body[i])
			i++
		}
		i++ // closing quote
		out = append(out, b.String())
	}
	return out
}
