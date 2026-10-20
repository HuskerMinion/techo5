# MT8183 DIP (ISP 5.0 pass 2): filling `tuning_data`

How to make the SCP's DIP service (linux-firmware `mediatek/mt8183/scp.img`, kukui_scp v2.0.13324)
apply black level (OBC2), white balance gains (PGN), a color matrix (G2G) and a gamma curve (GGM)
when `mtk-mdp3-dip.c` hands it a RAW10 BGGR frame from the OV13855. Researched 2026-10-18 from the
sources listed at the end and from a disassembly of the firmware. Nothing here has been tried on a
unit yet.

## Summary

1. **The tuning buffer is an image of the DIP's register space.** Byte offset N in the buffer holds
   the value for register `0x15022000 + N`. The firmware does not copy it anywhere: it builds the
   DIP command queue (CQ) *inside the tuning buffer* at offset `0x5580`, and points the CQ at the
   buffer's IOVA, so the DIP's CQ DMA loads each enabled module's registers straight from the
   buffer. Usable register area: `0x0000..0x557f` (the DIP register range is `0x6000`). The
   firmware also overwrites the CTL, DMA and size registers in it on every frame.
2. **`tuning_data.present` must be non-zero.** The firmware ORs the 8 bytes at frame offset `0x27c`.
   If they are zero it ignores our values: it sets its "tuning" pointer to NULL, writes its own
   defaults (G2G identity, linear GGM, UDM and G2C defaults) into the same buffer, and turns OBC2,
   PGN and the other tuned blocks off. The driver never sets `present` today, which is why the
   picture comes out dark, with no white balance and no gamma.
3. **When `present` is set, the firmware writes no defaults at all.** Every block that ends up
   enabled takes its registers as we leave them, and that includes the blocks the firmware enables
   on its own: UDM (demosaic) is always on. Each enable bit is owned either by the firmware or by
   the tuning buffer; the firmware merges the two with fixed masks. So the buffer has to supply
   UDM's and G2C's default registers, and turn G2C (RGB to YUV), G2G and GGM back on itself.
4. Layout used by the ChromeOS camera stack (and expected by the firmware):

   | Offset | Contents |
   | --- | --- |
   | `0x0000` | register image (the HAL reserves 28 KB) |
   | `0x5580..0x5887` | **written by the firmware**: the CQ descriptor list (97 entries of 8 bytes, plus the end marker) |
   | `0x7000` | `DmaConfigHeader` for IMGCI (the LSC table), 40 bytes; `enable` = byte 0 |
   | `0x7028` | `DmaConfigHeader` for LCEI (the LCE map) |
   | `0x7800` | LSC table data (IMGCI reads it from `tuning_iova + 0x7800`) |
   | `0x20000` | LCE data (LCEI reads it from `tuning_iova + 0x20000`) |

   The ChromeOS V4L2 meta buffer is 416 KB (`fw_param_fmts`, 1024 * (128 + 288)). Our 32 KB
   (`DIP_TUNING_SZ`) covers the register image and the two headers, which must stay zero (no LSC,
   no LCE). LSC would need the buffer to reach past `0x7800 + table size`.

## Firmware evidence (scp.img, SRAM segment loaded at 0)

The disassembly is `arm-none-eabi-objdump -b binary -marm -Mforce-thumb` of the SRAM segment (file
offset 0x10000). All the addresses below are SRAM addresses. The DIP state block is a global at
`0x5a600` (call it `g`).

### Frame intake (`0x35db8`, the IMG_IPI_FRAME handler)

The offsets of `struct img_ipi_frameparam` as the firmware reads them: inputs at `+0x28` (60 bytes
each), outputs at `+0xdc` (104 bytes each), `tuning_data` at `+0x27c` (present u64), `+0x284` (pa),
`+0x288` (iova), `subfrm_data` pa/iova at `+0x294`/`+0x298`, `config_data.pa` at `+0x2a4`. These
match `mtk-img-ipi.h` (the ChromeOS 5.10 layout).

