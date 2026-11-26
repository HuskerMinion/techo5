package update

import (
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// releaseKey is the public half of the key releases are signed with (tools/release.ps1 signs
// manifest.json into manifest.json.sig). An update installs as root, so a manifest is believed only
// when this key signed it: HTTPS alone would let anything that can present a certificate the device
// accepts, or a device told to skip certificate checks, hand it a root filesystem.
var releaseKey = "KVUuQUbhyKwPBbIneqFEvXYSI+3Hkfu/heCTy5YNVMk="

// client is the updater's own HTTP client. The diagnostics switch that skips certificate checks
// changes http.DefaultTransport for media and models; it never reaches here.
var client = &http.Client{
	Transport: &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout: 15 * time.Second,
	},
	// A release asset on GitHub answers with a redirect to its CDN, and a resumed download has to
	// arrive there still asking for the rest rather than the whole. Go carries the header over on its
	// own; this makes it so whatever the default becomes.
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("update: stopped after 10 redirects")
		}
		// The hash would still catch a changed file, but what is being fetched is nobody else's
		// business: an https asset does not go on over plain http.
		if via[0].URL.Scheme == "https" && req.URL.Scheme != "https" {
			return fmt.Errorf("update: refusing a redirect from https to %s", req.URL.Scheme)
		}
		if r := via[0].Header.Get("Range"); r != "" {
			req.Header.Set("Range", r)
		}
		return nil
	},
}

// clockSet is how an unset clock is recognized: this kernel starts in 1970 and NTP corrects it a
// little after boot. Certificates cannot be checked against a clock like that, so nothing is fetched.
var clockSet = func() bool { return time.Now().Year() >= 2025 }

// ErrClock is a check that was not made because the clock is not set yet.
var ErrClock = errors.New("update: the clock is not set yet; checking later")

// verify checks a manifest's detached signature: base64 of the 64-byte ed25519 signature over the
// manifest's bytes exactly as served.
func verify(manifest, sig []byte, key string) error {
	pub, err := base64.StdEncoding.DecodeString(key)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return errors.New("update: no usable release key in this build")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sig)))
	if err != nil || len(raw) != ed25519.SignatureSize {
		return fmt.Errorf("update: the manifest signature is malformed")
	}
	if !ed25519.Verify(ed25519.PublicKey(pub), manifest, raw) {
		return errors.New("update: the manifest is not signed by the release key")
	}
	return nil
}

// CatalogDomain is what the add-on catalog's signature is made over, ahead of the catalog's own bytes.
// The catalog is signed with the same key as the firmware's manifests, and without it a catalog's
// signature would be a good signature over those bytes for whatever reads them: a JSON file that parsed
// as both could be passed off as one where the other was asked for. With it, a signature made for a
// catalog never verifies a manifest, and a manifest's never verifies a catalog.
const CatalogDomain = "TECHO5 add-on catalog v1\n"

// CatalogDomainV2 is CatalogDomain for the format 2 catalog (catalog-v2.json), published beside the
// format 1 one that older devices read. Each format is signed under its own domain, so neither file's
// signature verifies the other: a device asking for one is never handed the other's bytes as it.
const CatalogDomainV2 = "TECHO5 add-on catalog v2\n"

// SignCatalog is Sign for the add-on catalog, over CatalogDomain and the catalog (cmd/mkcatalog).
func SignCatalog(catalog []byte, seed string) (string, error) {
	return Sign(append([]byte(CatalogDomain), catalog...), seed)
}

// SignCatalogV2 is SignCatalog for the format 2 catalog, over CatalogDomainV2.
func SignCatalogV2(catalog []byte, seed string) (string, error) {
	return Sign(append([]byte(CatalogDomainV2), catalog...), seed)
}

// VerifyCatalog checks an add-on catalog against its detached signature, for feature/addons, which
// keeps the catalog it last fetched and checks it again when it reads it back, so a copy changed on the
// device's storage is not believed either.
func VerifyCatalog(body, sig []byte) error {
	return verify(append([]byte(CatalogDomain), body...), sig, releaseKey)
}

// VerifyCatalogV2 is VerifyCatalog for the format 2 catalog.
func VerifyCatalogV2(body, sig []byte) error {
	return verify(append([]byte(CatalogDomainV2), body...), sig, releaseKey)
}

// FetchSignedCatalog is Fetch for the add-on catalog (feature/addons). The file is fetched through the
// updater's own client, its signature from beside it (url + ".sig"), and it is returned only once the
// signature is the release key's over CatalogDomain and exactly the bytes served. Nothing is fetched
// before the clock is set, for the reason Fetch gives. A catalog that is not there is ErrNotFound; one
// whose signature is not there is not.
func FetchSignedCatalog(ctx context.Context, url string, max int64) (body, sig []byte, err error) {
	return fetchSignedCatalog(ctx, url, max, VerifyCatalog)
}

// FetchSignedCatalogV2 is FetchSignedCatalog for the format 2 catalog, checked over CatalogDomainV2.
func FetchSignedCatalogV2(ctx context.Context, url string, max int64) (body, sig []byte, err error) {
	return fetchSignedCatalog(ctx, url, max, VerifyCatalogV2)
}

func fetchSignedCatalog(ctx context.Context, url string, max int64, check func(body, sig []byte) error) (body, sig []byte, err error) {
	if !clockSet() {
		return nil, nil, ErrClock
	}
	ctx, cancel := context.WithTimeout(ctx, manifestTimeout)
	defer cancel()
	body, err = get(ctx, url, max)
	if err != nil {
		return nil, nil, err
	}
	sig, err = get(ctx, url+".sig", 1<<10)
	if err != nil {
		// Not wrapped: a catalog published without its signature is a failure, never ErrNotFound, which
		// a caller may take for the file not being published at all, and fall back from.
		return nil, nil, fmt.Errorf("update: %s has no signature: %v", url, err)
	}
	if err := check(body, sig); err != nil {
		return nil, nil, err
	}
	return body, sig, nil
}

// Sign is what mkmanifest uses: the signature file's contents for a manifest, from the private key
// file's contents (base64 of the 32-byte seed).
func Sign(manifest []byte, seed string) (string, error) {
	s, err := base64.StdEncoding.DecodeString(strings.TrimSpace(seed))
	if err != nil || len(s) != ed25519.SeedSize {
		return "", errors.New("update: the signing key is not a base64 ed25519 seed")
	}
	return base64.StdEncoding.EncodeToString(ed25519.Sign(ed25519.NewKeyFromSeed(s), manifest)) + "\n", nil
}
