// Command mkcatalog checks the add-on catalog (catalog.json in github.com/HuskerMinion/techo5-addons)
// and signs it with the release key, into the two files devices read: catalog-v2.json, the whole
// catalog in format 2, and catalog.json, in format 1, for devices that read only that, with the
// versions they cannot read left out (catalog.V1). Each has its signature beside it (.sig).
// tools/sign-catalog.ps1 runs it on the computer that holds the key, and publishes the four;
// techo5-addons builds it from this repository's main to check its catalog on every push.
//
// It parses the file with the same definition a device does (feature/addons/catalog), so a catalog a
// device would refuse is refused here, before it is signed; and as the newest TECHO5 reads it, so a
// version requiring a feature this build does not know is refused, not skipped. What is signed is
// techo5-addons' catalog with one thing added, its expiry a month from now (Catalog.Expires), written
// out as the device will read it; the signatures are over update.CatalogDomainV2 and
// update.CatalogDomain ahead of those bytes, so neither verifies as the other or as a firmware
// manifest's. Against the published catalogs (-previous, either format, given once for each) its
// serial has to go up, and no version entry once published may change except to be withdrawn
// (catalog.Unchanged). The published catalog-v2.json has to be among them whenever any is given, as
// catalog.json alone does not hold every version, except at the one signing that first publishes it
// (-first-format2).
//
// With -pin it measures a downloaded file instead, and prints the url, sha256 and size an entry pins it
// by, so an entry is never typed by hand; with -file as well, the pin is an extra file's, under that
// name (catalog.File).
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
		previous previousList
		firstV2  = flag.Bool("first-format2", false, "the first signing of catalog-v2.json: -previous may then be catalog.json alone, there being no catalog-v2.json published yet")
		out      = flag.String("out", "", "the folder to write catalog-v2.json, catalog.json and their .sig files to")
		signKey  = flag.String("sign-key", "", "the release signing key file")
		pin      = flag.String("pin", "", "URL=FILE: print the url, sha256 and size an entry pins FILE by, and nothing else")
		manifest = flag.String("manifest", "", "an add-on's addon.json to check, against its entry in -in when it has one, and nothing else")
		file     = flag.String("file", "", "with -pin: the name an extra file is installed under, to print its pin as an entry's \"files\" lists it")
	)
	flag.Var(&previous, "previous", "a catalog published now, of either format, whose serial this one has to be above; once for each")
	flag.Parse()

	if *manifest != "" {
		if err := checkManifest(os.Stderr, *manifest, *in); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if *file != "" && *pin == "" {
		fmt.Fprintln(os.Stderr, "mkcatalog: -file goes with -pin")
		os.Exit(1)
	}
	if *pin != "" {
		if err := printPin(os.Stdout, *pin, *file); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if err := run(*in, previous, *firstV2, *out, *signKey); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// previousList is -previous, given once for each published file.
type previousList []string

func (p *previousList) String() string { return strings.Join(*p, ",") }

func (p *previousList) Set(s string) error {
	*p = append(*p, s)
	return nil
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

// run checks the catalog, against the published ones when given, and writes both formats of it and
// their signatures.
func run(in string, previous []string, firstV2 bool, out, keyFile string) error {
	body, err := os.ReadFile(in)
	if err != nil {
		return err
	}
	c, err := catalog.ParseKnown(body)
	if err != nil {
		return err
	}
	// The format 1 file is written from the format 2 source, never kept by hand: one source, one serial.
	if c.Format != catalog.Format {
		return fmt.Errorf("mkcatalog: %s is format %d; the source catalog is format %d, which the format %d file is written from", in, c.Format, catalog.Format, catalog.FormatV1)
	}
	sawV2 := false
	for _, path := range previous {
		old, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		p, err := catalog.ParseKnown(old)
		if err != nil {
			return fmt.Errorf("mkcatalog: reading the published catalog %s: %w", path, err)
		}
		sawV2 = sawV2 || p.Format == catalog.Format
		// A version a device installed from either file stays as it read it there.
		if err := catalog.Unchanged(p, c); err != nil {
			return err
		}
		// The published file is compared with what this one would be written as in its format: a format
		// 1 file holds only what format 1 can carry.
		next := c
		if p.Format == catalog.FormatV1 {
			next = c.V1()
		}
		// A device refuses a catalog whose serial is below the one it has, so one signed with the same
		// or a lower serial would never reach a device that has the published one.
		// The same serial is only a fresh expiry for the same entries, which a device takes (it refuses
		// only a lower serial); anything else that changed has to raise it.
		if c.Serial < p.Serial || (c.Serial == p.Serial && !sameEntries(next, p)) {
			return fmt.Errorf("mkcatalog: the serial is %d and the published catalog's (%s) is %d: raise it", c.Serial, filepath.Base(path), p.Serial)
		}
	}
	// Against catalog.json alone, a version only catalog-v2.json lists (one that requires anything) could
	// change unnoticed, so the published catalog-v2.json is wanted too, except at the one signing that
	// first publishes it, which says so.
	switch {
	case len(previous) > 0 && !sawV2 && !firstV2:
		return errors.New("mkcatalog: -previous names no format 2 catalog to check against; give the published catalog-v2.json too, or -first-format2 if this signing first publishes it")
	case sawV2 && firstV2:
		return errors.New("mkcatalog: -first-format2 is for the first signing of catalog-v2.json, and one is given in -previous")
	}
	for _, a := range c.Addons {
		for _, v := range a.Versions {
			if v.Todo != "" {
				fmt.Fprintf(os.Stderr, "mkcatalog: %s %s: %s\n", a.ID, v.Version, v.Todo)
			}
			if v.Manifest == nil && v.Withdrawn == "" && a.Withdrawn == "" {
				fmt.Fprintf(os.Stderr, "mkcatalog: %s %s pins no manifest: devices list it but cannot install it\n", a.ID, v.Version)
			}
			if len(v.Requires) > 0 {
				fmt.Fprintf(os.Stderr, "mkcatalog: %s %s requires %s: left out of the format %d file\n", a.ID, v.Version, strings.Join(v.Requires, ", "), catalog.FormatV1)
			}
		}
	}
	if (out == "") != (keyFile == "") {
		return errors.New("mkcatalog: -out and -sign-key go together")
	}
	signing := out != ""
	if signing {
		c.Expires = now().Add(expiresAfter).UTC().Truncate(time.Second)
	}
	files, err := written(c)
	if err != nil {
		return err
	}
	if !signing {
		fmt.Fprintf(os.Stderr, "mkcatalog: %s is a catalog a device would take (serial %d, %d add-ons, %d of them in the format %d file); not signed\n",
			in, c.Serial, len(c.Addons), len(c.V1().Addons), catalog.FormatV1)
		return nil
	}
	seed, err := os.ReadFile(keyFile)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	for _, f := range files {
		sig, err := f.sign(f.body, string(seed))
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(out, f.name), f.body, 0o644); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(out, f.name+".sig"), []byte(sig), 0o644); err != nil {
			return err
		}
	}
	fmt.Fprintf(os.Stderr, "mkcatalog: signed serial %d, expiring %s, into %s (catalog-v2.json, and catalog.json with %d of %d add-ons)\n",
		c.Serial, c.Expires.Format(time.RFC3339), out, len(c.V1().Addons), len(c.Addons))
	return nil
}

// publishedFile is one file the catalog is published as: its name, its bytes, and how it is signed.
type publishedFile struct {
	name string
	body []byte
	sign func([]byte, string) (string, error)
}

// written is the catalog as the two files it is published as, each read back as a device of its format
// reads it, so a check without signing checks the format 1 file too: a format 1 device refuses a key
// it does not know, and catalog.Parse holds a format 1 file to exactly the keys format 1 has.
func written(c catalog.Catalog) ([]publishedFile, error) {
	var files []publishedFile
	for _, f := range []struct {
		name   string
		format int
		cat    catalog.Catalog
		sign   func([]byte, string) (string, error)
	}{
		{"catalog-v2.json", catalog.Format, c, update.SignCatalogV2},
		{"catalog.json", catalog.FormatV1, c.V1(), update.SignCatalog},
	} {
		body, err := json.MarshalIndent(f.cat, "", "  ")
		if err != nil {
			return nil, err
		}
		body = append(body, '\n')
		back, err := catalog.Parse(body)
		if err == nil && back.Format != f.format {
			err = fmt.Errorf("it reads as format %d", back.Format)
		}
		if err != nil {
			return nil, fmt.Errorf("mkcatalog: the %s written from it does not read back: %w", f.name, err)
		}
		files = append(files, publishedFile{f.name, body, f.sign})
	}
	return files, nil
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
		if c, err := catalog.ParseKnown(body); err == nil {
			if a, ok := c.Find(m.ID); ok {
				if v, ok := a.Version(m.Version); ok {
					// One addon.json serves every build, so it has to match the entry as each build's
					// device reads it: the extra files are pinned per build.
					for _, arch := range catalog.Arches {
						if _, built := v.Binaries[arch]; !built {
							continue
						}
						if err := m.Matches(m.ID, v, arch); err != nil {
							return fmt.Errorf("mkcatalog: %s does not match its entry: %w", path, err)
						}
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

// printPin writes the url, sha256 and size of a file as an entry pins it; with a name, as an extra
// file under that name.
func printPin(w io.Writer, spec, name string) error {
	url, path, ok := strings.Cut(spec, "=")
	if !ok || !strings.HasPrefix(url, "https://") || path == "" {
		return fmt.Errorf("mkcatalog: -pin needs URL=FILE with an https url, not %q", spec)
	}
	if name != "" && !catalog.ValidFileName(name) {
		return fmt.Errorf("mkcatalog: -file %q is not a plain file name (letters, digits, dots, dashes, underscores and pluses, not starting with a dot)", name)
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
	var pinned any = update.Binary{URL: url, SHA256: hex.EncodeToString(h.Sum(nil)), Size: n}
	if name != "" {
		pinned = catalog.File{Name: name, Binary: pinned.(update.Binary)}
	}
	b, _ := json.MarshalIndent(pinned, "", "  ")
	_, err = fmt.Fprintln(w, string(b))
	return err
}
