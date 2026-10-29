// Package fastjpeg decodes JPEGs in techo5-jpeg (tools/jpeg), a small helper built on libjpeg-turbo,
// and with image/jpeg wherever that is not there or does not take the picture.
//
// The 2nd gen Show 8's kernel has no hardware JPEG decoder, and image/jpeg has no SIMD: a twelve
// megapixel photo takes it about 590 ms on the Show 8's cores as an armv7 binary, where the helper,
// libjpeg-turbo's NEON code, takes 260 ms, and 180 ms at the half size the panel needs. Most of
// image/jpeg's time is the Huffman decoding, not the inverse DCT, so vector code for the transform alone
// would not close that; a C library, kept in its own process so the daemon stays pure Go and a fault in
// the decoder ends only the helper, does. The helper is started once per picture: a few milliseconds,
// against the hundreds it saves.
//
// The picture comes back as image/jpeg returns it, a YCbCr picture at the JPEG's own subsampling (or a
// gray one), so it draws the same way. The two decoders' inverse DCTs round differently, so a sample
// can differ by one (about one in a hundred do, up to one in fifty); nothing else about the picture
// changes.
//
// A picture wanted only at some smaller size is decoded smaller with DecodeAtLeast: at a half, a
// quarter or an eighth, by the scaled inverse DCT, which averages the picture down where shrinking a
// whole decode with a bilinear filter skips most of it.
//
// A small picture, under about half a megapixel (minHelperPixels), is decoded in Go: there starting
// the helper would cost more than it saves. A picture over MaxPixels is not decoded at all.
package fastjpeg

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"time"
)

// helperPath is where the root filesystem puts the helper; only athena's images carry it
// (tools/linux/deploy-rootfs.sh).
var helperPath = "/usr/local/bin/techo5-jpeg"

// minHelperPixels is the smallest picture the helper decodes. Below it, the few milliseconds the helper
// takes to start are more than image/jpeg needs for the whole picture: a dashboard tile or a radar tile.
var minHelperPixels int64 = 1 << 19

// MaxPixels is the most pixels a JPEG may have to be decoded: 64 megapixels, twice what any caller
// here takes. Every caller checks its own, smaller bound first; this one keeps a future caller from
// unpacking a header's claim of gigabytes.
var MaxPixels int64 = 64 << 20

// helperWait bounds one decode: a twelve megapixel photo takes the helper about a quarter of a second.
var helperWait = 10 * time.Second

var (
	// present is whether there is a helper, looked for once.
	present     = sync.OnceValue(func() bool { _, err := os.Stat(helperPath); return err == nil })
	warnOnce    sync.Once
	errDeclined = errors.New("techo5-jpeg: a JPEG it does not decode")
	errRefused  = errors.New("techo5-jpeg: a JPEG it found damaged")
)

// Decode decodes a JPEG whole, as jpeg.Decode does.
func Decode(b []byte) (image.Image, error) {
	return decode(b, 0, 0)
}

// DecodeAtLeast decodes a JPEG at the smallest of its whole size, a half, a quarter and an eighth of
// it that is still at least w×h, so that a picture fitted to cover w×h is never enlarged. A w or h of
// zero or less is the whole picture. Where the helper is not there, or the picture is small, it comes
// back whole, as jpeg.Decode returns it.
func DecodeAtLeast(b []byte, w, h int) (image.Image, error) {
	return decode(b, w, h)
}

// Denominator is the largest of 1, 2, 4 and 8 that leaves a sw×sh picture at least w×h.
func Denominator(sw, sh, w, h int) int {
	d := 1
	for _, c := range []int{2, 4, 8} {
		if scaled(sw, c) >= w && scaled(sh, c) >= h {
			d = c
		}
	}
	return d
}

// scaled is a side of n decoded at 1/d, rounded up as libjpeg rounds it.
func scaled(n, d int) int { return (n + d - 1) / d }

