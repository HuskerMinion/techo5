/*
 * vcodec-dec: the MT8163's hardware H.264 and H.265 decoders as a filter, for echod's video player
 * (echod/internal/feature/video, the Show 5 2nd gen's hardware path).
 *
 *   vcodec-dec [-c h264|hevc] -s WxH < in.h264 > out.nv12
 *
 * Reads an Annex-B H.264 (or, with -c hevc, H.265) elementary stream on its standard input as it
 * comes, and writes every picture to show as NV12 of WxH on its standard output (the picture's top
 * left corner; what is outside it is cut, what it does not fill is black), in the order to show
 * them. Ends at the end of
 * the input after the decoder's last pictures, or when the output is closed. Exit status 3: the
 * stream is not one this decoder takes (its size changed, a fatal error), so the caller can fall
 * back to decoding on the cores.
 *
 * The call sequence is vdec_test's (MediaTek's driver API, libvcodecdrv: Create, Init with the
 * stream's parameter sets, the output buffer callback, DecodeEx per access unit, and the display /
 * free / input queues drained after each). The decoder writes MediaTek's block format (16x32 luma
 * tiles, 16x16 tiles of interleaved CbCr), turned into NV12 here. Its output buffers are this
 * program's own, cached (VCODEC_SHIM_ALLOC_CACHED), synced before they are read: reading the
 * stand-ins' write-combined buffers took 27 ms a 720p frame.
 *
 * H.265 is the same libvcodecdrv (gH265_DRV_IF, the HEVC_VLD block at 0x16028000 with the shared
 * VLD/MC/PP/MV ones), the same calls and the same block format out (it reports pixel format 4, but
 * the planes are laid out as H.264's; its buffers are 64-line aligned, 720p's 1280x768). Two things
 * differ: the access units split on H.265's NAL header, and DecodeEx takes a keyframe's access unit
 * a NAL unit at a time (VPS, SPS, PPS, SEI, then the slice), answering "need more output buffer"
 * after each until the slice is done; the same buffer is handed in again until it is.
 *
 * VCODEC_DEC_RAW=path (debugging) writes the first picture as the decoder made it.
 *
 * VCODEC_DEC_TEST feeds the decoder wrongly, to test the kernel's recovery from a hung decoder
 * (mt8163-vcodec.c resets it after a timed-out wait):
 *   midslice     H.265: a keyframe's access unit is not handed in again after "need more output
 *                buffer"; the next one is (the feeding that once hung the decoder for good)
 *   corrupt=N[,S[,K]]  N random bytes of each access unit after the first (or of the K-th
 *                only), past its first 8, are changed (random seed S, 1 by default)
 *   badfb=K      the K-th output buffer handed to the decoder has an address nothing is mapped
 *                at (0x00400000), so that its writes fault in the IOMMU
 *   wedge=K[,OFF[,VAL]]  at the K-th wait for the decoder's interrupt (it holds the hardware,
 *                powered, then), VAL (1) is written to its MISC register at OFF (0x100, the bus
 *                "break" of MediaTek's later decoders); MISC is printed first
 * The number of pictures out is printed at the end.
 */
#include <errno.h>
#include <fcntl.h>
#include <linux/dma-buf.h>
#include <signal.h>
#include <stdarg.h>
#include <sys/syscall.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/ioctl.h>
#include <sys/mman.h>
#include <unistd.h>

#include "common.h"
#include "vcodec_shim_uapi.h"
#include "vdec_drv_if_public.h"

#define MAX_FB   32
#define N_INPUT  4
#define ALIGN(x, a) (((x) + (a) - 1) & ~((a) - 1))
#define EXIT_UNSUPPORTED 3

/* An output frame: a cached buffer of ours, by its dma-buf */
static struct {
	int fd;
	uint8_t *va;
	uint32_t iova, size;
	VDEC_DRV_FRAMEBUF_T fb;
	int free;
} g_fb[MAX_FB];
static int g_nfb;
static uint32_t g_W, g_H;		/* the frames' (aligned) pitch and rows */
static uint32_t g_ow, g_oh;		/* the output's size */
static uint8_t *g_out;			/* one NV12 picture */
static int g_outfd = 1;			/* the pictures' descriptor (the library prints on 1) */
static int g_hevc;			/* -c hevc: the stream is H.265 */
static unsigned g_pics;			/* pictures written */
static unsigned g_badfb, g_fbs;		/* VCODEC_DEC_TEST=badfb=K; output buffers handed out */