```
35e32..35e50  r0 = scp_pa_to_va(frame->tuning_data.pa)            ; bl 0xb62
35e54         r7 = va + 0x5580
35e5a         g->cq_desc_va = va + 0x5580                        ; [g+0xb34]
35e5e         g->reg_image  = va                                 ; [g+0x3c4], whatever present says
35e62..35e9e  if (frame->tuning_data.present == 0) va = 0
35ea2         g->tuning     = va                                 ; [g+0xb2c], NULL when not present
35ef0..35f0e  g->tuning_iova = frame->tuning_data.iova           ; [g+0xb30]
35f12..35f42  writes 96 CQ descriptors {0x04004184, 0} from va+0x5580, then the end marker
              0x1c000000 at va+0x5880
35f46         reg_image[0x208] = tuning_iova + 0x5580            ; CQ thread 0 base address
35f50..35f74  copies the 40-byte headers at va+0x7000 and va+0x7028; when enable is set, IMGCI
              address = tuning_iova + 0x7800 and LCEI address = tuning_iova + 0x20000
360f0..36108  LSC2 needs g->tuning: reads tuning[0x1344] and [0x1348] bits 17:13; otherwise logs
              "Cannot enable LSC2 without tuning data"
```

`0x380c0`: if `g->tuning` is set, `g->has_tuning` (`[g+0xb28]`) = 1, and the CTL words are read from
the tuning image: `[0x4]` YUV_EN, `[0x8]` YUV2_EN, `[0xc]` RGB_EN, `[0xe8]` RGB2_EN, `[0x14]` bit 31
and `[0x1c]` bit 24 (two flags). They are merged with the firmware's own enables using these masks
(the literals at `0x3837c..0x38384`):

| Register | Offset | Bits taken from the firmware | Bits taken from tuning |
| --- | --- | --- | --- |
| CTL_YUV_EN | `0x004` | `0xffef8017` | `0x00107fe8` |
| CTL_YUV2_EN | `0x008` | the rest | `0x000004f0` |
| CTL_RGB_EN | `0x00c` | `0xe6230313` | `0x19dcfcec` |
| CTL_RGB2_EN | `0x0e8` | none | `0x0000000e` |

Without tuning, the firmware's own RGB_EN is `0xd2` (UFD, UDM, G2G, GGM; the literal at `0x36d2c`
is `0xd2`, or `0x12` when tuning is present), and it sets YUV_EN bit 3 (G2C) itself. The
no-tuning path (`0x38542`) also sets the `[0x14]` bit 31 flag to 1 and the `[0x1c]` bit 24 flag
to 0.

`0x1e298` writes the merged words into the image (`+0x4, +0x8, +0xc, +0xe8, +0x10, +0x14, +0x18,
+0x1c`) and calls one configuration function per enabled bit, from the table at `0x3f58c`. The
iterator at `0x19ac` adds one CQ entry per enabled bit from the same table. Each configuration
function is either "copy N registers from `g->tuning` into `g->reg_image`" (a no-op, since both are
our buffer) or, when `has_tuning` is 0, "write defaults".

### CQ module table (`0x3f100`, `{id, offset, count}`) joined with the enable table (`0x3f58c`, `{mask, id, fn}`)

The names come from matching the register counts and order against ISP 3.0 (`DIP_X_*`), from the
tile driver's module name list in the firmware (`0x4d2b9`: UNP, UFDG, ADBS2, RMG2, BNR2, RMM2,
DCPN2, LSC2, PGN, RCP2, RNR, UDM, FLC, GDR2, G2G, FLC2, LCE, GGM, ..., G2C, NBC, NBC2, SEEE, PCA,
NR3D), and from the firmware's default writers. Rows marked "confirmed" are backed by firmware code
that reads or writes them with that meaning.

**CTL_RGB_EN (`0x00c`)**

| Bit | Block | Registers | Count | Owner | Notes |
| --- | --- | --- | --- | --- | --- |
| 0 | UNP | `0x1000` | 1 | firmware | firmware writes 0 |
| 1 | UFD | `0x1040` | 8 | firmware | |
| **2** | **PGN** | **`0x1440`** | 6 | tuning | no default |
| 3 | SL2 | `0x15c0` | 12 | tuning | |
| **4** | **UDM** (demosaic) | **`0x1540`** | 23 | firmware | always on; defaults only without tuning, **so we must supply them** |
| 5 | LCE | `0x2100` | 29 | tuning | needs LCEI |
| **6** | **G2G** (CCM) | **`0x2080`** | 8 | tuning | confirmed: the default sets `[0xc] \|= 0x40` and an identity matrix |
| **7** | **GGM** | **`0x2180`** | 193 | tuning | confirmed: the default sets `[0xc] \|= 0x80` and a 192-entry LUT plus CTRL |
| 10 | DBS2 | `0x1140` | 13 | tuning | |
| **11** | **OBC2** | **`0x11c0`** | 8 | tuning | no default |
| 12 | RMG2 | `0x1200` | 3 | tuning | |
| 13 | BNR2 (bad pixels, Bayer NR) | `0x1240` | 32 | tuning | |
| 14 | RMM2 | `0x12c0` | 9 | tuning | |
| 15 | LSC2 | `0x1340` | 11 | tuning | confirmed (CTL2/CTL3 read at `0x1344`/`0x1348`); needs the IMGCI table |
| 16 | RCP2 | `0x1f60` | 2 | firmware | |
| 18 | RNR (raw NR) | `0x14c0` | 16 | tuning | |
| 19, 20, 22 | SL2G / SL2H / SL2x | `0x1180`, `0x1500`, `0x1400` | 12 each | tuning | probable |
| 23, 24 | FLC / FLC2 | `0x2000`, `0x2040` | 7 each | tuning | probable |
| 27 | G2G2 | `0x20c0` | 8 | tuning | probable |
| 28 | GGM2 | `0x2640` | 193 | tuning | same default LUT as GGM; not in the firmware's own `0xd2` |

