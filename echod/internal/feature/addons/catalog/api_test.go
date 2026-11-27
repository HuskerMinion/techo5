package catalog

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/update"
)

// mealsVersion is a version that speaks the add-on API, as Meals (#720) would be listed.
func mealsVersion(v string) Version {
	m := bin("addon.json")
	return Version{
		Version:      v,
		Requires:     []string{"api1"},
		MinTECHO5:    map[string]string{"show": "v1.7.0"},
		Binaries:     map[string]update.Binary{"arm": bin("arm")},
		Manifest:     &m,
		Ports:        []Port{},
		MemoryMB:     20,
		DiskMB:       2,
		API:          1,
		Capabilities: []string{"commands", "speak", "timers", "settings", "entities"},
		Commands:     []string{"next", "start_cooking"},
		Entities:     []string{"sensor.step"},
	}
}

func meals() Addon {
	return Addon{ID: "meals", Name: "Meals", Author: "An Author", Summary: "Cooks along with Mealie.",
		Repo: "https://github.com/example/meals", Versions: []Version{mealsVersion("0.1.0")}}
}

const mealsManifest = `{"id":"meals","version":"0.1.0","command":["meals"],"ports":[],"root":false,"memory_mb":20,
 "api":1,"capabilities":["commands","speak","timers","settings","entities"],
 "commands":[{"name":"next"},{"name":"start_cooking","args":{"recipe":{"type":"string","max":64}}}],
 "settings":{"fields":[{"key":"address","label":"Mealie address","type":"string"},{"key":"token","label":"API token","type":"secret"}],"test":true},
 "entities":[{"domain":"sensor","id":"step","name":"Step","text":true}]}`

func TestAnAPIVersionIsTaken(t *testing.T) {
	c := valid()
	c.Addons = append(c.Addons, meals())
	b, _ := json.Marshal(c)
	got, err := ParseKnown(b)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := got.Find("meals")
	if v := a.Versions[0]; v.API != 1 || len(v.Capabilities) != 5 || v.Commands[1] != "start_cooking" {
		t.Errorf("read as %+v", v)
	}
	// A format 1 device is not given it.
	if _, ok := got.V1().Find("meals"); ok {
		t.Error("an API version is in the format 1 copy")
	}
}

// A device that does not speak api1 skips the version rather than refusing the catalog, as it skips
// any other feature it does not know.
func TestAnAPIVersionIsSkippedWhereAPI1IsUnknown(t *testing.T) {
	was := Features
	Features = []string{"files"}
	defer func() { Features = was }()
	c := valid()
	c.Addons = append(c.Addons, meals())
	b, _ := json.Marshal(c)
	got, err := Parse(b)
	if err != nil {
		t.Fatalf("refused: %v", err)
	}
	if a, _ := got.Find("meals"); !a.NeedsNewer() {
		t.Errorf("read as %+v", a)
	}
}

func TestABadAPIVersionRefusesTheCatalog(t *testing.T) {
	for name, change := range map[string]func(v *Version){
		"no requires":       func(v *Version) { v.Requires = nil },
		"level 2":           func(v *Version) { v.API = 2 },
		"no level":          func(v *Version) { v.API = 0 },
		"unknown cap":       func(v *Version) { v.Capabilities = append(v.Capabilities, "ha.call") },
		"cap twice":         func(v *Version) { v.Capabilities = append(v.Capabilities, "speak") },
		"no caps":           func(v *Version) { v.Capabilities = nil; v.Commands = nil; v.Entities = nil },
		"commands, no cap":  func(v *Version) { v.Capabilities = []string{"speak", "entities"} },
		"cap, no commands":  func(v *Version) { v.Commands = nil },
		"entities, no cap":  func(v *Version) { v.Capabilities = []string{"commands"} },
		"bad entity domain": func(v *Version) { v.Entities = []string{"light.x"} },
		"bad entity":        func(v *Version) { v.Entities = []string{"sensor"} },
		"bad command":       func(v *Version) { v.Commands = []string{"Next"} },
		"root":              func(v *Version) { v.Root = true },
	} {
		c := valid()
		a := meals()
		change(&a.Versions[0])
		c.Addons = append(c.Addons, a)
		b, _ := json.Marshal(c)
		if _, err := ParseKnown(b); err == nil {
			t.Errorf("%s: taken", name)
		}
	}
}

