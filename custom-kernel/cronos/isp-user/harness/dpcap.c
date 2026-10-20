/*
 * dpcap: run MediaTek's DpFramework (the unit's own /vendor/lib/libdpframework.so) for one MDP job
 * and print the register program it would hand the kernel, without touching any hardware.
 *
 *   dpcap blit SRCFMT W H DSTFMT W H [profile]   a DpBlitStream, memory to memory
 *   dpcap isp TYPE PORT DSTFMT [profile [off=value ...]]
 *                                               a DpIspStream (pass 2 into the MDP; its tile
 *                                               calculation fails for the configurations tried)
 *
 * Formats: YUYV, UYVY, YVYU, I420, YV12, NV12, I422, or a DP_COLOR_ENUM value.
 *
 * /dev/mtk_cmdq is faked here: open() and ioctl() are defined in this executable, and the bionic
 * linker finds the executable's symbols before libc's (it is linked with --export-dynamic). The
 * library asks for the device tree data (QUERY_DTS: the GCE events come back as their own
 * numbers), then submits each job as CMDQ_IOCTL_ASYNC_EXEC, a list of struct op_meta (Amazon's GPL
 * drivers/misc/mediatek/mdp/mdp_def_ex.h): register writes with a mask, polls and event waits, each
 * register named by an engine (the MT8163 ENGBASE_* order of cmdq/v2/mt8163/cmdq_engine.h) and an
 * offset. Those are printed as absolute addresses. Buffers are given as fake MVAs: the source at
 * 0x10000000, the destination planes at 0x20000000, 0x20400000 and 0x20600000.
 *
 * Built freestanding with arm-none-eabi-gcc against bionic, like tpipe_test (tools/build.sh). It
 * runs on the unit (tools/run.sh, PROG=dpcap) or offline under qemu-arm with the vcodec runtime.
 */
typedef unsigned int size_t;
typedef unsigned int u32;
typedef unsigned short u16;
typedef unsigned long long u64;
typedef struct FILE FILE;

int printf(const char *, ...);
int fflush(FILE *);
#define printf(...) (printf(__VA_ARGS__), fflush(0))	/* nothing lost if the library crashes */
void *memset(void *, int, size_t);
long strtol(const char *, char **, int);
unsigned long strtoul(const char *, char **, int);
int strcmp(const char *, const char *);
char *strstr(const char *, const char *);
long syscall(long, ...);
int *__errno(void);
char *getenv(const char *);

#define NR_OPENAT 322
#define NR_IOCTL 54
#define AT_FDCWD (-100)
#define FAKE_CMDQ 900

/* ENGBASE_MMSYS_CONFIG ... ENGBASE_ISP_CAMSYS (cmdq/v2/mt8163/mdp_base.h) */
static const u32 engine_base[] = {
	0x14000000, 0x14001000, 0x14002000, 0x14003000, 0x14004000,
	0x14005000, 0x14006000, 0x14015000, 0x15000000, 0x15004000,
};

/* enum CMDQ_META_OP */
static const char *const op_names[] = {
	"W", "R", "P", "E", "EN", "EC", "ES", "EA", "WFD", "WREG", "WSEC", "NOP",
};

static void dump_submit(const unsigned char *arg)
{
	const unsigned char *m = (const unsigned char *)*(const u32 *)arg;
	u32 n = *(const u32 *)(arg + 8), i;

	printf("=== submit: %u ops, engines %#llx\n", n, *(const u64 *)(arg + 16));
	for (i = 0; i < n; i++, m += 16) {	/* struct op_meta: 16 bytes */
		u32 op = m[0], eng = *(const u16 *)(m + 2), off = *(const u16 *)(m + 4);
		u32 val = *(const u32 *)(m + 8), mask = *(const u32 *)(m + 12);
		u32 addr = (eng < sizeof(engine_base) / 4 ? engine_base[eng] : 0xee000000) + off;

		if (op >= 3 && op <= 7)
			printf("%-4s event %u\n", op_names[op], off);
		else if (op < sizeof(op_names) / sizeof(op_names[0]))
			printf("%-4s %08x %08x mask %08x\n", op_names[op], addr, val, mask);
		else
			printf("?%u engine %u offset %x value %x mask %x\n", op, eng, off, val, mask);
	}
}

