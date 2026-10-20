/*
 * tpipe_test: run MediaTek's MT8163 pass-2 tile driver (tpipe_main_platform in
 * the unit's own /vendor/lib/libdpframework.so) on a configuration given on the
 * command line, and print what it computes: the return code, the descriptor,
 * the tile register map and the tile table (TDR words), decoded by the
 * library's own tile_tdr_isp_fprint_dump.
 *
 *   tpipe_test [-n MAXTILES] [-o DIR] [field=value ...]
 *
 * Fields are the names tile_fprint_platform_config prints (top.cfa_en,
 * sw.src_width, img2o.img2o_stride, ...); see cfg_fields[] for the layout,
 * recovered from that function's disassembly. Defaults: 1600x1200 RAW10 in
 * through IMGI, UNP, CFA, C42 out through IMG2O as YUV422 (scenario 3, the
 * memory-to-memory one; cam_in_fmt 0/1/2 = RAW8/10/12).
 *
 * Built freestanding with arm-none-eabi-gcc against bionic (no NDK
 * toolchain): the few libc functions it needs are declared here.
 */

typedef unsigned int size_t;
typedef struct FILE FILE;

int printf(const char *, ...);
int snprintf(char *, size_t, const char *, ...);
void *calloc(size_t, size_t);
void free(void *);
long strtol(const char *, char **, int);
int strcmp(const char *, const char *);
int strncmp(const char *, const char *, size_t);
char *strchr(const char *, int);
FILE *fopen(const char *, const char *);
size_t fwrite(const void *, size_t, size_t, FILE *);
int fclose(FILE *);
int fflush(FILE *);
int mkdir(const char *, unsigned int);

/* libdpframework.so (C++ linkage, armv7, hence the mangled names). */
int tpipe_main_platform(const void *cfg, void *desc, char *buf, int size)
	__asm__("_Z19tpipe_main_platformPK23ISP_TPIPE_CONFIG_STRUCTP27ISP_TPIPE_DESCRIPTOR_STRUCTPci");
int tile_main_platform(const void *cfg, void *desc, char *buf, int size, const void *mdp, void *hex)
	__asm__("_Z18tile_main_platformPK23ISP_TPIPE_CONFIG_STRUCTP27ISP_TPIPE_DESCRIPTOR_STRUCTPciPK22MDP_TILE_CONFIG_STRUCTP24MDP_TILE_HEX_DUMP_STRUCT");
int tile_main_query_working_buffer_size(void)
	__asm__("_Z35tile_main_query_working_buffer_sizev");
int tpipe_main_query_platform_working_buffer_size(int tiles)
	__asm__("_Z45tpipe_main_query_platform_working_buffer_sizei");
const char *tile_print_isp_error_message(int msg)
	__asm__("_Z28tile_print_isp_error_message22ISP_TPIPE_MESSAGE_ENUM");
void tile_tdr_isp_fprint_dump(FILE *fp, void *map, int tiles, unsigned int *hex,
			      unsigned int *idx, unsigned int max, unsigned int arg7)
	__asm__("_Z24tile_tdr_isp_fprint_dumpP7__sFILEP19TILE_REG_MAP_STRUCTiPjS3_jj");

