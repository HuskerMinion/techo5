//go:build !panel

package timezone

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// HasDatabase is whether this image carries the zone database, and so every zone, rather than only
// Common.
func HasDatabase() bool {
	_, err := os.Stat(filepath.Join(zoneinfo, "UTC"))
	return err == nil
}

// fromDatabase puts the named zone at tmp as a link into the image's zoneinfo directory, and returns
// it loaded. Nil, and no error, when the image does not have the zone.
func fromDatabase(zone, tmp string) (*time.Location, error) {
	if _, err := os.Stat(filepath.Join(zoneinfo, zone)); err != nil {
		return nil, nil
	}
	l, err := time.LoadLocation(zone)
	if err != nil {
		return nil, err
	}
	if err := os.Symlink(filepath.Join(zoneinfo, zone), tmp); err != nil {
		return nil, err
	}
	return l, nil
}

// Regions are the groups the zone database is arranged in — America, Europe, Asia and the rest —
// for a screen that cannot list six hundred zones at once. Empty when the image carries no database.
func Regions() []string {
	entries, err := os.ReadDir(zoneinfo)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() && e.Name() != "posix" && e.Name() != "right" {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

// Zones are the zones in one region, by the name they are known by ("Denver", not
// "America/Denver"). A region with zones of its own inside it lists those too, as "Indiana/Knox".
func Zones(region string) []string {
	if !valid(region) {
		return nil
	}
	var out []string
	root := filepath.Join(zoneinfo, region)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		name, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		out = append(out, filepath.ToSlash(name))
		return nil
	})
	if err != nil {
		return nil
	}
	sort.Strings(out)
	return out
}
