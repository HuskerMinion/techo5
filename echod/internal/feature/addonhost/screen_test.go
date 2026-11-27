package addonhost

import (
	"context"
	"io/fs"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/addons"
	"github.com/HuskerMinion/techo5/echod/pkg/addonapi"
)

// display is a display that records what it was told.
type display struct {
	mu    sync.Mutex
	calls []string
}

func (d *display) note(s string)         { d.mu.Lock(); d.calls = append(d.calls, s); d.mu.Unlock() }
func (d *display) PageChanged(id string) { d.note("page " + id) }
func (d *display) CardChanged(id string) { d.note("card " + id) }
func (d *display) Open(id string)        { d.note("open " + id) }
func (d *display) Close(id string)       { d.note("close " + id) }
func (d *display) has(s string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, c := range d.calls {
		if c == s {
			return true
		}
	}
	return false
}

func screenHost(t *testing.T) (*hostRig, *display, *addonapi.Client) {
	t.Helper()
	skipWithoutAddons(t)
	r := newHostRig(t)
	r.g.Man.Capabilities = append(r.g.Man.Capabilities, addonapi.CapScreen)
	r.g.Agreed.Capabilities = append(r.g.Agreed.Capabilities, addonapi.CapScreen)
	d := &display{}
	r.h.SetScreen(d)
	path, err := r.h.Open(r.g)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := addonapi.Dial(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return r, d, c
}

func sctx(t *testing.T) context.Context {
	c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return c
}

var stepPage = addonapi.Column(addonapi.Title("Stir the onions"), addonapi.Button("next", "Next", "mdi:arrow-right"))

// On a device with a screen, an add-on granted it has a tile and a page the display reads; on a Dot,
// the screen's ops are refused, whatever the display tried to give.
func TestAddonScreen(t *testing.T) {
	r, d, c := screenHost(t)
	err := c.SetScene(sctx(t), addonapi.OnPage, stepPage)
	if !hasScreen() {
		if addonapi.CodeOf(err) != addonapi.CodeUnavailable {
			t.Fatalf("a scene on a %s: %v", addons.Family(), err)
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	if tiles := r.h.ScreenTiles(); len(tiles) != 1 || tiles[0] != (ScreenTile{ID: "meals", Name: "Meals"}) {
		t.Errorf("tiles %+v", tiles)
	}
	eventuallyTrue(t, "the display to be told", func() bool { return d.has("page meals") })
	if sc, ok := r.h.Scene("meals", addonapi.OnPage); !ok || sc.Root.Children[0].Text != "Stir the onions" {
		t.Errorf("the page %+v", sc)
	}
	ev := c.WatchScreen()
	r.h.Shown("meals", addonapi.OnPage, true)
	<-ev
	if !r.h.Touch("meals", addonapi.OnPage, "next") {
		t.Fatal("a tap on Next")
	}
	if e := <-ev; e.Touch.ID != "next" {
		t.Errorf("touch %+v", e)
	}
}

// A removed add-on's page and card go with it, and nothing of them was written in its folder.
func TestAddonScreenGoesOnRemoval(t *testing.T) {
	r, d, c := screenHost(t)
	if !hasScreen() {
		t.Skip("no screen on a " + addons.Family())
	}
	if err := c.SetScene(sctx(t), addonapi.OnCard, addonapi.Text("Stir")); err != nil {
		t.Fatal(err)
	}
	r.h.Removed("meals")
	if _, ok := r.h.Scene("meals", addonapi.OnCard); ok || len(r.h.ScreenTiles()) != 0 {
		t.Error("the card outlived the add-on")
	}
	eventuallyTrue(t, "the display to be told", func() bool { return d.has("card meals") })
	_ = filepath.WalkDir(r.root, func(p string, e fs.DirEntry, err error) error {
		if err == nil && !e.IsDir() {
			t.Errorf("%s was written", p)
		}
		return nil
	})
}
