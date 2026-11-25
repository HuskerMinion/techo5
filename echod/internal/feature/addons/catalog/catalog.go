// Package catalog is the add-on catalog's format: the list of add-ons TECHO5 has looked over, which a
// device installs from and nothing else (feature/addons). It is catalog.json in its own repository,
// github.com/HuskerMinion/techo5-addons, and a device believes it only with the release key's
// signature (update.FetchSignedCatalogV2, update.FetchSignedCatalog).
//
// The source is format 2, and cmd/mkcatalog publishes it twice: whole, as catalog-v2.json, and as
// catalog.json, in format 1, with only the versions a format 1 device can read. A format 1 device reads
// its catalog strictly and refuses all of it for one field it does not know, so a version that uses
// anything format 1 has no word for (extra files, today) is left out of its copy rather than breaking
// it. Format 2 is how that never has to happen again: a version names in "requires" the features it
// needs, and a device skips a version that needs one it does not know, instead of refusing the catalog.
// A field is still refused where nothing names it: a version that uses a feature has to require it,
// and a device that knows the feature reads its fields as strictly as the rest.
//
// The format lives here, apart from the feature that runs add-ons, so that the command that checks and
// signs the file and the device that reads it parse it with the same definition: a catalog written by
// hand that the device would refuse is refused before it is signed, rather than found out on devices.
//
// What an entry pins is what was looked over: each version's binaries by their SHA-256, any extra files
// it brings beside them by theirs, the manifest the version ships (addon.json) by its own, and what it
// asks of the device, its ports and whether it runs as root. A device opens in its firewall only what the catalog says, so a manifest that asks for
// more than its entry is refused even when its hash would match.
package catalog

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/lib/safename"
	"github.com/HuskerMinion/techo5/echod/internal/update"
)

// Format is the newest catalog format this build reads, and FormatV1 the first, which devices before
// format 2 read and nothing else. A catalog of a newer format is refused rather than half read: a field
// this build does not know could be one that limits what an add-on may do. Format 2 grows by Features
// rather than by new formats; a new format is for a change a version's requires cannot carry, and goes
// out under a file name of its own beside these.
const (
	FormatV1 = 1
	Format   = 2
)

// Features are what a format 2 version can require that this build knows: "files", the extra files a
// version brings (Version.Files). A version that requires anything else is for a newer TECHO5, and is
// skipped. Each feature here is a field a version may use only when it requires the feature.
var Features = []string{"files"}

// Catalog is the whole file.
type Catalog struct {
	Format int `json:"format"`

	// Serial goes up with every catalog signed. A device keeps the highest it has believed and refuses
	// a lower one: an old catalog is signed as well as today's, and served again it would bring back a
	// listing since withdrawn.
	Serial int64 `json:"serial"`

	// Expires is when the catalog stops being believed for new installs and updates: cmd/mkcatalog sets
	// it a month after signing. A device kept from every newer catalog (by a network that serves it an
	// old one, or none) would otherwise go on offering a version withdrawn since for as long as it ran.
	// Its installed add-ons keep running; only the offers stop, and the card says why. The repository's
	// copy has none: it is stamped into the copy that is signed.
	Expires time.Time `json:"expires,omitzero"`

	Addons []Addon `json:"addons"`
}

// Expired is whether the catalog may no longer be installed from at now: past its expiry, or never
// stamped with one.
func (c Catalog) Expired(now time.Time) bool { return c.Expires.IsZero() || !now.Before(c.Expires) }

// Addon is one add-on and every version of it that was looked over.
type Addon struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Author  string `json:"author"`
	Summary string `json:"summary"`
	Repo    string `json:"repo"`
	Page    string `json:"page"`

	// Withdrawn, when not empty, is why the add-on is no longer listed. The entry stays so a device
	// that has it can say so and suggest removing it; nothing of it is installed from then on.
	Withdrawn string `json:"withdrawn,omitempty"`

	Versions []Version `json:"versions"`

	// Skipped are the versions this build left out as needing a feature it does not know (Parse), by
	// version: an add-on with nothing else is shown as needing a newer TECHO5, not hidden.
	Skipped []string `json:"-"`
}

// NeedsNewer is whether every version of the add-on needs a newer TECHO5 than this build to read.
func (a Addon) NeedsNewer() bool { return len(a.Versions) == 0 && len(a.Skipped) > 0 }

