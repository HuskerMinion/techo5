package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/addons/catalog"
	"github.com/HuskerMinion/techo5/echod/internal/update"
)

const small = `{"format":2,"serial":2,"addons":[]}`

func write(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// The catalog is signed with its expiry stamped in, over the catalog domain, and only with a serial
// above the published one.
func TestSignsTheCatalogWithItsExpiry(t *testing.T) {
	dir := t.TempDir()
	_, k, _ := ed25519.GenerateKey(rand.Reader)
	key := write(t, dir, "key", base64.StdEncoding.EncodeToString(k.Seed()))
	in := write(t, dir, "in.json", small+"\n")
	out := filepath.Join(dir, "out")
	signedAt := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	restore := now
	t.Cleanup(func() { now = restore })
	now = func() time.Time { return signedAt }

	if err := run(in, []string{write(t, dir, "prev.json", `{"format":1,"serial":3,"addons":[]}`)}, true, out, key); err == nil {
		t.Error("signed a catalog with a serial below the published one")
	}
	if err := run(in, []string{write(t, dir, "prevsame.json", `{"format":1,"serial":2,"addons":[]}`)}, true, out, key); err != nil {
		t.Errorf("signing the published catalog again for a fresh expiry: %v", err)
	}
	if err := run(in, []string{write(t, dir, "prev1.json", `{"format":1,"serial":1,"addons":[]}`)}, true, out, key); err != nil {
		t.Fatal(err)
	}
	pub := k.Public().(ed25519.PublicKey)
	for name, domain := range map[string]string{"catalog.json": update.CatalogDomain, "catalog-v2.json": update.CatalogDomainV2} {
		got, _ := os.ReadFile(filepath.Join(out, name))
		c, err := catalog.Parse(got)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !c.Expires.Equal(signedAt.Add(expiresAfter)) || c.Serial != 2 {
			t.Errorf("%s signed as %+v", name, c)
		}
		sig, _ := os.ReadFile(filepath.Join(out, name+".sig"))
		raw, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sig)))
		if !ed25519.Verify(pub, append([]byte(domain), got...), raw) {
			t.Errorf("%s's signature does not verify over its domain", name)
		}
		other := update.CatalogDomain
		if domain == other {
			other = update.CatalogDomainV2
		}
		if ed25519.Verify(pub, append([]byte(other), got...), raw) {
			t.Errorf("%s's signature verifies over the other format's domain", name)
		}
		if ed25519.Verify(pub, got, raw) {
			t.Errorf("%s's signature verifies over the bare bytes, as a firmware manifest's would", name)
		}
	}

	// A version once published may not change, only be withdrawn.
	entry := func(port int, withdrawn string) string {
		w := ""
		if withdrawn != "" {
			w = `,"withdrawn":"` + withdrawn + `"`
		}
		return `{"id":"x-addon","name":"X","author":"A","summary":"Does X.","repo":"https://example.com/x","versions":[{"version":"1.0.0",
		 "min_techo5":{"show":"v1.0.0"},"binaries":{"arm":{"url":"https://example.com/x","sha256":"` + strings.Repeat("a", 64) + `","size":1}},
		 "ports":[{"proto":"tcp","port":` + fmt.Sprint(port) + `,"why":"x"}],"root":false,"memory_mb":10,"disk_mb":1` + w + `}]}`
	}
	prev := []string{write(t, dir, "prev2.json", `{"format":1,"serial":1,"addons":[`+entry(40000, "")+`]}`)}
	if err := run(write(t, dir, "changed.json", `{"format":2,"serial":2,"addons":[`+entry(40001, "")+`]}`), prev, true, out, key); err == nil {
		t.Error("signed a catalog that changes a published version")
	}
	if err := run(write(t, dir, "withdrawn.json", `{"format":2,"serial":2,"addons":[`+entry(40000, "it broke")+`]}`), prev, true, out, key); err != nil {
		t.Errorf("withdrawing a published version: %v", err)
	}
	if err := run(write(t, dir, "bad.json", `{"format":2,"serial":0}`), nil, false, out, key); err == nil {
		t.Error("signed a catalog a device would refuse")
	}
	if err := run(write(t, dir, "v1.json", `{"format":1,"serial":2,"addons":[]}`), nil, false, out, key); err == nil {
		t.Error("signed a format 1 source")
	}
	if err := run(in, nil, false, "", ""); err != nil {
		t.Errorf("a check without signing: %v", err)
	}
}