func decode(b []byte, w, h int) (image.Image, error) {
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	pixels := int64(cfg.Width) * int64(cfg.Height)
	if pixels > MaxPixels {
		return nil, fmt.Errorf("fastjpeg: a %d×%d JPEG, more than %d pixels", cfg.Width, cfg.Height, MaxPixels)
	}
	if present() && pixels >= minHelperPixels {
		d := 1
		if w > 0 && h > 0 {
			d = Denominator(cfg.Width, cfg.Height, w, h)
		}
		img, err := helper(b, d)
		if err == nil {
			return img, nil
		}
		switch {
		case errors.Is(err, errDeclined):
		case errors.Is(err, errRefused):
			// A damaged picture: image/jpeg says what is wrong with it.
			slog.Debug("fastjpeg: decoding in Go", "err", err)
		default:
			warnOnce.Do(func() { slog.Warn("fastjpeg: the helper failed, decoding in Go", "err", err) })
			slog.Debug("fastjpeg: the helper failed, decoding in Go", "err", err)
		}
	}
	return jpeg.Decode(bytes.NewReader(b))
}

// helper decodes b at 1/d in techo5-jpeg.
func helper(b []byte, d int) (image.Image, error) {
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(b))
	if err != nil {
		return nil, errDeclined // image/jpeg gives the reason
	}
	ctx, cancel := context.WithTimeout(context.Background(), helperWait)
	defer cancel()
	cmd := exec.CommandContext(ctx, helperPath, strconv.Itoa(d))
	confine(cmd)
	cmd.Stdin = bytes.NewReader(b)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, w, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	defer out.Close()
	widen(out)
	cmd.Stdout = w
	err = cmd.Start()
	w.Close()
	if err != nil {
		return nil, err
	}
	img, rerr := read(out, scaled(cfg.Width, d), scaled(cfg.Height, d))
	if rerr != nil {
		_, _ = io.Copy(io.Discard, out) // let it finish writing, so Wait reports how it ended
	}
	werr := cmd.Wait()
	var exit *exec.ExitError
	switch {
	case errors.As(werr, &exit) && exit.ExitCode() == 2:
		return nil, errDeclined
	case errors.As(werr, &exit) && exit.ExitCode() == 1:
		return nil, fmt.Errorf("%w: %s", errRefused, bytes.TrimSpace(stderr.Bytes()))
	case werr != nil:
		return nil, fmt.Errorf("%w: %s", werr, bytes.TrimSpace(stderr.Bytes()))
	case rerr != nil:
		return nil, rerr
	}
	return img, nil
}

// read takes the helper's picture off r, which must be w×h, into a picture of its own.
func read(r io.Reader, w, h int) (image.Image, error) {
	var hdr [16]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	gw, gh := binary.LittleEndian.Uint32(hdr[4:]), binary.LittleEndian.Uint32(hdr[8:])
	if string(hdr[:4]) != "T5J1" || int64(gw) != int64(w) || int64(gh) != int64(h) {
		return nil, fmt.Errorf("techo5-jpeg: a %d×%d picture, not the %d×%d asked for", gw, gh, w, h)
	}
	rect := image.Rect(0, 0, w, h)
	switch kind, ratio := hdr[12], image.YCbCrSubsampleRatio(hdr[13]); {
	case kind == 1:
		m := image.NewGray(rect)
		if _, err := io.ReadFull(r, m.Pix); err != nil {
			return nil, err
		}
		return m, nil
	case kind == 2 && ratio <= image.YCbCrSubsampleRatio411:
		// NewYCbCr lays the planes out as the helper writes them: each row its plane's width.
		m := image.NewYCbCr(rect, ratio)
		for _, p := range [][]byte{m.Y, m.Cb, m.Cr} {
			if _, err := io.ReadFull(r, p); err != nil {
				return nil, err
			}
		}
		return m, nil
	default:
		return nil, fmt.Errorf("techo5-jpeg: a picture of kind %d, subsampling %d", kind, ratio)
	}
}
