package catalog

import (
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/HuskerMinion/techo5/echod/internal/lib/safename"
)

// Manifest is addon.json, which an add-on's release carries: what the device runs and what it opens.
// The catalog pins it by its hash, and a device checks it against the catalog's entry as well, so what
// is opened in the firewall is what was shown on the card.
type Manifest struct {
	ID      string `json:"id"`
	Version string `json:"version"`

	// Command is the program and any arguments that come first. The program is the release's binary,
	// installed under this name in the add-on's bin folder: a plain name, never a path.
	Command []string `json:"command"`

	// Args follow the command. {state} in one is the add-on's own folder for what it keeps, which
	// outlives updates.
	Args []string `json:"args,omitempty"`

	// Files are the names of the extra files its catalog entry pins beside the program (Version.Files),
	// installed read-only in the same bin folder under these names, where TECHO5_ADDON_BIN finds them.
	Files []string `json:"files,omitempty"`

	Ports []Port `json:"ports"`
	Root  bool   `json:"root"`

	// Page, when there is one, is the add-on's own page for the card to link to. {device} is the
	// device's address as the browser reached it.
	Page string `json:"page,omitempty"`

	MemoryMB int `json:"memory_mb"`
}

// maxManifest is a sanity bound on addon.json: a few hundred bytes, as the catalog's entry is.
const maxManifest = 16 << 10

// MaxManifest is how much of a manifest is read.
func MaxManifest() int64 { return maxManifest }

// ParseManifest reads addon.json and checks it on its own terms.
func ParseManifest(b []byte) (Manifest, error) {
	var m Manifest
	if len(b) > maxManifest {
		return m, errors.New("addon.json: larger than any manifest should be")
	}
	if err := decodeStrict(b, &m); err != nil {
		return m, fmt.Errorf("addon.json: %w", err)
	}
	return m, m.Valid()
}

// Valid checks a manifest on its own terms.
func (m Manifest) Valid() error {
	switch {
	case !ValidID(m.ID):
		return fmt.Errorf("addon.json: %q is not an add-on id", m.ID)
	case m.Version == "":
		return errors.New("addon.json: no version")
	case len(m.Command) == 0:
		return errors.New("addon.json: no command")
	case !safename.OK(m.Command[0]) || strings.HasPrefix(m.Command[0], ".") || strings.ContainsAny(m.Command[0], " \t"):
		return fmt.Errorf("addon.json: the command %q is not a plain program name", m.Command[0])
	case m.MemoryMB <= 0:
		return errors.New("addon.json: no memory use")
	}
	for _, a := range append(append([]string(nil), m.Command[1:]...), m.Args...) {
		if strings.ContainsRune(a, 0) {
			return errors.New("addon.json: an argument holds a NUL")
		}
	}
	if len(m.Files) > MaxFiles {
		return fmt.Errorf("addon.json: it names %d extra files, more than %d", len(m.Files), MaxFiles)
	}
	for i, f := range m.Files {
		switch {
		case !ValidFileName(f):
			return fmt.Errorf("addon.json: the extra file %q is not a plain file name", f)
		case f == m.Command[0]:
			// Both go into bin/: the file would take the program's place.
			return fmt.Errorf("addon.json: the extra file %q has the program's name", f)
		case NamesClash(f, m.Command[0]):
			// The program's download clears what matches its name and a dot, and the file's the
			// program's likewise (NamesClash).
			return fmt.Errorf("addon.json: the extra file %q and the program %q are one name and the other with a dot after it", f, m.Command[0])
		case slices.Contains(m.Files[:i], f):
			return fmt.Errorf("addon.json: the extra file %q is named twice", f)
		}
		for _, g := range m.Files[:i] {
			if NamesClash(f, g) {
				return fmt.Errorf("addon.json: the extra files %q and %q are one name and the other with a dot after it", g, f)
			}
		}
	}
	if m.Page != "" {
		u, err := url.Parse(strings.ReplaceAll(m.Page, "{device}", "device.invalid"))
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("addon.json: the page %q is not a web address", m.Page)
		}
	}
	return ValidPorts(m.Ports, m.Root)
}

// Matches checks a manifest against the catalog entry that pinned it, for a device of the architecture
// arch: the same add-on and version, asking for exactly what the entry says it asks for, and naming
// exactly the extra files the entry pins for that build. The hash already says it is the file that was
// looked over; this says the entry describes that file, so the card did not show one thing while the
// firewall opens another, or the add-on looks for a file the device was never given.
func (m Manifest) Matches(id string, v Version, arch string) error {
	switch {
	case m.ID != id:
		return fmt.Errorf("addon.json is %s's, not %s's", m.ID, id)
	case m.Version != v.Version:
		return fmt.Errorf("addon.json is for %s, not %s", m.Version, v.Version)
	case m.Root != v.Root:
		return errors.New("addon.json and the catalog differ on whether it runs as root")
	case !Same(m.Ports, v.Ports):
		return errors.New("addon.json asks for other ports than the catalog lists")
	}
	names := slices.Sorted(slices.Values(m.Files))
	if pinned := FileNames(v.FilesFor(arch)); !slices.Equal(names, pinned) {
		return fmt.Errorf("addon.json names other extra files (%s) than the catalog pins for %s (%s)",
			listOrNone(names), arch, listOrNone(pinned))
	}
	return nil
}

func listOrNone(names []string) string {
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}

// Expand is the manifest's arguments with {state} replaced by the add-on's folder for what it keeps.
func (m Manifest) Expand(state string) []string {
	out := make([]string, 0, len(m.Command)-1+len(m.Args))
	for _, a := range append(append([]string(nil), m.Command[1:]...), m.Args...) {
		out = append(out, strings.ReplaceAll(a, "{state}", state))
	}
	return out
}

// PageFor is the add-on's page as a link for a browser that reached the device at host, or "" when it
// has none.
func (m Manifest) PageFor(host string) string {
	if m.Page == "" {
		return ""
	}
	return strings.ReplaceAll(m.Page, "{device}", host)
}