__attribute__((visibility("default"))) int open(const char *path, int flags, ...)
{
	__builtin_va_list ap;
	int mode;

	__builtin_va_start(ap, flags);
	mode = __builtin_va_arg(ap, int);
	__builtin_va_end(ap);
	if (strstr(path, "mtk_cmdq"))
		return FAKE_CMDQ;
	if (strstr(path, "mdp_sync") || strstr(path, "mtk_mira") || strstr(path, "sw_sync")) {
		*__errno() = 2;
		return -1;
	}
	return syscall(NR_OPENAT, AT_FDCWD, path, flags, mode);	/* errno set by syscall() */
}

__attribute__((visibility("default"))) int __open_2(const char *path, int flags)
{
	return open(path, flags, 0);
}

__attribute__((visibility("default"))) int ioctl(int fd, int req, ...)
{
	__builtin_va_list ap;
	unsigned char *arg;
	u32 nr = req & 0xff, size = ((u32)req >> 16) & 0x3fff, i;

	__builtin_va_start(ap, req);
	arg = __builtin_va_arg(ap, unsigned char *);
	__builtin_va_end(ap);
	if (fd != FAKE_CMDQ)
		return syscall(NR_IOCTL, fd, req, arg);
	switch (nr) {
	case 11:	/* CMDQ_IOCTL_QUERY_DTS: each event its own number; the mutex base last */
		memset(arg, 0, size);
		for (i = 0; i < 511; i++)
			((int *)arg)[i] = i;
		*(u32 *)(arg + size - 4) = 0x14015000;
		return 0;
	case 10:	/* CMDQ_IOCTL_QUERY_CAP_BITS: WFE supported */
		*(u32 *)arg = 1;
		return 0;
	case 20:	/* CMDQ_IOCTL_ASYNC_EXEC, struct mdp_submit */
		dump_submit(arg);
		return 0;
	default:	/* the wait (21) and the rest: done, nothing to read back */
		printf("[dpcap] cmdq ioctl %u, %u bytes\n", nr, size);
		memset(arg, 0, size);
		return 0;
	}
}

/* libdpframework.so (C++, armv7: the mangled names) */
void blit_ctor(void *) __asm__("_ZN12DpBlitStreamC1Ev");
int blit_src_buf(void *, void **va, void **mva, u32 *size, u32 planes)
	__asm__("_ZN12DpBlitStream12setSrcBufferEPPvS1_Pjj");
int blit_dst_buf(void *, void **va, void **mva, u32 *size, u32 planes)
	__asm__("_ZN12DpBlitStream12setDstBufferEPPvS1_Pjj");
int blit_src_cfg(void *, int w, int h, int yp, int uvp, u32 fmt, int profile, int field,
		 void *roi, int secure, int flush)
	__asm__("_ZN12DpBlitStream12setSrcConfigEiiii13DP_COLOR_ENUM15DP_PROFILE_ENUM17DpInterlaceFormatP6DpRect8DpSecureb");
int blit_dst_cfg(void *, int w, int h, int yp, int uvp, u32 fmt, int profile, int field,
		 void *roi, int secure, int flush)
	__asm__("_ZN12DpBlitStream12setDstConfigEiiii13DP_COLOR_ENUM15DP_PROFILE_ENUM17DpInterlaceFormatP6DpRect8DpSecureb");
int blit_orient(void *, u32) __asm__("_ZN12DpBlitStream14setOrientationEj");
int blit_go(void *, void *) __asm__("_ZN12DpBlitStream10invalidateEP7timeval");

void isp_ctor(void *, int type) __asm__("_ZN11DpIspStreamC1ENS_13ISPStreamTypeE");
int isp_src_cfg(void *, u32 fmt, int w, int h, int pitch, int sec)
	__asm__("_ZN11DpIspStream12setSrcConfigE13DP_COLOR_ENUMiiib");
int isp_src_buf(void *, u32 *mva, u32 *size, int planes) __asm__("_ZN11DpIspStream14queueSrcBufferEPjS0_i");
int isp_src_crop(void *, int x, int xs, int y, int ys, int w, int h)
	__asm__("_ZN11DpIspStream10setSrcCropEiiiiii");
