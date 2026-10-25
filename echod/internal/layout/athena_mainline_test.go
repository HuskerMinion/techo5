//go:build !dot && !spot && !panel

package layout

import (
	"encoding/binary"
	"testing"
)

// idmeImage builds an idme v2 area the way athena's boot1 lays it out, with made-up values.
func idmeImage(items [][2]string) []byte {
	b := []byte("beefdeed2.1\x00")
	b = binary.LittleEndian.AppendUint32(b, uint32(len(items)))
	for _, it := range items {
		name := make([]byte, 16)
		copy(name, it[0])
		val := make([]byte, 16)
		copy(val, it[1])
		b = append(b, name...)
		b = binary.LittleEndian.AppendUint32(b, uint32(len(val)))
		b = binary.LittleEndian.AppendUint32(b, 1)
		b = binary.LittleEndian.AppendUint32(b, 0x124)
		b = append(b, val...)
	}
	return b
}

func TestParseIdme(t *testing.T) {
	img := idmeImage([][2]string{{"board_id", "0000000000000001"}, {"serial", "TESTSERIAL000001"}, {"mac_addr", "0a1b2c3d4e5f"}})
	for _, c := range []struct{ name, want string }{
		{"board_id", "0000000000000001"},
		{"serial", "TESTSERIAL000001"},
		{"mac_addr", "0a1b2c3d4e5f"},
	} {
		got, ok := parseIdme(img, c.name)
		if !ok || got != c.want {
			t.Errorf("parseIdme(%q) = %q, %v; want %q", c.name, got, ok, c.want)
		}
	}
	if _, ok := parseIdme(img, "bt_mac_addr"); ok {
		t.Error("parseIdme found an item that is not there")
	}
	if _, ok := parseIdme([]byte("not idme at all, no magic here"), "serial"); ok {
		t.Error("parseIdme accepted an area without the beefdeed magic")
	}
	if got := MAC("0a1b2c3d4e5f"); got != "0a:1b:2c:3d:4e:5f" {
		t.Errorf("MAC = %q", got)
	}
}

func TestMainlineRelease(t *testing.T) {
	for rel, want := range map[string]bool{"4.4.146+": false, "6.18.56-techo5-athena\n": true, "6.18.56-dirty": true, "": false} {
		if got := mainlineRelease(rel); got != want {
			t.Errorf("mainlineRelease(%q) = %v, want %v", rel, got, want)
		}
	}
}