// An author's addon.json is checked as a device would, and against its entry when the catalog has one.
func TestCheckManifest(t *testing.T) {
	dir := t.TempDir()
	man := write(t, dir, "addon.json", `{"id":"x-addon","version":"1.0.0","command":["x-addon"],
	 "ports":[{"proto":"tcp","port":40000,"why":"its page"}],"root":false,"memory_mb":10}`)
	entry := func(port int) string {
		return write(t, dir, "catalog.json", `{"format":1,"serial":1,"addons":[{"id":"x-addon","name":"X","author":"A",
		 "summary":"Does X.","repo":"https://example.com/x","versions":[{"version":"1.0.0","min_techo5":{"show":"v1.0.0"},
		 "binaries":{"arm":{"url":"https://example.com/x","sha256":"`+strings.Repeat("a", 64)+`","size":1}},
		 "ports":[{"proto":"tcp","port":`+fmt.Sprint(port)+`,"why":"its page"}],"root":false,"memory_mb":10,"disk_mb":1}]}]}`)
	}
	var b bytes.Buffer
	if err := checkManifest(&b, man, entry(40000)); err != nil || !strings.Contains(b.String(), "matches its entry") {
		t.Errorf("a matching manifest: %v, %s", err, b.String())
	}
	if err := checkManifest(&b, man, entry(40001)); err == nil {
		t.Error("a manifest asking for other ports than its entry passed")
	}
	if err := checkManifest(&b, write(t, dir, "bad.json", `{"id":"x-addon"}`), ""); err == nil {
		t.Error("a manifest a device would refuse passed")
	}
}

func TestPin(t *testing.T) {
	dir := t.TempDir()
	f := write(t, dir, "x", "hello")
	var b bytes.Buffer
	if err := printPin(&b, "https://example.com/x="+f, ""); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824") || !strings.Contains(b.String(), `"size": 5`) {
		t.Errorf("pin: %s", b.String())
	}
	if err := printPin(&b, "http://example.com/x="+f, ""); err == nil {
		t.Error("pinned an http url")
	}

	// An extra file's pin, under its name, reads back as one.
	b.Reset()
	if err := printPin(&b, "https://example.com/x="+f, "netbird.tar.gz"); err != nil {
		t.Fatal(err)
	}
	var got catalog.File
	if err := json.Unmarshal(b.Bytes(), &got); err != nil || got.Name != "netbird.tar.gz" || got.Size != 5 ||
		got.SHA256 != "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824" {
		t.Errorf("an extra file's pin: %v %s", err, b.String())
	}
	if !strings.HasPrefix(strings.TrimSpace(b.String()), "{\n  \"name\": \"netbird.tar.gz\",\n  \"url\"") {
		t.Errorf("an extra file's pin is not written as an entry lists it:\n%s", b.String())
	}
	if err := printPin(&b, "https://example.com/x="+f, "../x"); err == nil {
		t.Error("pinned an extra file under a path")
	}
}

