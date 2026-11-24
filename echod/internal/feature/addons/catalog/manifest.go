package catalog

import (
	"errors"
	"fmt"
	"net/url"
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
	if m.Page != "" {
		u, err := url.Parse(strings.ReplaceAll(m.Page, "{device}", "device.invalid"))
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("addon.json: the page %q is not a web address", m.Page)
		}
	}
	return ValidPorts(m.Ports, m.Root)
}

// Matches checks a manifest against the catalog entry that pinned it: the same add-on and version, and
// asking for exactly what the entry says it asks for. The hash already says it is the file that was
// looked over; this says the entry describes that file, so the card did not show one thing while the
// firewall opens another.
func (m Manifest) Matches(id string, v Version) error {
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
	return nil
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
