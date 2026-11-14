//go:build !dot && !spot && !panel

package camera

import (
	"testing"
)

func BenchmarkHiResFullUncached(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		f := &Frame{raw: benchRaw}
		f.Full()
	}
}
