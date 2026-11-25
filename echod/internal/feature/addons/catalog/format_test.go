package catalog

import (
	"encoding/json"
	"strings"
	"testing"
)

// futureVersion is a version as a newer TECHO5's catalog might write it: requiring a feature this build
// does not know, and carrying a field only that feature explains.
const futureVersion = `{"version":"0.3.0","requires":["files","sandbox"],"sandbox":{"profile":"strict"},
 "min_techo5":{"show":"v1.7.0"},"binaries":{},"memory_mb":0}`

// withVersion is c written out with raws added as the last versions of its add-on at index i.
func withVersion(t *testing.T, c Catalog, i int, raws ...string) []byte {
	t.Helper()
	b, _ := json.Marshal(c)
	var top map[string]any
	_ = json.Unmarshal(b, &top)
	addons := top["addons"].([]any)
	a := addons[i].(map[string]any)
	for _, raw := range raws {
		var v any
		if err := json.Unmarshal([]byte(raw), &v); err != nil {
			t.Fatal(err)
		}
		a["versions"] = append(a["versions"].([]any), v)
	}
	out, _ := json.Marshal(top)
	return out
}

// A format 2 version that requires only what this build knows is read as any other; one that requires
// something it does not know is skipped, fields it alone has and all, and the catalog is taken.
func TestAVersionForANewerTECHO5IsSkippedNotRefused(t *testing.T) {
	c := valid()
	c.Addons = append(c.Addons, netbird())
	b := withVersion(t, c, 1, futureVersion)
	got, err := Parse(b)
	if err != nil {
		t.Fatalf("refused: %v", err)
	}
	a, _ := got.Find("techo5-netbird")
	if len(a.Versions) != 1 || a.Versions[0].Version != "0.2.0" || len(a.Versions[0].Requires) != 1 {
		t.Errorf("versions read: %+v", a.Versions)
	}
	if len(a.Skipped) != 1 || a.Skipped[0] != "0.3.0" || a.NeedsNewer() {
		t.Errorf("skipped %v, needs newer %v", a.Skipped, a.NeedsNewer())
	}
	if p, _ := got.Find("techo5-plex"); len(p.Versions) != 1 || len(p.Skipped) != 0 {
		t.Errorf("the other add-on: %+v", p)
	}

	// The signing command reads every version it signs, so it refuses what a device would skip.
	if _, err := ParseKnown(b); err == nil || !strings.Contains(err.Error(), "sandbox") {
		t.Errorf("ParseKnown: %v", err)
	}
}

// An add-on with nothing but versions for a newer TECHO5 is kept, as needing one, rather than refused
// for having no versions or left out as though it were not there.
func TestAnAddonForANewerTECHO5IsKept(t *testing.T) {
	c := valid()
	n := netbird()
	n.Versions = nil
	b, _ := json.Marshal(append(c.Addons, n))
	raw := strings.Replace(string(b), `"versions":null`, `"versions":[`+futureVersion+`]`, 1)
	doc := `{"format":2,"serial":3,"addons":` + raw + `}`
	got, err := Parse([]byte(doc))
	if err != nil {
		t.Fatalf("refused: %v", err)
	}
	a, ok := got.Find("techo5-netbird")
	if !ok || !a.NeedsNewer() || len(a.Versions) != 0 {
		t.Fatalf("read as %+v", a)
	}
	if _, ok := a.Latest(); ok {
		t.Error("a version for a newer TECHO5 is listed")
	}
}