/* ISP_TPIPE_CONFIG_STRUCT: 82 int32 fields, 0x148 bytes. */
struct field {
	const char *name;
	int off;
};
static const struct field cfg_fields[] = {
	{"top.scenario", 0}, {"top.mode", 4}, {"top.debug_sel", 8}, {"top.pixel_id", 12},
	{"top.cam_in_fmt", 16}, {"top.tcm_load_en", 20}, {"top.ctl_extension_en", 24},
	{"top.rsp_en", 28}, {"top.mdp_crop_en", 32}, {"top.imgi_en", 36}, {"top.lce_en", 40},
	{"top.lcei_en", 44}, {"top.lsci_en", 48}, {"top.unp_en", 52}, {"top.bnr_en", 56},
	{"top.lsc_en", 60}, {"top.sl2_en", 64}, {"top.c24_en", 68}, {"top.cfa_en", 72},
	{"top.c42_en", 76}, {"top.nbc_en", 80}, {"top.seee_en", 84}, {"top.imgo_en", 88},
	{"top.img2o_en", 92}, {"top.cdrz_en", 96}, {"top.mdp_sel", 100},
	{"top.interlace_mode", 104},
	{"sw.log_en", 108}, {"sw.src_width", 112}, {"sw.src_height", 116},
	{"sw.tpipe_irq_mode", 120}, {"sw.tpipe_width", 124}, {"sw.tpipe_height", 128},
	{"imgi.imgi_stride", 132}, {"lcei.lcei_stride", 136}, {"lsci.lsci_stride", 140},
	{"bnr.bpc_en", 144},
	{"lsc.sdblk_width", 148}, {"lsc.sdblk_xnum", 152}, {"lsc.sdblk_last_width", 156},
	{"lsc.sdblk_height", 160}, {"lsc.sdblk_ynum", 164}, {"lsc.sdblk_last_height", 168},
	{"lce.lce_bc_mag_kubnx", 172}, {"lce.lce_slm_width", 176}, {"lce.lce_bc_mag_kubny", 180},
	{"lce.lce_slm_height", 184},
	{"nbc.anr_eny", 188}, {"nbc.anr_enc", 192}, {"nbc.anr_iir_mode", 196},
	{"nbc.anr_scale_mode", 200}, {"seee.se_edge", 204},
	{"imgo.imgo_stride", 208}, {"imgo.imgo_crop_en", 212}, {"imgo.imgo_xoffset", 216},
	{"imgo.imgo_yoffset", 220}, {"imgo.imgo_xsize", 224}, {"imgo.imgo_ysize", 228},
	{"imgo.imgo_mux_en", 232}, {"imgo.imgo_mux", 236},
	{"cdrz.cdrz_input_crop_width", 240}, {"cdrz.cdrz_input_crop_height", 244},
	{"cdrz.cdrz_output_width", 248}, {"cdrz.cdrz_output_height", 252},
	{"cdrz.cdrz_luma_horizontal_integer_offset", 256},
	{"cdrz.cdrz_luma_horizontal_subpixel_offset", 260},
	{"cdrz.cdrz_luma_vertical_integer_offset", 264},
	{"cdrz.cdrz_luma_vertical_subpixel_offset", 268},
	{"cdrz.cdrz_horizontal_luma_algorithm", 272}, {"cdrz.cdrz_vertical_luma_algorithm", 276},
	{"cdrz.cdrz_horizontal_coeff_step", 280}, {"cdrz.cdrz_vertical_coeff_step", 284},
	{"img2o.img2o_stride", 288}, {"img2o.img2o_crop_en", 292}, {"img2o.img2o_xoffset", 296},
	{"img2o.img2o_yoffset", 300}, {"img2o.img2o_xsize", 304}, {"img2o.img2o_ysize", 308},
	{"img2o.img2o_mux_en", 312}, {"img2o.img2o_mux", 316},
	{"cfa.bayer_bypass", 320}, {"sl2.sl2_hrz_comp", 324},
};
#define NFIELDS ((int)(sizeof(cfg_fields) / sizeof(cfg_fields[0])))
#define CFG_BYTES 0x148

static int cfg[1024]; /* generous: the library reads 0x148 bytes */
/* MDP_TILE_CONFIG_STRUCT (0x5c bytes: 23 words, the last a byte), set with mdp.N=value (N a word
 * index); any set runs tile_main_platform with it, the ISP-to-MDP path's tile calculation */
static int mdp[64], use_mdp;
static unsigned int hexdump[1 << 16];

static int set_field(const char *name, int v)
{
	for (int i = 0; i < NFIELDS; i++)
		if (!strcmp(cfg_fields[i].name, name)) {
			cfg[cfg_fields[i].off / 4] = v;
			return 0;
		}
	return -1;
}

static void defaults(void)
{
	set_field("top.scenario", 3); /* ISP_SCENARIO_IP: memory to memory */
	set_field("top.mode", 0);     /* RAW sub-mode */
	set_field("top.cam_in_fmt", 1);
	set_field("top.imgi_en", 1);
	set_field("top.unp_en", 1);
	set_field("top.cfa_en", 1);
	set_field("top.c42_en", 1);
	set_field("top.img2o_en", 1);
	set_field("sw.log_en", 1);
	set_field("sw.src_width", 1600);
	set_field("sw.src_height", 1200);
	set_field("sw.tpipe_width", 768);
	set_field("sw.tpipe_height", 8192);
	set_field("sw.tpipe_irq_mode", 2);
	set_field("imgi.imgi_stride", 2000);
	set_field("img2o.img2o_stride", 3200);
	set_field("img2o.img2o_xsize", 3199);
	set_field("img2o.img2o_ysize", 1199);
	/* IMG2O fed from C42 rather than CDRZ: with mux 0 (CDRZ) and no MDP
	 * path the library fails with OUTPUT_DISABLE_INPUT_FUNC_CHECK_ERROR. */
	set_field("img2o.img2o_mux_en", 1);
	set_field("img2o.img2o_mux", 1);
}

static void write_file(const char *dir, const char *name, const void *p, size_t n)
{
	char path[256];
	snprintf(path, sizeof(path), "%s/%s", dir, name);
	FILE *f = fopen(path, "wb");
	if (!f) {
		printf("cannot write %s\n", path);
		return;
	}
	fwrite(p, 1, n, f);
	fclose(f);
}

