package catalog

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/update"
)

func extra(name string) File { return File{Name: name, Binary: bin(name)} }

// netbirdVersion is a version that brings an extra file beside each build, as NetBird's entry does:
// its own arm build with NetBird's linux_arm64 archive, keyed by the build that gets it.
func netbirdVersion() Version {
	v := plexVersion("0.2.0")
	v.Ports, v.Root = nil, true
	v.Files = map[string][]File{"arm": {extra("netbird.tar.gz")}, "arm64": {extra("netbird.tar.gz")}}
	v.Requires = []string{"files"}
	return v
}

func netbird() Addon {
	return Addon{ID: "techo5-netbird", Name: "NetBird", Author: "Someone", Summary: "Joins a NetBird network.",
		Repo: "https://github.com/example/techo5-netbird", Versions: []Version{netbirdVersion()}}
}

const netbirdManifest = `{"id":"techo5-netbird","version":"0.2.0","command":["techo5-netbird"],"files":["netbird.tar.gz"],
 "ports":[],"root":true,"memory_mb":40}`

func TestExtraFilesAreTaken(t *testing.T) {
	c := valid()
	c.Addons = append(c.Addons, netbird())
	b, _ := json.Marshal(c)
	got, err := Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := got.Find("techo5-netbird")
	if f := a.Versions[0].FilesFor("arm"); len(f) != 1 || f[0].Name != "netbird.tar.gz" || f[0].SHA256 != strings.Repeat("a", 64) {
		t.Errorf("read back as %+v", f)
	}
	// The format as the catalog writes it: a list of name, url, sha256 and size per build.
	if !strings.Contains(string(b), `"files":{"arm":[{"name":"netbird.tar.gz","url":"https://example.com/netbird.tar.gz","sha256":"`) {
		t.Errorf("written as %s", b)
	}
	// One without extra files writes none.
	plain, _ := json.Marshal(plexVersion("0.1.0"))
	if strings.Contains(string(plain), "files") {
		t.Errorf("a version without extra files: %s", plain)
	}
}

// Every way an extra file can be wrong refuses the catalog.
func TestABadExtraFileRefusesTheCatalog(t *testing.T) {
	set := func(arch string, files ...File) func(v *Version) {
		return func(v *Version) { v.Files[arch] = files }
	}
	many := make([]File, MaxFiles+1)
	for i := range many {
		many[i] = extra("f" + string(rune('a'+i)))
	}
	huge := extra("big.bin")
	huge.Size = MaxFilesMB<<20 + 1
	half := extra("half.bin")
	half.Size = MaxFilesMB << 19
	half2 := half
	half2.Name = "half2.bin"
	cases := map[string]func(v *Version){
		"a path":             set("arm", extra("../etc/passwd")),
		"a folder":           set("arm", extra("lib/x.so")),
		"hidden":             set("arm", extra(".profile")),
		"a space":            set("arm", extra("a b")),
		"a backslash":        set("arm", extra(`a\b`)),
		"a colon":            set("arm", extra("c:x")),
		"empty name":         set("arm", extra("")),
		"too long":           set("arm", extra(strings.Repeat("x", 65))),
		"twice":              set("arm", extra("a.bin"), extra("a.bin")),
		"too many":           set("arm", many...),
		"too large":          set("arm", huge),
		"too large together": func(v *Version) { v.Files = map[string][]File{"arm": {half, half2, extra("c")}} },
		// Two sizes of 2^62 add up to a negative total, which would pass the cap on the total alone.
		"sizes that wrap": func(v *Version) {
			a, b := extra("a"), extra("b")
			a.Size, b.Size = 1<<62, 1<<62
			v.Files = map[string][]File{"arm": {a, b}, "arm64": {a, b}}
		},
		"http":       func(v *Version) { f := extra("x"); f.URL = "http://example.com/x"; v.Files["arm"] = []File{f} },
		"short hash": func(v *Version) { f := extra("x"); f.SHA256 = "abc"; v.Files["arm"] = []File{f} },
		"no size":    func(v *Version) { f := extra("x"); f.Size = 0; v.Files["arm"] = []File{f} },
		"odd arch":   func(v *Version) { v.Files["amd64"] = []File{extra("netbird.tar.gz")} },
		"no binary for it": func(v *Version) {
			v.Binaries = map[string]update.Binary{"arm": bin("arm")}
		},
		"an empty list":      set("arm64"),
		"other names":        set("arm64", extra("other.tar.gz")),
		"one build has none": func(v *Version) { delete(v.Files, "arm64") },
	}
	for name, spoil := range cases {
		t.Run(name, func(t *testing.T) {
			c := valid()
			a := netbird()
			v := netbirdVersion()
			spoil(&v)
			a.Versions = []Version{v}
			c.Addons = []Addon{a}
			b, _ := json.Marshal(c)
			if _, err := Parse(b); err == nil {
				t.Error("taken")
			}
		})
	}
}

