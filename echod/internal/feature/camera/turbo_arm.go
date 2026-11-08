//go:build linux && arm && cgo

package camera

/*
#cgo CFLAGS: -O3
#cgo LDFLAGS: -ljpeg
#include <stdlib.h>
#include <stdio.h>
#include <string.h>
#include <setjmp.h>
#include <jpeglib.h>

typedef struct {
    struct jpeg_error_mgr pub;
    jmp_buf jump;
} safe_jpeg_error;

static void safe_jpeg_fail(j_common_ptr info) {
    safe_jpeg_error *e = (safe_jpeg_error*)info->err;
    longjmp(e->jump, 1);
}

// Each invocation owns its JPEG compressor. The caller can run several
// independent MCU-aligned scanline bands concurrently. No input pointer escapes.
static int jpeg_rgba_band(
    const unsigned char *rgba, int width, int height, int stride,
    int quality, unsigned char **output, unsigned long *output_length
) {
    struct jpeg_compress_struct c;
    safe_jpeg_error e;
    memset(&c, 0, sizeof(c));
    c.err = jpeg_std_error(&e.pub);
    e.pub.error_exit = safe_jpeg_fail;
    *output = NULL;
    *output_length = 0;
    if (setjmp(e.jump)) {
        if (c.mem != NULL) {
            jpeg_destroy_compress(&c);
        }
        free(*output);
        *output = NULL;
        *output_length = 0;
        return -1;
    }
    jpeg_create_compress(&c);
    jpeg_mem_dest(&c, output, output_length);
    c.image_width = width;
    c.image_height = height;
    c.input_components = 4;
    c.in_color_space = JCS_EXT_RGBA;
    jpeg_set_defaults(&c);
    jpeg_set_quality(&c, quality, TRUE);
    jpeg_start_compress(&c, TRUE);
    while (c.next_scanline < c.image_height) {
        JSAMPROW row[1] = {
            (JSAMPROW)(rgba + (size_t)c.next_scanline * (size_t)stride)
        };
        jpeg_write_scanlines(&c, row, 1);
    }
    jpeg_finish_compress(&c);
    jpeg_destroy_compress(&c);
    return 0;
}
*/
import "C"

import (
	"fmt"
	"image"
	"runtime"
	"sync"
	"unsafe"
)

// encodeNativeTurbo uses the device's NEON-accelerated libjpeg-turbo, split
// across four independently compressed MCU-aligned bands. It retains the
// original Bayer-developed full resolution and avoids color-space copies.
func encodeNativeTurbo(img *image.RGBA, quality int) ([]byte, error) {
	if err := validateNativeRGBA(img, quality); err != nil {
		return nil, err
	}
	bounds := img.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	rows := (height + mcuSize - 1) / mcuSize
	parts := nativeWorkers(rows)
	per := (rows + parts - 1) / parts
	parts = (rows + per - 1) / per
	segments := make([][]byte, parts)
	errors := make([]error, parts)
	var wg sync.WaitGroup
	wg.Add(parts)
	for i := range parts {
		i := i
		go func() {
			defer wg.Done()
			y0 := i * per * mcuSize
			y1 := min(y0+per*mcuSize, height)
			first := img.PixOffset(bounds.Min.X, bounds.Min.Y+y0)
			var output *C.uchar
			var length C.ulong
			code := C.jpeg_rgba_band(
				(*C.uchar)(unsafe.Pointer(&img.Pix[first])),
				C.int(width), C.int(y1-y0), C.int(img.Stride), C.int(quality),
				&output, &length,
			)
			// Never let an unexpectedly large JPEG exhaust the daemon.
			if output != nil {
				defer C.free(unsafe.Pointer(output))
			}
			if code != 0 || output == nil || length == 0 || uint64(length) > 12*1024*1024 {
				errors[i] = fmt.Errorf("native JPEG: encoder failed in band %d", i)
				return
			}
			segments[i] = C.GoBytes(unsafe.Pointer(output), C.int(length))
			runtime.KeepAlive(img)
		}()
	}
	wg.Wait()
	for _, err := range errors {
		if err != nil {
			return nil, err
		}
	}
	if parts == 1 {
		return segments[0], nil
	}
	return joinStripes(segments, height, per*(width/mcuSize))
}