int isp_dst_cfg(void *, int port, int w, int h, int yp, int uvp, u32 fmt, int profile, int field,
		void *roi, int sec, int secure)
	__asm__("_ZN11DpIspStream12setDstConfigEiiiii13DP_COLOR_ENUM15DP_PROFILE_ENUM17DpInterlaceFormatP6DpRectb8DpSecure");
int isp_dst_buf(void *, int port, u32 *mva, u32 *size, int planes) __asm__("_ZN11DpIspStream14queueDstBufferEiPjS0_i");
int isp_rot(void *, int port, int deg) __asm__("_ZN11DpIspStream11setRotationEii");
int isp_flip(void *, int port, int flip) __asm__("_ZN11DpIspStream13setFlipStatusEib");
int isp_param(void *, int *cfg, u32 tdri) __asm__("_ZN11DpIspStream12setParameterER23ISP_TPIPE_CONFIG_STRUCTj");
int isp_start(void *, void *) __asm__("_ZN11DpIspStream11startStreamEP7timeval");

/* DP_COLOR_ENUM: plane count [22:21], 4:2:x subsampling [19]/[18], bits [17:8], id [4:0] */
static u32 fmt_of(const char *s)
{
	if (!strcmp(s, "YUYV")) return 0x00281045;
	if (!strcmp(s, "UYVY")) return 0x00281044;
	if (!strcmp(s, "YVYU")) return 0x00281065;
	if (!strcmp(s, "I420")) return 0x006c0848;
	if (!strcmp(s, "YV12")) return 0x006c0868;
	if (!strcmp(s, "NV12")) return 0x004c084c;
	if (!strcmp(s, "I422")) return 0x00680849;
	return strtoul(s, 0, 0);
}

static void planes_of(u32 fmt, int w, int h, int *n, int *yp, int *uvp, u32 *size)
{
	int planes = (fmt >> 21) & 3, bits = (fmt >> 8) & 0x3ff, hf = (fmt >> 19) & 1,
	    vf = (fmt >> 18) & 1;

	*n = planes;
	*yp = w * bits / 8;
	*uvp = planes > 1 ? (planes == 2 ? *yp : *yp >> hf) : 0;
	size[0] = *yp * h;
	size[1] = planes > 1 ? *uvp * (h >> vf) : 0;
	size[2] = planes > 2 ? size[1] : 0;
}

static int blit_main(char **argv, int argc)
{
	static unsigned char obj[65536], buf[16];
	void *sva[3] = { buf, buf, buf }, *dva[3] = { buf, buf, buf };
	void *smva[3] = { (void *)0x10000000, (void *)0x10400000, (void *)0x10600000 };
	void *dmva[3] = { (void *)0x20000000, (void *)0x20400000, (void *)0x20600000 };
	u32 ssz[3], dsz[3], sf = fmt_of(argv[2]), df = fmt_of(argv[5]);
	int sw = strtol(argv[3], 0, 0), sh = strtol(argv[4], 0, 0);
	int dw = strtol(argv[6], 0, 0), dh = strtol(argv[7], 0, 0);
	int profile = argc > 8 ? strtol(argv[8], 0, 0) : 0;
	int sn, dn, syp, suvp, dyp, duvp;

	planes_of(sf, sw, sh, &sn, &syp, &suvp, ssz);
	planes_of(df, dw, dh, &dn, &dyp, &duvp, dsz);
	blit_ctor(obj);
	printf("setSrcBuffer %d\n", blit_src_buf(obj, sva, smva, ssz, sn));
	printf("setSrcConfig %d\n", blit_src_cfg(obj, sw, sh, syp, suvp, sf, profile, 0, 0, 0, 1));
	printf("setDstBuffer %d\n", blit_dst_buf(obj, dva, dmva, dsz, dn));
	printf("setDstConfig %d\n", blit_dst_cfg(obj, dw, dh, dyp, duvp, df, profile, 0, 0, 0, 1));
	blit_orient(obj, 0);
	printf("invalidate %d\n", blit_go(obj, 0));
	return 0;
}

