// Command mkcatalog checks the add-on catalog (catalog.json in github.com/HuskerMinion/techo5-addons)
// and signs it with the release key, writing catalog.json.sig beside a copy of it.
// tools/sign-catalog.ps1 runs it on the computer that holds the key, and publishes the two;
// techo5-addons builds it from this repository's main to check its catalog on every push.
//
// It parses the file with the same definition a device does (feature/addons/catalog), so a catalog a
// device would refuse is refused here, before it is signed. What is signed is techo5-addons' catalog
// with one thing added, its expiry a month from now (Catalog.Expires), written out as the device will
// read it; the signature is over update.CatalogDomain and those bytes, so it can never verify as a
// firmware manifest's. Against the published catalog (-previous) its serial has to go up, and no
// version entry once published may change except to be withdrawn (catalog.Unchanged).
//
// With -pin it measures a downloaded file instead, and prints the url, sha256 and size an entry pins it
// by, so an entry is never typed by hand.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/addons/catalog"
	"github.com/HuskerMinion/techo5/echod/internal/update"
)

func main() {
	var (
		in       = flag.String("in", "catalog.json", "the catalog to check and sign (a techo5-addons checkout's)")
		previous = flag.String("previous", "", "the catalog published now, whose serial this one has to be above")
		out      = flag.String("out", "", "the folder to write catalog.json and catalog.json.sig to")
		signKey  = flag.String("sign-key", "", "the release signing key file")
		pin      = flag.String("pin", "", "URL=FILE: print the url, sha256 and size an entry pins FILE by, and nothing else")
		manifest = flag.String("manifest", "", "an add-on's addon.json to check, against its entry in -in when it has one, and nothing else")
	)
	flag.Parse()

	if *manifest != "" {
		if err := checkManifest(os.Stderr, *manifest, *in); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if *pin != "" {
		if err := printPin(os.Stdout, *pin); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if err := run(*in, *previous, *out, *signKey); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// sameEntries is whether two catalogs list the same, whatever their expiry.
func sameEntries(a, b catalog.Catalog) bool {
	a.Expires, b.Expires = time.Time{}, time.Time{}
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	return string(ja) == string(jb)
}

// expiresAfter is how long a signed catalog is believed for new installs and updates. Signing it again
// within that, as every release does, keeps a device that can reach it offering what it lists.
const expiresAfter = 30 * 24 * time.Hour

// now is the clock; a variable, for tests.
var now = time.Now

// run checks the catalog, against the published one when given, and writes it and its signature.
func run(in, previous, out, keyFile string) error {
	body, err := os.ReadFile(in)
	if err != nil {
		return err
	}
	c, err := catalog.Parse(body)
	if err != nil {
		return err
	}
	if previous != "" {
		old, err := os.ReadFile(previous)
		if err != nil {
			return err
		}
		p, err := catalog.Parse(old)
		if err != nil {
			return fmt.Errorf("mkcatalog: reading the published catalog: %w", err)
		}
		if err := catalog.Unchanged(p, c); err != nil {
			return err
		}
		// A device refuses a catalog whose serial is below the one it has, so one signed with the same
		// or a lower serial would never reach a device that has the published one.
		// The same serial is only a fresh expiry for the same entries, which a device takes (it refuses
		// only a lower serial); anything else that changed has to raise it.
		if c.Serial < p.Serial || (c.Serial == p.Serial && !sameEntries(c, p)) {
			return fmt.Errorf("mkcatalog: the serial is %d and the published catalog's is %d: raise it", c.Serial, p.Serial)
		}
	}
	for _, a := range c.Addons {
		for _, v := range a.Versions {
			if v.Todo != "" {
				fmt.Fprintf(os.Stderr, "mkcatalog: %s %s: %s\n", a.ID, v.Version, v.Todo)
			}
			if v.Manifest == nil && v.Withdrawn == "" && a.Withdrawn == "" {
				fmt.Fprintf(os.Stderr, "mkcatalog: %s %s pins no manifest: devices list it but cannot install it\n", a.ID, v.Version)
			}
		}
	}
	if out == "" || keyFile == "" {
		if out != "" || keyFile != "" {
			return errors.New("mkcatalog: -out and -sign-key go together")
		}
		fmt.Fprintf(os.Stderr, "mkcatalog: %s is a catalog a device would take (serial %d, %d add-ons); not signed\n", in, c.Serial, len(c.Addons))
		return nil
	}
	seed, err := os.ReadFile(keyFile)
	if err != nil {
		return err
	}
	c.Expires = now().Add(expiresAfter).UTC().Truncate(time.Second)
	body, err = json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	if _, err := catalog.Parse(body); err != nil {
		return fmt.Errorf("mkcatalog: the stamped catalog does not read back: %w", err)
	}
	sig, err := update.SignCatalog(body, string(seed))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(out, "catalog.json"), body, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(out, "catalog.json.sig"), []byte(sig), 0o644); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "mkcatalog: signed serial %d, expiring %s, into %s\n", c.Serial, c.Expires.Format(time.RFC3339), out)
	return nil
}

// checkManifest reads an add-on's addon.json as a device would, and when the catalog has an entry for its
// version, checks that the entry describes it: what an author sends for listing is checked before its
// hash is pinned.
func checkManifest(w io.Writer, path, in string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	m, err := catalog.ParseManifest(b)
	if err != nil {
		return err
	}
	if body, err := os.ReadFile(in); err == nil {
		if c, err := catalog.Parse(body); err == nil {
			if a, ok := c.Find(m.ID); ok {
				if v, ok := a.Version(m.Version); ok {
					if err := m.Matches(m.ID, v); err != nil {
						return fmt.Errorf("mkcatalog: %s does not match its entry: %w", path, err)
					}
					fmt.Fprintf(w, "mkcatalog: %s is %s %s, and matches its entry\n", path, m.ID, m.Version)
					return nil
				}
			}
		}
	}
	fmt.Fprintf(w, "mkcatalog: %s is %s %s, a manifest a device would take; the catalog has no entry for it yet\n", path, m.ID, m.Version)
	return nil
}

// printPin writes the url, sha256 and size of a file as an entry pins it.
func printPin(w io.Writer, spec string) error {
	url, path, ok := strings.Cut(spec, "=")
	if !ok || !strings.HasPrefix(url, "https://") || path == "" {
		return fmt.Errorf("mkcatalog: -pin needs URL=FILE with an https url, not %q", spec)
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return err
	}
	b, _ := json.MarshalIndent(update.Binary{URL: url, SHA256: hex.EncodeToString(h.Sum(nil)), Size: n}, "", "  ")
	_, err = fmt.Fprintln(w, string(b))
	return err
}