// Version is one release of an add-on, as it was looked over. A new release is a new entry: a version
// is never trusted because an earlier one was.
type Version struct {
	Version string `json:"version"`

	// Requires names the features the version needs (Features), in format 2: a device that does not
	// know one skips the version, rather than refusing the catalog over a field it cannot read. A
	// version that uses a feature names it, so a device that knows it reads it strictly. Format 1 has
	// none, and its copy of the catalog leaves out every version that requires anything (V1).
	Requires []string `json:"requires,omitempty"`

	// MinTECHO5 is the oldest TECHO5 each family runs it on, by family ("show", "spot", "dot"). A
	// family it does not name is one it does not run on.
	MinTECHO5 map[string]string `json:"min_techo5"`

	// Boards narrows the families to these boards (layout.Board: "cronos", "athena", "rook", "biscuit"
	// and the rest). Empty is every board of the families named.
	Boards []string `json:"boards,omitempty"`

	// Binaries are the program for each architecture ("arm", "arm64"): a release asset, its SHA-256
	// and its size.
	Binaries map[string]update.Binary `json:"binaries"`

	// Files are the extra files each build brings beside its program, by the same architecture as
	// Binaries: the one of the TECHO5 build that gets them, whatever is inside them. A program built
	// for arm that unpacks another project's arm64 release lists that release under "arm". Each is
	// pinned as a binary is and installed read-only in the add-on's bin folder under its name, which
	// its addon.json names too. Every build lists the same names, as one addon.json serves them all.
	Files map[string][]File `json:"files,omitempty"`

	// Manifest is the addon.json this version ships, pinned by its hash. A version without one is
	// listed but cannot be installed: what it would run and open is not pinned.
	Manifest *update.Binary `json:"manifest,omitempty"`

	// What it asks of the device, shown before it is installed and checked against its manifest.
	Ports    []Port `json:"ports"`
	Root     bool   `json:"root"`
	MemoryMB int    `json:"memory_mb"`
	DiskMB   int    `json:"disk_mb"`

	// Withdrawn, when not empty, is why this version is no longer listed (a later one may be).
	Withdrawn string `json:"withdrawn,omitempty"`

	// Todo is a note for whoever signs the catalog next, never shown on a device.
	Todo string `json:"todo,omitempty"`
}

// File is one extra file a version pins: the name it is installed under, beside the program, and the
// download it is, checked as the binary is.
type File struct {
	Name string `json:"name"`
	update.Binary
}

// Extra files are held to a few, and to a size the device's storage can take beside everything else:
// they are for what a program needs beside it (another project's release it unpacks, a model), not
// for bringing a second add-on along.
const (
	MaxFiles   = 8
	MaxFilesMB = 128
)

