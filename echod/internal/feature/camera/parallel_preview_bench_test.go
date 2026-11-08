package camera

import "testing"

// Benchmarks the old and new Checkers MJPEG preview encoders on the same frame.
// Run with GOMAXPROCS=4 to model the device's four cores.
func BenchmarkCheckersMJPEGPreview(b *testing.B) {
	frame := fakeFrame{half: picture(640, 360)}
	b.Run("previous-serial", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if _, err := encodeWhole(frame.Image(), quality); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("parallel-stripes", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if _, err := encodeHalf(frame); err != nil {
				b.Fatal(err)
			}
		}
	})
}
