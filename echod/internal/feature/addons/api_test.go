package addons

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/feature/addons/catalog"
	"github.com/HuskerMinion/techo5/echod/pkg/addonapi"
)

// fakeAPI records what the supervisor asked of the API's server.
type fakeAPI struct {
	mu     sync.Mutex
	opened []APIGrant
	events []string
}

func (f *fakeAPI) Open(g APIGrant) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.opened = append(f.opened, g)
	f.events = append(f.events, "open "+g.ID)
	return "/run/techo5-addons/" + g.ID + "/api.sock", nil
}

func (f *fakeAPI) Close(id string) {
	f.mu.Lock()
	f.events = append(f.events, "close "+id)
	f.mu.Unlock()
}

func (f *fakeAPI) Removed(id string) {
	f.mu.Lock()
	f.events = append(f.events, "removed "+id)
	f.mu.Unlock()
}

func (f *fakeAPI) saw() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.events)
}

// apiVersion is a version of the test add-on that speaks the API.
func (r *rig) apiVersion(id, ver string) catalog.Version {
	v := r.version(id, ver, []byte("{}"))
	v.Requires, v.API = []string{"api1"}, 1
	v.Capabilities, v.Commands = []string{"commands", "speak", "settings"}, []string{"next"}
	b, _ := json.Marshal(catalog.Manifest{ID: id, Version: ver, Command: []string{id}, Ports: v.Ports, MemoryMB: v.MemoryMB,
		API: 1, Capabilities: v.Capabilities, Commands: []addonapi.Command{{Name: "next"}},
		Settings: &addonapi.Settings{Fields: []addonapi.Setting{{Key: "token", Label: "Token", Type: addonapi.TypeSecret}}}})
	m := r.assets.put("/"+id+"/"+ver+"/addon.json", b)
	v.Manifest = &m
	return v
}

// An add-on that speaks the API is given its socket before it starts, named in TECHO5_ADDON_API, with
// the capabilities it was agreed to; the socket goes when it stops, and what it set on the device when
// it is removed. Its settings stay with its state when that is kept.
func TestAnAPIAddonIsGivenItsSocket(t *testing.T) {
	r := newRig(t)
	api := &fakeAPI{}
	r.m.SetAPI(api)
	v := r.apiVersion("meals", "0.1.0")
	r.serve(catalog.Catalog{Format: 2, Serial: 1, Addons: []catalog.Addon{entry("meals", v)}})
	r.run()
	if err := r.m.Install(context.Background(), "meals", "0.1.0", r.shown("meals", "0.1.0"), true); err != nil {
		t.Fatal(err)
	}
	r.waitState("meals", StateRunning)
	sp := r.procs.last().spec
	if !slices.Contains(sp.Env, "TECHO5_ADDON_API=/run/techo5-addons/meals/api.sock") {
		t.Errorf("environment: %q", sp.Env)
	}
	api.mu.Lock()
	g := api.opened[0]
	api.mu.Unlock()
	if g.UID != firstUID || !slices.Equal(g.Capabilities(), []string{"commands", "speak", "settings"}) ||
		g.BinDir != filepath.Join(r.dir("meals"), "bin") {
		t.Errorf("grant %+v, capabilities %v", g, g.Capabilities())
	}
	if got, ok := r.m.APIGrantOf("meals"); !ok || got.UID != firstUID {
		t.Errorf("APIGrantOf: %+v %v", got, ok)
	}

	// A restart of its program keeps the socket where it was: it is opened once per start.
	r.procs.last().exit(nil)
	r.clock.waitPending(t, 1, restartFirst)
	r.clock.Advance(restartFirst)
	eventually(t, "a second start", func() bool { return r.procs.count() == 2 })
	if n := strings.Count(strings.Join(api.saw(), ","), "open"); n != 1 {
		t.Errorf("opened %d times across a restart: %v", n, api.saw())
	}

	if err := r.m.Stop("meals"); err != nil {
		t.Fatal(err)
	}
	if got := api.saw(); got[len(got)-1] != "close meals" {
		t.Errorf("after Stop: %v", got)
	}
	if err := os.WriteFile(filepath.Join(r.dir("meals"), "settings.json"), []byte(`{"token":"x"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := r.m.Remove("meals", true); err != nil {
		t.Fatal(err)
	}
	if got := api.saw(); got[len(got)-1] != "removed meals" {
		t.Errorf("after Remove: %v", got)
	}
	if !exists(filepath.Join(r.dir("meals"), "settings.json")) {
		t.Error("its settings went with a removal that kept its state")
	}
	if _, ok := r.m.APIGrantOf("meals"); ok {
		t.Error("a removed add-on still has a grant")
	}
}

// What is granted is what the manifest uses and the owner agreed to: an add-on whose record agreed to
// less (installed before an update that asked for more, say) is not granted the rest.
func TestTheGrantIsWhatWasAgreedTo(t *testing.T) {
	g := APIGrant{Man: catalog.Manifest{Capabilities: []string{"speak", "timers", "media"}},
		Agreed: catalog.Asks{Capabilities: []string{"timers", "speak"}}}
	if got := g.Capabilities(); !slices.Equal(got, []string{"speak", "timers"}) {
		t.Errorf("granted %v", got)
	}
}
