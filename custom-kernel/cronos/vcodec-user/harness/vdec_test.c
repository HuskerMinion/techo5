/*
 * vdec_test: decode an Annex-B H.264 elementary stream with the MT8163 hardware
 * decoder through libvcodecdrv's driver API (no OMX), and report per-frame
 * timing.
 *
 *   vdec_test [options] in.h264
 *     -o FILE   write the decoded frames (cropped to the display size)
 *     -f FMT    output format: nv12 (default), i420, yv12, or blk (raw MediaTek
 *               16x32 block layout, uncropped, which is what the HW writes)
 *     -n N      stop after N output frames
 *     -p N      number of output buffers (default: DPB size + 4, at least 8)
 *     -l        "no reorder" decode mode (output in decode order, low latency)
 *     -q        quiet: print only the summary
 *
 * Call sequence (see include/vdec_drv_if_public.h, from the vendor OMX):
 *   eVDecDrvCreate(&h, H264) -> eVDecDrvInit(h, &rb{SPS,PPS}, &seq)
 *   -> SetParam(CALLBACK) [required] -> SetParam(WAIT_KEYFRAME, 0)
 *   -> per access unit: eVDecDrvDecodeEx(h, &rb, 0, 0)  (synchronous)
 *        the driver pulls output buffers through the callback, then:
 *        GetParam(DISP_FRAME_BUFFER) until NULL  -> frames to show
 *        GetParam(FREE_FRAME_BUFFER) until NULL  -> buffers we may reuse
 *        GetParam(FREE_INPUT_BUFFER) until NULL  -> input buffers released
 *   -> EOS: eVDecDrvDecodeEx(h, NULL, 0, 0) and drain the same way
 *   -> eVDecDrvDeInit, eVDecDrvRelease
 * The output is always MediaTek block format (16x32 luma tiles, 16x16
 * interleaved CbCr tiles, same as V4L2 MM21). It is detiled here in software.
 */
#include <errno.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>

#include "common.h"
#include "vdec_drv_if_public.h"

#define MAX_FB   32
#define N_INPUT  4
#define ALIGN(x, a) (((x) + (a) - 1) & ~((a) - 1))

static struct {
	hbuf_t buf;
	VDEC_DRV_FRAMEBUF_T fb;
	int free;          /* may be handed to the driver */
} g_fb[MAX_FB];
static int g_nfb;
static uint32_t g_W, g_H;  /* buffer (aligned) size: pitch, rows */
static int g_quiet;
static unsigned g_cb_calls, g_cb_starved;

/* the driver asks for an output buffer while decoding */
static VAL_UINT32_T get_output_buffer(VAL_HANDLE_T cookie, P_VDEC_DRV_FRAMEBUF_T *ppFrame,
				      VAL_UINT32_T flag, VAL_BOOL_T bflag, VAL_VOID_T *extra)
{
	(void)cookie; (void)flag; (void)bflag; (void)extra;
	g_cb_calls++;
	for (int i = 0; i < g_nfb; i++) {
		if (g_fb[i].free) {
			g_fb[i].free = 0;
			VDEC_DRV_FRAMEBUF_T *fb = &g_fb[i].fb;
			memset(fb, 0, sizeof(*fb));
			fb->rBaseAddr.u4VA = (VAL_ULONG_T)(uintptr_t)g_fb[i].buf.va;
			fb->rBaseAddr.u4PA = g_fb[i].buf.pa;
			fb->rBaseAddr.u4Size = g_fb[i].buf.size;
			*ppFrame = fb;
			return 0;
		}
	}
	g_cb_starved++;
	*ppFrame = NULL;
	return (VAL_UINT32_T)-1;
}

static int fb_index(P_VDEC_DRV_FRAMEBUF_T fb)
{
	for (int i = 0; i < g_nfb; i++)
		if (fb == &g_fb[i].fb)
			return i;
	return -1;
}