**CTL_RGB2_EN (`0x0e8`)**: bit 1 `0x1600` (21 registers, probably ADBS2), bit 2 `0x1680` (8,
probably DCPN2), bit 3 `0x16c0` (16, probably CPN2). All three are owned by tuning.

**CTL_YUV_EN (`0x004`)**, the blocks that matter here:

| Bit | Block | Registers | Count | Owner | Notes |
| --- | --- | --- | --- | --- | --- |
| **3** | **G2C** (RGB to YUV) | **`0x3240`** | 13 | tuning | confirmed: the default sets `[0x4] \|= 8` and BT.601 full-range coefficients; **we must supply it** |
| 5 | NBC (ANR) | `0x3300` | 314 | tuning | probable |
| 6 | NBC2 | `0x4800` | 60 | tuning | probable |
| 7 | PCA | `0x4000` | 391 | tuning | probable |
| 8 | SEEE (edge enhancement) | `0x46c0` | 37 | tuning | probable |
| 9 | NR3D | `0x5000` | 155 | tuning | its function logs the NR3D strings |
| 20 | ? | `0x32c0` | 2 | tuning | no default (zeros) |
| 25 | ? | `0x3940` | 5 | firmware | default `0x3940 = 0xad`, the rest 0, only without tuning |
| 0, 1, 2, 4, 15..19, 21, 22, 24, 26 | crop, scalers (CDRZ at `0x5480`), C24/C42 and other blocks | | | firmware | the firmware writes them every frame |

**CTL_YUV2_EN (`0x008`)**: bits 0..3 are scalers (firmware-owned); bits 4..7 and 10 (`0x5280`,
`0x52c0`, `0x3840`, `0x4980`, `0x5540`) are owned by tuning.

### Defaults the firmware writes when there is no tuning (and that we have to supply)

- UDM, `0x26688`: `0x1540` 0x0002f004, `0x1544` 0x1430053f, `0x1548` 0x00500500, `0x154c` 0x052a30dc,
  `0x1550` 0x02a9124f, `0x1554` 0x0039b4a0, `0x1558` 0x0a529400, `0x155c` 0x81028000, `0x1560` 0x50,
  `0x1564` 0x84210000, `0x1568` 0x46ff1eff, `0x156c` 0x001eff55, `0x1570` 0x00942064,
  `0x1574` (FMT_SEL bit 19) << 10, `0x1578` 15, `0x157c` 255, `0x1580` 0x418, `0x1584` 25,
  `0x1588` 0x35, `0x158c` 0, `0x1590` 0, `0x1594` 0x35, `0x1598` 0x410, `0x159c` 0.
- G2C, `0x26914`: `0x3240` 0x012d0099, `0x3244` 0x3a, `0x3248` 0x075607aa, `0x324c` 0x100,
  `0x3250` 0x072a0100, `0x3254` 0x7d6, `0x3258` 0x0118000e, `0x325c` 0x0074b740, `0x3260` 0x133,
  `0x3264` 0x079f0a5a, `0x3268` 0, `0x326c` 0x03f70080, `0x3270` 0x1ce539ce. This is Y = 0.299 R +
  0.587 G + 0.114 B in Q9 (153, 301, 58), with U and V the same way: BT.601 full range.
- G2G, `0x26778`: `0x2080` 0x200, `0x2084` 0, `0x2088` 0x02000000, `0x208c` 0, `0x2090` 0,
  `0x2094` 0x200, `0x2098` 9 (identity).
- GGM, `0x26856`: 192 words from `0x40f9c` at `0x2180..0x247c`, then `0x2480` = 0x100307ff.
- `0x26ea4`: `0x3940` = 0xad.

