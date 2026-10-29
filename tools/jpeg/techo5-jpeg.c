// SPDX-License-Identifier: MIT
//
// techo5-jpeg: decodes one JPEG for echod with libjpeg-turbo, whose NEON code decodes a photo two to
// four times as fast as Go's image/jpeg on the 2nd gen Show 8, whose kernel has no hardware JPEG
// decoder. echod runs one per picture (echod/internal/lib/fastjpeg): the JPEG on stdin,
// the decoded picture on stdout. A picture comes from wherever the owner pointed the screen, so it is
// decoded in this process rather than the daemon's: a fault in the decoder ends this, and echod
// decodes the picture itself as it did before.
//
//   techo5-jpeg <denominator>
//
// denominator is 1, 2, 4 or 8: the picture is decoded at that fraction of its size, by the scaled
// inverse DCT, which costs less than decoding it whole and is as good as shrinking it afterwards.
//
// The picture comes back as Go's image.YCbCr or image.Gray holds it, so echod uses it as it is: a
// 16-byte header ("T5J1", width and height as 32-bit little-endian, the kind, the chroma subsampling
// as Go numbers it, two zero bytes) and then the planes, Y and then Cb and Cr, each row its plane's
// width with no padding, as image.NewYCbCr lays them out. Exit status 2 is a JPEG this does not
// decode (CMYK, RGB, unusual subsampling), which echod hands to image/jpeg; 1 is any other failure,
// with the reason on stderr.

#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <turbojpeg.h>
#include <unistd.h>

// maxInput bounds what is read: echod bounds every picture it fetches far below this.
#define maxInput (64 << 20)

enum { kindGray = 1, kindYCbCr = 2 };

static int fail(int status, const char *what, tjhandle h) {
	fprintf(stderr, "techo5-jpeg: %s%s%s\n", what, h ? ": " : "", h ? tj3GetErrorStr(h) : "");
	return status;
}

static int writeAll(const unsigned char *p, size_t n) {
	while (n > 0) {
		ssize_t w = write(1, p, n);
		if (w <= 0)
			return -1;
		p += w;
		n -= (size_t)w;
	}
	return 0;
}

int main(int argc, char **argv) {
	if (argc != 2)
		return fail(1, "usage: techo5-jpeg <denominator>", NULL);
	int denom = atoi(argv[1]);
	if (denom != 1 && denom != 2 && denom != 4 && denom != 8)
		return fail(1, "the denominator is 1, 2, 4 or 8", NULL);

	size_t n = 0, cap = 1 << 20;
	unsigned char *in = malloc(cap);
	for (;;) {
		if (!in)
			return fail(1, "out of memory", NULL);
		ssize_t r = read(0, in + n, cap - n);
		if (r < 0)
			return fail(1, "reading the JPEG", NULL);
		if (r == 0)
			break;
		n += (size_t)r;
		if (n == cap) {
			if (cap >= maxInput)
				return fail(1, "the JPEG is too large", NULL);
			cap *= 2;
			in = realloc(in, cap);
		}
	}

	tjhandle h = tj3Init(TJINIT_DECOMPRESS);
	if (!h)
		return fail(1, "starting libjpeg-turbo", NULL);
	// A warning is a damaged picture, which image/jpeg refuses; refusing it here too sends it there,
	// so a broken picture fails the same way it always has rather than showing half gray.
	tj3Set(h, TJPARAM_STOPONWARNING, 1);
	// A progressive JPEG can be made of endless scans, each costing a pass over the picture.
	tj3Set(h, TJPARAM_SCANLIMIT, 500);
	if (tj3DecompressHeader(h, in, n) < 0)
		return fail(1, "reading the header", h);

	int cs = tj3Get(h, TJPARAM_COLORSPACE), ss = tj3Get(h, TJPARAM_SUBSAMP);
	int ratio;
	switch (ss) {
	case TJSAMP_444: ratio = 0; break;
	case TJSAMP_422: ratio = 1; break;
	case TJSAMP_420: ratio = 2; break;
	case TJSAMP_440: ratio = 3; break;
	case TJSAMP_411: ratio = 4; break;
	case TJSAMP_GRAY: ratio = 0; break;
	default: return fail(2, "chroma subsampling Go's image.YCbCr does not hold", NULL);
	}
	int kind = ss == TJSAMP_GRAY ? kindGray : kindYCbCr;
	if ((kind == kindGray && cs != TJCS_GRAY) || (kind == kindYCbCr && cs != TJCS_YCbCr))
		return fail(2, "not a YCbCr or gray JPEG", NULL);
	if (tj3Get(h, TJPARAM_PRECISION) != 8)
		return fail(2, "not an 8-bit JPEG", NULL);

	tjscalingfactor sf = {1, denom};
	if (tj3SetScalingFactor(h, sf) < 0)
		return fail(1, "scaling", h);
	// echod has already measured the picture against what it will decode, from the same header.
	int w = TJSCALED(tj3Get(h, TJPARAM_JPEGWIDTH), sf), ht = TJSCALED(tj3Get(h, TJPARAM_JPEGHEIGHT), sf);

	int planes = kind == kindGray ? 1 : 3;
	unsigned char *p[3] = {0};
	int stride[3] = {0};
	size_t size[3] = {0}, total = 0;
	for (int c = 0; c < planes; c++) {
		stride[c] = tj3YUVPlaneWidth(c, w, ss);
		int ph = tj3YUVPlaneHeight(c, ht, ss);
		if (stride[c] <= 0 || ph <= 0)
			return fail(1, "sizing the planes", h);
		size[c] = (size_t)stride[c] * (size_t)ph;
		total += size[c];
	}
	// One allocation, the planes one after another, written out in one go.
	unsigned char *out = malloc(16 + total);
	if (!out)
		return fail(1, "out of memory", NULL);
	unsigned char *q = out + 16;
	for (int c = 0; c < planes; c++) {
		p[c] = q;
		q += size[c];
	}
	if (tj3DecompressToYUVPlanes8(h, in, n, p, stride) < 0)
		return fail(1, "decoding", h);
	free(in);

	// libjpeg-turbo pads the luma plane to whole chroma samples (a 4:2:0 picture's to an even width and
	// height); Go's image.YCbCr does not, so its rows are closed up. The chroma planes are the same in both.
	for (int y = 1; y < ht; y++)
		memmove(p[0] + (size_t)y * w, p[0] + (size_t)y * stride[0], w);

	memcpy(out, "T5J1", 4);
	for (int i = 0; i < 4; i++) {
		out[4 + i] = (unsigned char)((unsigned)w >> (8 * i));
		out[8 + i] = (unsigned char)((unsigned)ht >> (8 * i));
	}
	out[12] = (unsigned char)kind;
	out[13] = (unsigned char)ratio;
	out[14] = out[15] = 0;
	if (writeAll(out, 16 + (size_t)w * ht) < 0 || writeAll(p[0] + size[0], total - size[0]) < 0)
		return fail(1, "writing the picture", NULL);
	return 0;
}
