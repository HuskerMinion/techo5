// Package catalog is the add-on catalog's format: the list of add-ons TECHO5 has looked over, which a
// device installs from and nothing else (feature/addons). It is catalog.json in its own repository,
// github.com/HuskerMinion/techo5-addons, signed with the release key into catalog.json.sig
// (cmd/mkcatalog, tools/sign-catalog.ps1), and a device believes it only with that signature
// (update.FetchSigned).
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

// Format is the catalog format this build reads. A catalog of a newer format is refused rather than
// half read: a field this build does not know could be one that limits what an add-on may do.
const Format = 1

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
}

// Version is one release of an add-on, as it was looked over. A new release is a new entry: a version
// is never trusted because an earlier one was.
type Version struct {
	Version string `json:"version"`

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

// ValidFileName reports whether name can be an extra file's name.
func ValidFileName(name string) bool { return fileNamePattern.MatchString(name) && safename.OK(name) }

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
			seen[f.Name] = true
			if err := pinned(f.Binary); err != nil {
				return fmt.Errorf("catalog: %s's extra file %s for %s: %w", what, f.Name, arch, err)
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
	{Proto: "tcp", Port: 8181}, {Proto: "tcp", Port: 5000}, {Proto: "udp", From: 6001, To: 6010},
	{Proto: "tcp", Port: 4070}, {Proto: "tcp", Port: 8554}, {Proto: "udp", Port: 3702}, {Proto: "udp", Port: 1900},
	{Proto: "tcp", Port: 7000}, {Proto: "udp", From: 319, To: 320}, {Proto: "udp", Port: 68}, {Proto: "udp", Port: 546},
}

// Parse reads a catalog and checks every entry in it, refusing the whole file for one bad entry: a
// catalog is signed as a whole, and one that does not read as intended was not signed as intended.
func Parse(b []byte) (Catalog, error) {
	var c Catalog
	if err := decodeStrict(b, &c); err != nil {
		return c, fmt.Errorf("catalog: %w", err)
	}
	return c, c.Valid()
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
		if err := a.Valid(); err != nil {
			return err
		}
	}
	return nil
}

// Valid checks one add-on and every version of it.
func (a Addon) Valid() error {
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
	case len(a.Versions) == 0:
		return fmt.Errorf("catalog: %s has no versions", a.ID)
	}
	seen := map[string]bool{}
	for _, v := range a.Versions {
		if seen[v.Version] {
			return fmt.Errorf("catalog: %s %s is listed twice", a.ID, v.Version)
		}
		seen[v.Version] = true
		if err := v.valid(a.ID); err != nil {
			return err
		}
	}
	return nil
}

func (v Version) valid(id string) error {
	what := id + " " + v.Version
	if err := update.ValidVersion(v.Version); err != nil {
		return fmt.Errorf("catalog: %s: %w", what, err)
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
