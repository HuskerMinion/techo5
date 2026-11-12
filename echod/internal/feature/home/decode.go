package home

import (
	"bytes"
	"fmt"
	"image"

	"github.com/HuskerMinion/techo5/echod/internal/lib/fastjpeg"
)

// Everything on this screen arrives as somebody else's picture: a photo out of a Home Assistant media
// source, a camera's snapshot, a station's cover art, a map tile. Bounding the bytes is not enough,
// because what costs the memory is not the file but what it unpacks to. A megabyte of JPEG can carry
// 20000×15000 pixels, and decoding that asks for the better part of a gigabyte on a device that has one
// in total — so the daemon is killed and the screen goes with it, over a picture nobody asked for.
//
// So the header is read first, which is cheap and does not allocate the image, and the size it declares
// is measured against what the panel could possibly use. The budgets below are all far past anything
// the screen can show: the Show's panel is 960×480 and the Spot's is 480×480, a shade under half a
// megapixel each, and every picture here ends up scaled to that.

const (
	// maxPhotoPixels bounds slideshow photos, which are the owner's own library and the one place a
	// genuinely large picture turns up: a current phone takes twelve to twenty-four megapixels, a
	// camera thirty. Past this are the phones' fifty and hundred megapixel modes, which cost more to
	// unpack than the device can spare and show no more of the photo than the twelve megapixel version
	// would; one of those is skipped and the next photo comes up instead.
	maxPhotoPixels = 32 << 20

	// maxFramePixels bounds a camera snapshot. A 4K camera is eight megapixels and most doorbells are
	// nearer two, so this is already generous for something that gets scaled to the panel several
	// times a second.
	maxFramePixels = 12 << 20

	// maxArtPixels bounds cover art and station logos in JPEG, from services nobody here controls. Most
	// are a few hundred pixels square, but a media server sends a show's poster or fanart as it has it:
	// Plex's posters are 2000×3000 and its fanart 3840×2160, and a four megapixel bound left those blank
	// (#516). Twelve is the camera's bound, and a large JPEG decodes at a half or a quarter of its size.
	maxArtPixels = 12 << 20

	// maxWholeArtPixels bounds art in any other format, which is decoded whole: a 12 megapixel PNG is
	// 48 MB unpacked, 96 at sixteen bits, and fits easily under the download cap. Four megapixels is a
	// 2048×2048 picture, which covers every logo and cover seen in anything but JPEG.
	maxWholeArtPixels = 4 << 20

	// maxTilePixels bounds a map tile: 256 or 512 pixels square by nature, so four megapixels is
	// already far past anything a tile server sends.
	maxTilePixels = 4 << 20
)

// artPixels is the budget for a piece of art: maxArtPixels for a JPEG, which decodes at a fraction of
// its size, and maxWholeArtPixels for anything decoded whole.
func artPixels(b []byte) int64 {
	if _, format, err := image.DecodeConfig(bytes.NewReader(b)); err == nil && format != "jpeg" {
		return maxWholeArtPixels
	}
	return maxArtPixels
}

// decodeWithin decodes an image only if its header declares something this screen could use. what
// names the picture in the error, since every caller here logs the failure and carries on with the
// picture it had.
//
// w×h is the most the picture will be drawn at, or zero for its own size. A JPEG much larger than that
// is decoded at a half, a quarter or an eighth of its size where that still covers w×h (lib/fastjpeg):
// a twelve megapixel photo for the Show 8's panel needs only a quarter of its pixels, and decoding
// fewer costs less. Its shape is the same, so everything that fits it to the panel works as before.
func decodeWithin(b []byte, maxPixels int64, what string, w, h int) (image.Image, error) {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("%s: reading the image header: %w", what, err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return nil, fmt.Errorf("%s: a %s of %d×%d", what, format, cfg.Width, cfg.Height)
	}

	// In 64-bit arithmetic: two declared dimensions multiplied as ints overflow on a 32-bit device,
	// and a negative product would pass the bound it was meant to fail.
	if pixels := int64(cfg.Width) * int64(cfg.Height); pixels > maxPixels {
		return nil, fmt.Errorf("%s: %d×%d is %d pixels, more than the %d this screen will decode", what, cfg.Width, cfg.Height, pixels, maxPixels)
	}

	var img image.Image
	if format == "jpeg" {
		img, err = fastjpeg.DecodeAtLeast(b, w, h)
	} else {
		img, _, err = image.Decode(bytes.NewReader(b))
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", what, err)
	}
	return img, nil
}