// A build of one architecture only may bring files with it.
func TestExtraFilesForTheOneBuild(t *testing.T) {
	v := netbirdVersion()
	v.Binaries = map[string]update.Binary{"arm": bin("arm")}
	delete(v.Files, "arm64")
	if err := v.valid("x", Format); err != nil {
		t.Error(err)
	}
}

// The strict reading holds for the new keys as for the rest.
func TestExtraFilesAreReadStrictly(t *testing.T) {
	c := valid()
	c.Addons = []Addon{netbird()}
	b, _ := json.Marshal(c)
	good := string(b)
	for name, bad := range map[string]string{
		"a capital":         strings.Replace(good, `"files":`, `"Files":`, 1),
		"twice":             strings.Replace(good, `"files":{`, `"files":{},"files":{`, 1),
		"an unknown key":    strings.Replace(good, `{"name":"netbird.tar.gz",`, `{"name":"netbird.tar.gz","mode":"4755",`, 1),
		"a name twice":      strings.Replace(good, `{"name":"netbird.tar.gz",`, `{"name":"netbird.tar.gz","name":"sh",`, 1),
		"an arch twice":     strings.Replace(good, `"files":{"arm":`, `"files":{"arm":[],"arm":`, 1),
		"an arch in a case": strings.Replace(good, `"files":{"arm":`, `"files":{"ARM":`, 1),
	} {
		if _, err := Parse([]byte(bad)); err == nil {
			t.Errorf("%s: taken", name)
		}
	}
	m := strings.Replace(netbirdManifest, `"files":`, `"Files":`, 1)
	if _, err := ParseManifest([]byte(m)); err == nil {
		t.Error("a manifest's Files in a capital was taken")
	}
}

func TestAManifestNamesItsExtraFiles(t *testing.T) {
	m, err := ParseManifest([]byte(netbirdManifest))
	if err != nil {
		t.Fatal(err)
	}
	for _, arch := range Arches {
		if err := m.Matches("techo5-netbird", netbirdVersion(), arch); err != nil {
			t.Errorf("%s: %v", arch, err)
		}
	}
	// The entry pins a file the manifest does not name, or the other way round, or another one.
	none := m
	none.Files = nil
	more := m
	more.Files = []string{"netbird.tar.gz", "extra.bin"}
	other := m
	other.Files = []string{"wireguard.tar.gz"}
	for name, man := range map[string]Manifest{"none": none, "more": more, "other": other} {
		if err := man.Matches("techo5-netbird", netbirdVersion(), "arm"); err == nil {
			t.Errorf("a manifest naming %s matched", name)
		}
	}
	plain := netbirdVersion()
	plain.Files = nil
	if err := m.Matches("techo5-netbird", plain, "arm"); err == nil || !strings.Contains(err.Error(), "netbird.tar.gz") {
		t.Errorf("a manifest naming a file its entry does not pin: %v", err)
	}
	// Order does not matter.
	two := netbirdVersion()
	two.Files = map[string][]File{"arm": {extra("b.bin"), extra("a.bin")}, "arm64": {extra("a.bin"), extra("b.bin")}}
	ab := m
	ab.Files = []string{"a.bin", "b.bin"}
	if err := ab.Matches("techo5-netbird", two, "arm"); err != nil {
		t.Errorf("in another order: %v", err)
	}

	for name, bad := range map[string]string{
		"a path":          strings.Replace(netbirdManifest, `"files":["netbird.tar.gz"]`, `"files":["../x"]`, 1),
		"hidden":          strings.Replace(netbirdManifest, `"files":["netbird.tar.gz"]`, `"files":[".x"]`, 1),
		"twice":           strings.Replace(netbirdManifest, `"files":["netbird.tar.gz"]`, `"files":["a","a"]`, 1),
		"the program":     strings.Replace(netbirdManifest, `"files":["netbird.tar.gz"]`, `"files":["techo5-netbird"]`, 1),
		"too many":        strings.Replace(netbirdManifest, `"files":["netbird.tar.gz"]`, `"files":["a","b","c","d","e","f","g","h","i"]`, 1),
		"not a string":    strings.Replace(netbirdManifest, `"files":["netbird.tar.gz"]`, `"files":[1]`, 1),
		"an empty string": strings.Replace(netbirdManifest, `"files":["netbird.tar.gz"]`, `"files":[""]`, 1),
	} {
		if _, err := ParseManifest([]byte(bad)); err == nil {
			t.Errorf("%s: taken", name)
		}
	}
}

