//go:build panel

package timezone

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/layout"
)

// On a panel the zone database is Android's own: not a zoneinfo directory but one packed "tzdata"
// file, kept current by the system's own updates. A zone name is looked up in its index, and since an
// app cannot link into a system file and have that last, the zone's data is copied out instead.
var zoneDB = layout.ZoneDB

// HasDatabase is whether the tablet has Android's zone database, and so every zone, rather than only
// Common: the setup page then lists the database's zones alone, and diagnostics have nothing to say.
func HasDatabase() bool {
	zones, err := zoneTable()
	return err == nil && len(zones) > 0
}

// fromDatabase writes the named zone's zoneinfo data to tmp and returns it loaded. Nil, and no error,
// when the database does not have the zone (or there is no database).
func fromDatabase(zone, tmp string) (*time.Location, error) {
	zones, err := zoneTable()
	if err != nil {
		return nil, nil
	}
	data, ok := zones[zone]
	if !ok {
		return nil, nil
	}
	l, err := time.LoadLocationFromTZData(zone, data)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return nil, err
	}
	return l, nil
}

// Regions are the groups the zone database is arranged in — America, Europe, Asia and the rest —
// for a screen that cannot list six hundred zones at once. Empty when there is no database.
func Regions() []string {
	zones, err := zoneTable()
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	for name := range zones {
		if i := strings.IndexByte(name, '/'); i > 0 {
			seen[name[:i]] = true
		}
	}
	out := make([]string, 0, len(seen))
	for r := range seen {
		out = append(out, r)
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
	zones, err := zoneTable()
	if err != nil {
		return nil
	}
	var out []string
	for name := range zones {
		if strings.HasPrefix(name, region+"/") {
			out = append(out, name[len(region)+1:])
		}
	}
	sort.Strings(out)
	return out
}

// zoneTable reads the database: every zone by name, with its zoneinfo data. The layout is AOSP's
// ZoneInfoDB: a 12-byte "tzdataYYYYx" header, the offsets of the index and the data as big-endian
// 32-bit numbers, then index entries of a 40-byte name and the zone's offset and length in the data.
func zoneTable() (map[string][]byte, error) {
	b, err := os.ReadFile(zoneDB)
	if err != nil {
		return nil, err
	}
	const header, nameSize, entrySize = 24, 40, 52
	if len(b) < header || string(b[:6]) != "tzdata" {
		return nil, errors.New(zoneDB + ": not a tzdata file")
	}
	index := int64(binary.BigEndian.Uint32(b[12:]))
	data := int64(binary.BigEndian.Uint32(b[16:]))
	if index < header || index > data || data > int64(len(b)) {
		return nil, errors.New(zoneDB + ": index out of range")
	}
	out := map[string][]byte{}
	for e := index; e+entrySize <= data; e += entrySize {
		name := b[e : e+nameSize]
		if i := bytes.IndexByte(name, 0); i >= 0 {
			name = name[:i]
		}
		off := data + int64(binary.BigEndian.Uint32(b[e+nameSize:]))
		size := int64(binary.BigEndian.Uint32(b[e+nameSize+4:]))
		if len(name) == 0 || off+size > int64(len(b)) {
			continue
		}
		out[string(name)] = b[off : off+size]
	}
	return out, nil
}
