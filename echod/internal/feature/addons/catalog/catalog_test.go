package catalog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/update"
)

func bin(name string) update.Binary {
	return update.Binary{URL: "https://example.com/" + name, SHA256: strings.Repeat("a", 64), Size: 100}
}

func plexVersion(v string) Version {
	m := bin("addon.json")
	return Version{
		Version:   v,
		MinTECHO5: map[string]string{"show": "v1.6.52"},
		Binaries:  map[string]update.Binary{"arm": bin("arm"), "arm64": bin("arm64")},
		Manifest:  &m,
		Ports: []Port{
			{Proto: "tcp", Port: 32500, Why: "control"},
			{Proto: "tcp", Port: 32501, Why: "page"},
			{Proto: "udp", From: 32410, To: 32414, Why: "discovery"},
		},
		MemoryMB: 40,
		DiskMB:   12,
	}
}

func plex() Addon {
	return Addon{ID: "techo5-plex", Name: "Plex player", Author: "Someone", Summary: "Plays Plex.",
		Repo: "https://github.com/example/techo5-plex", Versions: []Version{plexVersion("0.1.0")}}
}

func valid() Catalog { return Catalog{Format: 2, Serial: 3, Addons: []Addon{plex()}} }

// The catalog in a techo5-addons checkout ($TECHO5_ADDONS, or beside this one) is one a device would
// take: it is what gets signed. It is skipped without one; techo5-addons checks its own catalog with
// this repository's mkcatalog on every push.
func TestTheAddonsCatalogParses(t *testing.T) {
	dir := os.Getenv("TECHO5_ADDONS")
	if dir == "" {
		dir = filepath.Join("..", "..", "..", "..", "..", "..", "techo5-addons")
	}
	path := filepath.Join(dir, "catalog.json")
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) && os.Getenv("TECHO5_ADDONS") == "" {
		t.Skipf("no techo5-addons checkout at %s", dir)
	}
	if err != nil {
		t.Fatal(err)
	}
	c, err := Parse(b)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	if _, ok := c.Find("techo5-plex"); !ok {
		t.Error("techo5-plex is not in the catalog")
	}
}

func TestAGoodCatalogIsTaken(t *testing.T) {
	b, _ := json.Marshal(valid())
	if _, err := Parse(b); err != nil {
		t.Fatal(err)
	}
}