// One addon.json serves every build, so it is checked against the extra files each build pins.
func TestCheckManifestWithExtraFiles(t *testing.T) {
	dir := t.TempDir()
	pin := func(name string) string {
		return `{"name":"` + name + `","url":"https://example.com/` + name + `","sha256":"` + strings.Repeat("a", 64) + `","size":1}`
	}
	bin := `{"url":"https://example.com/x","sha256":"` + strings.Repeat("a", 64) + `","size":1}`
	entry := func(files string) string {
		return write(t, dir, "catalog.json", `{"format":2,"serial":1,"addons":[{"id":"x-addon","name":"X","author":"A",
		 "summary":"Does X.","repo":"https://example.com/x","versions":[{"version":"1.0.0","requires":["files"],"min_techo5":{"show":"v1.0.0"},
		 "binaries":{"arm":`+bin+`,"arm64":`+bin+`},"files":`+files+`,
		 "ports":[],"root":true,"memory_mb":10,"disk_mb":1}]}]}`)
	}
	man := func(files string) string {
		return write(t, dir, "addon.json", `{"id":"x-addon","version":"1.0.0","command":["x-addon"],"files":`+files+`,
		 "ports":[],"root":true,"memory_mb":10}`)
	}
	both := entry(`{"arm":[` + pin("a.tar.gz") + `],"arm64":[` + pin("a.tar.gz") + `]}`)
	var b bytes.Buffer
	if err := checkManifest(&b, man(`["a.tar.gz"]`), both); err != nil || !strings.Contains(b.String(), "matches its entry") {
		t.Errorf("a matching manifest: %v, %s", err, b.String())
	}
	for name, files := range map[string]string{"none": `[]`, "another": `["b.tar.gz"]`, "one more": `["a.tar.gz","b.tar.gz"]`} {
		if err := checkManifest(&b, man(files), both); err == nil {
			t.Errorf("a manifest naming %s passed", name)
		}
	}
}