## Register formats

The ISP 5.0 OBC, PGN and G2G blocks have the same register counts as the ISP 3.0 blocks, and the
firmware's G2G and G2C defaults use the same packing, so the bitfields below are taken from
MediaTek's MT6795 `isp_reg.h` and the ISP 3.0 3A code (`IPv1.1/isp_mgr`), and checked against the
values in an OV13850 tuning file. The pipeline before GGM works on 12-bit values (PGN's offset is
"4096 + negative", its saturation limit is 4095, and the OBC values in the OV13850 file are on that
scale). GGM's output is 10-bit.

### OBC2: `0x11c0`, 8 registers, RGB_EN bit 11

| Offset | Register | Field |
| --- | --- | --- |
| `0x11c0` | OFFST0 | `OBC_OFST_B` [12:0], 13-bit two's complement, **added** to the pixel |
| `0x11c4` | OFFST1 | Gr |
| `0x11c8` | OFFST2 | Gb |
| `0x11cc` | OFFST3 | R |
| `0x11d0` | GAIN0 | `OBC_GAIN_B` [12:0], unsigned, 512 = 1.0 (MediaTek clamps it at 8191) |
| `0x11d4..0x11dc` | GAIN1..3 | Gr, Gb, R |

Black level 64 at 10 bits is 256 at 12 bits, so OFFST = (-256) & 0x1fff = **`0x1f00`**. GAIN
restores full scale: round(512 * 4095 / (4095 - 256)) = **`0x222`**. MediaTek's OV13850 tuning uses
`0x1EFC`/`0x222`, which is black level 65.

### PGN: `0x1440`, 6 registers, RGB_EN bit 2

| Offset | Register | Low field | High field |
| --- | --- | --- | --- |
| `0x1440` | SATU_1 | B [11:0] | Gb [27:16] |
| `0x1444` | SATU_2 | Gr [11:0] | R [27:16] |
| `0x1448` | GAIN_1 | B [12:0] | Gb [28:16] |
| `0x144c` | GAIN_2 | Gr [12:0] | R [28:16] |
| `0x1450` | OFST_1 | B [11:0] | Gb [27:16] |
| `0x1454` | OFST_2 | Gr [11:0] | R [27:16] |

Gains are 512 = 1.0. SATU is the clip level (4095). OFST is 12-bit two's complement (0 here). For
R 1.8, G 1.0, B 1.6: R = 922 (`0x39a`), G = 512 (`0x200`), B = 819 (`0x333`). So GAIN_1 =
`0x02000333` and GAIN_2 = `0x039a0200`.

### G2G (CCM): `0x2080`, 8 registers, RGB_EN bit 6

The coefficients are signed 13-bit, two's complement in [12:0] or [28:16], fixed point with
`G2G_ACC` fractional bits. With CTRL = 9, 512 = 1.0 (MediaTek calls it "Q1.3.9").

| Offset | Register | Contents |
| --- | --- | --- |
| `0x2080` | CNV_1 | C00 [12:0], C01 [28:16] |
| `0x2084` | CNV_2 | C02 [12:0] |
| `0x2088` | CNV_3 | C10 [12:0], C11 [28:16] |
| `0x208c` | CNV_4 | C12 [12:0] |
| `0x2090` | CNV_5 | C20 [12:0], C21 [28:16] |
| `0x2094` | CNV_6 | C22 [12:0] |
| `0x2098` | CTRL | `G2G_ACC` [3:0] = 9; bit 4 `MOFST_R`, bit 5 `POFST_R` |
| `0x209c` | (ISP 5.0 extra) | left at 0, as the firmware's default does |

Rows are output R, G, B, and each row should sum to 512. Identity: CNV_1 `0x200`, CNV_3
`0x02000000`, CNV_6 `0x200`, the rest 0. For example, MediaTek's OV13850 D65 matrix is
`{0x1ddc0420, 0x4, 0x03201fb0, 0x1f3c, 0x1eb60024, 0x326}` = [[2.06, -1.07, 0.01],
[-0.16, 1.56, -0.38], [0.07, -0.64, 1.57]].

### GGM: `0x2180`, 193 registers, RGB_EN bit 7

- `0x2180 + 4k`, k = 0..191: entry k holds three 10-bit outputs, [9:0], [19:10] and [29:20]. The
  firmware's default stores the same value in all three, so which field is R, G or B is not
  known; use the same curve in all three.