// The screen is stage 2: a version with the screen capability requires screen1, so a device before it
// skips the version, and names no Dot, which has no screen to draw it on.
func TestAScreenVersion(t *testing.T) {
	screen := func(v *Version) {
		v.Capabilities = append(v.Capabilities, "screen")
		v.Requires = append(v.Requires, "screen1")
		v.MinTECHO5["spot"] = "v1.7.0"
	}
	c := valid()
	a := meals()
	screen(&a.Versions[0])
	c.Addons = append(c.Addons, a)
	b, _ := json.Marshal(c)
	if _, err := ParseKnown(b); err != nil {
		t.Fatalf("a screen version: %v", err)
	}
	for name, change := range map[string]func(v *Version){
		"no screen1": func(v *Version) { v.Requires = []string{"api1"} },
		"screen1, no screen": func(v *Version) {
			v.Capabilities = slices.DeleteFunc(v.Capabilities, func(c string) bool { return c == "screen" })
		},
		"the dot":              func(v *Version) { v.MinTECHO5["dot"] = "v1.7.0" },
		"screen1 without api1": func(v *Version) { v.Requires = []string{"screen1"} },
	} {
		c := valid()
		a := meals()
		screen(&a.Versions[0])
		change(&a.Versions[0])
		c.Addons = append(c.Addons, a)
		b, _ := json.Marshal(c)
		if _, err := ParseKnown(b); err == nil {
			t.Errorf("%s: taken", name)
		}
	}
	// A device before stage 2 skips it, rather than refusing the catalog.
	was := Features
	Features = []string{"files", "api1"}
	defer func() { Features = was }()
	got, err := Parse(b)
	if err != nil {
		t.Fatalf("refused where screen1 is unknown: %v", err)
	}
	if a, _ := got.Find("meals"); !a.NeedsNewer() {
		t.Errorf("read where screen1 is unknown as %+v", a)
	}
}

func TestAnAPIManifest(t *testing.T) {
	m, err := ParseManifest([]byte(mealsManifest))
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Matches("meals", mealsVersion("0.1.0"), "arm"); err != nil {
		t.Errorf("matches its entry: %v", err)
	}
	for name, change := range map[string]func(v *Version){
		"another cap":     func(v *Version) { v.Capabilities = append(v.Capabilities, "sound") },
		"another command": func(v *Version) { v.Commands = append(v.Commands, "back") },
		"another entity":  func(v *Version) { v.Entities = append(v.Entities, "switch.cooking") },
		"no api":          func(v *Version) { v.API = 0 },
	} {
		v := mealsVersion("0.1.0")
		change(&v)
		if err := m.Matches("meals", v, "arm"); err == nil {
			t.Errorf("%s: matched", name)
		}
	}
	for name, bad := range map[string]string{
		"no api":           strings.Replace(mealsManifest, `"api":1,`, ``, 1),
		"api 2":            strings.Replace(mealsManifest, `"api":1`, `"api":2`, 1),
		"root":             strings.Replace(mealsManifest, `"root":false`, `"root":true`, 1),
		"unknown cap":      strings.Replace(mealsManifest, `"commands","speak"`, `"commands","ha.call"`, 1),
		"settings, no cap": strings.Replace(mealsManifest, `"settings","entities"]`, `"entities"]`, 1),
		"bad arg":          strings.Replace(mealsManifest, `"type":"string","max":64`, `"type":"list"`, 1),
		"unknown key":      strings.Replace(mealsManifest, `"text":true`, `"text":true,"color":"red"`, 1),
		"bad setting":      strings.Replace(mealsManifest, `"type":"secret"`, `"type":"password"`, 1),
		"name both":        strings.Replace(mealsManifest, `{"name":"next"}`, `{"name":"step"}`, 1),
	} {
		if _, err := ParseManifest([]byte(bad)); err == nil {
			t.Errorf("%s: taken", name)
		}
	}
}

