//go:build !dot && !spot

package display

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/voice"
	"github.com/HuskerMinion/techo5/echod/pkg/addonapi"
	"github.com/HuskerMinion/techo5/echod/pkg/addonapi/serve"
)

// helloDevice is the device as the add-on server sees it, with only hello answered: the screen's ops ask
// nothing else of it.
type helloDevice struct{ serve.Device }

func (helloDevice) Info() addonapi.Device { return addonapi.Device{Family: "show"} }

// A screen the night put out (Screen at night set to off) is turned on for a page an add-on opens in
// answer to somebody, as a tap on the dark screen turns it on, and not for an open the server refuses;
// nothing is saved, and once the room has been quiet the night puts it out again by its own rule.
func TestAddonOpenLightsADarkNight(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	config.Use(state)
	h := time.Now().Hour()
	if err := config.Set().Screen().Night(fmt.Sprintf("%d-%d", (h+23)%24, (h+2)%24)); err != nil {
		t.Fatal(err)
	}
	saved, _ := os.ReadFile(state)
	useHost(t, sampleHost(t, cookingPage()))
	d := &Display{
		poke: make(chan struct{}, 1),
		view: voice.State{Phase: "idle"},
		light: &esphome.Light{
			Base:                esphome.Base{ObjectID: "screen", Name: "Screen", Icon: "mdi:monitor"},
			SupportedColorModes: []esphome.ColorMode{esphome.ColorModeBrightness},
		},
		ceiling: 60,
	}
	d.nightDark = true // the night put it out

	// The add-on server, with the display as its screen.
	dir, err := os.MkdirTemp("", "aapi")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	srv := serve.New(filepath.Join(dir, "run"), helloDevice{}, func(uint32) bool { return true })
	srv.Chown = func(string, int) error { return nil }
	srv.SetScreen(addonSink{d})
	path, err := srv.Open(serve.Grant{ID: "meals", UID: uint32(os.Getuid()), Caps: []string{addonapi.CapScreen}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Close("meals") })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := addonapi.Dial(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.SetScene(ctx, addonapi.OnPage, addonapi.Column(addonapi.Title("Stir the onions"))); err != nil {
		t.Fatal(err)
	}

	// Nobody asked: refused, and the screen stays dark.
	if err := c.OpenScreen(ctx); addonapi.CodeOf(err) != addonapi.CodeBusy {
		t.Fatalf("an open nobody asked for: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	d.mu.Lock()
	on, dark, page := d.on, d.nightDark, d.addon.page
	d.mu.Unlock()
	if on || !dark || page != "" {
		t.Fatalf("a refused open: on %v, dark %v, page %q", on, dark, page)
	}

	// A tap on its tile, say: the open is allowed, the page comes up and the screen with it.
	srv.Asked("meals")
	if err := c.OpenScreen(ctx); err != nil {
		t.Fatal(err)
	}
	for end := time.Now().Add(2 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		d.mu.Lock()
		on, dark, page = d.on, d.nightDark, d.addon.page
		d.mu.Unlock()
		if on {
			break
		}
		if time.Now().After(end) {
			t.Fatal("the screen stayed dark for a page somebody asked for")
		}
	}
	if dark || page != "meals" {
		t.Errorf("lit: dark %v, page %q", dark, page)
	}
	if now, _ := os.ReadFile(state); string(now) != string(saved) {
		t.Error("lighting the screen for the page wrote the settings")
	}

	// While it is being used the night leaves it; once the room is quiet it puts it out again.
	if d.night(time.Now(), true, voice.State{Phase: "idle"}) {
		t.Error("the night put out a page just opened")
	}
	d.mu.Lock()
	d.touchedAt, d.viewAt = time.Now().Add(-time.Hour), time.Now().Add(-time.Hour)
	d.mu.Unlock()
	if !d.night(time.Now(), true, voice.State{Phase: "idle"}) {
		t.Fatal("the night left the screen on in a quiet room")
	}
	d.mu.Lock()
	on, dark = d.on, d.nightDark
	d.mu.Unlock()
	if on || !dark {
		t.Errorf("after the quiet: on %v, dark %v", on, dark)
	}
}