// fileNamePattern is an extra file's name: a plain file name of letters, digits, dots, dashes,
// underscores and pluses, not starting with a dot. It is joined to the add-on's bin folder, and shown
// on the card and in the logs, so nothing that walks a path, hides, or reads as something else.
var fileNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,63}$`)

// ValidFileName reports whether name can be an extra file's name. None ends in ".part": a download
// into bin/ is written as <name>.<hash>.part and, before it starts, clears what an earlier try left
// matching <name>.*.part (update.download), which would take a finished file of such a name with it.
func ValidFileName(name string) bool {
	return fileNamePattern.MatchString(name) && safename.OK(name) && !strings.HasSuffix(name, ".part")
}

// NamesClash is whether two names in one bin/ folder would tread on each other's downloads: the same
// name, or one that is the other followed by a dot. Downloading a, update.download clears a.*.part,
// which is where a.b's download is written.
func NamesClash(a, b string) bool {
	return a == b || strings.HasPrefix(b, a+".") || strings.HasPrefix(a, b+".")
}

// FilesFor is the extra files the build for arch brings.
func (v Version) FilesFor(arch string) []File { return v.Files[arch] }

// FileNames is the names of files, sorted.
func FileNames(files []File) []string {
	names := make([]string, 0, len(files))
	for _, f := range files {
		names = append(names, f.Name)
	}
	slices.Sort(names)
	return names
}

// FilesMB is how much room files take, in whole megabytes, rounded up.
func FilesMB(files []File) int64 {
	var n int64
	for _, f := range files {
		n += f.Size
	}
	return (n + 1<<20 - 1) >> 20
}

// validFiles checks a version's extra files: for an architecture it has a binary for, each a plain
// name given once, pinned, a few of them and not too large; and the same names for every build.
func (v Version) validFiles(what string) error {
	var first []string
	firstArch := ""
	for _, arch := range Arches {
		files, listed := v.Files[arch]
		_, built := v.Binaries[arch]
		switch {
		case listed && !built:
			return fmt.Errorf("catalog: %s has extra files for %s, which it has no binary for", what, arch)
		case listed && len(files) == 0:
			return fmt.Errorf("catalog: %s lists no extra files for %s; leave %s out instead", what, arch, arch)
		case len(files) > MaxFiles:
			return fmt.Errorf("catalog: %s has %d extra files for %s, more than %d", what, len(files), arch, MaxFiles)
		}
		var total int64
		seen := map[string]bool{}
		for _, f := range files {
			if !ValidFileName(f.Name) {
				return fmt.Errorf("catalog: %s's extra file %q for %s is not a plain file name (letters, digits, dots, dashes, underscores and pluses, not starting with a dot)", what, f.Name, arch)
			}
			if seen[f.Name] {
				return fmt.Errorf("catalog: %s lists the extra file %s twice for %s", what, f.Name, arch)
			}
			for other := range seen {
				if NamesClash(other, f.Name) {
					return fmt.Errorf("catalog: %s's extra files %s and %s for %s are one name and the other with a dot after it, whose downloads would tread on each other", what, other, f.Name, arch)
				}
			}
			seen[f.Name] = true
			if err := pinned(f.Binary); err != nil {
				return fmt.Errorf("catalog: %s's extra file %s for %s: %w", what, f.Name, arch, err)
			}
			// Each one is held to the cap on its own before it is added: a few sizes near 2^63 would
			// otherwise wrap the total negative and pass the check below, and the room asked of the
			// device with it.
			if f.Size > MaxFilesMB<<20 {
				return fmt.Errorf("catalog: %s's extra file %s for %s is larger than %d MB", what, f.Name, arch, MaxFilesMB)
			}
			total += f.Size
		}
		if total > MaxFilesMB<<20 {
			return fmt.Errorf("catalog: %s's extra files for %s come to more than %d MB", what, arch, MaxFilesMB)
		}
		// One addon.json names the files for every build (Manifest.Matches), so every build brings the
		// same names; a build with none where another has some could never match it.
		if !built || len(v.Files) == 0 {
			continue
		}
		names := FileNames(files)
		if firstArch == "" {
			first, firstArch = names, arch
		} else if !slices.Equal(first, names) {
			return fmt.Errorf("catalog: %s's builds for %s and %s bring different extra files; one addon.json names them for both", what, firstArch, arch)
		}
	}
	for arch := range v.Files {
		if !slices.Contains(Arches, arch) {
			return fmt.Errorf("catalog: %s has extra files for %q, which is not an architecture (%s)", what, arch, strings.Join(Arches, ", "))
		}
	}
	return nil
}

// Port is one port, or one range of them, an add-on needs open on Wi-Fi.
type Port struct {
	Proto string `json:"proto"` // tcp or udp
	Port  int    `json:"port,omitempty"`
	From  int    `json:"from,omitempty"`
	To    int    `json:"to,omitempty"`
	Why   string `json:"why"`
}

// Families are the device families a version can name, one per build (feature/addons family_*.go).
// The Panel is not among them: Android does not let an app run a program it downloaded.
var Families = []string{"show", "spot", "dot"}

// Boards are the boards a version can name, by family.
var Boards = map[string][]string{
	"show": {"cronos", "checkers", "crown", "athena", "skylight"},
	"spot": {"rook"},
	"dot":  {"biscuit", "radar", "donut", "cupcake"},
}

// Arches are the architectures a binary can be for.
var Arches = []string{"arm", "arm64"}

// idPattern is an add-on id: lowercase letters, digits and dashes, starting with a letter. It names a
// folder and a firewall rule's owner, and goes into a Home Assistant object id, so nothing else.
var idPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{1,39}$`)

