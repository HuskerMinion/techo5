//go:build !dot && !spot

package camera

import (
	"testing"
	"time"
)

// A scan asked for no length runs the default, and one asked for longer than Home Assistant waits for
// an answer runs only as long as it does.
func TestQRScanFor(t *testing.T) {
	for seconds, want := range map[int]time.Duration{0: qrScanDefault, -3: qrScanDefault, 10: 10 * time.Second, 60: qrScanLongest} {
		if got := qrScanFor(seconds); got != want {
			t.Errorf("qrScanFor(%d) = %v, want %v", seconds, got, want)
		}
	}
}