// What the card showed covers the extra files by name and hash, and is what it was for a listing
// without any.
func TestShownCoversExtraFiles(t *testing.T) {
	a, v := netbird(), netbirdVersion()
	base := Shown(a, v)
	renamed := netbirdVersion()
	renamed.Files["arm"] = []File{extra("other.tar.gz")}
	rehashed := netbirdVersion()
	f := extra("netbird.tar.gz")
	f.SHA256 = strings.Repeat("b", 64)
	rehashed.Files["arm64"] = []File{f}
	added := netbirdVersion()
	added.Files["arm"] = append(added.Files["arm"], extra("more.bin"))
	for name, w := range map[string]Version{"a name": renamed, "a hash": rehashed, "a file added": added} {
		if Shown(a, w) == base {
			t.Errorf("a change of %s left the digest the same", name)
		}
	}
	// A listing without extra files is shown as it was before there were any: the digest a page
	// served by the build before carries back still matches.
	p, pv := plex(), plexVersion("0.1.0")
	old, _ := json.Marshal(struct {
		ID, Name, Author, Version string
		Ports                     []Port
		Root                      bool
		MemoryMB, DiskMB          int
	}{p.ID, p.Name, p.Author, pv.Version, pv.Ports, pv.Root, pv.MemoryMB, pv.DiskMB})
	sum := sha256.Sum256(old)
	if got := Shown(p, pv); got != hex.EncodeToString(sum[:16]) {
		t.Errorf("a listing without extra files has another digest than before: %s", got)
	}
	pv.Files = map[string][]File{}
	if Shown(p, pv) != Shown(p, plexVersion("0.1.0")) {
		t.Error("an empty map of extra files changed the digest")
	}
}

// An update that brings a new extra file, or a changed one, asks again; the same one does not.
func TestMoreCoversExtraFiles(t *testing.T) {
	v := netbirdVersion()
	before := v.Asks("arm")
	if len(before.Files) != 1 || before.Files[0].Name != "netbird.tar.gz" {
		t.Fatalf("asks %+v", before)
	}
	if more := v.Asks("arm").More(before); len(more) != 0 {
		t.Errorf("the same file: %v", more)
	}
	changed := netbirdVersion()
	f := extra("netbird.tar.gz")
	f.SHA256 = strings.Repeat("c", 64)
	changed.Files["arm"] = []File{f}
	if more := changed.Asks("arm").More(before); len(more) != 1 || more[0] != "a changed netbird.tar.gz" {
		t.Errorf("a changed file: %v", more)
	}
	added := netbirdVersion()
	added.Files["arm"] = append(added.Files["arm"], extra("more.bin"))
	if more := added.Asks("arm").More(before); len(more) != 1 || more[0] != "the extra file more.bin" {
		t.Errorf("a file added: %v", more)
	}
	// One installed before extra files agreed to none: the first update bringing one asks.
	if more := v.Asks("arm").More(Asks{Root: true}); len(more) != 1 || more[0] != "the extra file netbird.tar.gz" {
		t.Errorf("from none: %v", more)
	}
	// Dropping one asks nothing.
	if more := (Asks{Root: true}).More(before); len(more) != 0 {
		t.Errorf("fewer files: %v", more)
	}
	// Only the device's own build counts.
	if a := v.Asks("riscv64"); len(a.Files) != 0 {
		t.Errorf("another build's files: %+v", a.Files)
	}
}