/* ---- MediaTek block (16x32 / CbCr 16x16) -> linear ---- */
static void detile(const uint8_t *src, uint32_t W, uint32_t H, uint32_t cw, uint32_t ch,
		   const char *fmt, FILE *out)
{
	static uint8_t *line;
	if (!line) {
		line = malloc(W);
	}
	const uint8_t *c = src + W * H;
	for (uint32_t y = 0; y < ch; y++) {
		for (uint32_t x = 0; x < cw; x += 16) {
			uint32_t n = cw - x < 16 ? cw - x : 16;
			memcpy(line + x, src + VDEC_MTK_BLK_Y_OFF(x, y, W), n);
		}
		fwrite(line, 1, cw, out);
	}
	uint32_t ch2 = (ch + 1) / 2, cw2 = (cw + 1) / 2;
	if (!strcmp(fmt, "nv12")) {
		for (uint32_t y = 0; y < ch2; y++) {
			for (uint32_t x = 0; x < cw2 * 2; x += 16) {
				uint32_t n = cw2 * 2 - x < 16 ? cw2 * 2 - x : 16;
				memcpy(line + x, c + VDEC_MTK_BLK_C_OFF(x, y, W), n);
			}
			fwrite(line, 1, cw2 * 2, out);
		}
		return;
	}
	/* planar: collect U and V planes, then write in I420 or YV12 order */
	uint8_t *u = malloc(cw2 * ch2), *v = malloc(cw2 * ch2);
	for (uint32_t y = 0; y < ch2; y++) {
		for (uint32_t x = 0; x < cw2 * 2; x += 16) {
			uint32_t n = cw2 * 2 - x < 16 ? cw2 * 2 - x : 16;
			memcpy(line + x, c + VDEC_MTK_BLK_C_OFF(x, y, W), n);
		}
		for (uint32_t x = 0; x < cw2; x++) {
			u[y * cw2 + x] = line[2 * x];     /* Cb first (MDP swap bit 0) */
			v[y * cw2 + x] = line[2 * x + 1];
		}
	}
	int yv12 = !strcmp(fmt, "yv12");
	fwrite(yv12 ? v : u, 1, cw2 * ch2, out);
	fwrite(yv12 ? u : v, 1, cw2 * ch2, out);
	free(u);
	free(v);
}

/* ---- access unit splitter (Annex-B) ---- */
typedef struct { size_t off, len; int has_vcl, idr; } au_t;

static int nal_type(const uint8_t *b, size_t off, int sc)
{
	return b[off + sc] & 0x1f;
}

/* split the stream into: config (NALs before the first VCL) + AUs */
static au_t *split_aus(const uint8_t *b, size_t len, size_t *n_au, size_t *cfg_len)
{
	size_t cap = 1024, n = 0;
	au_t *a = calloc(cap, sizeof(*a));
	int sc;
	size_t pos = next_start_code(b, len, 0, &sc);
	*cfg_len = 0;
	au_t cur = { .off = pos };
	int seen_vcl_any = 0;
	while (pos < len) {
		int sc2;
		size_t next = next_start_code(b, len, pos + sc, &sc2);
		int t = nal_type(b, pos, sc);
		int vcl = t >= 1 && t <= 5;
		int first_mb0 = vcl && (b[pos + sc + 1] & 0x80); /* ue(v) first_mb_in_slice == 0 */
		int starts_au = (vcl && first_mb0) || t == 9 || t == 7 || t == 8 || t == 6;
		if (!seen_vcl_any) {
			if (vcl) {
				seen_vcl_any = 1;
				*cfg_len = pos;
				cur.off = pos;
			}
		} else if (starts_au && cur.has_vcl) {
			cur.len = pos - cur.off;
			if (n == cap) a = realloc(a, (cap *= 2) * sizeof(*a));
			a[n++] = cur;
			memset(&cur, 0, sizeof(cur));
			cur.off = pos;
		}
		if (vcl) {
			cur.has_vcl = 1;
			if (t == 5) cur.idr = 1;
		}
		pos = next;
		sc = sc2;
	}
	if (cur.has_vcl) {
		cur.len = len - cur.off;
		if (n == cap) a = realloc(a, (cap + 1) * sizeof(*a));
		a[n++] = cur;
	}
	*n_au = n;
	return a;
}