// Requires is the only way past the strict reading. A version this build reads is read strictly, a
// field it does not know refusing the catalog as before; the whole document, skipped versions too, is
// held to one key once, in lowercase, with nothing after it; and the format 1 file knows no requires.
func TestTheStrictReadingStays(t *testing.T) {
	c := valid()
	c.Addons = append(c.Addons, netbird())
	b, _ := json.Marshal(c)
	good := string(b)
	future := string(withVersion(t, c, 1, futureVersion))
	for name, bad := range map[string]string{
		"an unknown field in a version read":                  strings.Replace(good, `"requires":["files"],`, `"requires":["files"],"sandbox":{},`, 1),
		"an unknown field in a version that requires nothing": strings.Replace(good, `"version":"0.1.0",`, `"version":"0.1.0","sandbox":{},`, 1),
		"an unknown field in an add-on":                       strings.Replace(good, `"id":"techo5-plex",`, `"id":"techo5-plex","sandbox":{},`, 1),
		"an unknown field at the top":                         strings.Replace(good, `"serial":3,`, `"serial":3,"sandbox":{},`, 1),
		"a key twice in a skipped version":                    strings.Replace(future, `"profile":"strict"`, `"profile":"strict","profile":"loose"`, 1),
		"a capital in a skipped version":                      strings.Replace(future, `"sandbox":{`, `"Sandbox":{`, 1),
		"requires in a capital":                               strings.Replace(future, `"requires":["files","sandbox"]`, `"Requires":["files","sandbox"]`, 1),
		"requires twice":                                      strings.Replace(future, `"requires":["files","sandbox"]`, `"requires":["files"],"requires":["files","sandbox"]`, 1),
		"requires not a list of names":                        strings.Replace(future, `"requires":["files","sandbox"]`, `"requires":"sandbox"`, 1),
		"something after the value":                           future + `{}`,
		"a format 1 version that requires":                    strings.Replace(strings.Replace(good, `"format":2`, `"format":1`, 1), `"version":"0.1.0",`, `"version":"0.1.0","requires":["files"],`, 1),
		"a format 1 version that skips":                       strings.Replace(future, `"format":2`, `"format":1`, 1),
		"format 3":                                            strings.Replace(good, `"format":2`, `"format":3`, 1),
		"a skipped version not a version":                     strings.Replace(future, `"version":"0.3.0"`, `"version":"next"`, 1),
		"a skipped version a read one's number":               strings.Replace(future, `"version":"0.3.0"`, `"version":"0.2.0"`, 1),
		"a skipped version twice":                             string(withVersion(t, c, 1, futureVersion, futureVersion)),
	} {
		if bad == good || bad == future {
			t.Fatalf("%s: the replacement did not apply", name)
		}
		if _, err := Parse([]byte(bad)); err == nil {
			t.Errorf("%s: taken", name)
		}
	}
}

// A version names what it uses: extra files without "files" in requires are refused, as a device that
// did not know them would read the version without them; requiring it without bringing any is refused,
// as it would keep the version from format 1 devices for nothing; and format 1 carries neither.
func TestAVersionRequiresWhatItUses(t *testing.T) {
	for name, set := range map[string]func(v *Version){
		"files without requiring them": func(v *Version) { v.Requires = nil },
		"requiring files with none":    func(v *Version) { v.Files = nil },
		"requiring files twice":        func(v *Version) { v.Requires = []string{"files", "files"} },
		"requiring something unknown":  func(v *Version) { v.Requires = []string{"files", "sandbox"} },
	} {
		v := netbirdVersion()
		set(&v)
		if err := v.valid("x", Format); err == nil {
			t.Errorf("%s: taken", name)
		}
	}
	if err := netbirdVersion().valid("x", FormatV1); err == nil {
		t.Error("extra files in format 1: taken")
	}
	plain := plexVersion("0.1.0")
	if err := plain.valid("x", FormatV1); err != nil {
		t.Errorf("a plain version in format 1: %v", err)
	}
}

// The format 1 copy is what a format 1 device can read of the catalog: every version that requires
// nothing, every add-on with one left, the same serial and expiry, and not a key it does not know.
func TestTheFormat1CopyLeavesOutWhatNeedsMore(t *testing.T) {
	c := valid()
	n := netbird()
	n.Versions = append(n.Versions, plexVersion("0.1.0"))
	only := netbird()
	only.ID = "techo5-only-files"
	c.Addons = append(c.Addons, n, only)
	if err := c.Valid(); err != nil {
		t.Fatal(err)
	}
	v1 := c.V1()
	if v1.Format != FormatV1 || v1.Serial != c.Serial || !v1.Expires.Equal(c.Expires) {
		t.Errorf("the head: %+v", v1)
	}
	if _, ok := v1.Find("techo5-only-files"); ok {
		t.Error("an add-on with nothing a format 1 device reads is in its copy")
	}
	if a, ok := v1.Find("techo5-netbird"); !ok || len(a.Versions) != 1 || a.Versions[0].Version != "0.1.0" {
		t.Errorf("the add-on with one plain version: %+v", a)
	}
	b, _ := json.Marshal(v1)
	if strings.Contains(string(b), `"requires"`) || strings.Contains(string(b), `"files"`) {
		t.Errorf("the format 1 copy has a key format 1 does not know: %s", b)
	}
	got, err := Parse(b)
	if err != nil || got.Format != FormatV1 {
		t.Errorf("the format 1 copy does not read back as format 1: %v", err)
	}
	// The source is left as it was.
	if a, _ := c.Find("techo5-netbird"); len(a.Versions) != 2 {
		t.Error("V1 changed the catalog it was made from")
	}
	// Nothing left is still a catalog, of no add-ons rather than of null.
	empty := Catalog{Format: Format, Serial: 1, Addons: []Addon{only}}.V1()
	if b, _ := json.Marshal(empty); !strings.Contains(string(b), `"addons":[]`) {
		t.Errorf("an empty format 1 copy: %s", b)
	}
}

// A format 1 catalog, as published before format 2, still reads.
func TestAFormat1CatalogStillReads(t *testing.T) {
	c := valid()
	c.Format = FormatV1
	b, _ := json.Marshal(c)
	if _, err := Parse(b); err != nil {
		t.Fatal(err)
	}
}
