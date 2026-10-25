package camera

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/jpeg"
	"runtime"
	"sync"
)

// The standard library encodes a JPEG on one core, and at the camera's full size that is most of a
// frame's cost: about 300 ms for 1600x1200 on a Show 5. A JPEG can be cut into independent parts
// with restart markers, though: every restart interval begins with its DC predictions at zero and on a
// byte boundary, which is exactly where a fresh encode of a strip begins and ends. So the picture is
// cut into horizontal strips a whole number of MCU rows tall, each strip is encoded on its own core
// with the same tables (the library's tables follow from the quality alone), and the strips' entropy
// data are joined with RST0..RST7 between them under one header that says the full height and the
// restart interval (DRI). Any decoder reads it as one picture, with the pixels a single encode gives.

// mcuSize is the library's MCU for a color picture: 4:2:0, 16 pixels square.
const mcuSize = 16

// stripable is a picture that can be cut into strips without copying, as RGBA and YCbCr can.
type stripable interface {
	image.Image
	SubImage(r image.Rectangle) image.Image
}

// encodeStripes is jpeg.Encode across cores. A picture too small to split is encoded whole.
func encodeStripes(img stripable, quality int) ([]byte, error) {
	b := img.Bounds()
	rows := (b.Dy() + mcuSize - 1) / mcuSize
	parts := min(runtime.NumCPU(), rows)
	if parts < 2 {
		return encodeWhole(img, quality)
	}
	// Every strip but the last must hold the same number of MCUs: the restart interval is one count.
	per := (rows + parts - 1) / parts
	parts = (rows + per - 1) / per
	strips := make([][]byte, parts)
	errs := make([]error, parts)
	var wg sync.WaitGroup
	for i := range parts {
		y0 := b.Min.Y + i*per*mcuSize
		y1 := min(y0+per*mcuSize, b.Max.Y)
		wg.Add(1)
		go func() {
			defer wg.Done()
			strips[i], errs[i] = encodeWhole(img.SubImage(image.Rect(b.Min.X, y0, b.Max.X, y1)), quality)
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}
	return joinStripes(strips, b.Dy(), per*((b.Dx()+mcuSize-1)/mcuSize))
}

func encodeWhole(img image.Image, quality int) ([]byte, error) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// joinStripes makes one JPEG of the strips: the first strip's header with the full height and a DRI
// of interval MCUs, then each strip's scan data, a restart marker between each two, and EOI.
func joinStripes(strips [][]byte, height, interval int) ([]byte, error) {
	if interval > 0xffff {
		return nil, fmt.Errorf("jpeg: restart interval %d does not fit", interval)
	}
	head, scan0, err := splitScan(strips[0])
	if err != nil {
		return nil, err
	}
	sof := bytes.Index(head, []byte{0xff, 0xc0})
	if sof < 0 || sof+9 > len(head) {
		return nil, fmt.Errorf("jpeg: no baseline frame header")
	}
	out := make([]byte, 0, len(head)+6+len(strips)*len(scan0)+2)
	out = append(out, head...)
	binary.BigEndian.PutUint16(out[sof+5:], uint16(height))
	out = append(out, 0xff, 0xdd, 0, 4, byte(interval>>8), byte(interval))
	for i, s := range strips {
		sos, scan, err := sosAndScan(s)
		if err != nil {
			return nil, err
		}
		if i == 0 {
			out = append(out, sos...)
		} else {
			out = append(out, 0xff, byte(0xd0+(i-1)%8))
		}
		out = append(out, scan...)
	}
	return append(out, 0xff, 0xd9), nil
}

// splitScan is a JPEG's bytes before its start of scan, and the rest.
func splitScan(j []byte) (head, rest []byte, err error) {
	i := bytes.Index(j, []byte{0xff, 0xda})
	if i < 0 {
		return nil, nil, fmt.Errorf("jpeg: no start of scan")
	}
	return j[:i], j[i:], nil
}

// sosAndScan is a JPEG's start-of-scan segment, and its entropy-coded data up to EOI.
func sosAndScan(j []byte) (sos, scan []byte, err error) {
	_, rest, err := splitScan(j)
	if err != nil {
		return nil, nil, err
	}
	if len(rest) < 4 {
		return nil, nil, fmt.Errorf("jpeg: short start of scan")
	}
	n := 2 + int(binary.BigEndian.Uint16(rest[2:]))
	if n > len(rest) || !bytes.HasSuffix(rest, []byte{0xff, 0xd9}) {
		return nil, nil, fmt.Errorf("jpeg: unexpected scan layout")
	}
	return rest[:n], rest[n : len(rest)-2], nil
}