int main(int argc, char **argv)
{
	const char *outpath = NULL, *fmt = "nv12";
	int max_frames = 0, pool = 0, no_reorder = 0, opt;
	while ((opt = getopt(argc, argv, "o:f:n:p:lq")) != -1) {
		switch (opt) {
		case 'o': outpath = optarg; break;
		case 'f': fmt = optarg; break;
		case 'n': max_frames = atoi(optarg); break;
		case 'p': pool = atoi(optarg); break;
		case 'l': no_reorder = 1; break;
		case 'q': g_quiet = 1; break;
		default: goto usage;
		}
	}
	if (optind >= argc) {
usage:
		fprintf(stderr, "usage: %s [-o out.yuv] [-f nv12|i420|yv12|blk] [-n frames] [-p bufs] [-l] [-q] in.h264\n", argv[0]);
		return 2;
	}
	if (strcmp(fmt, "nv12") && strcmp(fmt, "i420") && strcmp(fmt, "yv12") && strcmp(fmt, "blk"))
		goto usage;

	size_t len, n_au, cfg_len;
	uint8_t *bs = read_file(argv[optind], &len);
	if (!bs)
		return 1;
	au_t *au = split_aus(bs, len, &n_au, &cfg_len);
	size_t max_au = cfg_len;
	for (size_t i = 0; i < n_au; i++)
		if (au[i].len > max_au) max_au = au[i].len;
	printf("vdec_test: %s: %zu bytes, config %zu bytes, %zu access units (max %zu bytes)\n",
	       argv[optind], len, cfg_len, n_au, max_au);
	if (!n_au) {
		fprintf(stderr, "no VCL NAL units found (not Annex-B H.264?)\n");
		return 1;
	}

	FILE *out = NULL;
	if (outpath && !(out = fopen(outpath, "wb"))) {
		perror(outpath);
		return 1;
	}

	/* input buffers: ION/VCODEC_SHIM_ALLOC via eVideoMemAlloc, 64-byte aligned
	 * (we use 4096), plus >= 512 bytes of slack as the OMX keeps */
	uint32_t in_size = ALIGN((uint32_t)max_au + cfg_len + 4096, 4096);
	if (in_size < (1u << 20)) in_size = 1u << 20;
	static hbuf_t inb[N_INPUT];
	static VDEC_DRV_RINGBUF_T rb[N_INPUT];
	for (int i = 0; i < N_INPUT; i++)
		if (hbuf_alloc(&inb[i], in_size, 4096, VAL_MEM_CODEC_FOR_VDEC))
			return 1;

	VAL_HANDLE_T h = NULL;
	double t0 = now_ms();
	VDEC_DRV_MRESULT_T r = eVDecDrvCreate(&h, VDEC_DRV_VIDEO_FORMAT_H264);
	if (r != VDEC_DRV_MRESULT_OK || !h) {
		fprintf(stderr, "eVDecDrvCreate failed: %d\n", r);
		return 1;
	}

	/* fill a ring buffer descriptor for input slot i */
	#define FILL_RB(i, data, n, flags, ts) do {                                  \
		memcpy(inb[i].va, (data), (n));                                     \
		memset(inb[i].va + (n), 0, 512);                                    \
		hbuf_flush(&inb[i]);                                                \
		memset(&rb[i], 0, sizeof(rb[i]));                                   \
		rb[i].rBase.u4VA = (VAL_ULONG_T)(uintptr_t)inb[i].va;               \
		rb[i].rBase.u4PA = inb[i].pa;                                       \
		rb[i].rBase.u4Size = (n);                                           \
		rb[i].u4Read = rb[i].rBase.u4VA;                                    \
		rb[i].u4Write = rb[i].rBase.u4VA + (n);                             \
		rb[i].u4Timestamp = (ts);                                           \
		rb[i].u4InputFlag = (flags);                                        \
		rb[i].u4OmxInputBufSz = inb[i].size;                                \
	} while (0)

	/* Init: SPS/PPS first. If that is not enough, add access units (the
	 * vendor OMX retries up to 30 times). */
	VDEC_DRV_SEQINFO_T seq;
	size_t first_au = 0;
	for (int tries = 0;; tries++) {
		memset(&seq, 0, sizeof(seq));
		size_t n = tries == 0 ? cfg_len : au[tries - 1].off + au[tries - 1].len;
		if (n == 0) { tries++; n = au[0].off + au[0].len; }
		FILL_RB(0, bs, n, tries == 0 ? VDEC_DRV_INPUT_BUF_INIT_CONFIG_DATA : 0, 0);
		r = eVDecDrvInit(h, &rb[0], &seq);
		if (r == VDEC_DRV_MRESULT_OK)
			break;
		if (tries >= 30 || (size_t)tries >= n_au) {
			fprintf(stderr, "eVDecDrvInit failed: %d\n", r);
			eVDecDrvRelease(h);
			return 1;
		}
	}
	double t_init = now_ms() - t0;
	printf("vdec_test: init ok (%.1f ms): buffer %ux%u, picture %ux%u, bitdepth %d/%d\n", t_init,
	       seq.u4Width, seq.u4Height, seq.u4PicWidth, seq.u4PicHeight,
	       seq.u4BitDepthLuma, seq.u4BitDepthChroma);

	VAL_UINT32_T pixfmt = 0, dpb = 0;
	eVDecDrvGetParam(h, VDEC_DRV_GET_TYPE_GET_PIXEL_FORMAT, NULL, &pixfmt);
	eVDecDrvGetParam(h, VDEC_DRV_GET_TYPE_QUERY_VIDEO_DPB_SIZE, NULL, &dpb);
	printf("vdec_test: pixel format %u (2 = MTK block), DPB size %u\n", pixfmt, dpb);

	uint32_t pw = seq.u4PicWidth ? seq.u4PicWidth : seq.u4Width;
	uint32_t ph = seq.u4PicHeight ? seq.u4PicHeight : seq.u4Height;
	g_W = ALIGN(pw > seq.u4Width ? pw : seq.u4Width, 16);
	g_H = ALIGN(ph > seq.u4Height ? ph : seq.u4Height, 32);
	g_nfb = pool ? pool : (int)dpb + 4;
	if (g_nfb < 8) g_nfb = 8;
	if (g_nfb > MAX_FB) g_nfb = MAX_FB;
	uint32_t fsize = VDEC_MTK_BLK_FRAME_SIZE(g_W, g_H);
	for (int i = 0; i < g_nfb; i++) {
		if (hbuf_alloc(&g_fb[i].buf, fsize, 512, VAL_MEM_CODEC_FOR_VDEC))
			return 1;
		g_fb[i].free = 1;
	}
	printf("vdec_test: %d output buffers of %u bytes (pitch %u, rows %u)\n", g_nfb, fsize, g_W, g_H);

	if (no_reorder) {
		VDEC_DRV_SET_DECODE_MODE_T dm = { VDEC_DRV_DECODE_MODE_NO_REORDER, 0, 0 };
		eVDecDrvSetParam(h, VDEC_DRV_SET_TYPE_SET_DECODE_MODE, &dm, NULL);
	}
	static VDEC_DRV_CALLBACK_T cb;
	memset(&cb, 0, sizeof(cb));
	cb.u4hHandle = h;
	cb.pfnGetOutputBuffer = get_output_buffer;
	r = eVDecDrvSetParam(h, VDEC_DRV_SET_TYPE_SET_CALLBACK, &cb, NULL);
	if (r != VDEC_DRV_MRESULT_OK) {
		fprintf(stderr, "SetParam(CALLBACK) failed: %d\n", r);
		return 1;
	}
	VAL_UINT32_T wait_key = 0;
	eVDecDrvSetParam(h, VDEC_DRV_SET_TYPE_SET_WAIT_KEYFRAME, &wait_key, NULL);

	/* crop for the output files */
	uint32_t cw = pw, ch = ph;
	VDEC_DRV_CROPINFO_T crop;
	memset(&crop, 0, sizeof(crop));
	if (eVDecDrvGetParam(h, VDEC_DRV_GET_TYPE_GET_FRAME_CROP_INFO, NULL, &crop) == VDEC_DRV_MRESULT_OK &&
	    crop.u4CropRight > crop.u4CropLeft && crop.u4CropBottom > crop.u4CropTop) {
		/* only right/bottom cropping is applied (left/top are 0 in practice) */
		cw = crop.u4CropRight + 1;
		ch = crop.u4CropBottom + 1;
		if (cw > g_W) cw = g_W;
		if (ch > g_H) ch = g_H;
	}
	if (cw > g_W) cw = g_W;
	if (ch > g_H) ch = g_H;
	if (out)
		printf("vdec_test: writing %s %ux%u to %s\n", fmt, !strcmp(fmt, "blk") ? g_W : cw,
		       !strcmp(fmt, "blk") ? g_H : ch, outpath);

	unsigned n_out = 0, n_dec = 0, n_err = 0;
	double dsum = 0, dmin = 1e9, dmax = 0, t_out = 0;
	double tdec0 = now_ms();

	#define DRAIN() do {                                                         \
		P_VDEC_DRV_FRAMEBUF_T f;                                            \
		for (;;) {                                                          \
			f = NULL;                                                   \
			if (eVDecDrvGetParam(h, VDEC_DRV_GET_TYPE_DISP_FRAME_BUFFER, NULL, &f) != VDEC_DRV_MRESULT_OK || !f) \
				break;                                              \
			int fi = fb_index(f);                                       \
			if (f->u4FrameBufferStatus & (VDEC_DRV_FBSTSTUS_REPEAT_LAST | VDEC_DRV_FBSTSTUS_NOT_DISPLAY | VDEC_DRV_FBSTSTUS_NOT_USED)) \
				continue;                                           \
			if (f->u4FrameBufferStatus & VDEC_DRV_FBSTSTUS_ERROR)       \
				n_err++;                                            \
			n_out++;                                                    \
			if (out && fi >= 0) {                                       \
				double tw = now_ms();                               \
				hbuf_invalidate(&g_fb[fi].buf);                     \
				if (!strcmp(fmt, "blk"))                            \
					fwrite(g_fb[fi].buf.va, 1, VDEC_MTK_BLK_FRAME_SIZE(g_W, g_H), out); \
				else                                                \
					detile(g_fb[fi].buf.va, g_W, g_H, cw, ch, fmt, out); \
				t_out += now_ms() - tw;                             \
			}                                                           \
			if (fi < 0)                                                 \
				fprintf(stderr, "display frame %p is not ours\n", (void *)f); \
		}                                                                   \
		for (;;) {                                                          \
			f = NULL;                                                   \
			if (eVDecDrvGetParam(h, VDEC_DRV_GET_TYPE_FREE_FRAME_BUFFER, NULL, &f) != VDEC_DRV_MRESULT_OK || !f) \
				break;                                              \
			int fi = fb_index(f);                                       \
			if (fi >= 0) g_fb[fi].free = 1;                             \
		}                                                                   \
		for (;;) {                                                          \
			P_VDEC_DRV_RINGBUF_T ib = NULL;                             \
			if (eVDecDrvGetParam(h, VDEC_DRV_GET_TYPE_FREE_INPUT_BUFFER, NULL, &ib) != VDEC_DRV_MRESULT_OK || !ib) \
				break;                                              \
		}                                                                   \
	} while (0)

	for (size_t i = first_au; i < n_au; i++) {
		int slot = (int)(i % N_INPUT);
		/* eVDecDrvInit only parses the SPS; the PPS (and any SEI) before
		 * the first picture must reach DecodeEx too: send the config
		 * NALs together with the first access unit. */
		size_t off = i == 0 ? 0 : au[i].off;
		size_t n = au[i].off + au[i].len - off;
		FILL_RB(slot, bs + off, n, 0, (VAL_UINT32_T)(i * 33333));
		double ts = now_ms();
		r = eVDecDrvDecodeEx(h, &rb[slot], 0, NULL);
		double dt = now_ms() - ts;
		n_dec++;
		dsum += dt;
		if (dt < dmin) dmin = dt;
		if (dt > dmax) dmax = dt;
		unsigned before = n_out;
		DRAIN();
		if (!g_quiet)
			printf("au %5zu %s %7zu B: decode %7.2f ms, result %d, %u out\n", i,
			       au[i].idr ? "IDR" : "   ", n, dt, r, n_out - before);
		if (r == VDEC_DRV_MRESULT_RESOLUTION_CHANGED) {
			fprintf(stderr, "resolution changed: not handled by this test, stopping\n");
			break;
		}
		if (r == VDEC_DRV_MRESULT_FATAL) {
			fprintf(stderr, "fatal decode error, stopping\n");
			break;
		}
		if (max_frames && n_out >= (unsigned)max_frames)
			break;
	}
	/* EOS: flush the DPB */
	for (int k = 0; k < 4; k++) {
		unsigned before = n_out;
		eVDecDrvDecodeEx(h, NULL, 0, NULL);
		DRAIN();
		if (n_out == before && k)
			break;
	}
	double tdec = now_ms() - tdec0;

	printf("vdec_test: %u access units decoded, %u frames out (%u with errors)\n", n_dec, n_out, n_err);
	if (n_dec)
		printf("vdec_test: DecodeEx avg %.2f ms, min %.2f, max %.2f -> %.1f fps (HW+driver); "
		       "wall %.1f ms incl. output %.1f ms; callback %u calls, %u starved\n",
		       dsum / n_dec, dmin, dmax, 1000.0 * n_dec / dsum, tdec, t_out, g_cb_calls, g_cb_starved);

	eVDecDrvDeInit(h);
	eVDecDrvRelease(h);
	for (int i = 0; i < g_nfb; i++) hbuf_free(&g_fb[i].buf);
	for (int i = 0; i < N_INPUT; i++) hbuf_free(&inb[i]);
	if (out) fclose(out);
	free(au);
	free(bs);
	return n_out ? 0 : 1;
}
