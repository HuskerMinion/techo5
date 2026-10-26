//go:build dot

package layout

import (
	"io/fs"
	"strings"
	"testing"
)

// idme's product id decides, as read with the NUL and newline it can end in; Fire OS's product name
// stands in where idme says nothing we know; anything else is run as a Dot and reported.
func TestDotBoard(t *testing.T) {
	cases := []struct {
		typeID, product string
		want            string
		known           bool
	}{
		{"A7WXQPH584YP\x00", "", boardRadar, true},
		{"A7WXQPH584YP\n", "", boardRadar, true},
		{"A3S5BH2HU6VAYF", "", boardBiscuit, true},
		{"A3S5BH2HU6VAYF\x00\n", "radar_puffin", boardBiscuit, true},
		{"", "radar_puffin\n", boardRadar, true},
		{"", "biscuit_puffin\n", boardBiscuit, true},
		{"AXXXXXXXXXXX", "", boardBiscuit, false},
		{"", "rook\n", boardBiscuit, false},
		{"", "", boardBiscuit, false},
		// Only the whole id counts: one that merely contains a known one is not it.
		{"A7WXQPH584YPX", "", boardBiscuit, false},
	}
	for _, c := range cases {
		got, known := dotBoard(c.typeID, c.product)
		if got != c.want || known != c.known {
			t.Errorf("dotBoard(%q, %q) = %q, %v; want %q, %v", c.typeID, c.product, got, known, c.want, c.known)
		}
	}
}

// Each board reports its own model and default name; an unknown one gets the Dot's.
func TestBoardInfo(t *testing.T) {
	cases := []struct{ board, wantBoard, model, name string }{
		{boardRadar, "radar", "Echo 2nd gen (radar)", "Echo"},
		{boardBiscuit, "biscuit", "Echo Dot 2 (biscuit)", "Echo Dot"},
		{"", "biscuit", "Echo Dot 2 (biscuit)", "Echo Dot"},
	}
	for _, c := range cases {
		b, m, n := boardInfo(c.board)
		if b != c.wantBoard || m != c.model || n != c.name {
			t.Errorf("boardInfo(%q) = %q, %q, %q; want %q, %q, %q", c.board, b, m, n, c.wantBoard, c.model, c.name)
		}
	}
}

// A board that is a guess is said at start (boot.Run), and loudest where the Linux image has no idme
// to tell the boards by: an Echo 2nd gen would run muted as a Dot.
func TestBoardDoubt(t *testing.T) {
	missing := &fs.PathError{Op: "open", Path: deviceTypePath, Err: fs.ErrNotExist}
	if got := boardDoubt("", missing, "", false); !strings.Contains(got, "no "+deviceTypePath) || !strings.Contains(got, "radar") {
		t.Errorf("no idme on Linux: %q", got)
	}
	if got := boardDoubt("AXXXXXXXXXXX\x00", nil, "", false); !strings.Contains(got, `"AXXXXXXXXXXX"`) {
		t.Errorf("an unknown id: %q", got)
	}
	if got := boardDoubt("", missing, "rook\n", true); !strings.Contains(got, `"rook"`) || strings.Contains(got, "no "+deviceTypePath) {
		t.Errorf("Fire OS with an unknown product: %q", got)
	}
}

// On Linux 6.1 there is no /proc/idme: the Echo 2nd gen's device type is read from the device tree's
// idme node, NUL-terminated as the bootloader writes it, and still names the board.
func TestBoardFromTheDeviceTree(t *testing.T) {
	read := func(path string) ([]byte, error) {
		if path == "/proc/device-tree/idme/device_type_id/value" {
			return []byte(radarTypeID + "\x00"), nil
		}
		return nil, &fs.PathError{Op: "open", Path: path, Err: fs.ErrNotExist}
	}
	raw, err := readIDME("device_type_id", read)
	if err != nil {
		t.Fatal(err)
	}
	if board, ok := dotBoard(string(raw), ""); !ok || board != boardRadar {
		t.Errorf("board %q (%v)", board, ok)
	}
}
