package catalog

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// A file that could be read two ways is refused: a key given twice, a key in another case (which Go
// would take for the same field), a key no field has, and anything after the value.
func TestAmbiguousJSONIsRefused(t *testing.T) {
	good := `{"id":"techo5-plex","version":"0.1.0","command":["techo5-plex"],"ports":[],"root":false,"memory_mb":40}`
	if _, err := ParseManifest([]byte(good)); err != nil {
		t.Fatal(err)
	}
	for name, bad := range map[string]string{
		"twice":        strings.Replace(good, `"root":false`, `"root":false,"root":true`, 1),
		"in a capital": strings.Replace(good, `"root":false`, `"root":false,"Root":true`, 1),
		"capital only": strings.Replace(good, `"root":false`, `"ROOT":false`, 1),
		"unknown":      strings.Replace(good, `"root":false`, `"root":false,"setuid":true`, 1),
		"nested twice": strings.Replace(good, `"ports":[]`, `"ports":[{"proto":"tcp","port":40000,"port":40001,"why":"x"}]`, 1),
		"trailing":     good + `{"root":true}`,
		"long s":       strings.Replace(good, `"ports":[]`, `"ports":[],"portſ":[{"proto":"tcp","port":40000,"why":"x"}]`, 1),
		"long s alone": strings.Replace(good, `"ports":[]`, `"portſ":[]`, 1),
		"kelvin":       strings.Replace(good, `"root":false`, `"root":false,"Key":1`, 1),
		"a dash":       strings.Replace(good, `"root":false`, `"root":false,"x-y":1`, 1),
	} {
		if _, err := ParseManifest([]byte(bad)); err == nil {
			t.Errorf("%s: taken", name)
		}
	}
	c, _ := json.Marshal(valid())
	twice := strings.Replace(string(c), `"serial":3`, `"serial":3,"serial":9`, 1)
	if _, err := Parse([]byte(twice)); err == nil {
		t.Error("a catalog with its serial twice was taken")
	}
}

func TestExpired(t *testing.T) {
	now := time.Date(2026, 11, 24, 0, 0, 0, 0, time.UTC)
	c := valid()
	if !c.Expired(now) {
		t.Error("a catalog never stamped is not expired")
	}
	c.Expires = now.Add(time.Hour)
	if c.Expired(now) || !c.Expired(now.Add(time.Hour)) {
		t.Error("the expiry is not where it was set")
	}
}

// What the card showed is tied to the box: a change to the asks, the author or the version changes it.
func TestShownChangesWithWhatWasShown(t *testing.T) {
	a, v := plex(), plexVersion("0.1.0")
	base := Shown(a, v)
	if Shown(a, v) != base {
		t.Fatal("not stable")
	}
	w := v
	w.Ports = append(w.Ports, Port{Proto: "tcp", Port: 40000, Why: "x"})
	b := a
	b.Author = "Someone Else"
	r := v
	r.Root = true
	for name, got := range map[string]string{"ports": Shown(a, w), "author": Shown(b, v), "root": Shown(a, r)} {
		if got == base {
			t.Errorf("a change of %s left the digest the same", name)
		}
	}
}

// A version once published stays as it was, except that it may be withdrawn.
func TestUnchanged(t *testing.T) {
	prev := valid()
	same := valid()
	same.Serial++
	same.Addons[0].Versions[0].Todo = "a new note"
	same.Addons[0].Versions = append(same.Addons[0].Versions, plexVersion("0.2.0"))
	if err := Unchanged(prev, same); err != nil {
		t.Errorf("adding a version: %v", err)
	}
	withdrawn := valid()
	withdrawn.Addons[0].Versions[0].Withdrawn = "it broke things"
	if err := Unchanged(prev, withdrawn); err != nil {
		t.Errorf("withdrawing a version: %v", err)
	}
	for name, spoil := range map[string]func(c *Catalog){
		"a port":       func(c *Catalog) { c.Addons[0].Versions[0].Ports[0].Port = 40000 },
		"a hash":       func(c *Catalog) { c.Addons[0].Versions[0].Binaries["arm"] = bin("other") },
		"root":         func(c *Catalog) { c.Addons[0].Versions[0].Root = true },
		"the author":   func(c *Catalog) { c.Addons[0].Author = "Someone Else" },
		"gone version": func(c *Catalog) { c.Addons[0].Versions = []Version{plexVersion("0.2.0")} },
		"gone add-on":  func(c *Catalog) { c.Addons = nil },
	} {
		next := valid()
		spoil(&next)
		if err := Unchanged(prev, next); err == nil {
			t.Errorf("changing %s passed", name)
		}
	}
	if err := Unchanged(withdrawn, prev); err == nil {
		t.Error("taking a withdrawal back passed")
	}
}
