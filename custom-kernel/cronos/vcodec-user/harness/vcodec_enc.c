/*
 * vcodec-enc: the MT8163's hardware H.264 encoder as a filter, for echod's RTSP camera stream on the
 * Show 5 2nd gen (echod/internal/feature/rtsp, hardware/vencpipe).
 *
 *   vcodec-enc -s WxH [-b bps] [-r fps] [-g gop] < in.i420 > out.h264
 *
 * Reads I420 frames of WxH (Y, then Cb, then Cr, each tightly packed) on its standard input as they
 * come, and writes each one's H.264 (Annex-B, High profile, CBR) on its standard output as soon as
 * it is encoded, every keyframe with the sequence and picture parameter sets in front of it, so a
 * viewer can start at any keyframe. SIGUSR1 makes the next frame a keyframe (a viewer has joined).
 * Ends at the end of the input, or when the output is closed.
 *
 * Each frame's H.264 comes as a record, so the reader need not find where one ends in the stream:
 * u32 length, u32 flags (bit 0 a keyframe, bit 1 the parameter sets alone, the first record), both
 * little-endian, then the bytes. -a writes the bare stream instead.
 *
 * The call sequence is venc_test's (MediaTek's driver API, libvcodecdrv: Create, Init, PARAM_ENC,
 * the sequence header, then ENCODE_FRAME per frame). The frame is read straight into the encoder's
 * input buffer when its size is a multiple of 16 both ways (the Show 5's camera's 1600x1200 is), and
 * copied row by row into the 16-aligned buffer otherwise.
 */
#include <errno.h>
#include <signal.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>

#include "common.h"
#include "venc_drv_if_public.h"

#define ALIGN(x, a) (((x) + (a) - 1) & ~((a) - 1))
#define BS_SIZE 0x180000

static int g_outfd = 1;
static int g_bare;
static volatile sig_atomic_t g_key;