static int frame_alloc(int i, uint32_t size)
{
	static int dev = -1;
	struct vcodec_shim_alloc a = { .size = size, .align = 4096, .flags = VCODEC_SHIM_ALLOC_CACHED };

	if (dev < 0 && (dev = open("/dev/Vcodec", O_RDWR | O_CLOEXEC)) < 0)
		return -1;
	if (ioctl(dev, VCODEC_SHIM_ALLOC, &a) < 0)
		return -1;
	g_fb[i].va = mmap(NULL, a.size, PROT_READ | PROT_WRITE, MAP_SHARED, a.fd, 0);
	if (g_fb[i].va == MAP_FAILED) {
		close(a.fd);
		return -1;
	}
	g_fb[i].fd = a.fd;
	g_fb[i].iova = (uint32_t)a.iova;
	g_fb[i].size = (uint32_t)a.size;
	g_fb[i].free = 1;
	return 0;
}

static void frame_sync(int i, int start)
{
	struct dma_buf_sync s = { DMA_BUF_SYNC_READ | (start ? DMA_BUF_SYNC_START : DMA_BUF_SYNC_END) };

	ioctl(g_fb[i].fd, DMA_BUF_IOCTL_SYNC, &s);
}

/*
 * VCODEC_DEC_TEST=wedge: the library's ioctl() is this one (the program's symbols come first), which
 * passes everything on, and at the K-th VCODEC_WAITISR writes the decoder's register first.
 */
static unsigned g_wedge, g_wedge_off = 0x100, g_wedge_val = 1, g_waits;

static void wedge(void)
{
	int fd = open("/dev/Vcodec", O_RDWR | O_CLOEXEC);
	volatile uint32_t *misc = fd < 0 ? MAP_FAILED :
		mmap(NULL, 0x1000, PROT_READ | PROT_WRITE, MAP_SHARED, fd, 0x16020000);

	if (misc != MAP_FAILED) {
		for (unsigned o = 0; o < 0x200; o += 4)
			fprintf(stderr, "%s%08x", o % 32 ? " " : "\nMISC ", misc[o / 4]);
		fprintf(stderr, "\nvcodec-dec: MISC 0x%x was 0x%08x, now 0x%x\n", g_wedge_off,
			misc[g_wedge_off / 4], g_wedge_val);
		misc[g_wedge_off / 4] = g_wedge_val;
		munmap((void *)misc, 0x1000);
	}
	if (fd >= 0)
		close(fd);
}

__attribute__((visibility("default"))) int ioctl(int fd, int req, ...)
{
	va_list ap;
	void *arg;

	va_start(ap, req);
	arg = va_arg(ap, void *);
	va_end(ap);
	if (g_wedge && (unsigned)req == 0x40044d0bu && ++g_waits == g_wedge)	/* VCODEC_WAITISR */
		wedge();
	return (int)syscall(__NR_ioctl, fd, req, arg);
}

