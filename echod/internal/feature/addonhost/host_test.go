package addonhost

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/feature/addons"
	"github.com/HuskerMinion/techo5/echod/internal/feature/addons/catalog"
	"github.com/HuskerMinion/techo5/echod/internal/feature/timer"
	"github.com/HuskerMinion/techo5/echod/internal/lib/keepawake"
	"github.com/HuskerMinion/techo5/echod/pkg/addonapi"
)

// mealsManifest is what Meals (#720) would declare.
func mealsManifest() catalog.Manifest {
	return catalog.Manifest{ID: "meals", Version: "0.1.0", Command: []string{"meals"}, MemoryMB: 20, API: 1,
		Capabilities: []string{"commands", "events", "timers", "keepawake", "settings", "entities"},
		Commands: []addonapi.Command{
			{Name: "next"},
			{Name: "step", Args: map[string]addonapi.Arg{"to": {Type: addonapi.TypeString, Max: 4, Required: true}}},
		},
		Settings: &addonapi.Settings{Test: true, Fields: []addonapi.Setting{
			{Key: "address", Label: "Mealie address", Type: addonapi.TypeString},
			{Key: "token", Label: "API token", Type: addonapi.TypeSecret},
			{Key: "loud", Label: "Read aloud", Type: addonapi.TypeBool},
		}},
		Entities: []addonapi.Entity{
			{Domain: addonapi.DomainSensor, ID: "step", Name: "Step", Text: true},
			{Domain: addonapi.DomainSwitch, ID: "cooking", Name: "Cooking"},
			{Domain: addonapi.DomainNumber, ID: "servings", Name: "Servings", Min: 1, Max: 12, Step: 1},
		},
	}
}

type hostRig struct {
	t    *testing.T
	h    *Host
	root string
	g    addons.APIGrant

	mu      sync.Mutex
	removed bool
}

