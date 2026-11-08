package camera

import (
	"testing"
)

func BenchmarkHiResEncodeStripes(b *testing.B) {
	img := picture(1280, 720)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := encodeStripes(img, 85); err != nil {
			b.Fatal(err)
		}
	}
}
