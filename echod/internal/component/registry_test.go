package component

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// bare is a component that is nothing but a name, which is all a component has to be.
type bare struct{ name string }

func (b bare) Name() string { return b.name }

// full implements every optional half, and records what was called on it.
type full struct {
	bare
	entity   *esphome.Switch
	restored int
	ran      bool
}

func newFull(name string) *full {
	return &full{
		bare:   bare{name: name},
		entity: &esphome.Switch{Base: esphome.Base{ObjectID: name}},
	}
}

func (f *full) Entities() []esphome.Entity { return []esphome.Entity{f.entity} }
func (f *full) Restore(c config.Config)    { f.restored++ }
func (f *full) Run(context.Context) error  { f.ran = true; return nil }

func names(cs []Component) []string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.Name())
	}
	return out
}

// Phase decides first, then the declared order, then the name. Registration order decides nothing,
// because it is Go's import graph and not a decision anybody made.
func TestOrderIsDeclaredNotRegistered(t *testing.T) {
	r := New()

	r.Add(Network, bare{"api"})
	r.Add(Hardware, bare{"speaker"}, Order(20))
	r.Add(Device, bare{"voice"})
	r.Add(Hardware, bare{"led"}, Order(10))
	r.Add(Hardware, bare{"zzz"}, Order(10))
	r.Add(Hardware, bare{"aaa"}, Order(10))

	want := []string{"aaa", "led", "zzz", "speaker", "voice", "api"}
	if got := names(r.All()); !slices.Equal(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
}

// once has work to do at start-up and no loop, which is what most boot-time setup looks like.
type once struct {
	bare
	started chan struct{}
	closed  bool
}

func newOnce(name string) *once {
	return &once{bare: bare{name: name}, started: make(chan struct{})}
}

func (o *once) Start(context.Context) error { close(o.started); return nil }
func (o *once) Close() error                { o.closed = true; return nil }

// A component with nothing to do is skipped; one with a loop is supervised; and one that only has
// work to do at start-up is still started, in its place in the order, rather than silently ignored.
func TestWhatReachesTheSupervisor(t *testing.T) {
	r := New()
	loop, setup := newFull("speaker"), newOnce("procs")

	r.Add(Hardware, bare{"nothing"})
	r.Add(Hardware, loop)
	r.Add(Hardware, setup)

	g := r.Group()
	if n := len(g.Status()); n != 2 {
		t.Fatalf("the group took %d services, want the loop and the one-shot", n)
	}

	ctx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- g.Run(ctx) }()

	select {
	case <-setup.started:
	case <-time.After(5 * time.Second):
		t.Fatal("a component with no loop was never started")
	}

	stop()
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Run has returned, so everything the group did is visible here.
	if !loop.ran {
		t.Error("the component with a loop never ran")
	}
	if !setup.closed {
		t.Error("a one-shot component was not closed on the way out")
	}
}

// Entities, restores and samples all follow the same order, and skip the components that have none.
func TestOptionalHalves(t *testing.T) {
	r := New()
	second, first := newFull("second"), newFull("first")

	r.Add(Device, second, Order(2))
	r.Add(Device, first, Order(1))
	r.Add(Device, bare{"nothing"}, Order(3))

	ents := r.Entities()
	if len(ents) != 2 {
		t.Fatalf("collected %d entities, want 2", len(ents))
	}
	if got := ents[0].(*esphome.Switch).ObjectID; got != "first" {
		t.Errorf("entities led with %q", got)
	}

	r.Restore(config.Defaults())

	if first.restored != 1 || second.restored != 1 {
		t.Errorf("restored %d and %d times, want once each", first.restored, second.restored)
	}
}

// Nothing registered is not a failure: a device with no hardware at all still starts.
func TestEmptyRegistry(t *testing.T) {
	r := New()

	if got := r.All(); len(got) != 0 {
		t.Errorf("All = %v", got)
	}
	if got := r.Entities(); got != nil {
		t.Errorf("Entities = %v", got)
	}
	if got := r.Handlers(); got != nil {
		t.Errorf("Handlers = %v", got)
	}
	r.Restore(config.Defaults())
}

// acting is a component with one action that fails.
type acting struct{ bare }

func (acting) Actions() []*esphome.Action {
	return []*esphome.Action{{Name: "go", Run: func(esphome.Call) (any, error) { return "said", errors.New("no such thing") }}}
}

// An action's failure is logged, since Home Assistant hears of it only when it asked for an answer,
// and the action still answers as it did.
func TestAFailedActionIsLogged(t *testing.T) {
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })

	r := New()
	r.Add(Device, acting{bare{"acting"}})
	acts := r.Actions()
	answer, err := acts[0].Run(esphome.Call{})
	if answer != "said" || err == nil {
		t.Errorf("the action answered %v, %v", answer, err)
	}
	if !strings.Contains(buf.String(), "action=go") || !strings.Contains(buf.String(), "no such thing") {
		t.Errorf("the failure was not logged: %q", buf.String())
	}
	// Asked again, the actions are logged once each, not wrapped twice.
	buf.Reset()
	r.Actions()[0].Run(esphome.Call{})
	if n := strings.Count(buf.String(), "action failed"); n != 1 {
		t.Errorf("logged %d times", n)
	}
}