func newHostRig(t *testing.T) *hostRig {
	t.Helper()
	dir, err := os.MkdirTemp("", "ahost")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	root := filepath.Join(dir, "addons")
	if err := os.MkdirAll(filepath.Join(root, "meals", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	r := &hostRig{t: t, root: root}
	r.h = newHost(filepath.Join(dir, "run"), func() string { return root }, func(uint32) bool { return true }, &device{})
	r.h.srv.Chown = func(string, int) error { return nil }
	r.g = addons.APIGrant{ID: "meals", UID: uint32(os.Getuid()), Man: mealsManifest(),
		Agreed: catalog.Asks{Capabilities: mealsManifest().Capabilities}, BinDir: filepath.Join(root, "meals", "bin")}
	r.h.grants = func() []addons.APIGrant {
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.removed {
			return nil
		}
		return []addons.APIGrant{r.g}
	}
	r.h.names = func() map[string]string { return map[string]string{"meals": "Meals"} }
	t.Cleanup(func() { r.h.Close("meals") })
	return r
}

func form(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

// Settings are written only when Save changes them, root's and 0600, holding only what the schema
// declares; a secret left empty keeps the one saved, and its Clear box clears it.
func TestSettingsAreWrittenOnlyOnChange(t *testing.T) {
	r := newHostRig(t)
	path := filepath.Join(r.root, "meals", settingsFile)
	changed, err := r.h.SaveSettings("meals", form(map[string]string{"set_address": "http://mealie.local", "secret_token": "s3cret", "set_loud": "yes"}))
	if err != nil || !changed {
		t.Fatalf("the first save: %v %v", changed, err)
	}
	fi, err := os.Stat(path)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("the file: %v %v", fi, err)
	}
	before := fi.ModTime()
	// The same again, the secret left empty as the page leaves it: nothing is written.
	changed, err = r.h.SaveSettings("meals", form(map[string]string{"set_address": "http://mealie.local", "set_loud": "yes"}))
	if err != nil || changed {
		t.Errorf("saving the same: %v %v", changed, err)
	}
	if fi, _ := os.Stat(path); !fi.ModTime().Equal(before) {
		t.Error("the file was written for no change")
	}
	_, values, _ := r.h.Settings("meals")
	if values["token"] != "s3cret" || values["loud"] != true {
		t.Errorf("values %v", values)
	}
	// A box left unticked is off; the Clear box clears the secret.
	if _, err := r.h.SaveSettings("meals", form(map[string]string{"set_address": "http://mealie.local", "clear_token": "yes"})); err != nil {
		t.Fatal(err)
	}
	_, values, _ = r.h.Settings("meals")
	if values["token"] != "" || values["loud"] != false {
		t.Errorf("after clearing: %v", values)
	}
	// What the schema does not declare is not handed on, whatever the file says.
	if err := os.WriteFile(path, []byte(`{"address":"x","old":"y","loud":"not a bool"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, values, _ = r.h.Settings("meals")
	if len(values) != 1 || values["address"] != "x" {
		t.Errorf("read %v", values)
	}
	if _, err := r.h.SaveSettings("meals", form(map[string]string{"set_address": strings.Repeat("x", addonapi.MaxStringLength+1)})); err == nil {
		t.Error("a setting past its length was saved")
	}
	if _, err := r.h.SaveSettings("other", form(nil)); err == nil {
		t.Error("an add-on without settings saved some")
	}
}

// An add-on not granted settings has none to draw, whatever its manifest declares.
func TestSettingsNeedTheCapability(t *testing.T) {
	r := newHostRig(t)
	r.g.Agreed.Capabilities = []string{"commands"}
	if _, _, ok := r.h.Settings("meals"); ok {
		t.Error("settings without the capability agreed to")
	}
}

func TestEntitiesAreTheAddonsOwn(t *testing.T) {
	r := newHostRig(t)
	var ids []string
	for _, e := range r.h.Entities() {
		ids = append(ids, e.Object())
	}
	// The command that needs an argument has no button; every object id is under the add-on's prefix.
	want := []string{"addon_meals_next", "addon_meals_step", "addon_meals_cooking", "addon_meals_servings"}
	if !slices.Equal(ids, want) {
		t.Errorf("entities %v, want %v", ids, want)
	}
	// Removed, they are not listed again.
	r.mu.Lock()
	r.removed = true
	r.mu.Unlock()
	if got := r.h.Entities(); len(got) != 0 {
		t.Errorf("a removed add-on's entities: %d", len(got))
	}
	// Without the capability, none.
	r.mu.Lock()
	r.removed = false
	r.g.Agreed.Capabilities = []string{"commands"}
	r.mu.Unlock()
	r.h.entities = map[string]*addonEntities{}
	if got := r.h.Entities(); len(got) != 1 || got[0].Object() != "addon_meals_next" {
		t.Errorf("with commands alone: %d", len(got))
	}
}

func TestFireKeepsWhoseEventItIs(t *testing.T) {
	var got []component.Event
	stop := component.Fire.Listen(func(e component.Event) { got = append(got, e) })
	defer stop()
	d := &device{}
	if err := d.Fire("meals", "step_changed", map[string]string{"step": "3"}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != AddonEvent || got[0].Data["addon"] != "meals" || got[0].Data["name"] != "step_changed" || got[0].Data["step"] != "3" {
		t.Errorf("fired %+v", got)
	}
	if err := d.Fire("meals", "x", map[string]string{"addon": "other"}); addonapi.CodeOf(err) != addonapi.CodeBadRequest {
		t.Errorf("an event posing as another add-on's: %v", err)
	}
}

func TestResample(t *testing.T) {
	in := []int16{0, 100, 200, 300}
	if got := resample(in, 16000, 48000); len(got) != 12 || got[3] != 100 {
		t.Errorf("up: %v", got)
	}
	if got := resample(in, 48000, 48000); len(got) != 4 {
		t.Errorf("same: %v", got)
	}
}

// The whole of it, end to end: an add-on's client against the real server and this device's side,
// with the real timers and keep-awake. Its commands answer Home Assistant's action, its timer is one of
// the device's own, its hold keeps the screen in use until its connection drops, its entity's state
// reaches Home Assistant's entity, its settings reach it when saved, and its removal takes its timers.
func TestEndToEnd(t *testing.T) {
	r := newHostRig(t)
	if _, err := r.h.SaveSettings("meals", form(map[string]string{"set_address": "http://mealie.local", "secret_token": "s3cret"})); err != nil {
		t.Fatal(err)
	}
	path, err := r.h.Open(r.g)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := addonapi.Dial(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	w := c.Welcome()
	if w.Addon != "meals" || w.Settings["token"] != "s3cret" || !slices.Contains(w.Capabilities, "timers") || w.Device.Family != addons.Family() {
		t.Errorf("welcome %+v", w)
	}

	// The add-on answers its commands, and records what else it is told.
	var mu sync.Mutex
	var told []string
	go func() {
		for ev := range c.Events() {
			mu.Lock()
			told = append(told, ev.Event)
			mu.Unlock()
			if ev.Event == addonapi.EventCommand {
				var ce addonapi.CommandEvent
				_ = ev.Decode(&ce)
				if ce.Call != 0 {
					_ = c.ReplyCommand(context.Background(), ce.Call, true, "Step two: stir the "+ce.Command)
				}
			}
		}
	}()
	action := r.h.Actions()[0]
	call, _ := esphome.NewCall(map[string]any{"addon": "meals", "command": "next", "args": ""})
	got, err := action.Run(call)
	if err != nil || got.(map[string]string)["text"] != "Step two: stir the next" {
		t.Errorf("addon_command: %v %v", got, err)
	}
	call, _ = esphome.NewCall(map[string]any{"addon": "meals", "command": "step", "args": `{"to":"12345"}`})
	if _, err := action.Run(call); err == nil {
		t.Error("addon_command with an argument past its length")
	}
	call, _ = esphome.NewCall(map[string]any{"addon": "nobody", "command": "next", "args": ""})
	if _, err := action.Run(call); err == nil || !strings.Contains(err.Error(), "not running") {
		t.Errorf("addon_command to an add-on not running: %v", err)
	}

	// Its timer is one of the device's own, which it alone can cancel.
	id, err := c.StartTimer(ctx, "rice", 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { timer.Get().CancelAllOwned("meals") })
	if list := timer.Get().OwnedBy("meals", time.Now()); len(list) != 1 || list[0].ID != id {
		t.Errorf("the device's timers: %+v", list)
	}
	other := timer.Get().Start("pasta", time.Hour)
	t.Cleanup(func() { timer.Get().Cancel(other) })
	if err := c.CancelTimer(ctx, other); addonapi.CodeOf(err) != addonapi.CodeBadRequest {
		t.Errorf("canceling the device's own timer: %v", err)
	}
	// Canceled on the device, the add-on is told.
	timer.Get().Cancel(id)
	eventuallyTrue(t, "the cancel to be told", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return slices.Contains(told, addonapi.EventTimer)
	})

	// Its hold keeps the screen in use, until its connection drops.
	if _, err := c.HoldAwake(ctx, time.Minute); err != nil {
		t.Fatal(err)
	}
	if !keepawake.Held() {
		t.Error("the hold did not reach the screen")
	}

	// Its entity's state reaches Home Assistant's.
	r.h.Entities()
	if err := c.SetEntity(ctx, "step", "Chop the onions"); err != nil {
		t.Fatal(err)
	}
	if ts := r.h.entitiesOf("meals").byID["step"].(*esphome.TextSensor); ts.Get() != "Chop the onions" {
		t.Errorf("the sensor says %q", ts.Get())
	}
	if err := c.SetEntity(ctx, "servings", 40.0); addonapi.CodeOf(err) != addonapi.CodeBadRequest {
		t.Errorf("a number past its bounds: %v", err)
	}
	// A switch turned in Home Assistant is the add-on's to carry out: it is told.
	r.h.entitiesOf("meals").byID["cooking"].(*esphome.Switch).OnCommand(true)
	eventuallyTrue(t, "the switch to be told", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return slices.Contains(told, addonapi.EventEntityCommand)
	})

	// Saved settings reach it.
	if _, err := r.h.SaveSettings("meals", form(map[string]string{"set_address": "http://other"})); err != nil {
		t.Fatal(err)
	}
	eventuallyTrue(t, "the settings to be told", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return slices.Contains(told, addonapi.EventSettings)
	})

	// What it was not granted is refused.
	if _, err := c.Speak(ctx, "hello"); addonapi.CodeOf(err) != addonapi.CodeDenied {
		t.Errorf("speak without the capability: %v", err)
	}

	// Its connection drops: the hold goes with it, the timer does not.
	if _, err := c.StartTimer(ctx, "beans", time.Hour); err != nil {
		t.Fatal(err)
	}
	c.Close()
	eventuallyTrue(t, "the hold to end with the connection", func() bool { return !keepawake.Held() })
	if len(timer.Get().OwnedBy("meals", time.Now())) != 1 {
		t.Error("a dropped connection took the add-on's timer")
	}
	// Removed, its timers go, and its socket.
	r.h.Removed("meals")
	if len(timer.Get().OwnedBy("meals", time.Now())) != 0 {
		t.Error("a removed add-on's timer is left")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("the socket outlived the add-on: %v", err)
	}
}

func eventuallyTrue(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(2 * time.Millisecond) {
		if ok() {
			return
		}
	}
	t.Fatalf("waited too long for %s", what)
}

// An add-on's audio is not fetched from the device: an address naming it by number is refused before
// anything is fetched.
func TestAddonAudioIsNotFromTheDevice(t *testing.T) {
	d := &device{host: newHostRig(t).h}
	for _, u := range []string{"http://127.0.0.1:8181/x.mp3", "http://[::1]/x.mp3", "http://169.254.1.1/x", "http://a b/x"} {
		if err := d.Media("meals", addonapi.OpMediaPlay, addonapi.MediaPlay{URL: u, Kind: addonapi.MediaAudio}); addonapi.CodeOf(err) != addonapi.CodeBadRequest {
			t.Errorf("%s: %v", u, err)
		}
	}
}

// An add-on's own sound is never louder than -6 dBFS before the device's volume.
func TestAddonSoundsAreCapped(t *testing.T) {
	loud := []int16{32767, -32768, 100}
	got := capPeak(loud, maxPeak)
	for _, s := range got {
		if s > 16384 || s < -16384 {
			t.Errorf("a sample at %d", s)
		}
	}
	quiet := []int16{100, -200}
	if got := capPeak(quiet, maxPeak); got[1] != -200 {
		t.Error("a quiet sound was changed")
	}
}