- The firmware's identity table puts entry k at input x_k on the 12-bit scale, with output x_k / 4:
  - x_k = 8k for k < 64 (outputs 0, 2, ..., 126)
  - x_k = 512 + 16(k - 64) for 64 <= k < 96 (outputs 128, ..., 252)
  - x_k = 1024 + 32(k - 96) for 96 <= k < 192 (outputs 256, ..., 992)

  Its last three entries are held at 992.
- `0x2480` GGM_CTRL: the firmware default is **`0x100307ff`**. On the ISP 4.0 and 5.0 layout,
  bit 0 is probably `GGM_LNR` (linear bypass), [10:1] `END_VAR` (0x3ff) and [20:16] `RMP_VAR` (3).
  On MT6795, bit 0 was `GGM_EN`, so this is unconfirmed. Write the curve and keep `0x100307ff`.
  If the output stays linear, use `0x100307fe`.

## Recipe

For each frame (the firmware rewrites parts of the buffer every frame, so rebuild it each time):

1. `memset(tuning, 0, DIP_TUNING_SZ)`. Leave `0x7000..0x704f` zero (no LSC, no LCE).
2. Write the UDM and G2C defaults above, and `0x3940 = 0xad`.
3. Set CTL_YUV_EN `[0x004]` = `BIT(3)` (G2C).
4. Set CTL_RGB_EN `[0x00c]` = `BIT(2) | BIT(6) | BIT(7) | BIT(11)` = **`0x8c4`** (PGN, G2G, GGM,
   OBC2). The firmware ORs in its own bits (UFD, UDM).
5. Set `[0x014]` = `BIT(31)`. This mirrors the flag the no-tuning path uses; the firmware rewrites
   the rest of FMT_SEL.
6. Write OBC2, PGN, G2G and GGM as above.
7. In the frame: `tuning_data.present = 1`, with `pa` and `iova` as now.

A more robust variant ("snapshot"): send one frame with `present = 0` and read the tuning buffer
back once the SCP replies (it is the same coherent SCP DRAM). The firmware has written its complete
default image into it: the final CTL words at `0x4`, `0x8`, `0xc`, `0xe8`, `0x14` and `0x1c`, and
the UDM, G2C, G2G, GGM and any other defaults for exactly the blocks that frame used. Keep
`0x0000..0x557f` as a template. For later frames: copy the template, OR `0x804` (PGN and OBC2) into
`[0xc]`, write OBC2, PGN, G2G and the GGM LUT, and set `present`. This avoids guessing which
firmware-owned blocks (such as YUV bit 20 and bit 25, which are conditional) need values, and it is
also the simplest way to log what the firmware turns on now (`[0x4]`, `[0xc]`, `[0xe8]`).

Things to check on the unit:

- Log `[0x4]`, `[0x8]`, `[0xc]` and `[0xe8]` after a `present = 0` frame. Expected: RGB_EN at least
  `0xd2`, and YUV_EN with bit 3 set.
- With OBC alone (gains 1.0, PGN off), black should go to about 0. If shadows clip, the offset
  scale is 10-bit after all: try `0x1fc0` (-64).
- With GGM_CTRL `0x100307ff` and an obviously non-linear LUT, see whether it takes effect, to settle
  what bit 0 does.

## C for the driver