// Every way an entry can be wrong refuses the whole catalog, rather than taking the rest of it.
func TestABadEntryRefusesTheCatalog(t *testing.T) {
	cases := map[string]func(c *Catalog){
		"no format":         func(c *Catalog) { c.Format = 0 },
		"newer format":      func(c *Catalog) { c.Format = Format + 1 },
		"no serial":         func(c *Catalog) { c.Serial = 0 },
		"twice":             func(c *Catalog) { c.Addons = append(c.Addons, plex()) },
		"bad id":            func(c *Catalog) { c.Addons[0].ID = "../etc" },
		"id with a capital": func(c *Catalog) { c.Addons[0].ID = "Plex" },
		"no author":         func(c *Catalog) { c.Addons[0].Author = " " },
		"no name":           func(c *Catalog) { c.Addons[0].Name = "" },
		"http repo":         func(c *Catalog) { c.Addons[0].Repo = "http://example.com" },
		"no versions":       func(c *Catalog) { c.Addons[0].Versions = nil },
		"version twice": func(c *Catalog) {
			c.Addons[0].Versions = append(c.Addons[0].Versions, plexVersion("0.1.0"))
		},
		"unrankable version": func(c *Catalog) { c.Addons[0].Versions[0].Version = "latest" },
		"no family":          func(c *Catalog) { c.Addons[0].Versions[0].MinTECHO5 = nil },
		"the panel":          func(c *Catalog) { c.Addons[0].Versions[0].MinTECHO5 = map[string]string{"panel": "v1.0.0"} },
		"board of another family": func(c *Catalog) {
			c.Addons[0].Versions[0].Boards = []string{"biscuit"}
		},
		"no binaries": func(c *Catalog) { c.Addons[0].Versions[0].Binaries = nil },
		"odd arch": func(c *Catalog) {
			c.Addons[0].Versions[0].Binaries = map[string]update.Binary{"amd64": bin("x")}
		},
		"short hash": func(c *Catalog) {
			b := bin("arm")
			b.SHA256 = "abc"
			c.Addons[0].Versions[0].Binaries["arm"] = b
		},
		"http binary": func(c *Catalog) {
			b := bin("arm")
			b.URL = "http://example.com/arm"
			c.Addons[0].Versions[0].Binaries["arm"] = b
		},
		"manifest without a size": func(c *Catalog) { c.Addons[0].Versions[0].Manifest.Size = 0 },
		"no memory":               func(c *Catalog) { c.Addons[0].Versions[0].MemoryMB = 0 },
		"no disk":                 func(c *Catalog) { c.Addons[0].Versions[0].DiskMB = 0 },
		"techo5's own port": func(c *Catalog) {
			c.Addons[0].Versions[0].Ports = append(c.Addons[0].Versions[0].Ports, Port{Proto: "tcp", Port: 8181, Why: "x"})
		},
		"a range over techo5's": func(c *Catalog) {
			c.Addons[0].Versions[0].Ports = []Port{{Proto: "udp", From: 6000, To: 6005, Why: "x"}}
		},
		"low port without root": func(c *Catalog) {
			c.Addons[0].Versions[0].Ports = []Port{{Proto: "tcp", Port: 80, Why: "x"}}
		},
		"no reason": func(c *Catalog) { c.Addons[0].Versions[0].Ports[0].Why = "" },
		"odd proto": func(c *Catalog) { c.Addons[0].Versions[0].Ports[0].Proto = "sctp" },
		"both":      func(c *Catalog) { c.Addons[0].Versions[0].Ports[0].From = 1 },
		"backwards": func(c *Catalog) {
			c.Addons[0].Versions[0].Ports = []Port{{Proto: "udp", From: 32414, To: 32410, Why: "x"}}
		},
		"asked twice": func(c *Catalog) {
			c.Addons[0].Versions[0].Ports = append(c.Addons[0].Versions[0].Ports, Port{Proto: "udp", Port: 32412, Why: "x"})
		},
	}
	for name, spoil := range cases {
		t.Run(name, func(t *testing.T) {
			c := valid()
			c.Addons = []Addon{plex()}
			m := *c.Addons[0].Versions[0].Manifest
			c.Addons[0].Versions[0].Manifest = &m
			spoil(&c)
			b, _ := json.Marshal(c)
			if _, err := Parse(b); err == nil {
				t.Error("taken")
			}
		})
	}
}

// A port below 1024 is fine for an add-on that runs as root.
func TestRootMayAskForALowPort(t *testing.T) {
	if err := ValidPorts([]Port{{Proto: "udp", Port: 51, Why: "x"}}, true); err != nil {
		t.Error(err)
	}
}

func TestRunsOn(t *testing.T) {
	v := plexVersion("0.1.0")
	show := Device{Family: "show", Board: "cronos", Running: "v1.6.60", Arch: "arm"}
	if ok, why := v.RunsOn(show); !ok {
		t.Errorf("a Show on a new enough TECHO5: %s", why)
	}
	for name, d := range map[string]Device{
		"a dot":         {Family: "dot", Board: "biscuit", Running: "v1.6.60", Arch: "arm"},
		"an old show":   {Family: "show", Board: "cronos", Running: "v1.6.40", Arch: "arm"},
		"another arch":  {Family: "show", Board: "cronos", Running: "v1.6.60", Arch: "riscv64"},
		"another board": {Family: "show", Board: "skylight", Running: "v1.6.60", Arch: "arm"},
	} {
		w := v
		if name == "another board" {
			w.Boards = []string{"cronos"}
		}
		if ok, _ := w.RunsOn(d); ok {
			t.Errorf("%s: runs", name)
		}
	}
	// A development build is not held back by the TECHO5 version.
	if ok, why := v.RunsOn(Device{Family: "show", Board: "cronos", Running: "v0.0.0-dev", Arch: "arm"}); !ok {
		t.Errorf("a development build: %s", why)
	}
	// A version without a pinned manifest is listed, not installable.
	w := v
	w.Manifest = nil
	if ok, why := w.RunsOn(show); ok || !strings.Contains(why, "manifest") {
		t.Errorf("no manifest: %v, %q", ok, why)
	}
}