// ValidID reports whether id can be an add-on's id.
func ValidID(id string) bool { return idPattern.MatchString(id) }

// Reserved are the ports TECHO5 serves itself (tools/linux/rootfs/usr/local/sbin/techo5-firewall, and
// the Dot's): an add-on may not ask for one. It could not listen there beside the daemon anyway, and a
// listing that seemed to open one would say something about the device that is not so.
var Reserved = []Port{
	{Proto: "tcp", Port: 22}, {Proto: "tcp", Port: 6053}, {Proto: "udp", Port: 5353}, {Proto: "tcp", Port: 8928},
	{Proto: "tcp", Port: 8940}, {Proto: "tcp", Port: 8181}, {Proto: "tcp", Port: 5000}, {Proto: "udp", From: 6001, To: 6010},
	{Proto: "tcp", Port: 4070}, {Proto: "tcp", Port: 8554}, {Proto: "udp", Port: 3702}, {Proto: "udp", Port: 1900},
	{Proto: "tcp", Port: 7000}, {Proto: "udp", From: 319, To: 320}, {Proto: "udp", Port: 68}, {Proto: "udp", Port: 546},
}

// Parse reads a catalog, as a device does, and checks every entry in it, refusing the whole file for
// one bad entry: a catalog is signed as a whole, and one that does not read as intended was not signed
// as intended. The one thing it leaves out rather than refuses is a format 2 version that requires a
// feature this build does not know (Addon.Skipped): that version is for a newer TECHO5, and is not
// read at all, so a field only it has is no reason to refuse the rest.
func Parse(b []byte) (Catalog, error) { return parse(b, true) }

// ParseKnown is Parse for the command that signs the catalog (cmd/mkcatalog), which is built from the
// newest TECHO5 and has to check every version it signs: a version requiring a feature it does not know
// is refused, not skipped.
func ParseKnown(b []byte) (Catalog, error) { return parse(b, false) }

func parse(b []byte, skip bool) (Catalog, error) {
	var c Catalog
	// The whole document is held to the strict reading first, skipped versions and all: one key given
	// twice, or in another case, or anything after the value, refuses it wherever it is.
	if err := checkKeys(b); err != nil {
		return c, fmt.Errorf("catalog: %w", err)
	}
	if !json.Valid(b) {
		return c, errors.New("catalog: not one JSON value")
	}
	var head struct {
		Format int `json:"format"`
	}
	if err := json.Unmarshal(b, &head); err != nil {
		return c, fmt.Errorf("catalog: %w", err)
	}
	var skipped map[int][]string
	if head.Format == Format {
		var err error
		if b, skipped, err = dropUnknown(b, skip); err != nil {
			return c, err
		}
	}
	if err := decodeStrict(b, &c); err != nil {
		return c, fmt.Errorf("catalog: %w", err)
	}
	for i, versions := range skipped {
		c.Addons[i].Skipped = versions
	}
	return c, c.Valid()
}