```c
/* The DIP register image in the tuning buffer: offset = register - 0x15022000 */
#define DIP_CTL_YUV_EN		0x0004
#define DIP_CTL_RGB_EN		0x000c
#define DIP_CTL_FMT_SEL		0x0014
#define DIP_RGB_PGN		BIT(2)
#define DIP_RGB_UDM		BIT(4)	/* the firmware's */
#define DIP_RGB_G2G		BIT(6)
#define DIP_RGB_GGM		BIT(7)
#define DIP_RGB_OBC2		BIT(11)
#define DIP_RGB_LSC2		BIT(15)
#define DIP_YUV_G2C		BIT(3)
#define DIP_OBC2		0x11c0	/* OFFST0..3, GAIN0..3: B, Gr, Gb, R */
#define DIP_PGN			0x1440	/* SATU_1/2, GAIN_1/2, OFST_1/2 */
#define DIP_UDM			0x1540
#define DIP_G2G			0x2080	/* CNV_1..6, CTRL */
#define DIP_GGM_LUT		0x2180	/* 192 x {10,10,10} */
#define DIP_GGM_CTRL		0x2480
#define DIP_G2C			0x3240
#define DIP_TUNING_CQ_OFS	0x5580	/* the firmware's; keep clear */

static const u32 dip_udm_default[23] = {
	0x0002f004, 0x1430053f, 0x00500500, 0x052a30dc, 0x02a9124f, 0x0039b4a0,
	0x0a529400, 0x81028000, 0x00000050, 0x84210000, 0x46ff1eff, 0x001eff55,
	0x00942064, 0x00000000, 0x0000000f, 0x000000ff, 0x00000418, 0x00000019,
	0x00000035, 0x00000000, 0x00000000, 0x00000035, 0x00000410,
};

static const u32 dip_g2c_bt601_full[13] = {
	0x012d0099, 0x0000003a, 0x075607aa, 0x00000100, 0x072a0100, 0x000007d6,
	0x0118000e, 0x0074b740, 0x00000133, 0x079f0a5a, 0x00000000, 0x03f70080,
	0x1ce539ce,
};

/* out = 1023 * (x_k / 4095)^(1/2.2), x_k the firmware identity table's input points */
static const u16 dip_gamma_22[192] = {
	   0,   60,   82,   99,  113,  125,  136,  145,  155,  163,  171,  179,
	 186,  193,  199,  206,  212,  218,  223,  229,  234,  240,  245,  250,
	 255,  259,  264,  269,  273,  277,  282,  286,  290,  294,  298,  302,
	 306,  310,  314,  317,  321,  325,  328,  332,  335,  339,  342,  346,
	 349,  352,  355,  359,  362,  365,  368,  371,  374,  377,  380,  383,
	 386,  389,  392,  395,  398,  403,  409,  414,  419,  425,  430,  435,
	 440,  445,  450,  455,  460,  464,  469,  473,  478,  483,  487,  491,
	 496,  500,  504,  509,  513,  517,  521,  525,  529,  533,  537,  541,
	 545,  553,  560,  567,  575,  582,  589,  596,  603,  610,  617,  623,
	 630,  636,  643,  649,  655,  661,  667,  673,  679,  685,  691,  697,
	 703,  708,  714,  720,  725,  730,  736,  741,  747,  752,  757,  762,
	 767,  773,  778,  783,  788,  793,  798,  802,  807,  812,  817,  822,
	 826,  831,  836,  840,  845,  849,  854,  858,  863,  867,  872,  876,
	 881,  885,  889,  893,  898,  902,  906,  910,  915,  919,  923,  927,
	 931,  935,  939,  943,  947,  951,  955,  959,  963,  967,  971,  975,
	 978,  982,  986,  990,  994,  997, 1001, 1005, 1008, 1012, 1016, 1019,
};

static inline void dip_tw(void *t, u32 ofs, u32 val)
{
	*(u32 *)(t + ofs) = val;
}

static inline u32 dip_s13(int v)
{
	return (u32)v & 0x1fff;
}

/*
 * blc: black level at 10 bits; gains and ccm in 1/512 (Q9); ccm[row][col], rows R, G, B out.
 * The caller zeroes the buffer first and sets frame->tuning_data.present = 1.
 */
static void dip_fill_tuning(void *t, unsigned int blc, u16 gr, u16 gg, u16 gb,
			    const s16 ccm[3][3])
{
	u32 ofst = dip_s13(-(int)(blc * 4));		/* 12-bit scale */
	u32 gain = DIV_ROUND_CLOSEST(512 * 4095, 4095 - blc * 4);
	int i;

	for (i = 0; i < ARRAY_SIZE(dip_udm_default); i++)
		dip_tw(t, DIP_UDM + 4 * i, dip_udm_default[i]);
	for (i = 0; i < ARRAY_SIZE(dip_g2c_bt601_full); i++)
		dip_tw(t, DIP_G2C + 4 * i, dip_g2c_bt601_full[i]);
	dip_tw(t, 0x3940, 0xad);

	dip_tw(t, DIP_CTL_YUV_EN, DIP_YUV_G2C);
	dip_tw(t, DIP_CTL_RGB_EN, DIP_RGB_OBC2 | DIP_RGB_PGN | DIP_RGB_G2G | DIP_RGB_GGM);
	dip_tw(t, DIP_CTL_FMT_SEL, BIT(31));

	for (i = 0; i < 4; i++) {
		dip_tw(t, DIP_OBC2 + 4 * i, ofst);
		dip_tw(t, DIP_OBC2 + 0x10 + 4 * i, gain);
	}

	dip_tw(t, DIP_PGN + 0x00, 0x0fff0fff);			/* SATU B, Gb */
	dip_tw(t, DIP_PGN + 0x04, 0x0fff0fff);			/* SATU Gr, R */
	dip_tw(t, DIP_PGN + 0x08, gb | (u32)gg << 16);		/* GAIN B, Gb */
	dip_tw(t, DIP_PGN + 0x0c, gg | (u32)gr << 16);		/* GAIN Gr, R */
	dip_tw(t, DIP_PGN + 0x10, 0);
	dip_tw(t, DIP_PGN + 0x14, 0);

	dip_tw(t, DIP_G2G + 0x00, dip_s13(ccm[0][0]) | dip_s13(ccm[0][1]) << 16);
	dip_tw(t, DIP_G2G + 0x04, dip_s13(ccm[0][2]));
	dip_tw(t, DIP_G2G + 0x08, dip_s13(ccm[1][0]) | dip_s13(ccm[1][1]) << 16);
	dip_tw(t, DIP_G2G + 0x0c, dip_s13(ccm[1][2]));
	dip_tw(t, DIP_G2G + 0x10, dip_s13(ccm[2][0]) | dip_s13(ccm[2][1]) << 16);
	dip_tw(t, DIP_G2G + 0x14, dip_s13(ccm[2][2]));
	dip_tw(t, DIP_G2G + 0x18, 9);				/* G2G_ACC: Q9 */

	for (i = 0; i < 192; i++) {
		u32 v = dip_gamma_22[i];

		dip_tw(t, DIP_GGM_LUT + 4 * i, v | v << 10 | v << 20);
	}
	dip_tw(t, DIP_GGM_CTRL, 0x100307ff);	/* the firmware's; try 0x100307fe if linear */
}

/*
 * In dip_device_run(), after the memset:
 *	static const s16 ccm[3][3] = {
 *		{ 768, -205,  -51 },	// 1.50 -0.40 -0.10
 *		{-128,  742, -102 },	// -0.25 1.45 -0.20
 *		{ -26, -256,  794 },	// -0.05 -0.50 1.55
 *	};	// or { {512,0,0}, {0,512,0}, {0,0,512} } for identity
 *	dip_fill_tuning(dip->work + DIP_TUNING_OFS, 64, 922, 512, 819, ccm);
 *	f->tuning_data.present = 1;
 */
```