static void want_key(int sig)
{
	(void)sig;
	g_key = 1;
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

static int put(const uint8_t *p, uint32_t n, uint32_t flags)
{
	uint32_t hdr[2] = { n, flags };	/* the SoC is little-endian */

	if (!g_bare && write_all((const uint8_t *)hdr, sizeof(hdr)))
		return -1;
	return write_all(p, n);
}

static int read_all(uint8_t *p, size_t n)
{
	while (n) {
		ssize_t r = read(0, p, n);

		if (r < 0 && errno == EINTR)
			continue;
		if (r <= 0)
			return -1;
		p += r;
		n -= (size_t)r;
	}
	return 0;
}

int main(int argc, char **argv)
{
	uint32_t w = 0, h = 0, bitrate = 6000000, fps = 15, gop = 15;
	int opt;

	while ((opt = getopt(argc, argv, "s:b:r:g:a")) != -1) {
		switch (opt) {
		case 's': if (sscanf(optarg, "%ux%u", &w, &h) != 2) goto usage; break;
		case 'b': bitrate = (uint32_t)strtoul(optarg, NULL, 0); break;
		case 'r': fps = (uint32_t)atoi(optarg); break;
		case 'g': gop = (uint32_t)atoi(optarg); break;
		case 'a': g_bare = 1; break;
		default: goto usage;
		}
	}
	if (!w || !h || (w & 1) || (h & 1) || !fps) {
usage:
		fprintf(stderr, "usage: %s -s WxH [-b bps] [-r fps] [-g gop] [-a] < in.i420 > out\n", argv[0]);
		return 2;
	}
	signal(SIGPIPE, SIG_IGN);
	/* the stream only on the real standard output: the library prints to it */
	g_outfd = dup(1);
	if (g_outfd < 0 || dup2(2, 1) < 0)
		return 1;
	struct sigaction sa;
	memset(&sa, 0, sizeof(sa));
	sa.sa_handler = want_key;
	sigaction(SIGUSR1, &sa, NULL);	/* no SA_RESTART: a read in progress just goes round again */

	uint32_t bw = ALIGN(w, 16), bh = ALIGN(h, 16), cw = w / 2, ch = h / 2;
	uint32_t frame_size = bw * bh + 2 * (bw / 2) * (bh / 2);
	size_t in_frame = (size_t)w * h * 3 / 2;
	int direct = bw == w && bh == h;
	hbuf_t fb, bsb;
	if (hbuf_alloc(&fb, ALIGN(frame_size, 4096), 512, VAL_MEM_CODEC_FOR_VENC) ||
	    hbuf_alloc(&bsb, BS_SIZE, 512, VAL_MEM_CODEC_FOR_VENC))
		return 1;
	uint8_t *Y = fb.va, *U = fb.va + bw * bh, *V = U + (bw / 2) * (bh / 2);
	uint8_t *raw = direct ? NULL : malloc(in_frame);
	memset(fb.va, 0x80, frame_size);

	VAL_HANDLE_T hd = NULL;
	if (eVEncDrvCreate(&hd, VENC_DRV_VIDEO_FORMAT_H264) != VENC_DRV_MRESULT_OK || !hd)
		return 1;
	VAL_RECORD_SIZE_T rs = { w, h, bw, bh };
	eVEncDrvSetParam(hd, VENC_DRV_SET_TYPE_RECORD_SIZE, &rs, NULL);
	if (eVEncDrvInit(hd) != VENC_DRV_MRESULT_OK)
		return 1;
	static VENC_DRV_PARAM_ENC_EXTRA_T extra;
	static VENC_DRV_PARAM_ENC_T enc;
	extra.u4IntraFrameRate = gop ? gop : 1;
	extra.u4BitRate = bitrate;
	extra.u4FrameRateQ16 = fps << 16;
	extra.u4BitrateMode = 1;	/* CBR */
	enc.eVEncFormat = VENC_DRV_YUV_FORMAT_420;
	enc.u4Profile = VENC_DRV_H264_VIDEO_PROFILE_HIGH;
	enc.u4Level = (w * h > 1280 * 720) ? VENC_DRV_VIDEO_LEVEL_4 : VENC_DRV_VIDEO_LEVEL_3_1;
	enc.u4Width = w;
	enc.u4Height = h;
	enc.u4BufWidth = bw;
	enc.u4BufHeight = bh;
	enc.u4NumPFrm = extra.u4IntraFrameRate - 1;
	enc.eFrameRate = fps;
	enc.pvExtraEnc = &extra;
	if (eVEncDrvSetParam(hd, VENC_DRV_SET_TYPE_PARAM_ENC, &enc, NULL) != VENC_DRV_MRESULT_OK)
		return 1;
	VAL_UINT32_T one = 1;
	eVEncDrvSetParam(hd, VENC_DRV_SET_TYPE_PREPEND_HEADER, &one, NULL);

	static VENC_DRV_PARAM_BS_BUF_T bs;
	static VENC_DRV_PARAM_FRM_BUF_T frm;
	static VENC_DRV_DONE_RESULT_T res;
	#define SET_BS() do {                                                  \
		memset(&bs, 0, sizeof(bs));                                    \
		bs.rBSAddr.u4VA = (VAL_ULONG_T)(uintptr_t)bsb.va;              \
		bs.rBSAddr.u4PA = bsb.pa;                                      \
		bs.rBSAddr.u4Size = BS_SIZE;                                   \
		bs.u4BSStartVA = bs.rBSAddr.u4VA;                              \
		memset(&res, 0, sizeof(res));                                  \
	} while (0)

	SET_BS();
	if (eVEncDrvEncode(hd, VENC_DRV_START_OPT_ENCODE_SEQUENCE_HEADER, NULL, &bs, &res) != VENC_DRV_MRESULT_OK ||
	    !bs.u4BSSize || bs.u4BSSize > BS_SIZE)
		return 1;
	hbuf_invalidate(&bsb);
	if (put(bsb.va, bs.u4BSSize, 2))
		return 0;
	fprintf(stderr, "vcodec-enc: %ux%u, %u bit/s, %u fps, keyframe every %u\n", w, h, bitrate, fps, gop);

	int status = 0;
	for (uint32_t i = 0;; i++) {
		if (direct) {
			if (read_all(fb.va, in_frame))
				break;
		} else {
			if (read_all(raw, in_frame))
				break;
			const uint8_t *su = raw + (size_t)w * h, *sv = su + (size_t)cw * ch;
			for (uint32_t j = 0; j < h; j++)
				memcpy(Y + j * bw, raw + (size_t)j * w, w);
			for (uint32_t j = 0; j < ch; j++) {
				memcpy(U + j * (bw / 2), su + (size_t)j * cw, cw);
				memcpy(V + j * (bw / 2), sv + (size_t)j * cw, cw);
			}
		}
		hbuf_flush(&fb);
		if (g_key) {
			g_key = 0;
			eVEncDrvSetParam(hd, VENC_DRV_SET_TYPE_FORCE_INTRA_ON, NULL, NULL);
		}
		memset(&frm, 0, sizeof(frm));
		frm.rFrmBufAddr.u4VA = (VAL_ULONG_T)(uintptr_t)fb.va;
		frm.rFrmBufAddr.u4PA = fb.pa;
		frm.rFrmBufAddr.u4Size = frame_size;
		frm.rTimeStamp.u4TimeStamp[0] = (VAL_UINT32_T)((uint64_t)i * 1000000 / fps);
		SET_BS();
		if (eVEncDrvEncode(hd, VENC_DRV_START_OPT_ENCODE_FRAME, &frm, &bs, &res) != VENC_DRV_MRESULT_OK ||
		    res.eMessage != VENC_DRV_MESSAGE_OK || bs.u4BSSize > BS_SIZE) {
			fprintf(stderr, "vcodec-enc: frame %u failed\n", i);
			status = 1;
			break;
		}
		hbuf_invalidate(&bsb);
		if (put(bsb.va, bs.u4BSSize, res.fgIsKeyFrm ? 1 : 0))
			break;
	}
	eVEncDrvDeInit(hd);
	eVEncDrvRelease(hd, VENC_DRV_VIDEO_FORMAT_H264);
	return status;
}