// dropUnknown takes out of a format 2 catalog every version that requires a feature this build does not
// know, before the rest is read strictly; with skip false it refuses the catalog for one instead. Only
// requires (and the version, to say which) is read from a version before it is dropped: whatever else
// it has is a newer TECHO5's to read. What is dropped is returned by the add-on's place in the list.
func dropUnknown(b []byte, skip bool) ([]byte, map[int][]string, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(b, &top); err != nil {
		return nil, nil, fmt.Errorf("catalog: %w", err)
	}
	var addons []map[string]json.RawMessage
	if raw, ok := top["addons"]; ok {
		if err := json.Unmarshal(raw, &addons); err != nil {
			return nil, nil, fmt.Errorf("catalog: the add-ons: %w", err)
		}
	}
	skipped := map[int][]string{}
	for i, a := range addons {
		raw, ok := a["versions"]
		if !ok {
			continue
		}
		var versions []json.RawMessage
		if err := json.Unmarshal(raw, &versions); err != nil {
			return nil, nil, fmt.Errorf("catalog: add-on %d's versions: %w", i+1, err)
		}
		kept := []json.RawMessage{}
		var id string
		_ = json.Unmarshal(a["id"], &id)
		seen := map[string]bool{}
		for _, v := range versions {
			var peek struct {
				Version  string   `json:"version"`
				Requires []string `json:"requires"`
			}
			if err := json.Unmarshal(v, &peek); err != nil {
				return nil, nil, fmt.Errorf("catalog: add-on %d's versions: %w", i+1, err)
			}
			// A version number is given once in an add-on, skipped or not: the card shows a skipped one's
			// number, and one that was also a version read would be two entries under one name.
			if seen[peek.Version] {
				return nil, nil, fmt.Errorf("catalog: %s %s is listed twice", id, peek.Version)
			}
			seen[peek.Version] = true
			unknown := slices.DeleteFunc(slices.Clone(peek.Requires), func(f string) bool { return slices.Contains(Features, f) })
			if len(unknown) == 0 {
				kept = append(kept, v)
				continue
			}
			// Nothing else of a skipped version is read, but its number is shown, so it is held to the
			// shape every version has.
			if err := update.ValidVersion(peek.Version); err != nil {
				return nil, nil, fmt.Errorf("catalog: %s %q: %w", id, peek.Version, err)
			}
			if !skip {
				return nil, nil, fmt.Errorf("catalog: %s %s requires %s, which this build does not know (%s)", id, peek.Version, strings.Join(unknown, ", "), strings.Join(Features, ", "))
			}
			skipped[i] = append(skipped[i], peek.Version)
		}
		if len(kept) == len(versions) {
			continue
		}
		if a["versions"], ok = marshalRaw(kept); !ok {
			return nil, nil, errors.New("catalog: rewriting the versions")
		}
	}
	if len(skipped) == 0 {
		return b, nil, nil
	}
	var ok bool
	if top["addons"], ok = marshalRaw(addons); !ok {
		return nil, nil, errors.New("catalog: rewriting the add-ons")
	}
	out, err := json.Marshal(top)
	return out, skipped, err
}

func marshalRaw(v any) (json.RawMessage, bool) {
	b, err := json.Marshal(v)
	return b, err == nil
}

// V1 is the catalog as a format 1 device is given it (catalog.json): every version that requires
// nothing, and every add-on with one left. Such a device refuses a whole catalog for one field it does
// not know, so a version that uses anything newer is left out of its copy, not written into it.
func (c Catalog) V1() Catalog {
	out := Catalog{Format: FormatV1, Serial: c.Serial, Expires: c.Expires, Addons: []Addon{}}
	for _, a := range c.Addons {
		var versions []Version
		for _, v := range a.Versions {
			if len(v.Requires) == 0 {
				versions = append(versions, v)
			}
		}
		if len(versions) == 0 {
			continue
		}
		a.Versions, a.Skipped = versions, nil
		out.Addons = append(out.Addons, a)
	}
	return out
}

// Valid checks the whole catalog.
func (c Catalog) Valid() error {
	switch {
	case c.Format == 0:
		return errors.New("catalog: no format")
	case c.Format > Format:
		return fmt.Errorf("catalog: format %d is newer than this build reads (%d)", c.Format, Format)
	case c.Serial <= 0:
		return errors.New("catalog: no serial")
	}
	seen := map[string]bool{}
	for _, a := range c.Addons {
		if seen[a.ID] {
			return fmt.Errorf("catalog: %s is listed twice", a.ID)
		}
		seen[a.ID] = true
		if err := a.valid(c.Format); err != nil {
			return err
		}
	}
	return nil
}

// valid checks one add-on and every version of it, in a catalog of the format given.
func (a Addon) valid(format int) error {
	switch {
	case !ValidID(a.ID):
		return fmt.Errorf("catalog: %q is not an add-on id (lowercase letters, digits and dashes)", a.ID)
	case strings.TrimSpace(a.Name) == "":
		return fmt.Errorf("catalog: %s has no name", a.ID)
	case strings.TrimSpace(a.Author) == "":
		return fmt.Errorf("catalog: %s has no author", a.ID)
	case strings.TrimSpace(a.Summary) == "":
		return fmt.Errorf("catalog: %s says nothing about what it does", a.ID)
	case !webLink(a.Repo):
		return fmt.Errorf("catalog: %s's repository is not an https link", a.ID)
	case a.Page != "" && !webLink(a.Page):
		return fmt.Errorf("catalog: %s's page is not an https link", a.ID)
	case len(a.Versions) == 0 && len(a.Skipped) == 0:
		return fmt.Errorf("catalog: %s has no versions", a.ID)
	}
	seen := map[string]bool{}
	for _, v := range a.Versions {
		if seen[v.Version] {
			return fmt.Errorf("catalog: %s %s is listed twice", a.ID, v.Version)
		}
		seen[v.Version] = true
		if err := v.valid(a.ID, format); err != nil {
			return err
		}
	}
	return nil
}