A pure 1/2.2 power curve rises steeply near black (entry 1 is 60) and brings out noise. An sRGB-like
curve with a linear toe (slope about 12.92 below 0.31%) is the usual alternative.

## Optional blocks

- **LSC2** (RGB_EN bit 15, `0x1340`, 11 registers): needs the shading table through IMGCI. Fill
  the `DmaConfigHeader` at `+0x7000` (`enable` = 1, `port_index`, `img_fmt`, width, height,
  bits per pixel, iova, size, strides) and put the table at `+0x7800`. The buffer has to grow past
  32 KB, and the table format (MediaTek's per-block coefficients) is not covered here. Without it
  the firmware refuses LSC2 ("Cannot enable LSC2 without tuning data"). Skip it.
- **NR**: BNR2 (RGB_EN bit 13, `0x1240`, 32 registers: defect pixels and Bayer NR), RNR (bit 18,
  `0x14c0`), NBC/ANR (YUV_EN bit 5, `0x3300`, 314) and NBC2 (bit 6, `0x4800`, 60). No register
  formats were found for ISP 5.0, and the firmware has no defaults for them to copy. Leave them off.
- **EE**: SEEE (YUV_EN bit 8, `0x46c0`, 37 registers), probable; no formats either. Leave it off.

## Confidence

- **High.** The buffer is the register image, with the CQ built in it at `0x5580`. The `present`
  flag is required. Ownership is decided by the masks. The defaults have to be supplied, and the
  UDM and G2C values are as given. Bit and offset for G2G (bit 6, `0x2080`), GGM (bit 7, `0x2180`),
  G2C (YUV bit 3, `0x3240`) and LSC2 (bit 15, `0x1340`) are backed by firmware code. The G2G format
  (Q9, ACC = 9, packing) is confirmed by the firmware's defaults and MediaTek's tuning data.
- **Medium-high.** OBC2 at `0x11c0` on bit 11, and PGN at `0x1440` on bit 2: the counts and the
  order match ISP 3.0 exactly, and the MT6795 formats apply.
- **Medium.** The 12-bit scale for the OBC offset. GGM's input points and its CTRL bit 0.

## Sources

- ChromeOS kernel, chromeos-5.10, `drivers/media/platform/mtk-isp/isp_50/dip/`: `mtk_dip-hw.h`
  (sub-frame 20 KB, tuning 32 KB), `mtk_dip-sys.c` (a zeroed internal tuning buffer with `present`
  left 0 when user space sends none), `mtk_dip-dev.c` (`tuning_data.present = true` for a user
  buffer), `mtk_dip-v4l2.c` (`V4L2_META_FMT_MTISP_PARAMS`, 1024 * (128 + 288)):
  https://chromium.googlesource.com/chromiumos/third_party/kernel/+/refs/heads/chromeos-5.10/drivers/media/platform/mtk-isp/isp_50/dip/
- ChromeOS camera HAL for MT8183, platform2 release-R85, `camera/hal/mediatek/mtkcam/drv/iopipe/src/pass2/NormalStream.cpp`
  (meta buffer layout: 28 KB tuning, 2 KB LSC/LCE headers, 98 KB LSC, 288 KB LCE) and
  `NormalStream.h` (`DmaConfigHeader`):
  https://chromium.googlesource.com/chromiumos/platform2/+/refs/heads/release-R85-13310.B/camera/hal/mediatek/
- MT6771 kernel, `drivers/misc/mediatek/cameraisp/dip/isp_50/camera_dip.{c,h}` (`DIP_A_BASE_HW`
  0x15022000, `DIP_REG_RANGE` 0x6000, the tuning buffer dumped as `0x15022000 + 4i`, `DIP_INIT_ARY`
  with the `DIP_X_*` names):
  https://github.com/Hadenix/kernel_umidigi_f2_mt6771_4.14/tree/HEAD/drivers/misc/mediatek/cameraisp/dip/isp_50
- MT6771 mtkcam3 MFNR platform header (the `DIP_X_CTL_YUV_EN` and `DIP_X_CTL_RGB_EN` names on
  ISP 5.0, and G2C written into the tuning image):
  https://github.com/kongdeyou/mtkcam3/blob/HEAD/feature/mfnr/modules/mfb/inc/platform/mt6771/MfllMfb_platform.h
- MT6795 `isp_reg.h` (PGN, OBC, G2G and GGM bitfields):
  https://github.com/nofearnohappy/vendor_AOSP/blob/HEAD/mediatek/proprietary/hardware/mtkcam/legacy/platform/mt6795/include/mtkcam/drv/isp_reg.h
- ISP 3.0 3A register managers, `IPv1.1/isp_mgr/isp_mgr_{obc,pgn,ccm,ggm,ctl}` (OBC2 and PGN
  register order, PGN gain and offset arithmetic, `AWB_SCALE_UNIT` 512):
  https://github.com/488315-archive/mt8163-vendor/tree/HEAD/mediatek/proprietary/hardware/mtkcam/aaa/source/IPv1.1/isp_mgr
- OV13850 ISP tuning (OBC `0x1EFC`/`0x222`, CCM "Q1.3.9"):
  https://github.com/nofearnohappy/vendor_AOSP/blob/HEAD/mediatek/proprietary/custom/mt6795/hal/imgsensor/ov13850_mipi_raw/camera_isp_regs_ov13850mipiraw.h
- Firmware: linux-firmware 20260916 `mediatek/mt8183/scp.img`. Disassembled functions: `0x35db8`
  (frame intake), `0x380c0`/`0x38542` (enable merge), `0x1e298` (CTL write-back and the
  per-module calls), `0x19ac` (CQ build), `0x26438..0x26e7c` and `0xb8c..0x10f0` (the per-module
  functions). Tables: `0x3f100` (CQ modules), `0x3f58c`/`0x3f70c`/`0x3f730`/`0x3f8a4` (enable
  bits), `0x40f9c` (identity GGM), `0x4d2b9` (tile module names).

## Color, measured on the unit (2026-10-18)

The first stream through the DIP (RTSP, NV12) came out nearly gray: chroma within ±6 of neutral where
the cores' development of the same frame showed color. The Bayer order is right (BGGR, `pBAA`; GBRG
and GRBG both come out magenta). Against the raw cells after the same white balance, the DIP's R/G and
B/G kept the direction of every color but only about 0.4 of its distance from gray, evenly across
hues and regions, which is a linear loss before GGM (the firmware's UDM defaults, most likely). A G2G
matrix of saturation 2.5 about BT.601's luma, `2.5 I - 1.5 [Y; Y; Y]` = {1050, -451, -88; -230, 829,
-88; -230, -451, 1192} in 1/512, put the ratios back within a few percent at all eight regions
checked and leaves the luma as it was; it is the driver's default color matrix
(`dip_ccm_default`). The stream's saturation went from 1.3 to 4.3 (ffmpeg signalstats SATAVG) on the
same room. The remaining green cast is the camera's gray-world white balance, which the cores' picture
shares.
