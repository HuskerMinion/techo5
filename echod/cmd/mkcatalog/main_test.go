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

const small = `{"format":1,"serial":2,"addons":[]}`

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

	if err := run(in, write(t, dir, "prev.json", `{"format":1,"serial":3,"addons":[]}`), out, key); err == nil {
		t.Error("signed a catalog with a serial below the published one")
	}
	if err := run(in, write(t, dir, "prevsame.json", `{"format":1,"serial":2,"addons":[]}`), out, key); err != nil {
		t.Errorf("signing the published catalog again for a fresh expiry: %v", err)
	}
	if err := run(in, write(t, dir, "prev1.json", `{"format":1,"serial":1,"addons":[]}`), out, key); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(out, "catalog.json"))
	c, err := catalog.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Expires.Equal(signedAt.Add(expiresAfter)) || c.Serial != 2 {
		t.Errorf("signed as %+v", c)
	}
	sig, _ := os.ReadFile(filepath.Join(out, "catalog.json.sig"))
	raw, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sig)))
	pub := k.Public().(ed25519.PublicKey)
	if !ed25519.Verify(pub, append([]byte(update.CatalogDomain), got...), raw) {
		t.Error("the signature does not verify over the catalog domain")
	}
	if ed25519.Verify(pub, got, raw) {
		t.Error("the signature verifies over the bare bytes, as a firmware manifest's would")
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
	prev := write(t, dir, "prev2.json", `{"format":1,"serial":1,"addons":[`+entry(40000, "")+`]}`)
	if err := run(write(t, dir, "changed.json", `{"format":1,"serial":2,"addons":[`+entry(40001, "")+`]}`), prev, out, key); err == nil {
		t.Error("signed a catalog that changes a published version")
	}
	if err := run(write(t, dir, "withdrawn.json", `{"format":1,"serial":2,"addons":[`+entry(40000, "it broke")+`]}`), prev, out, key); err != nil {
		t.Errorf("withdrawing a published version: %v", err)
	}
	if err := run(write(t, dir, "bad.json", `{"format":1,"serial":0}`), "", out, key); err == nil {
		t.Error("signed a catalog a device would refuse")
	}
	if err := run(in, "", "", ""); err != nil {
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
		return write(t, dir, "catalog.json", `{"format":1,"serial":1,"addons":[{"id":"x-addon","name":"X","author":"A",
		 "summary":"Does X.","repo":"https://example.com/x","versions":[{"version":"1.0.0","min_techo5":{"show":"v1.0.0"},
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