func (v Version) valid(id string, format int) error {
	what := id + " " + v.Version
	if err := update.ValidVersion(v.Version); err != nil {
		return fmt.Errorf("catalog: %s: %w", what, err)
	}
	if err := v.validRequires(what, format); err != nil {
		return err
	}
	if len(v.MinTECHO5) == 0 {
		return fmt.Errorf("catalog: %s names no family it runs on", what)
	}
	for family, min := range v.MinTECHO5 {
		if !slices.Contains(Families, family) {
			return fmt.Errorf("catalog: %s names %q, which is not a family (%s)", what, family, strings.Join(Families, ", "))
		}
		if err := update.ValidVersion(min); err != nil {
			return fmt.Errorf("catalog: %s's TECHO5 for %s: %w", what, family, err)
		}
	}
	for _, b := range v.Boards {
		if !v.namesBoard(b) {
			return fmt.Errorf("catalog: %s names board %q, which is not one of the families it names", what, b)
		}
	}
	if len(v.Binaries) == 0 {
		return fmt.Errorf("catalog: %s has no binaries", what)
	}
	for arch, b := range v.Binaries {
		if !slices.Contains(Arches, arch) {
			return fmt.Errorf("catalog: %s has a binary for %q, which is not an architecture (%s)", what, arch, strings.Join(Arches, ", "))
		}
		if err := pinned(b); err != nil {
			return fmt.Errorf("catalog: %s's %s binary: %w", what, arch, err)
		}
	}
	if err := v.validFiles(what); err != nil {
		return err
	}
	if v.Manifest != nil {
		if err := pinned(*v.Manifest); err != nil {
			return fmt.Errorf("catalog: %s's manifest: %w", what, err)
		}
	}
	if err := ValidPorts(v.Ports, v.Root); err != nil {
		return fmt.Errorf("catalog: %s: %w", what, err)
	}
	switch {
	case v.MemoryMB <= 0:
		return fmt.Errorf("catalog: %s gives no memory use", what)
	case v.DiskMB <= 0:
		return fmt.Errorf("catalog: %s gives no disk use", what)
	}
	return nil
}

// uses is the features the version's fields use, which it has to require.
func (v Version) uses() []string {
	var f []string
	if len(v.Files) > 0 {
		f = append(f, "files")
	}
	return f
}

// validRequires checks what a version requires against what it uses. Format 1 has no requires, nor any
// field that would need one: a format 1 device cannot read either. In format 2 each requirement is a
// feature this build knows, named once; each feature the version uses is required, so a device that
// does not know it skips the version rather than reading it without the part it cannot see; and
// nothing is required that is not used, which would keep the version from format 1 devices for nothing.
func (v Version) validRequires(what string, format int) error {
	uses := v.uses()
	if format < Format {
		switch {
		case len(v.Requires) > 0:
			return fmt.Errorf("catalog: %s requires %s, which format %d cannot say", what, strings.Join(v.Requires, ", "), format)
		case len(uses) > 0:
			return fmt.Errorf("catalog: %s uses %s, which format %d cannot carry", what, strings.Join(uses, ", "), format)
		}
		return nil
	}
	for i, f := range v.Requires {
		switch {
		case !slices.Contains(Features, f):
			return fmt.Errorf("catalog: %s requires %q, which this build does not know (%s)", what, f, strings.Join(Features, ", "))
		case slices.Contains(v.Requires[:i], f):
			return fmt.Errorf("catalog: %s requires %s twice", what, f)
		case !slices.Contains(uses, f):
			return fmt.Errorf("catalog: %s requires %s but does not use it, which would keep it from older devices for nothing", what, f)
		}
	}
	for _, f := range uses {
		if !slices.Contains(v.Requires, f) {
			return fmt.Errorf("catalog: %s uses %s but does not require it: a device that does not know %s would read the version without them", what, f, f)
		}
	}
	return nil
}

