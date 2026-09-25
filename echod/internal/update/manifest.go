package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"runtime"
	"time"
)

// Manifest is what a release says about itself, and the only thing a device reads to decide there is
// something newer. It is written by the release build and served beside the binaries it describes.
//
// The device does not compare versions: Home Assistant does that, with a parser that forces the update
// card permanently on for anything it cannot rank. So Version has to stay something AwesomeVersion can
// read — dotted numerals, optionally a prerelease, and any build detail after an underscore, which is
// where Home Assistant truncates before comparing.
type Manifest struct {
	// Version is what a device reports as available, and what Home Assistant ranks against what it is
	// running. ValidVersion is that rule, and a manifest breaking it is refused rather than offered.
	Version string `json:"version"`

	// URL, SHA256 and Size are the arm64 build. An echod that reads only these is arm64 by
	// construction, and they are its only route onto a newer build.
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`

	// Binaries is keyed by the Go architecture each build targets.
	Binaries map[string]Binary `json:"binaries"`

	// Rootfs is the whole root filesystem for devices that boot from slots (the Linux image),
	// keyed the same way. A manifest without one leaves those devices where they are.
	Rootfs map[string]Binary `json:"rootfs,omitempty"`

	// Assets is everything else a release ships that somebody's computer downloads and then runs or
	// writes to a unit, keyed by the file name the release publishes it under: the Show's boot images,
	// the Dot's and the Spot's Bluetooth kernels, the Spot's rescue bundle, the Dot's rescue packages.
	//
	// The daemon reads none of these — the installers do. They are named here because this is the one
	// file in a release that carries a signature, and until they were, the installers had nothing but
	// the release's SHA256SUMS to check them against. That file is plain text with nothing over it, so
	// anything able to serve a different manifest could serve a matching SHA256SUMS beside it, and the
	// Spot's installer unpacks the rescue bundle and runs scripts out of it on the user's own machine.
	//
	// Keyed by name rather than by architecture because these are not per-device builds a unit chooses
	// between: they are named files an installer asks for, and the name is what it has to go on.
	Assets map[string]Binary `json:"assets,omitempty"`

	Title      string `json:"title,omitempty"`
	Notes      string `json:"notes,omitempty"`
	ReleaseURL string `json:"release_url,omitempty"`
}

// Binary is one build of a release. SHA256 and Size are both checked before anything is written: a
// length that matches proves nothing, and neither proves the file came from us, which is what signing
// is for.
type Binary struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

const flatArch = "arm64"

// arch is a variable so a test can stand somewhere other than the machine it runs on. archSuffix
// separates builds that share an architecture but not a device: the Dot and the Show are both arm,
// and each other's binaries would start and then drive the wrong hardware.
var arch = runtime.GOARCH + archSuffix

// manifestTimeout bounds the fetch. Home Assistant asks for this on connect and after every selection
// change, so it has to fail quickly rather than hold up a configuration reply.
const manifestTimeout = 10 * time.Second

// maxManifest is a sanity bound on the response. A manifest is a few hundred bytes; anything else is a
// captive portal or a mistake.
const maxManifest = 64 << 10

// Fetch reads the channel's manifest and checks that it describes something installable. A manifest
// that arrives without a version or without somewhere to fetch a binary from is a broken release, and
// saying so here is better than failing half way through an install.
//
// The manifest is believed only with its signature (trust.go): the file beside it, manifest.json.sig,
// must be the release key's signature over exactly the bytes served. And nothing is fetched on a clock
// that has not been set, which is how a device that just booted would otherwise meet certificates.
func Fetch(ctx context.Context, c Channel) (Manifest, error) {
	var m Manifest
	if !clockSet() {
		return m, ErrClock
	}

	ctx, cancel := context.WithTimeout(ctx, manifestTimeout)
	defer cancel()

	url := channelURL(c)
	body, err := get(ctx, url, maxManifest)
	if err != nil {
		return m, err
	}
	sig, err := get(ctx, url+".sig", 1<<10)
	if err != nil {
		return m, fmt.Errorf("update: the manifest has no signature: %w", err)
	}
	if err := verify(body, sig, releaseKey); err != nil {
		return m, err
	}
	if err := json.Unmarshal(body, &m); err != nil {
		return m, fmt.Errorf("update: reading the manifest at %s: %w", url, err)
	}
	return m, m.Valid()
}

// channelURL is Channel.URL, a variable so a test can serve the channel itself.
var channelURL = Channel.URL

// get fetches a small file through the updater's own client, refusing anything over max bytes.
func get(ctx context.Context, url string, max int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("update: fetching %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("update: fetching %s: %s", url, resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return nil, fmt.Errorf("update: reading %s: %w", url, err)
	}
	if int64(len(b)) > max {
		return nil, fmt.Errorf("update: %s is larger than %d bytes", url, max)
	}
	return b, nil
}

func (m Manifest) flat() Binary {
	return Binary{URL: m.URL, SHA256: m.SHA256, Size: m.Size}
}

// versionPattern is the shape Home Assistant can rank: dotted numerals, optionally a prerelease
// (-beta.4), optionally build detail after the underscore Home Assistant truncates at before it
// compares. The leading v is optional because the releases predating the tags carry none and
// AwesomeVersion ranks either — what it cannot rank is a name with anything else in front of the
// numbers, and a tag name (dot-v0.5.10) is exactly that.
var versionPattern = regexp.MustCompile(`^v?\d+\.\d+\.\d+(-[0-9A-Za-z.]+)?(_[0-9A-Za-z.-]+)?$`)

// ValidVersion reports whether a version is one Home Assistant can rank against what a device is
// running. It is exported so the release tooling can refuse a bad version before it measures or writes
// anything, rather than publishing a manifest every device would then have to throw away.
//
// A version Home Assistant cannot rank is worse than a missing one: it does not compare the two
// strings for order, it offers the update whenever they differ, so the card comes on and never goes
// off however many times somebody installs it.
func ValidVersion(version string) error {
	switch {
	case version == "":
		return errors.New("update: the manifest names no version")
	case !versionPattern.MatchString(version):
		return fmt.Errorf("update: %q is not a version Home Assistant can rank; it has to be vX.Y.Z, optionally -prerelease and _build detail", version)
	}
	return nil
}

// Valid reports whether the manifest describes something installable, which is checked both where one
// is written and where one is read. It does not ask whether this device is served — For does that.
func (m Manifest) Valid() error {
	if err := ValidVersion(m.Version); err != nil {
		return err
	}

	if flat := m.flat(); flat != (Binary{}) {
		if err := flat.valid(m.Version, flatArch); err != nil {
			return err
		}
	} else if len(m.Binaries) == 0 {
		return fmt.Errorf("update: the manifest for %s carries no binaries", m.Version)
	}

	for a, b := range m.Binaries {
		if err := b.valid(m.Version, a); err != nil {
			return err
		}
	}

	// An asset entry that is half filled in is worse than none at all: an installer would take it for
	// cover it does not give. A manifest carrying no assets at all is fine — every release published
	// before they were named here is one, and the installers say so plainly rather than guessing.
	for name, b := range m.Assets {
		switch {
		case b.URL == "":
			return fmt.Errorf("update: the %s named by %s has no url", name, m.Version)
		case len(b.SHA256) != 64:
			return fmt.Errorf("update: the %s named by %s has no usable sha256", name, m.Version)
		case b.Size <= 0:
			return fmt.Errorf("update: the %s named by %s gives no size", name, m.Version)
		}
	}
	return nil
}

// Asset is the signed description of one of a release's named files, for whoever is about to download
// it. The second result is false when this release names no such file, which is not an error here: it
// is what every release published before Assets existed looks like, and the caller decides what to say.
func (m Manifest) Asset(name string) (Binary, bool) {
	b, ok := m.Assets[name]
	return b, ok
}

func (b Binary) valid(version, arch string) error {
	switch {
	case b.URL == "":
		return fmt.Errorf("update: the %s binary for %s has no url", arch, version)
	case len(b.SHA256) != 64:
		return fmt.Errorf("update: the %s binary for %s has no usable sha256", arch, version)
	case b.Size <= 0:
		return fmt.Errorf("update: the %s binary for %s gives no size", arch, version)
	}
	return nil
}

// For is the build a device of this architecture may install. A manifest carrying only the top-level
// fields answers for arm64.
func (m Manifest) For(arch string) (Binary, error) {
	if b, ok := m.Binaries[arch]; ok {
		return b, nil
	}
	if len(m.Binaries) == 0 && arch == flatArch {
		return m.flat(), nil
	}
	return Binary{}, fmt.Errorf("update: %s carries no %s binary", m.Version, arch)
}
