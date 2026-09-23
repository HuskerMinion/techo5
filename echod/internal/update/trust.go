package update

import (
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
}

// clockSet is how an unset clock is recognized: this kernel starts in 1970 and NTP corrects it a
// little after boot. Certificates cannot be checked against a clock like that, so nothing is fetched.
var clockSet = func() bool { return time.Now().Year() >= 2025 }

var errClock = errors.New("update: the clock is not set yet; checking later")

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

// Sign is what mkmanifest uses: the signature file's contents for a manifest, from the private key
// file's contents (base64 of the 32-byte seed).
func Sign(manifest []byte, seed string) (string, error) {
	s, err := base64.StdEncoding.DecodeString(strings.TrimSpace(seed))
	if err != nil || len(s) != ed25519.SeedSize {
		return "", errors.New("update: the signing key is not a base64 ed25519 seed")
	}
	return base64.StdEncoding.EncodeToString(ed25519.Sign(ed25519.NewKeyFromSeed(s), manifest)) + "\n", nil
}