// An update that adds a capability, a command or an entity asks again; one that adds none does not.
func TestMoreCoversTheAPI(t *testing.T) {
	before := mealsVersion("0.1.0").Asks("arm")
	if more := before.More(before); len(more) != 0 {
		t.Errorf("the same: %v", more)
	}
	v := mealsVersion("0.2.0")
	v.Capabilities = append(v.Capabilities, "media")
	v.Commands = append(v.Commands, "back")
	v.Entities = append(v.Entities, "switch.cooking")
	more := v.Asks("arm").More(before)
	if !slices.Equal(more, []string{"the media capability", "the command back", "the entity switch.cooking"}) {
		t.Errorf("more: %v", more)
	}
	fewer := mealsVersion("0.2.0")
	fewer.Commands = fewer.Commands[:1]
	if more := fewer.Asks("arm").More(before); len(more) != 0 {
		t.Errorf("fewer: %v", more)
	}
	// One installed before the API asks for every capability of an update that brings it.
	if more := mealsVersion("0.2.0").Asks("arm").More(Asks{}); len(more) != 8 {
		t.Errorf("from nothing: %v", more)
	}
}

// The digest the box agrees to covers what a version uses of the API, and a listing without any keeps
// the digest it had before the API.
func TestShownCoversTheAPI(t *testing.T) {
	a, v := meals(), mealsVersion("0.1.0")
	base := Shown(a, v)
	for name, change := range map[string]func(v *Version){
		"cap":     func(v *Version) { v.Capabilities = append(v.Capabilities, "sound") },
		"command": func(v *Version) { v.Commands = append(v.Commands, "back") },
		"entity":  func(v *Version) { v.Entities = append(v.Entities, "switch.x") },
	} {
		w := mealsVersion("0.1.0")
		change(&w)
		if Shown(a, w) == base {
			t.Errorf("a change of %s left the digest the same", name)
		}
	}
	// Without any of it, the digest is the one this version had before the API's fields were added.
	p := plexVersion("0.1.0")
	b, _ := json.Marshal(struct {
		ID, Name, Author, Version string
		Ports                     []Port
		Root                      bool
		MemoryMB, DiskMB          int
		Files                     map[string][]FilePin `json:",omitempty"`
	}{plex().ID, plex().Name, plex().Author, p.Version, p.Ports, p.Root, p.MemoryMB, p.DiskMB, nil})
	sum := sha256.Sum256(b)
	if want := hex.EncodeToString(sum[:16]); Shown(plex(), p) != want {
		t.Errorf("a listing without the API changed digest: %s, was %s", Shown(plex(), p), want)
	}
}