/* the driver asks for an output buffer while decoding */
static VAL_UINT32_T get_output_buffer(VAL_HANDLE_T cookie, P_VDEC_DRV_FRAMEBUF_T *ppFrame,
				      VAL_UINT32_T flag, VAL_BOOL_T bflag, VAL_VOID_T *extra)
{
	(void)cookie; (void)flag; (void)bflag; (void)extra;
	for (int i = 0; i < g_nfb; i++) {
		if (g_fb[i].free) {
			VDEC_DRV_FRAMEBUF_T *fb = &g_fb[i].fb;

			g_fb[i].free = 0;
			memset(fb, 0, sizeof(*fb));
			fb->rBaseAddr.u4VA = (VAL_ULONG_T)(uintptr_t)g_fb[i].va;
			fb->rBaseAddr.u4PA = ++g_fbs == g_badfb ? 0x00400000 : g_fb[i].iova;
			fb->rBaseAddr.u4Size = g_fb[i].size;
			*ppFrame = fb;
			return 0;
		}
	}
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

static int write_all(const uint8_t *p, size_t n)
{
	while (n) {
		ssize_t w = write(g_outfd, p, n);

		if (w < 0) {
			if (errno == EINTR)
				continue;
			return -1;
		}
		p += w;
		n -= (size_t)w;
	}
	return 0;
}

/* MediaTek block format to NV12 of g_ow x g_oh (cw x ch of it is the picture) */
static int emit(const uint8_t *src, uint32_t cw, uint32_t ch)
{
	uint8_t *y = g_out, *c = g_out + g_ow * g_oh;
	const uint8_t *sc = src + g_W * g_H;
	uint32_t w = cw < g_ow ? cw : g_ow, h = ch < g_oh ? ch : g_oh;

	for (uint32_t r = 0; r < h; r++)
		for (uint32_t x = 0; x < w; x += 16)
			memcpy(y + r * g_ow + x, src + VDEC_MTK_BLK_Y_OFF(x, r, g_W), w - x < 16 ? w - x : 16);
	for (uint32_t r = 0; r < (h + 1) / 2; r++)
		for (uint32_t x = 0; x < (w + 1) / 2 * 2; x += 16) {
			uint32_t n = (w + 1) / 2 * 2 - x;

			memcpy(c + r * g_ow + x, sc + VDEC_MTK_BLK_C_OFF(x, r, g_W), n < 16 ? n : 16);
		}
	g_pics++;
	return write_all(g_out, (size_t)g_ow * g_oh * 3 / 2);
}

/* ---- the input, access unit by access unit ---- */
static uint8_t *g_in;
static size_t g_len, g_cap;
static int g_eof;

/* more of the stream; 0 at its end */
static int fill(void)
{
	if (g_eof)
		return 0;
	if (g_cap - g_len < 65536) {
		g_cap = g_cap ? g_cap * 2 : 1 << 20;
		g_in = realloc(g_in, g_cap);
		if (!g_in)
			exit(1);
	}
	ssize_t n;
	do
		n = read(0, g_in + g_len, g_cap - g_len);
	while (n < 0 && errno == EINTR);
	if (n <= 0) {
		g_eof = 1;
		return 0;
	}
	g_len += (size_t)n;
	return 1;
}

/*
 * The next access unit at or after *pos (NAL units to its last VCL one and what follows that does
 * not start the next): [*start, *end). The parameter sets and SEI before a picture go with it.
 * 0 when the stream is done.
 */
static int next_au(size_t *pos, size_t *start, size_t *end)
{
	for (;;) {
		int sc;
		size_t p = next_start_code(g_in, g_len, *pos, &sc), au = p;
		int seen_vcl = 0;

		while (p < g_len) {
			int sc2;
			size_t nx = next_start_code(g_in, g_len, p + sc, &sc2);

			if (p + sc >= g_len)
				break;			/* a start code with nothing after it yet */
			if (nx == g_len && !g_eof)
				break;			/* the NAL unit may not be whole yet */
			int t, vcl, starts;

			if (g_hevc) {
				/* H.265: type in bits 1..6 of a two-byte header; a picture's first slice
				 * segment has first_slice_segment_in_pic_flag, the third byte's top bit.
				 * VPS/SPS/PPS, AUD, prefix SEI and the reserved 41..44, 48..55 go with
				 * the next picture; suffix SEI (40) with this one */
				t = (g_in[p + sc] >> 1) & 0x3f;
				vcl = t < 32;
				int first = vcl && p + sc + 2 < g_len && (g_in[p + sc + 2] & 0x80);
				starts = (vcl && first) || (t >= 32 && t <= 35) || t == 39 ||
					 (t >= 41 && t <= 44) || (t >= 48 && t <= 55);
			} else {
				t = g_in[p + sc] & 0x1f;
				vcl = t >= 1 && t <= 5;
				int first_mb0 = vcl && p + sc + 1 < g_len && (g_in[p + sc + 1] & 0x80);
				starts = (vcl && first_mb0) || t == 9 || t == 7 || t == 8 || t == 6;
			}

			if (seen_vcl && starts) {
				*start = au;
				*end = *pos = p;
				return 1;
			}
			if (vcl)
				seen_vcl = 1;
			p = nx;
			sc = sc2;
		}
		if (g_eof) {
			if (!seen_vcl)
				return 0;
			*start = au;
			*end = *pos = g_len;
			return 1;
		}
		/* drop what was used, then read more */
		if (au > 0) {
			memmove(g_in, g_in + au, g_len - au);
			g_len -= au;
			*pos = 0;
		}
		fill();
	}
}

int main(int argc, char **argv)
{
	int opt;

	signal(SIGPIPE, SIG_IGN);
	/* the pictures only on the real standard output: the library prints to it ("[Info] Flush
	 * buffer ..."), so its descriptor 1 is the standard error from here on */
	g_outfd = dup(1);
	if (g_outfd < 0 || dup2(2, 1) < 0)
		return 1;
	while ((opt = getopt(argc, argv, "s:c:")) != -1) {
		if (opt == 'c' && !strcmp(optarg, "hevc"))
			g_hevc = 1;
		else if (opt == 'c' && !strcmp(optarg, "h264"))
			g_hevc = 0;
		else if (opt != 's' || sscanf(optarg, "%ux%u", &g_ow, &g_oh) != 2)
			goto usage;
	}
	if (!g_ow || !g_oh || g_ow % 2 || g_oh % 2) {
usage:
		fprintf(stderr, "usage: %s [-c h264|hevc] -s WxH < in.h264 > out.nv12\n", argv[0]);
		return 2;
	}
	g_out = calloc(1, (size_t)g_ow * g_oh * 3 / 2);
	memset(g_out + g_ow * g_oh, 128, (size_t)g_ow * g_oh / 2);

	static hbuf_t inb[N_INPUT];
	static VDEC_DRV_RINGBUF_T rb[N_INPUT];
	uint32_t in_size = 2u << 20;
	for (int i = 0; i < N_INPUT; i++)
		if (hbuf_alloc(&inb[i], in_size, 4096, VAL_MEM_CODEC_FOR_VDEC))
			return 1;

	#define FILL_RB(i, data, n, flags) do {                                      \
		memcpy(inb[i].va, (data), (n));                                     \
		memset(inb[i].va + (n), 0, 512);                                    \
		hbuf_flush(&inb[i]);                                                \
		memset(&rb[i], 0, sizeof(rb[i]));                                   \
		rb[i].rBase.u4VA = (VAL_ULONG_T)(uintptr_t)inb[i].va;               \
		rb[i].rBase.u4PA = inb[i].pa;                                       \
		rb[i].rBase.u4Size = (n);                                           \
		rb[i].u4Read = rb[i].rBase.u4VA;                                    \
		rb[i].u4Write = rb[i].rBase.u4VA + (n);                             \
		rb[i].u4InputFlag = (flags);                                        \
		rb[i].u4OmxInputBufSz = inb[i].size;                                \
	} while (0)

	/* The stream's first access unit, with its parameter sets, starts the decoder */
	size_t pos = 0, s0, e0;
	fill();
	if (!next_au(&pos, &s0, &e0))
		return EXIT_UNSUPPORTED;
	if (e0 - s0 + 512 > in_size)
		return EXIT_UNSUPPORTED;

	VAL_HANDLE_T h = NULL;
	if (eVDecDrvCreate(&h, g_hevc ? VDEC_DRV_VIDEO_FORMAT_H265 : VDEC_DRV_VIDEO_FORMAT_H264) != VDEC_DRV_MRESULT_OK || !h)
		return EXIT_UNSUPPORTED;
	VDEC_DRV_SEQINFO_T seq;
	memset(&seq, 0, sizeof(seq));
	FILL_RB(0, g_in + s0, e0 - s0, VDEC_DRV_INPUT_BUF_INIT_CONFIG_DATA);
	if (eVDecDrvInit(h, &rb[0], &seq) != VDEC_DRV_MRESULT_OK) {
		fprintf(stderr, "vcodec-dec: the decoder does not take this stream\n");
		eVDecDrvRelease(h);
		return EXIT_UNSUPPORTED;
	}

	VAL_UINT32_T dpb = 0;
	eVDecDrvGetParam(h, VDEC_DRV_GET_TYPE_QUERY_VIDEO_DPB_SIZE, NULL, &dpb);
	uint32_t pw = seq.u4PicWidth ? seq.u4PicWidth : seq.u4Width;
	uint32_t ph = seq.u4PicHeight ? seq.u4PicHeight : seq.u4Height;
	g_W = ALIGN(pw > seq.u4Width ? pw : seq.u4Width, 16);
	g_H = ALIGN(ph > seq.u4Height ? ph : seq.u4Height, 32);
	g_nfb = (int)dpb + 4;
	if (g_nfb < 8)
		g_nfb = 8;
	if (g_nfb > MAX_FB)
		g_nfb = MAX_FB;
	for (int i = 0; i < g_nfb; i++)
		if (frame_alloc(i, VDEC_MTK_BLK_FRAME_SIZE(g_W, g_H))) {
			fprintf(stderr, "vcodec-dec: no frame buffers: %s\n", strerror(errno));
			return 1;
		}

	static VDEC_DRV_CALLBACK_T cb;
	cb.u4hHandle = h;
	cb.pfnGetOutputBuffer = get_output_buffer;
	if (eVDecDrvSetParam(h, VDEC_DRV_SET_TYPE_SET_CALLBACK, &cb, NULL) != VDEC_DRV_MRESULT_OK)
		return EXIT_UNSUPPORTED;
	VAL_UINT32_T wait_key = 0;
	eVDecDrvSetParam(h, VDEC_DRV_SET_TYPE_SET_WAIT_KEYFRAME, &wait_key, NULL);

	uint32_t cw = pw, ch = ph;
	VDEC_DRV_CROPINFO_T crop;
	memset(&crop, 0, sizeof(crop));
	if (eVDecDrvGetParam(h, VDEC_DRV_GET_TYPE_GET_FRAME_CROP_INFO, NULL, &crop) == VDEC_DRV_MRESULT_OK &&
	    crop.u4CropRight > crop.u4CropLeft && crop.u4CropBottom > crop.u4CropTop) {
		cw = crop.u4CropRight + 1;
		ch = crop.u4CropBottom + 1;
	}
	if (cw > g_W)
		cw = g_W;
	if (ch > g_H)
		ch = g_H;
	VAL_UINT32_T pixfmt = 0;
	eVDecDrvGetParam(h, VDEC_DRV_GET_TYPE_GET_PIXEL_FORMAT, NULL, &pixfmt);
	fprintf(stderr, "vcodec-dec: %s %ux%u (buffers %ux%u, format %u), %d frame buffers, out %ux%u\n",
		g_hevc ? "hevc" : "h264", cw, ch, seq.u4Width, seq.u4Height, pixfmt, g_nfb, g_ow, g_oh);
	const char *rawpath = getenv("VCODEC_DEC_RAW");	/* debugging: the first frame as the decoder wrote it */
	const char *test = getenv("VCODEC_DEC_TEST");
	int midslice = test && !strcmp(test, "midslice");
	unsigned corrupt = 0, seed = 1, only = 0;
	if (test && sscanf(test, "corrupt=%u,%u,%u", &corrupt, &seed, &only) >= 1)
		srand(seed);
	if (test && sscanf(test, "badfb=%u", &g_badfb) == 1 && !g_badfb)
		g_badfb = 1;
	if (test && sscanf(test, "wedge=%u,%x,%x", &g_wedge, &g_wedge_off, &g_wedge_val) >= 1 && !g_wedge)
		g_wedge = 1;
	if (test && !midslice && !corrupt && !g_badfb && !g_wedge) {
		fprintf(stderr, "vcodec-dec: VCODEC_DEC_TEST=%s: not a test\n", test);
		return 2;
	}

	int broken = 0;
	#define DRAIN() do {                                                         \
		P_VDEC_DRV_FRAMEBUF_T f;                                            \
		for (;;) {                                                          \
			f = NULL;                                                   \
			if (eVDecDrvGetParam(h, VDEC_DRV_GET_TYPE_DISP_FRAME_BUFFER, NULL, &f) != VDEC_DRV_MRESULT_OK || !f) \
				break;                                              \
			int fi = fb_index(f);                                       \
			if (fi < 0 || (f->u4FrameBufferStatus & (VDEC_DRV_FBSTSTUS_REPEAT_LAST | VDEC_DRV_FBSTSTUS_NOT_DISPLAY | VDEC_DRV_FBSTSTUS_NOT_USED))) \
				continue;                                           \
			frame_sync(fi, 1);                                          \
			if (rawpath) {                                              \
				FILE *rf = fopen(rawpath, "wb");                    \
				if (rf) { fwrite(g_fb[fi].va, 1, g_fb[fi].size, rf); fclose(rf); } \
				rawpath = NULL;                                     \
			}                                                           \
			if (emit(g_fb[fi].va, cw, ch))                              \
				broken = 1;                                         \
			frame_sync(fi, 0);                                          \
		}                                                                   \
		for (;;) {                                                          \
			f = NULL;                                                   \
			if (eVDecDrvGetParam(h, VDEC_DRV_GET_TYPE_FREE_FRAME_BUFFER, NULL, &f) != VDEC_DRV_MRESULT_OK || !f) \
				break;                                              \
			int fi = fb_index(f);                                       \
			if (fi >= 0)                                                \
				g_fb[fi].free = 1;                                  \
		}                                                                   \
		for (;;) {                                                          \
			P_VDEC_DRV_RINGBUF_T ib = NULL;                             \
			if (eVDecDrvGetParam(h, VDEC_DRV_GET_TYPE_FREE_INPUT_BUFFER, NULL, &ib) != VDEC_DRV_MRESULT_OK || !ib) \
				break;                                              \
		}                                                                   \
	} while (0)

	int status = 0;
	size_t start = s0, end = e0;
	for (unsigned n = 0; !broken; n++) {
		int slot = (int)(n % N_INPUT);

		if (end - start + 512 > in_size) {
			fprintf(stderr, "vcodec-dec: an access unit of %zu bytes\n", end - start);
			status = EXIT_UNSUPPORTED;
			break;
		}
		for (unsigned k = 0; n > 0 && (!only || n == only) && k < corrupt && end - start > 8; k++)
			g_in[start + 8 + (size_t)rand() % (end - start - 8)] ^= (uint8_t)(1 + rand() % 255);
		FILL_RB(slot, g_in + start, end - start, 0);
		VDEC_DRV_MRESULT_T r = eVDecDrvDecodeEx(h, &rb[slot], 0, NULL);
		DRAIN();
		/* H.265: the driver takes the access unit a NAL unit at a time when it holds more than a
		 * picture's slices (the parameter sets in front of a keyframe), saying "more output
		 * buffer" and keeping where it stopped; the same buffer again goes on from there */
		for (int k = 0; g_hevc && !midslice && r == VDEC_DRV_MRESULT_NEED_MORE_OUTPUT_BUFFER && k < 64 && !broken; k++) {
			r = eVDecDrvDecodeEx(h, &rb[slot], 0, NULL);
			DRAIN();
		}
		if (r == VDEC_DRV_MRESULT_RESOLUTION_CHANGED || r == VDEC_DRV_MRESULT_FATAL) {
			fprintf(stderr, "vcodec-dec: %s\n", r == VDEC_DRV_MRESULT_FATAL ?
				"the decoder failed" : "the picture's size changed");
			status = EXIT_UNSUPPORTED;
			break;
		}
		if (!next_au(&pos, &start, &end))
			break;
	}
	/* the end: what the decoder still holds */
	for (int k = 0; k < 4 && !broken && !status; k++) {
		eVDecDrvDecodeEx(h, NULL, 0, NULL);
		DRAIN();
	}
	eVDecDrvDeInit(h);
	eVDecDrvRelease(h);
	if (test)
		fprintf(stderr, "vcodec-dec: %u pictures out\n", g_pics);
	return status;
}
