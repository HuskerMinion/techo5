/*
 * venc_test: encode raw YUV frames to an H.264 Annex-B stream with the MT8163
 * hardware encoder through libvcodecdrv's driver API (no OMX), and report
 * per-frame timing.
 *
 *   venc_test -s WxH [options] -o out.h264 [in.yuv]
 *     -s WxH    picture size (required)
 *     -f FMT    input layout: nv12 (default), i420 or yv12. Frames are tightly
 *               packed (stride = width); the planes are copied into a
 *               16-aligned stride/height buffer as the HW requires.
 *     -b BPS    bitrate in bit/s (default 4000000)
 *     -r FPS    frame rate (default 30)
 *     -g N      I-frame period in frames (default 30)
 *     -P PROF   baseline | main | high (default main)
 *     -n N      number of frames (default: all frames in the file, or 60
 *               generated ones)
 *     -H        repeat SPS/PPS before every IDR
 *     -q        quiet
 *   Without in.yuv, a moving test pattern is generated.
 *
 * Sequence (include/venc_drv_if_public.h, from the vendor OMX):
 *   eVEncDrvCreate(&h, H264) -> SetParam(RECORD_SIZE) -> eVEncDrvInit(h)
 *   -> SetParam(PARAM_ENC, &enc{fmt, profile, level, w, h, bufw, bufh, extra})
 *   -> eVEncDrvEncode(h, SEQUENCE_HEADER, NULL, &bs, &res)   (SPS+PPS)
 *   -> per frame: eVEncDrvEncode(h, ENCODE_FRAME, &frm, &bs, &res)
 *   -> SetParam(STATISTIC_OFF), eVEncDrvDeInit, eVEncDrvRelease(h, H264)
 * Only the device addresses ("PA") are given to the HW. The chroma planes are
 * found from the luma address, bufW and bufH, so the frame has to be one
 * contiguous buffer.
 */
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>

#include "common.h"
#include "venc_drv_if_public.h"

#define ALIGN(x, a) (((x) + (a) - 1) & ~((a) - 1))
#define BS_SIZE 0x180000 /* vendor OMX default output buffer; multiple of 128 */

static void pattern(uint8_t *y, uint8_t *c, uint32_t w, uint32_t h, uint32_t stride, int nv12,
		    uint8_t *u, uint8_t *v, uint32_t cstride, int frame)
{
	for (uint32_t j = 0; j < h; j++)
		for (uint32_t i = 0; i < w; i++)
			y[j * stride + i] = (uint8_t)(((i + frame * 4) ^ (j + frame * 2)) & 0xff);
	for (uint32_t j = 0; j < h / 2; j++)
		for (uint32_t i = 0; i < w / 2; i++) {
			uint8_t cb = (uint8_t)(128 + 60 * ((i * 8 / w) & 1)), cr = (uint8_t)(64 + (j * 128 / h));
			if (nv12) {
				c[j * stride + 2 * i] = cb;
				c[j * stride + 2 * i + 1] = cr;
			} else {
				u[j * cstride + i] = cb;
				v[j * cstride + i] = cr;
			}
		}
}