// A published version's extra files are held as the rest of it is.
func TestUnchangedCoversExtraFiles(t *testing.T) {
	prev := valid()
	prev.Addons = []Addon{netbird()}
	for name, spoil := range map[string]func(v *Version){
		"a hash": func(v *Version) {
			f := extra("netbird.tar.gz")
			f.SHA256 = strings.Repeat("d", 64)
			v.Files["arm"] = []File{f}
		},
		"a name":       func(v *Version) { v.Files["arm"] = []File{extra("x")} },
		"a file added": func(v *Version) { v.Files["arm"] = append(v.Files["arm"], extra("x")) },
		"gone":         func(v *Version) { v.Files = nil },
		"a url":        func(v *Version) { f := extra("netbird.tar.gz"); f.URL += "?2"; v.Files["arm"] = []File{f} },
	} {
		next := valid()
		next.Addons = []Addon{netbird()}
		v := netbirdVersion()
		spoil(&v)
		next.Addons[0].Versions = []Version{v}
		if err := Unchanged(prev, next); err == nil {
			t.Errorf("changing %s passed", name)
		}
	}
	// Added to a version that had none.
	before := valid()
	after := valid()
	after.Addons[0].Versions[0].Files = map[string][]File{"arm": {extra("x")}, "arm64": {extra("x")}}
	if err := Unchanged(before, after); err == nil {
		t.Error("adding extra files to a published version passed")
	}
	same := valid()
	same.Addons = []Addon{netbird()}
	if err := Unchanged(prev, same); err != nil {
		t.Errorf("the same: %v", err)
	}
}

// A download into bin/ clears what an earlier try left matching its name followed by .*.part, so no
// name may end in .part, nor be another's (or the program's) followed by a dot: each would let one
// download delete another's file.
func TestExtraFileNamesCannotTreadOnEachOthersDownloads(t *testing.T) {
	for _, name := range []string{"x.part", "netbird.tar.gz.part", "a.b.part"} {
		if ValidFileName(name) {
			t.Errorf("%s: a valid name", name)
		}
	}
	for name, files := range map[string][]File{
		"one and one with a dot": {extra("x"), extra("x.abc")},
		"the other way round":    {extra("x.abc.def"), extra("x.abc")},
	} {
		v := netbirdVersion()
		v.Files = map[string][]File{"arm": files, "arm64": files}
		if err := v.valid("x", Format); err == nil {
			t.Errorf("%s: taken", name)
		}
	}
	ok := netbirdVersion()
	ok.Files = map[string][]File{"arm": {extra("x.a"), extra("xa")}, "arm64": {extra("x.a"), extra("xa")}}
	if err := ok.valid("x", Format); err != nil {
		t.Errorf("names that only share a start: %v", err)
	}
	for name, files := range map[string]string{
		"a file and one with a dot": `["netbird.tar","netbird.tar.gz"]`,
		"the program with a dot":    `["techo5-netbird.tar.gz"]`,
		"ending in .part":           `["netbird.part"]`,
	} {
		m := strings.Replace(netbirdManifest, `"files":["netbird.tar.gz"]`, `"files":`+files, 1)
		if _, err := ParseManifest([]byte(m)); err == nil {
			t.Errorf("a manifest with %s: taken", name)
		}
	}
	// A program whose name a file extends with a dot.
	m := strings.Replace(netbirdManifest, `"command":["techo5-netbird"]`, `"command":["netbird.tar"]`, 1)
	if _, err := ParseManifest([]byte(m)); err == nil {
		t.Error("a manifest whose file extends the program's name: taken")
	}
}
