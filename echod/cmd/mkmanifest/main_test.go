package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/update"
)

// deployed is the manifest as every device released before the binaries map existed parses it. Those
// devices update through these four fields and nothing else, so a release that stops filling them in
// leaves every one of them stuck on the build it is running.
type deployed struct {
	Version string `json:"version"`
	URL     string `json:"url"`
	SHA256  string `json:"sha256"`
	Size    int64  `json:"size"`
}

func write(t *testing.T) (deployed, update.Manifest) {
	t.Helper()
	dir := t.TempDir()

	for name, body := range map[string]string{"echod-arm64": "sixty four", "echod-arm": "thirty two"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	out := filepath.Join(dir, "manifest.json")
	err := run(update.Manifest{Version: "0.0.7"}, "https://example/download/0.0.7", map[string]string{
		"arm64": filepath.Join(dir, "echod-arm64"),
		"arm":   filepath.Join(dir, "echod-arm"),
	}, nil, out)
	if err != nil {
		t.Fatal(err)
	}

	encoded, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}

	var old deployed
	var now update.Manifest
	if err := json.Unmarshal(encoded, &old); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, &now); err != nil {
		t.Fatal(err)
	}
	return old, now
}

func TestADeployedDeviceCanStillReadIt(t *testing.T) {
	old, now := write(t)

	if old.Version == "" || old.URL == "" || len(old.SHA256) != 64 || old.Size <= 0 {
		t.Fatalf("a device on the old manifest reads %+v, which it will refuse", old)
	}

	// Those fields have to be the arm64 build: every deployed device is one.
	arm64 := now.Binaries["arm64"]
	if old.URL != arm64.URL || old.SHA256 != arm64.SHA256 || old.Size != arm64.Size {
		t.Errorf("the top-level fields describe %+v, want the arm64 build %+v", old, arm64)
	}
}

func TestEachArchitectureGetsItsOwnBuild(t *testing.T) {
	_, now := write(t)

	if err := now.Valid(); err != nil {
		t.Fatal(err)
	}

	for arch, want := range map[string]string{
		"arm64": "https://example/download/0.0.7/echod-arm64",
		"arm":   "https://example/download/0.0.7/echod-arm",
	} {
		b, err := now.For(arch)
		if err != nil {
			t.Errorf("%s: %v", arch, err)
			continue
		}
		if b.URL != want {
			t.Errorf("%s: offered %s, want %s", arch, b.URL, want)
		}
	}

	if now.Binaries["arm64"].SHA256 == now.Binaries["arm"].SHA256 {
		t.Error("both architectures were measured as the same file")
	}
}

// The tag says which device a build is for (dot-vX.Y.Z), the binary built from it is stamped with the
// version alone, and Home Assistant offers an update whenever the two differ. Passing the tag here is
// what put a permanent update card on every Dot and Spot, so a release stops at this point rather than
// writing a manifest no device can ever clear.
func TestARunRefusesATagName(t *testing.T) {
	for version, want := range map[string]bool{
		"v0.7.13":          true,
		"v0.4.10-beta.4":   true,
		"0.5.10":           true,
		"v0.5.10_20260922": true,
		"dot-v0.5.10":      false,
		"spot-v0.4.10":     false,
		"":                 false,
		"v0.5":             false,
	} {
		dir := t.TempDir()
		build := filepath.Join(dir, "echod-arm")
		if err := os.WriteFile(build, []byte("thirty two"), 0o755); err != nil {
			t.Fatal(err)
		}
		out := filepath.Join(dir, "manifest.json")

		err := run(update.Manifest{Version: version}, "https://example/download",
			map[string]string{"arm": build}, nil, out)
		switch {
		case want && err != nil:
			t.Errorf("%q was refused: %v", version, err)
			continue
		case !want && err == nil:
			t.Errorf("%q was accepted, and a release would publish it", version)
		case strings.HasPrefix(version, "dot-") || strings.HasPrefix(version, "spot-"):
			if !strings.Contains(err.Error(), "tag name") {
				t.Errorf("%q was refused without saying a tag name was passed: %v", version, err)
			}
		}
		if _, statErr := os.Stat(out); want == (statErr != nil) {
			t.Errorf("%q: manifest written %v, wanted %v", version, statErr == nil, want)
		}
	}
}

// The Dot's build and rootfs are keyed apart from the Show's, and neither lands in the flat fields an
// older device reads.
func TestTheDotIsKeyedApart(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{"echod-arm": "show", "echod-arm-dot": "dot", "show.tar.gz": "s", "dot.tar.gz": "d"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	out := filepath.Join(dir, "manifest.json")
	err := run(update.Manifest{Version: "0.0.8"}, "https://example/download/0.0.8",
		map[string]string{"arm": filepath.Join(dir, "echod-arm"), "arm-dot": filepath.Join(dir, "echod-arm-dot")},
		map[string]string{"arm": filepath.Join(dir, "show.tar.gz"), "arm-dot": filepath.Join(dir, "dot.tar.gz")}, out)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var m update.Manifest
	if err := json.Unmarshal(encoded, &m); err != nil {
		t.Fatal(err)
	}
	if got := m.Binaries["arm-dot"].URL; got != "https://example/download/0.0.8/echod-arm-dot" {
		t.Errorf("dot binary at %q", got)
	}
	if got := m.Rootfs["arm-dot"].URL; got != "https://example/download/0.0.8/dot.tar.gz" {
		t.Errorf("dot rootfs at %q", got)
	}
	if got := m.Rootfs["arm"].URL; got != "https://example/download/0.0.8/show.tar.gz" {
		t.Errorf("show rootfs at %q", got)
	}
	if m.URL != "https://example/download/0.0.8/echod-arm" {
		t.Errorf("flat fields carry %q, want the Show's arm build", m.URL)
	}
}