// One source makes both files: the format 2 one whole, and the format 1 one with every version that
// requires anything left out, and an add-on with nothing left out with it, so a format 1 device reads
// it as it read catalog.json before. The published files of either format hold the serial and the
// versions they list.
func TestWritesBothFormats(t *testing.T) {
	dir := t.TempDir()
	_, k, _ := ed25519.GenerateKey(rand.Reader)
	key := write(t, dir, "key", base64.StdEncoding.EncodeToString(k.Seed()))
	pin := `{"url":"https://example.com/x","sha256":"` + strings.Repeat("a", 64) + `","size":1}`
	version := func(v, extra string) string {
		return `{"version":"` + v + `",` + extra + `"min_techo5":{"show":"v1.0.0"},"binaries":{"arm":` + pin + `},
		 "ports":[],"root":true,"memory_mb":10,"disk_mb":1}`
	}
	files := `"requires":["files"],"files":{"arm":[{"name":"a.bin","url":"https://example.com/a","sha256":"` + strings.Repeat("b", 64) + `","size":1}]},`
	addon := func(id string, versions ...string) string {
		return `{"id":"` + id + `","name":"X","author":"A","summary":"Does X.","repo":"https://example.com/x","versions":[` + strings.Join(versions, ",") + `]}`
	}
	source := func(serial int, addons ...string) string {
		return write(t, dir, fmt.Sprintf("in%d.json", serial), fmt.Sprintf(`{"format":2,"serial":%d,"addons":[%s]}`, serial, strings.Join(addons, ",")))
	}
	in := source(2, addon("mixed", version("1.0.0", ""), version("1.1.0", files)), addon("files-only", version("1.0.0", files)))
	out := filepath.Join(dir, "out")
	if err := run(in, nil, false, out, key); err != nil {
		t.Fatal(err)
	}
	read := func(name string) catalog.Catalog {
		b, err := os.ReadFile(filepath.Join(out, name))
		if err != nil {
			t.Fatal(err)
		}
		c, err := catalog.Parse(b)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return c
	}
	v2, v1 := read("catalog-v2.json"), read("catalog.json")
	if v2.Format != 2 || len(v2.Addons) != 2 {
		t.Errorf("catalog-v2.json: %+v", v2)
	}
	if v1.Format != 1 || len(v1.Addons) != 1 || len(v1.Addons[0].Versions) != 1 || v1.Addons[0].Versions[0].Version != "1.0.0" {
		t.Errorf("catalog.json: %+v", v1)
	}
	raw, _ := os.ReadFile(filepath.Join(out, "catalog.json"))
	if strings.Contains(string(raw), `"requires"`) || strings.Contains(string(raw), `"files"`) {
		t.Errorf("catalog.json has a key format 1 does not know:\n%s", raw)
	}

	// Against the published files: the same serial and entries again is a fresh expiry, from either.
	published := []string{filepath.Join(out, "catalog-v2.json"), filepath.Join(out, "catalog.json")}
	if err := run(in, published, false, filepath.Join(dir, "again"), key); err != nil {
		t.Errorf("signing the published catalog again: %v", err)
	}
	// A version only the format 2 file has is held as one the format 1 file has.
	changed := source(3, addon("mixed", version("1.0.0", ""), version("1.1.0", strings.Replace(files, `"size":1}`, `"size":2}`, 1))),
		addon("files-only", version("1.0.0", files)))
	if err := run(changed, published, false, filepath.Join(dir, "changed"), key); err == nil {
		t.Error("signed a change to a version only the format 2 file has")
	}
	if err := run(changed, published[1:], true, filepath.Join(dir, "changed1"), key); err != nil {
		t.Errorf("against the format 1 file alone, which never had it: %v", err)
	}
	// A version once in the format 1 file cannot come to require something, which would take it away
	// from format 1 devices that installed it.
	moved := source(4, addon("mixed", version("1.0.0", files), version("1.1.0", files)), addon("files-only", version("1.0.0", files)))
	if err := run(moved, published[1:], true, filepath.Join(dir, "moved"), key); err == nil {
		t.Error("signed a version of the format 1 file that came to require files")
	}
	// A new version only the format 2 file has needs a new serial against the format 2 file, though
	// the format 1 file it would write is the same.
	added := source(2, addon("mixed", version("1.0.0", ""), version("1.1.0", files), version("1.2.0", files)), addon("files-only", version("1.0.0", files)))
	if err := run(added, published[1:], true, filepath.Join(dir, "added1"), key); err != nil {
		t.Errorf("against the format 1 file, whose entries are the same: %v", err)
	}
	if err := run(added, published, false, filepath.Join(dir, "added"), key); err == nil {
		t.Error("signed new entries under the format 2 file's serial")
	}
	// A requirement this build does not know is refused, not skipped: what is signed is read whole.
	future := source(5, addon("mixed", version("1.0.0", ""), version("2.0.0", `"requires":["sandbox"],`)))
	if err := run(future, nil, false, "", ""); err == nil || !strings.Contains(err.Error(), "sandbox") {
		t.Errorf("a requirement this build does not know: %v", err)
	}
}

// Against catalog.json alone, a version only catalog-v2.json lists could change unnoticed, so the
// published catalog-v2.json is wanted too, except at the signing that first publishes it, which says so
// with -first-format2; that switch is refused once a catalog-v2.json is given.
func TestThePublishedFormat2CatalogIsWanted(t *testing.T) {
	dir := t.TempDir()
	in := write(t, dir, "in.json", small)
	v1 := write(t, dir, "catalog.json", `{"format":1,"serial":1,"addons":[]}`)
	v2 := write(t, dir, "catalog-v2.json", `{"format":2,"serial":1,"addons":[]}`)
	if err := run(in, []string{v1}, false, "", ""); err == nil || !strings.Contains(err.Error(), "-first-format2") {
		t.Errorf("against catalog.json alone: %v", err)
	}
	if err := run(in, []string{v1}, true, "", ""); err != nil {
		t.Errorf("the first format 2 signing: %v", err)
	}
	if err := run(in, []string{v1, v2}, false, "", ""); err != nil {
		t.Errorf("against both: %v", err)
	}
	if err := run(in, []string{v1, v2}, true, "", ""); err == nil {
		t.Error("-first-format2 with a catalog-v2.json given was taken")
	}
	if err := run(in, nil, false, "", ""); err != nil {
		t.Errorf("nothing published yet: %v", err)
	}
}
