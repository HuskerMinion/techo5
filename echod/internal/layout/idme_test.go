package layout

import (
	"errors"
	"os"
	"testing"
)

func TestReadIDMEKernelInterfaces(t *testing.T) {
	for _, tc := range []struct {
		name  string
		files map[string]string
		want  string
	}{
		{"vendor", map[string]string{"/proc/idme/mac_addr": "020000000001\n"}, "020000000001\n"},
		{"device tree", map[string]string{"/proc/device-tree/idme/mac_addr/value": "020000000001\x00"}, "020000000001\x00"},
		{"vendor preferred", map[string]string{"/proc/idme/mac_addr": "020000000001", "/proc/device-tree/idme/mac_addr/value": "020000000002"}, "020000000001"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := readIDME("mac_addr", func(path string) ([]byte, error) {
				if s, ok := tc.files[path]; ok {
					return []byte(s), nil
				}
				return nil, os.ErrNotExist
			})
			if err != nil || string(got) != tc.want {
				t.Fatalf("got %q, %v", got, err)
			}
			if MAC(string(got)) != "02:00:00:00:00:01" {
				t.Fatal("identity normalization failed")
			}
		})
	}
	_, err := readIDME("mac_addr", func(string) ([]byte, error) { return nil, os.ErrPermission })
	if !errors.Is(err, os.ErrPermission) {
		t.Fatalf("permission error lost: %v", err)
	}
	_, err = readIDME("mac_addr", func(string) ([]byte, error) { return nil, os.ErrNotExist })
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing identity error lost: %v", err)
	}
}