// namesBoard is whether board is a board of one of the families the version names.
func (v Version) namesBoard(board string) bool {
	for family := range v.MinTECHO5 {
		if slices.Contains(Boards[family], board) {
			return true
		}
	}
	return false
}

// pinned is a download described well enough to be checked: an https link, a hash and a size.
func pinned(b update.Binary) error {
	switch {
	case !webLink(b.URL):
		return errors.New("its url is not an https link")
	case len(b.SHA256) != 64 || strings.Trim(b.SHA256, "0123456789abcdef") != "":
		return errors.New("its sha256 is not 64 lowercase hex characters")
	case b.Size <= 0:
		return errors.New("it gives no size")
	}
	return nil
}

func webLink(s string) bool {
	u, err := url.Parse(s)
	return err == nil && u.Scheme == "https" && u.Host != ""
}

// ValidPorts checks what an add-on asks to have opened: tcp or udp, a port or a range, each with a
// reason, none of TECHO5's own, and none below 1024 for an add-on that does not run as root, which
// could not listen there.
func ValidPorts(ports []Port, root bool) error {
	for i, p := range ports {
		lo, hi := p.Range()
		switch {
		case p.Proto != "tcp" && p.Proto != "udp":
			return fmt.Errorf("port %d: %q is not tcp or udp", i+1, p.Proto)
		case p.Port != 0 && (p.From != 0 || p.To != 0):
			return fmt.Errorf("port %d: give a port or a range, not both", i+1)
		case lo < 1 || hi > 65535 || lo > hi:
			return fmt.Errorf("port %d: %s is not a port or a range of them", i+1, p)
		case p.Port == 0 && lo == hi:
			return fmt.Errorf("port %d: a range of one port is a port", i+1)
		case strings.TrimSpace(p.Why) == "":
			return fmt.Errorf("port %d: %s says nothing about why it is needed", i+1, p)
		case lo < 1024 && !root:
			return fmt.Errorf("port %d: %s is below 1024, which only a program running as root can listen on", i+1, p)
		}
		for _, r := range Reserved {
			if p.Overlaps(r) {
				return fmt.Errorf("port %d: %s is TECHO5's own", i+1, p)
			}
		}
		for _, q := range ports[:i] {
			if p.Overlaps(q) {
				return fmt.Errorf("port %d: %s is asked for twice", i+1, p)
			}
		}
	}
	return nil
}

// Range is the ports as a range, a single port being a range of one.
func (p Port) Range() (lo, hi int) {
	if p.Port != 0 {
		return p.Port, p.Port
	}
	return p.From, p.To
}

// Overlaps is whether two entries share a port of the same protocol.
func (p Port) Overlaps(q Port) bool {
	if p.Proto != q.Proto {
		return false
	}
	plo, phi := p.Range()
	qlo, qhi := q.Range()
	return plo <= qhi && qlo <= phi
}

// Covers is whether every port of q is one of p's.
func (p Port) Covers(q Port) bool {
	if p.Proto != q.Proto {
		return false
	}
	plo, phi := p.Range()
	qlo, qhi := q.Range()
	return plo <= qlo && qhi <= phi
}

// String is the port as the card and the firewall write it: "32500/tcp", "32410-32414/udp".
func (p Port) String() string {
	lo, hi := p.Range()
	if lo == hi {
		return fmt.Sprintf("%d/%s", lo, p.Proto)
	}
	return fmt.Sprintf("%d-%d/%s", lo, hi, p.Proto)
}

// Listed is whether the add-on is still listed, and the version with it.
func (a Addon) Listed(v Version) bool { return a.Withdrawn == "" && v.Withdrawn == "" }

// Find is the add-on with this id.
func (c Catalog) Find(id string) (Addon, bool) {
	for _, a := range c.Addons {
		if a.ID == id {
			return a, true
		}
	}
	return Addon{}, false
}

// Version is the entry for one version.
func (a Addon) Version(version string) (Version, bool) {
	for _, v := range a.Versions {
		if v.Version == version {
			return v, true
		}
	}
	return Version{}, false
}

// Device is what a version is checked against: the family of this build, its board, the TECHO5
// version it runs, and its architecture.
type Device struct {
	Family, Board, Running, Arch string
}

