package catalog

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// Shown is a digest of what the card showed the owner before they ticked the box: the add-on, its
// author, the version, and what that version asks of the device, the extra files it brings among it. The install form carries it back, and
// an install or update goes ahead only while the catalog still says the same, so the box agrees to what
// was read, not to whatever a catalog fetched between the page and the press says.
func Shown(a Addon, v Version) string {
	// The files by name and hash, every build's: what is put on the device beside the program. Left out
	// when there are none, so a listing without any is shown as it was before extra files.
	var files map[string][]FilePin
	for arch, fs := range v.Files {
		if files == nil {
			files = map[string][]FilePin{}
		}
		for _, f := range fs {
			files[arch] = append(files[arch], FilePin{Name: f.Name, SHA256: f.SHA256})
		}
	}
	b, _ := json.Marshal(struct {
		ID, Name, Author, Version string
		Ports                     []Port
		Root                      bool
		MemoryMB, DiskMB          int
		Files                     map[string][]FilePin `json:",omitempty"`
	}{a.ID, a.Name, a.Author, v.Version, v.Ports, v.Root, v.MemoryMB, v.DiskMB, files})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:16])
}

// Unchanged checks a catalog about to be signed against the one published: an add-on or a version once
// listed stays, and a version entry stays as it was looked over, except that it may be withdrawn. Its
// note for the signer (Todo) is not part of what was reviewed and may change. A device that installed a
// version trusts that entry as it read it; a changed one would be another version under the same name.
// An add-on's author and its own withdrawal are held the same way; its name, summary and links may be
// corrected.
func Unchanged(prev, next Catalog) error {
	for _, pa := range prev.Addons {
		na, ok := next.Find(pa.ID)
		if !ok {
			return fmt.Errorf("catalog: %s was listed and is gone; withdraw it instead", pa.ID)
		}
		if na.Author != pa.Author {
			return fmt.Errorf("catalog: %s's author changed from %q to %q", pa.ID, pa.Author, na.Author)
		}
		if pa.Withdrawn != "" && na.Withdrawn == "" {
			return fmt.Errorf("catalog: %s was withdrawn and is listed again; list it as a new version instead", pa.ID)
		}
		for _, pv := range pa.Versions {
			nv, ok := na.Version(pv.Version)
			if !ok {
				return fmt.Errorf("catalog: %s %s was listed and is gone; withdraw it instead", pa.ID, pv.Version)
			}
			if pv.Withdrawn != "" && nv.Withdrawn != pv.Withdrawn {
				return fmt.Errorf("catalog: %s %s's withdrawal changed", pa.ID, pv.Version)
			}
			a, b := pv, nv
			a.Todo, b.Todo = "", ""
			if a.Withdrawn == "" {
				b.Withdrawn = "" // being withdrawn is the one change allowed
			}
			ja, _ := json.Marshal(a)
			jb, _ := json.Marshal(b)
			if !bytes.Equal(ja, jb) {
				return fmt.Errorf("catalog: %s %s changed; a new release is a new version entry, and an old one can only be withdrawn", pa.ID, pv.Version)
			}
		}
	}
	return nil
}
