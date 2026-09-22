package update

import (
	"strings"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/layout"
)

// A release published before Binaries existed describes an arm64 build in its top-level fields, and an
// arm64 device has to go on updating through it — that is the only route off the build it is running.
func TestForReadsAReleaseThatPredatesBinaries(t *testing.T) {
	m := Manifest{
		Version: "0.0.6",
		URL:     "https://example/echod",
		SHA256:  strings.Repeat("a", 64),
		Size:    24 << 20,
	}
	if err := m.Valid(); err != nil {
		t.Fatal(err)
	}

	b, err := m.For("arm64")
	if err != nil {
		t.Fatal(err)
	}
	if b.URL != m.URL || b.SHA256 != m.SHA256 || b.Size != m.Size {
		t.Errorf("offered %+v, want the manifest's own fields", b)
	}

	if _, err := m.For("arm"); err == nil {
		t.Error("offered a 32-bit device a release that only carries arm64")
	}
}

// Every device takes the build for what it is running, and a release carrying both must not hand
// either one the other's.
func TestForTakesTheArchitectureTheDeviceRuns(t *testing.T) {
	m := Manifest{
		Version: "0.0.7",
		URL:     "https://example/echod-arm64",
		SHA256:  strings.Repeat("a", 64),
		Size:    24 << 20,
		Binaries: map[string]Binary{
			"arm64": {URL: "https://example/echod-arm64", SHA256: strings.Repeat("a", 64), Size: 24 << 20},
			"arm":   {URL: "https://example/echod-arm", SHA256: strings.Repeat("b", 64), Size: 22 << 20},
		},
	}
	if err := m.Valid(); err != nil {
		t.Fatal(err)
	}

	for arch, want := range map[string]string{
		"arm64": "https://example/echod-arm64",
		"arm":   "https://example/echod-arm",
	} {
		b, err := m.For(arch)
		if err != nil {
			t.Errorf("%s: %v", arch, err)
			continue
		}
		if b.URL != want {
			t.Errorf("%s: offered %s, want %s", arch, b.URL, want)
		}
	}
}

// Home Assistant offers an update whenever the version it is told about differs from the one running,
// not when it is newer, so a manifest naming something it cannot rank leaves a card on for good. A
// device has to refuse such a manifest instead of offering it — which is what happened when a release
// passed the git tag (dot-v0.5.10) where the version belongs.
func TestValidRefusesAVersionHomeAssistantCannotRank(t *testing.T) {
	binaries := map[string]Binary{"arm": {URL: "https://example/echod-arm", SHA256: strings.Repeat("a", 64), Size: 22 << 20}}

	for version, want := range map[string]bool{
		"v0.7.13":              true,
		"v0.4.10-beta.4":       true,
		"0.5.10":               true, // Releases before the tags carry no v, and AwesomeVersion ranks either.
		"v0.5.10_20260922":     true, // Build detail, which Home Assistant truncates at the underscore.
		"dot-v0.5.10":          false,
		"spot-v0.4.10":         false,
		"":                     false,
		"v0.5":                 false,
		"dev":                  false,
		"TECHO5 v0.5.10":       false,
		"v0.5.10 (deadbeef)":   false,
		"refs/tags/dot-v0.5.1": false,
	} {
		err := Manifest{Version: version, Binaries: binaries}.Valid()
		switch {
		case want && err != nil:
			t.Errorf("%q was refused: %v", version, err)
		case !want && err == nil:
			t.Errorf("%q was accepted, and would reach Home Assistant", version)
		case !want && version != "" && !strings.Contains(err.Error(), version):
			t.Errorf("%q was refused without saying which version: %v", version, err)
		}
	}
}

// The other half of the same trap: the version a device reports as running is stamped in at build
// time, and an unstamped build falls back to layout.Version's default. If that default cannot be
// ranked either, every hand-built daemon shows a card no install can clear.
func TestTheDefaultVersionStampCanBeRanked(t *testing.T) {
	if err := ValidVersion(layout.Version); err != nil {
		t.Errorf("the default stamp %q would leave an update card on for good: %v", layout.Version, err)
	}
}

// A release with only the Show's build serves no Dot, and one with the Dot's serves it. (Off a slot
// system, which is where tests run; on a slot device the rootfs map decides.)
func TestServesOnlyWhatThisDeviceCanInstall(t *testing.T) {
	if slotSystem() {
		t.Skip("running on a slot device")
	}
	restore := arch
	t.Cleanup(func() { arch = restore })
	arch = "arm-dot"

	show := Manifest{Version: "0.0.9", Binaries: map[string]Binary{
		"arm": {URL: "https://example/echod-arm", SHA256: strings.Repeat("b", 64), Size: 22 << 20},
	}}
	if show.Serves() {
		t.Error("a Show-only release was offered to a Dot")
	}
	show.Binaries["arm-dot"] = Binary{URL: "https://example/echod-arm-dot", SHA256: strings.Repeat("c", 64), Size: 22 << 20}
	if !show.Serves() {
		t.Error("a release carrying the Dot's build was not offered to it")
	}
}
