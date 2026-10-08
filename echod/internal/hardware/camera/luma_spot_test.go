//go:build spot

package camera

import "testing"

func TestLumaGridReadsTheFrame(t *testing.T) {
	raw := make([]byte, frameBytes)
	for i := range raw {
		raw[i] = 90
	}
	for i, c := range (&Frame{raw: raw}).Luma(32, 24) {
		if c != 90 {
			t.Fatalf("cell %d = %d", i, c)
		}
	}
}