func TestNewestSkipsWithdrawnAndUnrunnable(t *testing.T) {
	a := plex()
	newer := plexVersion("0.2.0")
	newest := plexVersion("0.10.0")
	newest.Withdrawn = "it deleted things"
	future := plexVersion("0.3.0")
	future.MinTECHO5 = map[string]string{"show": "v9.0.0"}
	a.Versions = append(a.Versions, newer, newest, future)
	d := Device{Family: "show", Board: "cronos", Running: "v1.6.60", Arch: "arm"}
	if v, ok := a.Newest(d); !ok || v.Version != "0.2.0" {
		t.Errorf("newest: %v %v", v.Version, ok)
	}
	if v, ok := a.Latest(); !ok || v.Version != "0.3.0" {
		t.Errorf("latest: %v %v", v.Version, ok)
	}
	a.Withdrawn = "gone"
	if _, ok := a.Newest(d); ok {
		t.Error("a withdrawn add-on offered a version")
	}
}

func TestMoreIsWhatWasNotAgreedTo(t *testing.T) {
	before := plexVersion("0.1.0").Asks("arm")
	if more := before.More(before); len(more) != 0 {
		t.Errorf("the same asks: %v", more)
	}
	fewer := Asks{Ports: before.Ports[:1]}
	if more := fewer.More(before); len(more) != 0 {
		t.Errorf("fewer asks: %v", more)
	}
	narrower := Asks{Ports: []Port{{Proto: "udp", Port: 32412, Why: "x"}}}
	if more := narrower.More(before); len(more) != 0 {
		t.Errorf("a port inside an agreed range: %v", more)
	}
	wider := Asks{Ports: append(before.Ports, Port{Proto: "tcp", Port: 32600, Why: "x"}), Root: true}
	more := wider.More(before)
	if len(more) != 2 || more[0] != "to run as root" || more[1] != "port 32600/tcp" {
		t.Errorf("more asks: %v", more)
	}
}

func TestManifest(t *testing.T) {
	good := `{"id":"techo5-plex","version":"0.1.0","command":["techo5-plex"],"args":["-data","{state}"],
	 "ports":[{"proto":"tcp","port":32500,"why":"a"},{"proto":"tcp","port":32501,"why":"b"},{"proto":"udp","from":32410,"to":32414,"why":"c"}],
	 "root":false,"page":"http://{device}:32501/","memory_mb":40}`
	m, err := ParseManifest([]byte(good))
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Matches("techo5-plex", plexVersion("0.1.0"), "arm"); err != nil {
		t.Errorf("matches its entry: %v", err)
	}
	if got := m.Expand("/data/x/state"); strings.Join(got, " ") != "-data /data/x/state" {
		t.Errorf("args: %q", got)
	}
	if got := m.PageFor("192.0.2.4"); got != "http://192.0.2.4:32501/" {
		t.Errorf("page: %q", got)
	}
	if err := m.Matches("techo5-plex", plexVersion("0.2.0"), "arm"); err == nil {
		t.Error("matched another version's entry")
	}
	wider := plexVersion("0.1.0")
	wider.Ports = wider.Ports[:2]
	if err := m.Matches("techo5-plex", wider, "arm"); err == nil {
		t.Error("a manifest asking for more ports than its entry matched")
	}
	rooted := plexVersion("0.1.0")
	rooted.Root = true
	if err := m.Matches("techo5-plex", rooted, "arm"); err == nil {
		t.Error("matched an entry that differs on root")
	}
	for name, bad := range map[string]string{
		"a path":     strings.Replace(good, `"command":["techo5-plex"]`, `"command":["../../bin/sh"]`, 1),
		"hidden":     strings.Replace(good, `"command":["techo5-plex"]`, `"command":[".x"]`, 1),
		"no command": strings.Replace(good, `"command":["techo5-plex"]`, `"command":[]`, 1),
		"own port":   strings.Replace(good, `"port":32500`, `"port":6053`, 1),
		"cast port":  strings.Replace(good, `"port":32500`, `"port":8940`, 1),
		"bad page":   strings.Replace(good, `http://{device}:32501/`, `javascript:alert(1)`, 1),
		"no memory":  strings.Replace(good, `"memory_mb":40`, `"memory_mb":0`, 1),
	} {
		if _, err := ParseManifest([]byte(bad)); err == nil {
			t.Errorf("%s: taken", name)
		}
	}
}
