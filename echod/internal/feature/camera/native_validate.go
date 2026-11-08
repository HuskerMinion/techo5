package camera

import (
	"fmt"
	"image"
	"runtime"
)

// Checkers' native JPEG encoder passes image storage directly to libjpeg.
// Validate both the visible rectangle and its stride before C sees it.
func validateNativeRGBA(img *image.RGBA, quality int) error {
	if img == nil {
		return fmt.Errorf("native JPEG: no image")
	}
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	if w < 16 || h < 16 || w > 8192 || h > 8192 || w%16 != 0 ||
		quality < 1 || quality > 100 {
		return fmt.Errorf("native JPEG: unsupported dimensions %dx%d or quality %d", w, h, quality)
	}
	if img.Stride < w*4 || img.Stride > len(img.Pix) {
		return fmt.Errorf("native JPEG: invalid RGBA stride %d for width %d", img.Stride, w)
	}
	// int64 avoids overflow from height*stride on 32-bit ARM. A SubImage's
	// Pix slice begins at the visible rectangle's top-left pixel.
	end := int64(h-1)*int64(img.Stride) + int64(w)*4
	if end < 0 || end > int64(len(img.Pix)) {
		return fmt.Errorf("native JPEG: truncated RGBA rows")
	}
	return nil
}

// Honour the Go process's concurrency budget. The Echo shares its CPU
// with wake-word detection, playback, networking, and touch rendering.
func nativeWorkers(rows int) int {
	return min(4, runtime.NumCPU(), runtime.GOMAXPROCS(0), rows)
}