// A screen version may carry an icon the reviewer chose: mdi: and an icon this build draws. Without one
// it reads as before; a wrong one, or one on a version with no page to mark, refuses the catalog. A device
// before stage 2 skips such a version as it does any screen version, a format 1 device is never given it,
// and the icon is a version's, never the add-on's, where every older device would refuse the key.
func TestAnIcon(t *testing.T) {
	withIcon := func(icon string) Catalog {
		c := valid()
		a := meals()
		v := &a.Versions[0]
		v.Capabilities = append(v.Capabilities, "screen")
		v.Requires = append(v.Requires, "screen1")
		v.MinTECHO5["spot"] = "v1.7.0"
		v.Icon = icon
		c.Addons = append(c.Addons, a)
		return c
	}
	for _, icon := range []string{"", "mdi:chef-hat", "mdi:silverware-fork-knife"} {
		b, _ := json.Marshal(withIcon(icon))
		got, err := ParseKnown(b)
		if err != nil {
			t.Fatalf("%q: %v", icon, err)
		}
		a, _ := got.Find("meals")
		if a.Versions[0].Icon != icon {
			t.Errorf("%q read as %q", icon, a.Versions[0].Icon)
		}
		if icon == "" && strings.Contains(string(b), `"icon"`) {
			t.Error("no icon is written as one")
		}
	}
	// An icon this build's font does not have is not one to refuse the catalog for: a device reads it, and
	// draws the initials; mkcatalog, which signs it, refuses it (Publishable).
	unknown, _ := json.Marshal(withIcon("mdi:no-such-icon-at-all"))
	if got, err := ParseKnown(unknown); err != nil {
		t.Errorf("a catalog with an icon the font does not have is refused: %v", err)
	} else if err := got.Publishable(); err == nil || !strings.Contains(err.Error(), "icon") {
		t.Errorf("an icon the font does not have is publishable: %v", err)
	} else if ValidIcon("mdi:no-such-icon-at-all") {
		t.Error("an icon the font does not have is drawn")
	}
	for _, icon := range []string{"chef-hat", "hass:chef-hat", "mdi:", "mdi:Chef-Hat",
		"mdi: chef-hat", "mdi:hass:chef-hat", "mdi:chef-hat ", "https://example.com/icon.png", "mdi:lock\u200b"} {
		b, _ := json.Marshal(withIcon(icon))
		if _, err := ParseKnown(b); err == nil {
			t.Errorf("%q: taken", icon)
		}
	}
	if got, _ := ParseKnown(func() []byte { b, _ := json.Marshal(withIcon("mdi:chef-hat")); return b }()); got.Publishable() != nil {
		t.Errorf("a known icon is not publishable: %v", got.Publishable())
	}
	// Only on a version with the screen: anything else has no tile or page for it.
	c := valid()
	a := meals()
	a.Versions[0].Icon = "mdi:chef-hat"
	a.Versions[0].Requires = append(a.Versions[0].Requires, "screen1")
	c.Addons = append(c.Addons, a)
	if b, _ := json.Marshal(c); func() bool { _, err := ParseKnown(b); return err == nil }() {
		t.Error("an icon without the screen is taken")
	}
	// An icon with the screen still requires screen1, as the screen does.
	c = withIcon("mdi:chef-hat")
	c.Addons[1].Versions[0].Requires = []string{"api1"}
	if b, _ := json.Marshal(c); func() bool { _, err := ParseKnown(b); return err == nil }() {
		t.Error("an icon is taken without screen1")
	}
	// Format 1 cannot carry it, and its copy leaves the version out.
	c = withIcon("mdi:chef-hat")
	if v1 := c.V1(); len(v1.Addons) != 1 || v1.Addons[0].ID == "meals" {
		t.Errorf("the format 1 copy has %+v", v1.Addons)
	}
	b, _ := json.Marshal(c.V1())
	if strings.Contains(string(b), "chef-hat") {
		t.Error("the format 1 copy carries the icon")
	}
	// A device before stage 2 skips the version, icon and all, rather than refusing the catalog.
	b, _ = json.Marshal(withIcon("mdi:chef-hat"))
	was := Features
	Features = []string{"files", "api1"}
	got, err := Parse(b)
	Features = was
	if err != nil {
		t.Fatalf("refused where screen1 is unknown: %v", err)
	}
	if a, _ := got.Find("meals"); !a.NeedsNewer() {
		t.Errorf("read where screen1 is unknown as %+v", a)
	}
	// Beside the add-on's name, where nothing requires it, the key is refused: there every device before
	// it would refuse the whole catalog.
	var raw map[string]any
	_ = json.Unmarshal(b, &raw)
	raw["addons"].([]any)[1].(map[string]any)["icon"] = "mdi:chef-hat"
	b, _ = json.Marshal(raw)
	if _, err := ParseKnown(b); err == nil {
		t.Error("an icon on the add-on, not its version, is taken")
	}
}

// An add-on's name, drawn beside its page and card, is held to words that hide and reorder nothing before
// the catalog is signed; a device reads a catalog with one, and draws the name without them.
func TestANameIsPublishable(t *testing.T) {
	for _, name := range []string{"Meals", "Crème brûlée", "Plex Remote"} {
		c := valid()
		c.Addons[0].Name = name
		if err := c.Publishable(); err != nil {
			t.Errorf("%q: %v", name, err)
		}
	}
	for _, name := range []string{"Me\u202eals", "Pay\u200bpal", "Settings\u2028", "Lock\x07", "A\u0085B", "\ufeffMeals"} {
		c := valid()
		c.Addons[0].Name = name
		b, _ := json.Marshal(c)
		if _, err := ParseKnown(b); err != nil {
			t.Errorf("%q: a device refuses the catalog: %v", name, err)
		}
		if err := c.Publishable(); err == nil {
			t.Errorf("%q is publishable", name)
		}
	}
}