int main(int argc, char **argv)
{
	uint32_t w = 0, h = 0, bitrate = 4000000, fps = 30, gop = 30, prof = VENC_DRV_H264_VIDEO_PROFILE_MAIN;
	const char *fmt = "nv12", *outpath = NULL;
	int nframes = 0, quiet = 0, prepend = 0, opt;
	while ((opt = getopt(argc, argv, "s:f:b:r:g:P:n:o:Hq")) != -1) {
		switch (opt) {
		case 's': if (sscanf(optarg, "%ux%u", &w, &h) != 2) goto usage; break;
		case 'f': fmt = optarg; break;
		case 'b': bitrate = (uint32_t)strtoul(optarg, NULL, 0); break;
		case 'r': fps = (uint32_t)atoi(optarg); break;
		case 'g': gop = (uint32_t)atoi(optarg); break;
		case 'P':
			prof = !strcmp(optarg, "baseline") ? VENC_DRV_H264_VIDEO_PROFILE_BASELINE :
			       !strcmp(optarg, "high") ? VENC_DRV_H264_VIDEO_PROFILE_HIGH :
			       VENC_DRV_H264_VIDEO_PROFILE_MAIN;
			break;
		case 'n': nframes = atoi(optarg); break;
		case 'o': outpath = optarg; break;
		case 'H': prepend = 1; break;
		case 'q': quiet = 1; break;
		default: goto usage;
		}
	}
	int nv12 = !strcmp(fmt, "nv12"), yv12 = !strcmp(fmt, "yv12"), i420 = !strcmp(fmt, "i420");
	if (!w || !h || !outpath || !(nv12 || yv12 || i420) || (w & 1) || (h & 1)) {
usage:
		fprintf(stderr, "usage: %s -s WxH [-f nv12|i420|yv12] [-b bps] [-r fps] [-g gop] "
			"[-P baseline|main|high] [-n frames] [-H] [-q] -o out.h264 [in.yuv]\n", argv[0]);
		return 2;
	}
	const char *inpath = optind < argc ? argv[optind] : NULL;
	FILE *in = NULL;
	size_t in_frame = (size_t)w * h * 3 / 2;
	if (inpath) {
		if (!(in = fopen(inpath, "rb"))) { perror(inpath); return 1; }
		fseek(in, 0, SEEK_END);
		long sz = ftell(in);
		fseek(in, 0, SEEK_SET);
		if (!nframes || (long)(nframes * in_frame) > sz) nframes = (int)(sz / in_frame);
	} else if (!nframes) {
		nframes = 60;
	}
	FILE *out = fopen(outpath, "wb");
	if (!out) { perror(outpath); return 1; }

	uint32_t bw = ALIGN(w, 16), bh = ALIGN(h, 16);
	uint32_t cstride = yv12 ? ALIGN(bw / 2, 16) : bw / 2; /* planar chroma stride */
	uint32_t frame_size = nv12 ? bw * bh * 3 / 2 : bw * bh + 2 * cstride * (bh / 2);
	hbuf_t fb, bsb;
	if (hbuf_alloc(&fb, ALIGN(frame_size, 4096), 512, VAL_MEM_CODEC_FOR_VENC) ||
	    hbuf_alloc(&bsb, BS_SIZE, 512, VAL_MEM_CODEC_FOR_VENC))
		return 1;
	uint8_t *Y = fb.va, *C = fb.va + bw * bh;
	uint8_t *P2 = fb.va + bw * bh, *P3 = P2 + cstride * (bh / 2); /* I420: U,V  YV12: V,U */
	uint8_t *U = yv12 ? P3 : P2, *V = yv12 ? P2 : P3;
	memset(fb.va, 0x80, frame_size);

	VAL_HANDLE_T hd = NULL;
	VENC_DRV_MRESULT_T r = eVEncDrvCreate(&hd, VENC_DRV_VIDEO_FORMAT_H264);
	if (r != VENC_DRV_MRESULT_OK || !hd) { fprintf(stderr, "eVEncDrvCreate failed: %d\n", r); return 1; }
	VAL_RECORD_SIZE_T rs = { w, h, bw, bh };
	eVEncDrvSetParam(hd, VENC_DRV_SET_TYPE_RECORD_SIZE, &rs, NULL); /* EMI hint; failure is harmless */
	double t0 = now_ms();
	r = eVEncDrvInit(hd);
	if (r != VENC_DRV_MRESULT_OK) { fprintf(stderr, "eVEncDrvInit failed: %d\n", r); return 1; }

	static VENC_DRV_PARAM_ENC_EXTRA_T extra;
	static VENC_DRV_PARAM_ENC_T enc;
	memset(&extra, 0, sizeof(extra));
	memset(&enc, 0, sizeof(enc));
	extra.u4IntraFrameRate = gop ? gop : 1;
	extra.u4BitRate = bitrate;
	extra.u4FrameRateQ16 = fps << 16;
	extra.u4BitrateMode = 1; /* CBR, as the OMX does */
	enc.eVEncFormat = nv12 ? VENC_DRV_YUV_FORMAT_NV12 : yv12 ? VENC_DRV_YUV_FORMAT_YV12 : VENC_DRV_YUV_FORMAT_420;
	enc.u4Profile = prof;
	enc.u4Level = (w * h > 1280 * 720) ? VENC_DRV_VIDEO_LEVEL_4 : VENC_DRV_VIDEO_LEVEL_3_1;
	enc.u4Width = w;
	enc.u4Height = h;
	enc.u4BufWidth = bw;
	enc.u4BufHeight = bh;
	enc.u4NumPFrm = extra.u4IntraFrameRate - 1;
	enc.eFrameRate = fps;
	enc.pvExtraEnc = &extra;
	r = eVEncDrvSetParam(hd, VENC_DRV_SET_TYPE_PARAM_ENC, &enc, NULL);
	if (r != VENC_DRV_MRESULT_OK) { fprintf(stderr, "SetParam(PARAM_ENC) failed: %d\n", r); return 1; }
	eVEncDrvSetParam(hd, VENC_DRV_SET_TYPE_STATISTIC_ON, NULL, NULL);
	if (prepend) {
		VAL_UINT32_T one = 1;
		eVEncDrvSetParam(hd, VENC_DRV_SET_TYPE_PREPEND_HEADER, &one, NULL);
	}
	printf("venc_test: init ok (%.1f ms): %ux%u (buffer %ux%u) %s, %u bps, %u fps, gop %u, profile 0x%x level %u\n",
	       now_ms() - t0, w, h, bw, bh, fmt, bitrate, fps, gop, prof, enc.u4Level);

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
	r = eVEncDrvEncode(hd, VENC_DRV_START_OPT_ENCODE_SEQUENCE_HEADER, NULL, &bs, &res);
	if (r != VENC_DRV_MRESULT_OK || !bs.u4BSSize || bs.u4BSSize > BS_SIZE) {
		fprintf(stderr, "sequence header failed: %d (size %lu)\n", r, (unsigned long)bs.u4BSSize);
		return 1;
	}
	hbuf_invalidate(&bsb);
	fwrite(bsb.va, 1, bs.u4BSSize, out);
	printf("venc_test: SPS/PPS %lu bytes\n", (unsigned long)bs.u4BSSize);

	uint8_t *rawbuf = in ? malloc(in_frame) : NULL;
	double esum = 0, emin = 1e9, emax = 0, tw0 = now_ms();
	uint64_t total = bs.u4BSSize;
	int n_enc = 0, n_key = 0;
	for (int i = 0; i < nframes; i++) {
		if (in) {
			if (fread(rawbuf, 1, in_frame, in) != in_frame) break;
			const uint8_t *sy = rawbuf, *sc = rawbuf + (size_t)w * h;
			for (uint32_t j = 0; j < h; j++) memcpy(Y + j * bw, sy + j * w, w);
			if (nv12) {
				for (uint32_t j = 0; j < h / 2; j++) memcpy(C + j * bw, sc + j * w, w);
			} else {
				const uint8_t *su = sc, *sv = sc + (size_t)(w / 2) * (h / 2);
				if (yv12) { const uint8_t *t = su; su = sv; sv = t; } /* file is Y,V,U */
				for (uint32_t j = 0; j < h / 2; j++) {
					memcpy(U + j * cstride, su + j * (w / 2), w / 2);
					memcpy(V + j * cstride, sv + j * (w / 2), w / 2);
				}
			}
		} else {
			pattern(Y, C, w, h, bw, nv12, U, V, cstride, i);
		}
		hbuf_flush(&fb);
		memset(&frm, 0, sizeof(frm));
		frm.rFrmBufAddr.u4VA = (VAL_ULONG_T)(uintptr_t)fb.va;
		frm.rFrmBufAddr.u4PA = fb.pa;
		frm.rFrmBufAddr.u4Size = frame_size;
		frm.rTimeStamp.u4TimeStamp[0] = (VAL_UINT32_T)((uint64_t)i * 1000000 / fps);
		SET_BS();
		double ts = now_ms();
		r = eVEncDrvEncode(hd, VENC_DRV_START_OPT_ENCODE_FRAME, &frm, &bs, &res);
		double dt = now_ms() - ts;
		if (r != VENC_DRV_MRESULT_OK || res.eMessage != VENC_DRV_MESSAGE_OK ||
		    bs.u4BSSize > BS_SIZE) {
			fprintf(stderr, "frame %d: encode failed: ret %d msg %d size %lu\n", i, r,
				res.eMessage, (unsigned long)bs.u4BSSize);
			break;
		}
		hbuf_invalidate(&bsb);
		fwrite(bsb.va, 1, bs.u4BSSize, out);
		total += bs.u4BSSize;
		n_enc++;
		n_key += res.fgIsKeyFrm != 0;
		esum += dt;
		if (dt < emin) emin = dt;
		if (dt > emax) emax = dt;
		if (!quiet)
			printf("frame %4d %s %7lu B: encode %6.2f ms\n", i, res.fgIsKeyFrm ? "IDR" : "   ",
			       (unsigned long)bs.u4BSSize, dt);
	}
	double twall = now_ms() - tw0;
	if (n_enc)
		printf("venc_test: %d frames (%d key), %llu bytes (%.0f kbit/s at %u fps); Encode avg %.2f ms, "
		       "min %.2f, max %.2f -> %.1f fps; wall %.1f ms\n", n_enc, n_key,
		       (unsigned long long)total, total * 8.0 * fps / n_enc / 1000.0, fps,
		       esum / n_enc, emin, emax, 1000.0 * n_enc / esum, twall);

	eVEncDrvSetParam(hd, VENC_DRV_SET_TYPE_STATISTIC_OFF, NULL, NULL);
	eVEncDrvDeInit(hd);
	eVEncDrvRelease(hd, VENC_DRV_VIDEO_FORMAT_H264);
	hbuf_free(&fb);
	hbuf_free(&bsb);
	fclose(out);
	if (in) fclose(in);
	free(rawbuf);
	return n_enc ? 0 : 1;
}