/*
 * ISP_TPIPE_CONFIG_STRUCT (tpipe_test.c's cfg_fields), by byte offset; the stream copies 0x170. The
 * 0x28 bytes past the tile driver's 0x148 are DpWrapper_ISP's (found in its onConfigFrame,
 * initTilePath and onConfigTile): 0x150 the ISP command queue's base, written to CQ1/2/3's base
 * register (0x0ac/0x0b0/0x0b4) as 0x168 says (0/1/2); 0x15c the tile table's address for the ISP,
 * written to TDRI's base (0x204) for each tile; 0x160 where the library writes the tile table (a
 * pointer of ours); 0x16c a flag; 0x148 kept by initTilePath.
 */
static int icfg[1024];
static u32 tdr[1 << 18];

static int isp_main(char **argv, int argc)
{
	static unsigned char obj[1 << 20];
	u32 smva[3] = { 0x10000000 }, ssz[3] = { 2400000 };
	u32 dmva[3] = { 0x20000000, 0x20400000, 0x20600000 }, dsz[3] = { 1920000, 480000, 480000 };
	/* DPCAP_SIZE=WxH: the picture's size (the RAW10 frame keeps its 2000-byte lines) */
	int W = 1600, H = 1200;
	char *sz = getenv("DPCAP_SIZE");

	if (sz) {
		W = strtol(sz, &sz, 10);
		H = strtol(sz + 1, 0, 10);
	}
	int type = strtol(argv[2], 0, 0), port = strtol(argv[3], 0, 0);
	u32 df = fmt_of(argv[4]);
	int planes = (df >> 21) & 3, i;

	/* RAW10 WxH in, demosaiced, CDRZ 1:1, the resampler and the MDP crop out to the MDP */
	icfg[0] = 3; icfg[3] = 3; icfg[4] = 1; icfg[8] = 1; icfg[9] = 1; icfg[13] = 1;
	icfg[18] = 1; icfg[19] = 1; icfg[24] = 1;
	icfg[7] = 1; icfg[27] = 1; icfg[28] = W; icfg[29] = H; icfg[30] = 2; icfg[31] = 768;
	icfg[32] = 8192; icfg[33] = 2000;
	icfg[60] = W; icfg[61] = H; icfg[62] = W; icfg[63] = H;
	icfg[0x150 / 4] = 0x31000000;
	icfg[0x15c / 4] = 0x30000000;
	icfg[0x160 / 4] = (int)tdr;
	for (i = 6; i < argc; i++) {
		char *eq = strstr(argv[i], "=");

		if (eq)
			icfg[strtol(argv[i], 0, 0) / 4] = strtol(eq + 1, 0, 0);
	}
	isp_ctor(obj, type);
	printf("setSrcConfig %d\n", isp_src_cfg(obj, 0x00200a94, W, H, 2000, 0));	/* BAYER10 */
	printf("queueSrcBuffer %d\n", isp_src_buf(obj, smva, ssz, 1));
	printf("setSrcCrop %d\n", isp_src_crop(obj, 0, 0, 0, 0, W, H));
	printf("setDstConfig %d\n", isp_dst_cfg(obj, port, W, H, W,
		planes == 3 ? W / 2 : planes == 2 ? W : 0, df,
		argc > 5 ? strtol(argv[5], 0, 0) : 0, 0, 0, 0, 0));
	isp_rot(obj, port, getenv("DPCAP_ROT") ? strtol(getenv("DPCAP_ROT"), 0, 0) : 0);
	isp_flip(obj, port, 0);
	printf("queueDstBuffer %d\n", isp_dst_buf(obj, port, dmva, dsz, planes));
	printf("setParameter %d\n", isp_param(obj, icfg, 0x30000000));
	printf("startStream %d\n", isp_start(obj, 0));
	for (i = sizeof(tdr) / 4; i > 0 && !tdr[i - 1]; i--)
		;
	printf("=== tile table: %d words", i);
	for (int k = 0; k < i; k++)
		printf("%s%08x", k % 8 ? " " : "\n", tdr[k]);
	printf("\n");
	return 0;
}

int main(int argc, char **argv)
{
	if (argc >= 8 && !strcmp(argv[1], "blit"))
		return blit_main(argv, argc);
	if (argc >= 5 && !strcmp(argv[1], "isp"))
		return isp_main(argv, argc);
	printf("usage: dpcap blit SRCFMT W H DSTFMT W H [profile]\n"
	       "       dpcap isp TYPE PORT DSTFMT [profile [offset=value ...]]\n");
	return 2;
}