// RunsOn says whether the version can be installed on the device, and when it cannot, why, in words
// for the card.
func (v Version) RunsOn(d Device) (bool, string) {
	min, ok := v.MinTECHO5[d.Family]
	if !ok {
		return false, "not made for this kind of device"
	}
	if len(v.Boards) > 0 && !slices.Contains(v.Boards, d.Board) {
		return false, "not made for this model"
	}
	if _, ok := v.Binaries[d.Arch]; !ok {
		return false, "no build of it for this device's processor (" + d.Arch + ")"
	}
	if older(d.Running, min) {
		return false, "needs TECHO5 " + min + " or later"
	}
	if v.Manifest == nil {
		return false, "its release does not carry its manifest yet, so it cannot be installed"
	}
	return true, ""
}

// older is whether running ranks below min. A development build is not held back: an unstamped one is
// v0.0.0-dev, which ranks below every release (layout.Version), and one that cannot be ranked at all is
// left to whoever presses Install, as update.Newer leaves it. Both are somebody trying a build, who
// would otherwise be told every add-on needs a newer TECHO5.
func older(running, min string) bool {
	return running != min && update.Newer(min, running) && update.Newer(running, "v0.0.1")
}

// Newest is the newest version that is listed and runs on the device, if there is one.
func (a Addon) Newest(d Device) (Version, bool) {
	var best Version
	found := false
	if a.Withdrawn != "" {
		return best, false
	}
	for _, v := range a.Versions {
		if v.Withdrawn != "" {
			continue
		}
		if ok, _ := v.RunsOn(d); !ok {
			continue
		}
		if !found || (v.Version != best.Version && update.Newer(v.Version, best.Version)) {
			best, found = v, true
		}
	}
	return best, found
}

// Latest is the newest version listed at all, whether or not it runs on the device: the card shows it
// grayed out with the reason when nothing runs.
func (a Addon) Latest() (Version, bool) {
	var best Version
	found := false
	for _, v := range a.Versions {
		if v.Withdrawn != "" {
			continue
		}
		if !found || (v.Version != best.Version && update.Newer(v.Version, best.Version)) {
			best, found = v, true
		}
	}
	return best, found
}

// Asks is what an add-on asks of the device beyond being run: what the owner agrees to with the box.
// Files are the extra files its build for this device brings, by name and hash: what is put on the
// device beside the program is agreed to as the program is, so a new or changed one asks again.
type Asks struct {
	Ports []Port    `json:"ports"`
	Root  bool      `json:"root"`
	Files []FilePin `json:"files,omitempty"`
}

// FilePin is an extra file as it was agreed to.
type FilePin struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
}

// Asks is what this version asks of a device of the architecture arch.
func (v Version) Asks(arch string) Asks {
	a := Asks{Ports: slices.Clone(v.Ports), Root: v.Root}
	for _, f := range v.FilesFor(arch) {
		a.Files = append(a.Files, FilePin{Name: f.Name, SHA256: f.SHA256})
	}
	return a
}

// More is what a asks that was not agreed to in before, in words for the card: each port not covered
// by one agreed to, root when it was not, and each extra file not agreed to as it is now. Nothing means
// the box need not be asked again.
func (a Asks) More(before Asks) []string {
	var more []string
	if a.Root && !before.Root {
		more = append(more, "to run as root")
	}
	for _, p := range a.Ports {
		covered := false
		for _, q := range before.Ports {
			if q.Covers(p) {
				covered = true
				break
			}
		}
		if !covered {
			more = append(more, "port "+p.String())
		}
	}
	for _, f := range a.Files {
		i := slices.IndexFunc(before.Files, func(g FilePin) bool { return g.Name == f.Name })
		switch {
		case i < 0:
			more = append(more, "the extra file "+f.Name)
		case before.Files[i].SHA256 != f.SHA256:
			more = append(more, "a changed "+f.Name)
		}
	}
	return more
}

// Same is whether two lists of ports open the same ports, in any order.
func Same(a, b []Port) bool {
	if len(a) != len(b) {
		return false
	}
	for _, p := range a {
		found := false
		for _, q := range b {
			if p.Proto == q.Proto && p.Port == q.Port && p.From == q.From && p.To == q.To {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