int main(int argc, char **argv)
{
	int maxtiles = 64;
	const char *dir = 0;
	int ai = 1;

	defaults();
	for (; ai < argc; ai++) {
		if (!strcmp(argv[ai], "-n") && ai + 1 < argc) {
			maxtiles = (int)strtol(argv[++ai], 0, 0);
		} else if (!strcmp(argv[ai], "-o") && ai + 1 < argc) {
			dir = argv[++ai];
		} else {
			char *eq = strchr(argv[ai], '=');
			char name[96];
			if (eq && !strncmp(argv[ai], "mdp.", 4)) {
				mdp[strtol(argv[ai] + 4, 0, 0) & 63] = (int)strtol(eq + 1, 0, 0);
				use_mdp = 1;
				continue;
			}
			int n = eq ? (int)(eq - argv[ai]) : 0;
			if (!eq || n >= (int)sizeof(name)) {
				printf("bad argument %s\n", argv[ai]);
				return 2;
			}
			for (int i = 0; i < n; i++)
				name[i] = argv[ai][i];
			name[n] = 0;
			if (set_field(name, (int)strtol(eq + 1, 0, 0)) < 0) {
				printf("unknown field %s\n", name);
				return 2;
			}
		}
	}

	int size = tpipe_main_query_platform_working_buffer_size(maxtiles);
	if (tile_main_query_working_buffer_size() > size)
		size = tile_main_query_working_buffer_size();
	char *buf = calloc((size_t)size + 64, 1);
	unsigned int *desc = calloc(64, 4);
	printf("working buffer %d bytes for %d tiles\n", size, maxtiles);
	printf("config:\n");
	for (int i = 0; i < NFIELDS; i++)
		if (cfg[cfg_fields[i].off / 4])
			printf("  [0x%03x] %-44s %d\n", cfg_fields[i].off, cfg_fields[i].name,
			       cfg[cfg_fields[i].off / 4]);
	fflush(0);

	int ret = use_mdp ? tile_main_platform(cfg, desc, buf, size, mdp, hexdump) :
		  tpipe_main_platform(cfg, desc, buf, size);
	printf("tpipe_main_platform = %d (%s)\n", ret, tile_print_isp_error_message(ret));

	printf("descriptor:");
	for (int i = 0; i < 16; i++)
		printf("%s %08x", i % 8 ? "" : "\n ", desc[i]);
	printf("\n");

	/* TILE_REG_MAP_STRUCT: the first 980 bytes of the working buffer. */
	unsigned int *map = (unsigned int *)buf;
	printf("reg map (non-zero words):");
	int k = 0;
	for (int i = 0; i < 980 / 4; i++)
		if (map[i])
			printf("%s [%03x]=%x", k++ % 6 ? "" : "\n ", i * 4, map[i]);
	printf("\n");

	if (dir) {
		mkdir(dir, 0755);
		write_file(dir, "config.bin", cfg, CFG_BYTES);
		write_file(dir, "desc.bin", desc, 64 * 4);
		write_file(dir, "work.bin", buf, (size_t)size);
	}

	/*
	 * ISP_TPIPE_DESCRIPTOR_STRUCT, as tile_main_platform fills it (found by running):
	 *   [0] TDR words (the tile table the ISP reads through TDRI), inside the working buffer
	 *   [1] per-tile records, 6 words each: in_xs, in_xe, in_ys, in_ye, ?, first TDR word
	 *   [2] TDR words used    [3] TDR capacity (36 words a tile)
	 *   [4] TDR words a tile  [5] tiles made    [6] tile capacity
	 *   [7] tiles across      [9] tiles down (guess)
	 */
	unsigned int *tdr = (unsigned int *)desc[0];
	unsigned int *info = (unsigned int *)desc[1];
	unsigned int words = desc[2], tiles = desc[5];
	if (ret == 1 && tdr && info && tiles <= 64) {
		printf("%u tiles, %u TDR words (%u a tile)\n", tiles, words, desc[4]);
		for (unsigned int t = 0; t < tiles; t++) {
			unsigned int *r = info + 6 * t;
			printf("tile %u: in x %u..%u y %u..%u  [%u] tdr@%u:", t, r[0], r[1], r[2], r[3],
			       r[4], r[5]);
			for (unsigned int i = 0; i < desc[4] && r[5] + i < words; i++)
				printf(" %08x", tdr[r[5] + i]);
			printf("\n");
		}
		fflush(0);
		if (dir) {
			write_file(dir, "tdr.bin", tdr, words * 4);
			char path[256];
			snprintf(path, sizeof(path), "%s/tdr.txt", dir);
			FILE *f = fopen(path, "w");
			if (f) {
				unsigned int idx = 0;
				tile_tdr_isp_fprint_dump(f, map, (int)tiles, tdr, &idx, words, desc[4]);
				fclose(f);
			}
		}
	}
	return ret == 1 ? 0 : 1;
}
