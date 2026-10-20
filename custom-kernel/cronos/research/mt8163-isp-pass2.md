# Developing the camera's RAW10 on the MT8163 ISP (pass 2) instead of the cores

Status: offline analysis, then four stages on a unit. Stage 1 was read-only register probes and the
vendor tile driver run in user space. Stage 2 ran pass 2: a captured or synthetic RAW10 frame in
through IMGI, demosaiced YUV422 out through IMG2O, the whole 1600x1200 frame in 5.4 ms from a tile
table we generate ourselves. Stage 3 put it in production: echod's tone in the ISP, pass 2 in the
camera driver beside the capture, and the H.264 stream developed on it. Stage 4 (2026-10-19) has
the MDP make most of the I420 from pass 2's output. Date: 2026-10-18. The on-unit results are in
"Stage 1 on the unit", "Stage 2: pass 2 runs", "Stage 3: in production" and "Stage 4: the MDP
makes the I420" at the end; parts of §3, §4 and §6 are superseded there.

## Verdict

**The hardware can do it, but the sources we have do not show how.** The MT8163 camera ISP is MediaTek's
"ISP 3.0" generation (the MT6582 family; the GPL driver says "special for 6582"). Its pass 2 reads a
RAW frame back from memory through **IMGI**. It runs the frame through a fixed chain of blocks:

- UNP (unpack)
- BNR/BPC (bad-pixel and noise reduction)
- LSC (lens shading)
- SL2
- CFA (demosaic)
- C42 (4:4:4 to 4:2:2)
- NBC/ANR (noise)
- SEEE (edge enhancement)
- CDRZ (resizer)

The YUV comes out either through **IMG2O** to memory, or through the **CAM→MDP direct link (CAMIN)**
to the MDP. There, PRZ0/1 (the resizers) and WDMA/WROT can write planar I420/NV12. Everything around
it is already present, or is close to present, on our mainline 7.2.11 port:

- the ISP power domain
- the imgsys clocks
- SMI larb2
- the M4U ports IMGI and IMG2O on larb2, and MDP RDMA/WDMA/WROT on larb0
- the GCE mailbox
- the MMSYS reset bits for CAM_MDP

No secure-world barrier stands in the way either: Fire OS mapped the whole 64 KiB ISP window into user
space, and our pass-1 driver already writes the same 4 KiB page.

**What is missing is the programming model.** Amazon's GPL driver (`camera_isp.c`) is a register-poke
shim with ioctls and mmap. It names only these registers:

- the CTL interrupt and status registers
- a few DMA base addresses
- TG
- the pass-2 reset bits

It points at a closed `isp_reg.h` for the rest. The registers of the processing blocks (CFA, the
color matrix, gamma, RGB→YUV) appear only as **unnamed offsets** in debug dumps. Two more pieces are
closed too:

- **The pass-2 tiling.** The ISP processes the frame as tiles described by a TDR table in memory.
- **The MDP tile maths.** It is closed too.

One piece of luck: the unit's own `libdpframework.so` exports MediaTek's tile driver
(`tpipe_main_platform`, `tile_main_platform`). It names every tile-relevant field of the pass-2
configuration. The camera HAL is not in our backups: there is no `libcamdrv`, `libimageio` or tuning
library. So there are **no names, offsets or default values for CFA, G2G (CCM), GGM (gamma) or G2C
(RGB→YUV)**.

Recommendation:

1. **Do the CPU path first.** The 36 ms on four cores is far slower than this work should take (see
   the cost estimate below). Profile it, then move the inner loop to arm64 NEON in Go assembly. The
   repo already does that, in `echod/internal/lib/vec/*_arm64.s`. Expected result: 5–10 ms a frame
   on the four cores, about one core busy at 30 fps. The develop then stops being the limit: the
   sensor's 30 fps and the encoder's 20 ms are. Effort: about 1–2 weeks. Risk: low.
2. **Keep ISP pass 2 as a research track.** Start with the read-only probes below. Commit to it only
   if a Fire OS image for this device turns up, with `libcamdrv*` and `libimageio*`: those carry the
   block register map. Without them, it is a black-box reverse-engineering job of 2–3 months, with a
   real chance of stalling on the CFA, GGM-LUT or TDR details. What it would win over NEON: about one
   core of CPU at 30 fps, hardware scaling, and LSC/BPC/NR quality. It would not win frame rate.

Confidence:

| Claim | Confidence |
| --- | --- |
| Pass 2 exists, with IMGI in and IMG2O or MDP out, and with CFA/C42/CDRZ/NBC/SEEE in the chain | **High.** Interrupt bits, the DMA enum, CMDQ engines and `libdpframework` field names all agree. |
| RGB→YUV, CCM and gamma blocks exist in pass 2 | **Medium.** They are implied by a YUV output from a Bayer input and by the generation, but not named in anything we hold. The tile config omits them, since they are pixel-wise. |
| AP (non-secure) access to the ISP registers | **High.** Fire OS mmaps them to user space, and our pass-1 driver writes them. |
| IOMMU, SMI, power and clocks are sufficient on our mainline port | **High** for larb2, the ISP domain and the imgsys clocks; those already serve pass 1. **Medium** for the MDP clocks and routing, which no mainline client exercises yet. |
| The GPL headers are enough to program pass 2 without the closed HAL | **No.** High confidence that they are not. |
| NEON reaches ≤10 ms a frame | **Medium-high.** This is an estimate; profile first. |

## 1. What the GPL sources contain

Sources: `sources/amazon-kernel` (lineage-18.1, 4.9.337) and a blob-less clone of branch
`3.18/cm-14.1` of the same repository. The 3.18 cameraisp files are the same set: neither tree has
`isp_reg.h`, `camera_isp_D*.h` or any named block map. File paths below are relative to
`drivers/misc/mediatek/` unless stated.

### 1.1 The driver is a shim; the map is in user space

- `cameraisp/src/mt8163/camera_isp.c:143-145`:

  ```c
  #define ISP_REG_RANGE (0x10000) /* 0x6100,the same with the value in isp_reg.h and page-aligned */
  ```

  The real register struct lives in the closed `isp_reg.h`, which is 0x6100 bytes long.
- `camera_isp.c:6392` (`ISP_mmap`), `:6415` `case IMGSYS_BASE_ADDR` (0x15000000, 64 KiB): Fire OS
  user space mapped the whole imgsys/CAM window. `ISP_WRITE_REGISTER`/`ISP_READ_REGISTER` ioctls
  (`camera_isp.c:5122-5141`, `inc/camera_isp.h:479-481`) take arbitrary offsets. So the HAL
  (`libcamdrv`) programmed every block from user space, and **no TrustZone or DEVAPC step guards the
  ISP**. `devapc/mt8163/devapc.c` and `devapc.h` mention nothing camera-related.
- `camera_isp.c:735-740`: the comments "tile tool parse range" and "for tpipe main start" mark
  `ISP_DumpReg` as the dump the tile tool consumes.

### 1.2 Named registers (CAM base `0x15004000`, `ISP_ADDR`)

From `camera_isp.c:265-306` and `inc/camera_isp.h:49-170`:

| Offset | Name in GPL source | Notes |
| --- | --- | --- |
| `0x004` | `ISP_REG_ADDR_EN1` (`CAM_CTL_EN1`) | module enables (bits unnamed) |
| `0x008` | `CTL_EN2` | bit 23 `UV_CRSA_EN` (`:305`) |
| `0x024/028` | `INT_STATUS` / `DMA_INT` | bit 14 `PASS2_DON`, bit 15 `TPIPE_DON` (`camera_isp.h:58-59`) |
| `0x030/034`, `0x03c/040` | `INTB`/`DMAB`, `INTC`/`DMAC` | pass-2 CQ B/C; `DISPO_DONE` bit 5, `VIDO_DONE` bit 6 (`camera_isp.h:110-111`, `:132-133`) |
| `0x044/048` | `INT_STATUSX` / `DMA_INTX` | |
| `0x050` | "Tile setting" (`camera_isp.c:782`) | |
| `0x05c` | `SW_CTL` | reset trigger/status (`:314-316`) |
| `0x0a8–0x0bc` | "cq0 base", `CQ0C_BASE` (`:275`, `:804`) | command-queue bases |
| `0x0f4/0x0f8` | `IMGO_FBC` / `IMG2O_FBC` | frame-buffer control (pass 1 ring) |
| `0x110` | `CTL_CROP_X` | bit 15 `MDP_CROP_EN` (`:306`): ISP→MDP crop window |
| `0x160/0x164` | `CTL_DBG_SET` / `DBG_PORT` | probes `IMGI`/`CFA`/`YUV`/`OUT` status (`:287-303`) |
| `0x248`, `0x280`, `0x29c`, `0x2d8`, `0x2f8` | IMGI, LSCI, FLKI, VIPI, VIP2I DMA (`:1271-1290`) | only the CON words are dumped |
| `0x300` / `0x320` | `IMGO_BASE_ADDR` / `IMG2O_BASE_ADDR` | IMGO is what our driver uses today |
| `0x410–0x44c` | TG1 | |
| `0x948` | "unp offset" (`:972`) | UNP is pass 2's RAW unpacker |
| `0xb00–0xde0` | "cdp (including EIS)" (`:7135`) | CDRZ, the crop/resize path |
| `0x500–0xaff` | **unnamed** | the RAW/RGB/YUV processing blocks; the proc dump skips them (`:7113-7180`) |

The order of the `CTL_DBG_SET` probe points (`camera_isp.c:289-303`) is IMGI → CFA → YUV → OUT. This is
the pass-2 data path at coarse grain.

The pass-2 reset path is `ISPResetPass2` (`camera_isp.c:1955`). It clears `UV_CRSA_EN` and
`MDP_CROP_EN`, then polls the CQ debug port. The CMDQ MDP reset callback runs it
(`cmdqCoreRegisterCB(CMDQ_GROUP_ISP, …)`, `:7470`): the MDP driver resets the ISP's side of the
direct link.

### 1.3 DMA ports (`inc/camera_isp.h:263-276`)

`_imgi_, _imgci_, _vipi_, _vip2i_, _imgo_, _img2o_, _dispo_, _vido_, _fdo_, _lsci_, _lcei_`. On
MT8163 the M4U carries only six larb2 ports (`m4u/mt8163/m4u_port.h:48-53`):

- IMGO
- IMG2O
- LSCI
- IMGI
- ESFKO
- AAO

The header itself comments that VIPI/VIP2I/IMG3O "have been removed in current IC"
(`m4u_port.h:96-128`). So pass 2 here has **one Bayer input (IMGI)** and **one direct YUV output
(IMG2O)**. Planar 4:2:0 needs the MDP: DISPO and VIDO are MDP WDMA and WROT.

### 1.4 Pass 2 → MDP

- `cmdq/v2/mt8163/cmdq_engine.h:19-30`: the engines are `ISP_IMGI, ISP_IMGO, ISP_IMG2O`, then
  `MDP_CAMIN, MDP_RDMA0, MDP_RSZ0, MDP_RSZ1, MDP_TDSHP0, MDP_WROT0, MDP_WDMA`.
- `cmdq/v2/mt8163/mdp_base.h:31-42`: MMSYS_CONFIG `0x14000000`, RDMA `0x14001000`, RSZ0/1
  `0x14002000`/`0x14003000`, WDMA `0x14004000`, WROT `0x14005000`, TDSHP `0x14006000`, MUTEX
  `0x14015000`, IMGSYS `0x15000000`, ISP_CAMSYS `0x15004000`.
- `cmdq/v2/mt8163/cmdq_mdp.c:157-171`: the MMSYS routing registers.

  | Offset | Register |
  | --- | --- |
  | `0x01c` | `ISP_MOUT_EN` |
  | `0x020` | `MDP_RDMA_MOUT_EN` |
  | `0x024`/`0x028` | `MDP_PRZ0/1_MOUT_EN` |
  | `0x02c` | `MDP_TDSHP_MOUT_EN` |
  | `0x044`/`0x048` | `MDP_PRZ0/1_SEL_IN` |
  | `0x04c` | `MDP_TDSHP_SEL_IN` |
  | `0x050` | `MDP_WDMA_SEL_IN` |
  | `0x054` | `MDP_WROT_SEL_IN` |

  `:731`: "MDP_CAMIN can only reset by mmsys".
- The vendor DT (`arch/arm64/boot/dts/mediatek/mt8163.dtsi:1259-1288`) has the `mdp_rdma/rsz0/rsz1/
  wdma/wrot` nodes. `:701-704` gives the mutex SOF/EOF ids `isp_frame_done_p2_0/1` (131/130).

### 1.5 Power, clocks, larb (vendor DT, `mt8163.dtsi:1518-1569`)

- `ispsys@15000000`: reg `0x15004000+0x9000` (CAM), `0x15000000+0x10000` (imgsys config) and
  `0x10217000` (MIPI analog). IRQs SPI 218 (SENINF) and SPI 219 (CAM0).
- Clocks: `IMG_LARB2_SMI, IMG_CAM_SMI, IMG_CAM_CAM, IMG_SEN_TG, IMG_SEN_CAM, IMG_CAM_SV`, and
  `MM_SMI_COMMON`. The driver also takes a DISP power reference (`camera_isp.c:2020-2034`): "must keep
  this clk open order: … DISP … ISP".
- `larb2@15001000`: `power-domains = <&scpsys MT8163_POWER_DOMAIN_ISP>`.
- The MDP clocks are `MT_CG_DISP0_CAM_MDP`, `MDP_RDMA`, `RSZ0/1`, `TDSHP`, `WROT` and `WDMA`
  (`mt8163.dtsi:724-732`).

## 2. What our mainline 7.2.11 port already offers

- **ISP power domain:** `drivers/pmdomain/mediatek/mt8163-pm-domains.h:35-43` (`"isp"`,
  `SPM_ISP_PWR_CON`, SRAM PDN bits 8-11).
- **imgsys gates:** `clk-mt8163-imgsys.c` in `patches/0000-mt8163-linux-7.2.patch:3400-3408`
  (LARB2_SMI bit 0, JPGENC 4, CAM_SMI 5, CAM_CAM 6, SEN_TG 7, SEN_CAM 8, CAM_SV 9).
- **mmsys gates and resets:** `CLK_MM_CAM_MDP`, `MDP_RDMA/RSZ0/RSZ1/TDSHP/WDMA/WROT`
  (`0000-…patch:3823-3829`), and `MT8163_MMSYS_RST_CAM_MDP`/`MDP_*` (`:7966-7972`). The mmsys
  routing table in the patch covers only the display path (`:7941-8020`); no MDP or ISP routes.
- **IOMMU and SMI:** `drivers/iommu/mtk_iommu.c:1631` (`mt8163_data`), `drivers/memory/mtk-smi.c:554`
  (larb, as MT8173) and `:793` (common). The ports are in `include/dt-bindings/memory/mt8163-larb-port.h`
  (`0000-…patch:9007-9024`): `M4U_PORT_CAM_IMGI` = larb2 port 3, `M4U_PORT_CAM_IMG2O` = larb2 port 1,
  `M4U_PORT_MDP_RDMA/WDMA/WROT` = larb0 ports 14/15/16.
- **The camera node** (`config/cronos-techo5.dtsi:368-388`) already has the ISP domain, larb2 and
  `iommus = <&iommu M4U_PORT_CAM_IMGO>`. Pass 2 would add `M4U_PORT_CAM_IMGI` and
  `M4U_PORT_CAM_IMG2O` (and LSCI for shading), plus larb0's MDP ports for the I420 path. Our driver
  already defers IOMMU activation while the bootloader scans out (`patches/mt8163-cronos-camera.c:366`).
  The MDP ports sit on larb0 with the display, which makes that matter more.
- **GCE:** `mediatek,mt8163-gce` is in the port (`0000-…patch:2301-2306`). Pass 2 does not need it:
  the CPU can write the registers directly.
- **No MDP driver fits.**
  - Mainline `drivers/media/platform/mediatek/mdp` (MT8173) has its frame configuration computed by
    the VPU firmware (`mtk_mdp_m2m.c:521` `mtk_mdp_vpu_process`). MT8163 has no video VPU (see
    `mt8163-video-codec.md`).
  - `mdp3` (MT8183+) gets its configuration from SCP firmware.
  - What mdp3 does provide is a reusable register vocabulary for the same IP family:
    `mdp3/mdp_reg_rsz.h:10-23` (`PRZ_ENABLE 0x000` … `PRZ_CHROMA_HORIZONTAL_SUBPIXEL_OFFSET 0x034`)
    and `mdp3/mdp_reg_wdma.h:10-26` (`WDMA_EN 0x008`, `WDMA_CFG 0x014`, …, `WDMA_DST_ADDR 0xf00`,
    U/V `0xf04/0xf08`). These are MT8183 offsets: assume the MT8163 offsets are close but differ
    until checked.
- **No ISP 3.0 pass-2 driver anywhere upstream.** The 2025 LKML series "mediatek ISP3.0 camsv"
  (MT8365 SENINF/CAMSV, by BayLibre) covers RAW capture only, the equivalent of what we already
  have. It is not in our 7.2.11 tree.

## 3. Fire OS / vendor user space we hold

- **The cronos unit's vendor backup** (`backups/<unit>/vendor-backup.tar`) is the LineageOS 18.1
  vendor partition. It keeps Amazon's MediaTek blobs for display and video, **but no camera HAL**:
  - no `libcam.*`, `libcamdrv*`, `libimageio*`, `libfeatureio*` or `libcameracustom`
  - no `camera.mt8163.so`
  - no NVRAM ISP tuning

  Only AOSP's generic `camera.device@1.0/3.2-impl.so` is present. We have no Fire OS system image
  for cronos.
- The other `backups/` unit is athena (MT8183, with `cam_vpu*` partitions), which is a different ISP.
  Its DIP work is in `../athena/research/dip-tuning.md`.
- **`vendor/lib/libdpframework.so`** (400,580 B, armv7, stripped, but with dynamic symbols) is
  MediaTek's DpFramework for MT8163. It contains **the pass-2 + MDP tile driver**:

  | Address | Exported symbol (`nm -D`, demangled) |
  | --- | --- |
  | `0x3b4f4` | `tpipe_main_platform(const ISP_TPIPE_CONFIG_STRUCT*, ISP_TPIPE_DESCRIPTOR_STRUCT*, char*, int)` |
  | `0x39954` | `tile_main_platform(const ISP_TPIPE_CONFIG_STRUCT*, ISP_TPIPE_DESCRIPTOR_STRUCT*, char*, int, const MDP_TILE_CONFIG_STRUCT*, MDP_TILE_HEX_DUMP_STRUCT*)` |
  | `0x3b756` | `tpipe_main_query_platform_working_buffer_size(int)` |
  | `0x3a228` | `tile_fprint_platform_config(...)`: prints the config, field by field |
  | `0x495e0` / `0x48ad8` | `tile_tdr_isp_reg_to_dump`, `tile_tdr_isp_func_run`: the TDR (tile descriptor) encoding |

  Its strings name the fields of `ISP_TPIPE_CONFIG_STRUCT`, in order:

  - **sw:** `sw.log_en, src_width, src_height, tpipe_width, tpipe_height, tpipe_irq_mode`
  - **top:** `top.pixel_id, cam_in_fmt, mode, scenario, debug_sel, imgi_en, unp_en, bnr_en, lsc_en,
    lsci_en, sl2_en, c24_en, cfa_en, lce_en, lcei_en, c42_en, nbc_en, seee_en, cdrz_en, imgo_en,
    img2o_en, tcm_load_en, ctl_extension_en, rsp_en, mdp_crop_en, mdp_sel, interlace_mode`
  - **per-block:** `imgi.imgi_stride`, `bnr.bpc_en`, `cfa.bayer_bypass`, `lsc.sdblk_*`, `lce.*`,
    `imgo.*`, `img2o.img2o_{crop_en,stride,xoffset,yoffset,xsize,ysize,mux_en,mux}`,
    `nbc.anr_{eny,enc,iir_mode,scale_mode}`, `seee.se_edge`,
    `cdrz.cdrz_{input_crop_*,output_*,*_offset,*_algorithm,*_coeff_step}`, `sl2.sl2_hrz_comp`

  The per-tile register names it fills in: `IMGI_TILE_{OFFSET_ADDR,XSIZE,YSIZE}`,
  `IMG2O_TILE_*`, `IMGO_TILE_*`, `LSCI_TILE_*`, `CDRZ_Tile_*`, `UNP_{START,END}BIT_OFFSET`,
  `SL2_TILE_{X,Y}OFF`, `CTRL_{CDRZ,IMG2O,IMGO,RSP,MDP_CROP}_EN`, `CTRL_MDP_{X,Y}{START,END}`. Two
  messages show the main use case: "DpIsp: ISP Pass2 only" and "DpWrapper_ISP: MDP_Crop is enabled in
  ISP Pass2 only".

  It also holds the MMSYS routing addresses as literals: `0x1400001c–0x2c` and `0x14000044–0x54`, the
  MOUT/SEL_IN registers of §1.4. Strings name `DISP_isp_mout_en`, `DISP_mdp_prz0_sel`,
  `DISP_mdp_wdma_sel` and `DISP_mdp_wrot0_sel`. No `0x15004xxx` literal appears: ISP offsets are
  struct-relative.

  The tile config **does not** include OBC, PGN (white balance), G2G, GGM or G2C. They are
  per-pixel, so tiling ignores them. Their registers come from `libcamdrv` and the tuning libraries,
  which we lack.

## 4. What a develop on pass 2 needs, block by block

| Need | Block | Do we know how? |
| --- | --- | --- |
| Read packed RAW10 from memory | IMGI + UNP | Partly. Base, stride and tile offsets come via the TDR; `top.cam_in_fmt` and `pixel_id` come from the tile config; UNP's offset is `0x948`. The IMGI register block itself is unnamed (inferred around `0x22c–0x24c` from the dump list). |
| Black level | OBC | **No.** Unnamed. |
| White balance | PGN | **No.** |
| Demosaic | CFA | Enable bit known only as a tile field (`top.cfa_en`, `cfa.bayer_bypass`); registers unnamed. |
| Color matrix | G2G | **No.** |
| Gamma | GGM | **No.** It is probably a LUT window (`top.tcm_load_en` hints at an SRAM load). |
| RGB→YUV | G2C | **No.** It may have usable reset defaults (BT.601); check by probe. |
| 4:4:4→4:2:2 | C42 | Tile field `top.c42_en`. |
| Scaling | CDRZ, or MDP PRZ | CDRZ: tile fields and the `0xb00–0xde0` window. PRZ: mdp3 headers, approximately. |
| Write YUV422 to memory | IMG2O | Base `0x320`, FBC `0xf8`, tile fields. The format is probably packed YUYV; the I420 the encoder wants needs the MDP or a cheap CPU 4:2:2→4:2:0 step. |
| Write I420/NV12 | CAMIN → PRZ0 → WDMA/WROT | MMSYS routing known (§1.4); WDMA/WROT/PRZ register sets known only through mdp3's MT8183 headers and libdpframework. |
| Tiling | TDR descriptor + TDRI DMA | The encoder exists only as binary code (`tpipe_main_platform`). It could run under the bionic shim that `vcodec-user/` already uses for `libvcodecdrv`. Alternatively, a single tile may cover 1600 px if the line buffers allow it; unknown. |

**Whether one tile can span 1600 pixels is the crux** for skipping the TDR. The tile driver has
`ISP_MESSAGE_TILE_*_OVER_TILE_WIDTH_ERROR` checks, and `sw.tpipe_width` is a parameter. Running
`tpipe_main_platform` once on the host would answer it: for example, a 1600x1200 RAW10 → IMG2O
config, either under qemu-arm with the bionic shim, or on the unit.

## 5. Cost estimate: ISP versus NEON

### 5.1 The CPU path today

`echod/internal/hardware/camera/camera.go:1145` (`Frame.I420`) and `:1100` (`i420Rows`) do no
demosaic. They work one 2x2 Bayer cell at a time:

- unpack the 10-bit samples from 5 bytes
- 5 lookups in 2048-entry leveling/gamma tables
- BT.601 integer arithmetic for 4 Y and 1 Cb/Cr sample

The work is split into `GOMAXPROCS` bands. At 1600x1200 that is 480,000 cells. 36 ms of wall time on
4 cores at about 1.3 GHz is about **390 cycles a cell**. That is several times what this scalar loop
should cost on an A53 (about 40–80 cycles). So:

- **First, measure:** `BenchmarkI420` (`bench_test.go:225`) on the unit at `GOMAXPROCS=1` and `4`, the
  cpufreq state during the stream, and whether the frame is read straight from a buffer the kernel
  keeps write-combined or uncached, rather than from cached memory. The camera driver allocates its
  slots with the DMA API, and `vb2-vmalloc` copies them. Fixing any of these could halve the time with
  no assembly at all.
- **Then NEON**, in Go assembly. CGO is off, and the repo's `vec/dot_arm64.s` shows the house style
  and the A53 scheduling notes. Integer NEON ops the Go assembler lacks get `WORD` encodings.
  - Unpack 16 pixels at a time with `TBL` shuffles plus shifts.
  - Do black subtraction and per-channel gain as 16-bit fixed point (`UMULL`/`SQRDMULH`).
  - Do gamma through a 9-bit-index table held in registers (8×64-byte `TBX` chain), or a piecewise
    curve.
  - Run the BT.601 matrix as 16-bit multiply-accumulates.

  Estimate: 4–8 cycles a pixel, which is about **5–10 ms per frame on one core, about 2–3 ms on
  four**. Memory traffic is about 5.3 MB a frame (2.4 MB in, 2.9 MB out), which is trivial.
  - Effort: 1–2 weeks, including bit-exact tests against `i420_test.go`.
  - Result: 30 fps, set by the sensor, with the hardware encoder's 20 ms overlapping the develop.
  - Room left for a real demosaic later.

### 5.2 The ISP path

- Hardware throughput is probably about 1 pixel per clock at mm_sel (a few hundred MHz), which is
  about 5–10 ms a frame. CPU cost would be about zero.
- What it adds over NEON: real BPC/LSC/NR/edge processing and free scaling.
- Effort, with today's sources:

  | Stage | Effort | Risk |
  | --- | --- | --- |
  | Probes and decoding the tile config | 1 week | – |
  | First IMG2O output | 3–5 weeks | high |
  | Color (OBC/PGN/G2G/GGM/G2C), black-box | 2–4 weeks | high |
  | MDP I420 out | 2–3 weeks | – |
  | Driver integration | 1–2 weeks | – |

  The total is **about 2–3 months**, and it may not converge without the HAL. If a Fire OS cronos
  image with `libcamdrv` turns up, the color stages shrink to days, because the HAL's register
  writes can be traced.

## 6. Staged plan (read-only first)

**P0. Desk work (no unit).**

1. Disassemble `tile_fprint_platform_config` (`0x3a228`) to get the byte layout of
   `ISP_TPIPE_CONFIG_STRUCT`.
2. Disassemble `tile_tdr_isp_reg_to_dump` (`0x495e0`) to get the TDR word format and the register
   offsets each tile field lands on. This names part of the CTL/DMA map.
3. Run `tpipe_main_platform` under the `vcodec-user` bionic runtime (qemu-arm on the host) with a
   1600x1200 RAW10 → IMG2O configuration. Record the tile count and the TDR. This settles the
   single-tile question.
4. Look for a Fire OS image for this device, with `/system` and `/vendor` holding `libcamdrv*`,
   `libimageio*` and `libcam.*`. Do not guess sources; only use an image whose origin is clear.

**P1. Read-only probes on a unit** (camera streaming, so the ISP domain and clocks are on).

1. Hex-dump `0x15004000–0x15004fff` and `0x15000000–0x15000fff` with a `devmem`-style read.
   - Record reset values.
   - Look for implemented versus read-as-zero words in `0x500–0xaff`. Block boundaries usually
     show as clusters.
   - Look for plausible defaults: an identity G2G (0x200 = 1.0 in Q9), BT.601 G2C coefficients,
     and the CFA thresholds.
   - Watch for read-clear registers: the GPL dump wraps `0x024–0x038` in `#if 1 /* it may touch
     ReadClear register */` (`camera_isp.c:759`). Skip them.
2. Read `MMSYS_CONFIG + 0x01c..0x054` and the MDP blocks' first words with the display running,
   to confirm their offsets against mdp3's headers.

**P2. First pass-2 write test** (needs a test build and care with the shared IOMMU).

1. Add `M4U_PORT_CAM_IMGI` and `M4U_PORT_CAM_IMG2O` to the camera node.
2. Feed IMGI a captured RAW10 frame.
3. Enable only IMGI + UNP + CFA (or `cfa.bayer_bypass`) + C42 + IMG2O, single tile, and trigger
   pass 2 (start bit unknown; find it through the TDR/CQ decoding in P0). Expect `PASS2_DON` in
   `INT_STATUS`.
4. Compare the output with the Go reference.

**P3.** Find and program OBC, PGN, G2G, GGM and G2C, either by bisecting enable bits and register
writes against test patterns, or by tracing the HAL if P0's image search succeeds.

**P4.** Add the CAMIN → PRZ0 → WDMA I420 route: MMSYS MOUT/SEL, the mutex with the `isp_frame_done_p2`
SOF/EOF, and the WDMA planar setup. Or keep IMG2O YUYV and do 4:2:2→4:2:0 on NEON (under 1 ms).

**P5.** Integrate: a V4L2 m2m node (RAW10 in, I420 out), or let the capture driver queue pass 2
behind each IMGO frame.

## 7. Risks

- **Register map:** most block registers are unnamed and have no known defaults. Black-box
  bisection of a 0x600-byte window with interdependent enables is slow, and can wedge the ISP (it
  needs `SW_CTL` reset, `camera_isp.c:2177-2264`).
- **Tiling:** if 1600 px needs more than one tile, the TDR must be produced either by the closed
  tile driver or by a reimplementation. Running armv7 bionic code in the loop for every format
  change is acceptable; the TDR is static for a fixed format.
- **Shared IOMMU and larb0:** MDP ports share larb0 with the display. A bad DMA during bring-up can
  fault the display or the IOMMU. Keep the existing deferral, and test MDP from the rescue
  environment.
- **CAM_MDP clocks and resets:** these are exercised by nothing today. Clock-tree naming mistakes
  surface as silent hangs.
- **Bandwidth:** with pass 2 we gain an extra RAW read (2.4 MB a frame), which is fine. The bigger
  concern is the vendor's comment about a "bandwidth limitor for TG"
  (`camera_isp.c:2224-2232`, EMI `0x120`). Pass 1 and pass 2 share the CAM block's DMA; overruns
  show as `IMGO_ERR`.
- **Legal:** stick to GPL sources, our own probes and the unit's own binaries, as we did for the
  vcodec work. Leaked MediaTek `isp_reg.h` trees for MT6582-family chips may exist online. We did not
  find or use one (a web search turned up nothing). Do not import one.

## Sources

- Amazon GPL kernel, `sources/amazon-kernel` (branch `lineage-18.1`, Linux 4.9.337):
  - `drivers/misc/mediatek/cameraisp/src/mt8163/camera_isp.c`, `inc/camera_isp.h`,
    `inc/camera_isp_FrmB.h`
  - `drivers/misc/mediatek/cmdq/v2/mt8163/{cmdq_engine.h,mdp_base.h,cmdq_mdp.c}`
  - `drivers/misc/mediatek/m4u/mt8163/m4u_port.h`
  - `drivers/misc/mediatek/devapc/mt8163/devapc.{c,h}`
  - `arch/arm64/boot/dts/mediatek/mt8163.dtsi`

  Read with `git show` from the partial clone; the checkout was not changed.
- The same repository, branch `3.18/cm-14.1`: a blob-less clone, used to list its files. Same
  cameraisp file set, with no register header.
- Our port:
  - `patches/0000-mt8163-linux-7.2.patch` (imgsys/mmsys clocks, larb ports, GCE)
  - `config/cronos-techo5.dtsi` (camera node)
  - `patches/mt8163-cronos-camera.c`
  - mainline 7.2.11 in the `techo5-k6` build volume: `drivers/pmdomain/mediatek/mt8163-pm-domains.h`,
    `drivers/iommu/mtk_iommu.c`, `drivers/memory/mtk-smi.c`, `drivers/media/platform/mediatek/{mdp,mdp3}`
- Unit vendor backup (`backups/`, LineageOS 18.1 vendor): `vendor/lib/libdpframework.so`, read with
  `nm -D` and `strings` only. The file list shows no camera HAL.
- `echod/internal/hardware/camera/camera.go`, `bench_test.go`, `echod/internal/lib/vec/dot_arm64.s`.
- Related notes: `mt8163-video-codec.md` (the bionic-shim precedent), `../athena/research/dip-tuning.md`
  (MT8183 DIP, a different ISP generation).
- Web: LKML, "media: platform: mediatek: isp: add mediatek ISP3.0 camsv" (2025), for example
  <https://lkml.iu.edu/hypermail/linux/kernel/2503.2/06243.html>. Searches for an MT6582-family
  `isp_reg.h` found nothing.

## Update: the CPU path measured (2026-10-18)

Step 1 settled the cost. On a quiet unit `BenchmarkI420` was 61 ms on one core and 18.7 ms on four:
the 36 ms quoted above was a live run with the stream's other work serial beside it. Inlining the
cell by hand (Go would not inline it) and unpacking as the loop goes made it 47 ms and 14.1 ms,
byte-identical (`i420_test.go`). The stream's other serial cost was the per-frame tone statistics,
which unpacked half the frame; they now read the sampled cells straight from the packed lines
(identical tone). With both a frame ahead of the encoder (feature/camstream `pumpH264`), the live
stream runs at 29.5 frames a second, the sensor's own rate. NEON would now only free core time,
as would ISP pass 2.

## Stage 1 on the unit (2026-10-18)

Stage 1 asked one question: can pass 2 be programmed from Linux without MediaTek's camera HAL? It
was answered in two parts. First, a read-only register map taken on the unit while it streamed.
Second, the unit's own tile driver, run under the vcodec runtime. Tools: `../isp-user/`
(`regprobe`, `tpipe_test`). Nothing was written to the hardware.

**Verdict: yes, as far as stage 1 can show.**

- The tile driver runs on the unit. For 1600x1200 RAW10 → YUV422 (IMG2O) it produces a three-tile
  table.
- Every field of that table is decoded, and the computation is simple enough to redo ourselves.
- The register page shows the processing blocks are implemented, with recognizable reset values:
  - an identity color matrix
  - BT.601 RGB→YUV coefficients
  - two unity-gain blocks
- Still unknown:
  - the pass-2 start bit
  - the module-enable bit assignments for pass 2
  - the black-level (OBC) block
  - the gamma LUT
  - the TDR fetch (TDRI) programming beyond its base address

### A. Register map (read-only, camera streaming)

How it was taken:

1. echod was stopped with the hold file.
2. `regprobe -stream` opened the camera itself and waited for the first frame, so that the ISP
   domain and the imgsys clocks were on.
3. It took 10 snapshots of each page, 120 ms apart, across frames 2–11.

It skipped CAM `0x020–0x04c`, the interrupt status pairs that clear on read, and the write-only
`0x080–0x094`. The raw dump is not kept in the repo. Results:

- CAM: 178 non-zero words, 5 changing.
- imgsys: 13 non-zero words, none changing.
- MMSYS: 25 non-zero words, 2 changing.

**CAM page `0x15004000`.** The marks: [G] = named in Amazon's GPL `camera_isp.c`; [R] = recognized
from its value; [?] = inferred from the layout only.

| Offset | Value(s) | Reading |
| --- | --- | --- |
| `0x004` EN1 / `0x00c` DMA_EN / `0x010` FMT_SEL | `0x1001` / `0x1` / `0x11000` | Our pass-1 setup (TG1 + PAK, IMGO, TG1_FMT RAW10) [G] |
| `0x018` | 0 | Bit 5 `PASS2_DB_EN`: the GPL debug dump labels it "PASS2_DB_EN (~0x20)" [G] |
| `0x050` / `0x054` | 0 / 0 | "Tile setting"; `0x054` bit 31 `TDR_EN` ("TDR_EN (0x80000000)") [G] |
| `0x05c` SW_CTL | 2 | [G] |
| `0x078` MUX_SEL2 | `0x40000` | IMGO_MUX_EN, which we set |
| `0x0f4` / `0x0f8` | `0x01100000` | IMGO / IMG2O FBC [G] |
| `0x14c` | `0x064004b0` | IMGO size 1600x1200, which we set |
| `0x150` CLK_EN | `0x9efd` | We write `0x1ffff`, so these are the **implemented clock-enable bits** |
| `0x16c`, `0x1a0`, `0x1b4` | change | Status or debug words |
| `0x180` / `0x184` | `0x20120924` / `0x65820000` | Hardware date and IP id, **"6582"**: the MT6582 ISP 3.0 is confirmed [R] |
| `0x200` / `0x204` | `0x001a0012` / 0 | `0x204` = `TDR_BASE_ADDR` (GPL label); `0x200` is probably its DMA control [G]/[?] |
| `0x22c–0x24c` | `0x248`/`0x24c` = `0x08404040`/`0x00404000` | IMGI: base `0x230`, offset `0x234`, xsize `0x238`, ysize `0x23c`, stride `0x240`, CON/CON2 `0x248`/`0x24c` [G] |
| `0x280`/`0x284`, `0x314`/`0x318`, `0x334`/`0x338`, `0x380`/`0x384`, `0x39c`/`0x3a0` | `0x08NNNNNN` / `0x00NNNN00` | The DMA CON/CON2 pairs (FIFO thresholds) of LSCI, IMGO, IMG2O, ESFKO and AAO, with reset values [G] |
| `0x300–0x310` | base cycling over 3 slots; `0x7cf`, `0x4af`, `0x7d0` | IMGO base, xsize, ysize and stride (ours) |
| `0x3a8–0x3f0` | `0xffff0000` / `0xffffffff` | DMA debug or status words (dumped by the GPL `0x3a4–0x3d8` loop) |
| `0x410–0x44c` | TG1; `0x44c` frame counter advancing | [G] |
| `0x534–0x540` | `0x6000`, `0x6000`, `0x00ff00ff`, `0x20202020` | **LSC** (in the GPL tile dump; block counts, last block, ratio) [?] |
| `0x5b4–0x5e8` | gains `0x02000200`/`0x200`, clip `0x0fff0fff`/`0xfff` | **A gain-and-clip block, unity in Q9, 12-bit**: RPG or PGN (white balance) [R] |
| `0x654–0x674` | the same shape | **The second gain block** (the other of RPG/PGN) [R] |
| `0x6a0–0x6f8` | many `0x1fff1fff` and thresholds | BNR (BPC/NR1) [?] |
| `0x800–0x84c` | `0x00100000`, `0x1980301b`, … | **CFA (demosaic)** defaults (`0x800` is in the tile dump) [?] |
| `0x880–0x88c` | `0x0fff0fff` ×2, `0x02000200` ×2 | A third gain-and-clip block [?] |
| `0x8a4–0x8e8` | `0x01084208`, `0x0003fe00`, … | Unidentified (a piecewise curve, by its 5-bit packed fields) [?] |
| `0x910–0x938` | `0x920`=`0x200`, `0x928`=`0x02000000`, `0x934`=`0x200`; `0x938`=9 | **G2G (color matrix), identity in Q9**: nine 16-bit coefficients packed two per word [R] |
| `0x948` | 0 | UNP offset [G] |
| `0xa00–0xa14` | `0x012d0099 0x3a 0x075607aa 0x100 0x072a0100 0x7d6` | **G2C, full-range BT.601 in Q9**, 11-bit two's complement: Y 153/301/58, Cb −86/−170/256, Cr 256/−214/−42 [R] |
| `0xa20–0xa6c` | | **NBC/ANR** (`0xa20` in the tile dump) [?] |
| `0xa94–0xafc` | | **SEEE** (`0xaa0` and `0xacc` in the tile dump) [?] |
| `0xb00–0xde0` | `0xdc0`, `0xdc4`, `0xddc` only | CDRZ/CDP: mostly zero, idle [G] |

A black-level (OBC) block is not visible: it would read zero with no offsets set. Neither is a
gamma LUT. The 4 KiB page holds no LUT-sized run, so GGM's table must sit beyond `0x15004fff`,
in the rest of the 0x9000 window the driver claims. That page was not read in this stage.

**imgsys `0x15000000`.**

- `CG_CON` `0x000` = `0xe`: LARB2, JPGENC and CAM_SMI…CAM_SV are ungated; bits 1–3 are unused.
- `0x010–0x020` hold alternating `0xaaaa…`/`0x5555…` patterns, which look like SRAM or bus
  settings.
- `0x038` = `0x001fc040`.

**MMSYS `0x14000000`.**

- All the MDP routing registers read zero: `ISP_MOUT_EN 0x01c`, `MDP_*_MOUT_EN 0x020–0x02c`,
  `MDP_*_SEL_IN 0x044–0x054`. Nothing routes ISP→MDP today.
- The display routes `0x030`, `0x038`, `0x03c` and `0x058` are 1.
- `0x8a0` and `0x8a8` change: they are `DISP_DL_VALID_0` and `DISP_DL_READY_0`
  (`cmdq_mdp.c:181-184`).
- `MMSYS_CG_CON0` `0x100` = `0xffe06bfc`: bits 2–9 are set, so **CAM_MDP, MDP_RDMA, RSZ0, RSZ1,
  TDSHP, WDMA and WROT are all gated**. The MDP blocks were therefore not read: a read of a gated
  block risks a bus stall. The I420 path through the MDP needs those clocks first.

### B. The tile driver, run on the unit

**Method.** `isp-user/harness/tpipe_test` is an armv7 bionic PIE, built with `arm-none-eabi-gcc`
against the NDK's bionic stubs. The harness declares its libc calls itself, so no NDK toolchain
is needed. It runs under the runtime the image already carries for the video filters
(`/usr/local/lib/techo5-vcodec`), with libdpframework from the unit's `/vendor/lib`.

- libdpframework imports only 22 non-bionic symbols. The vcodec stand-ins cover 18: `liblog`,
  `libcutils`, `libion`, `libion_mtk`.
- The rest are a failing `libsync` and empty `libgralloc_extra`, `libhardware` and
  `libnativewindow`.
- The library's static constructors run without complaint.

The layouts below come from these functions:

- `tile_fprint_platform_config` (`0x3a228`) and `tile_copy_config_by_platform` (`0x39ef0`)
- `tile_main_platform` (`0x39954`)
- `tile_tdr_isp_reg_to_dump` (`0x495e0`) and `tile_tdr_isp_fprint_dump` (`0x49b80`)

The library's MiniDebugInfo (`.gnu_debugdata`) names a few static functions as well.

**`ISP_TPIPE_CONFIG_STRUCT`** has 82 `int32` fields (0x148 bytes), in this order:

| Offset | Fields |
| --- | --- |
| `0x000–0x068` | `top.`: scenario, mode, debug_sel, pixel_id, cam_in_fmt, tcm_load_en, ctl_extension_en, rsp_en, mdp_crop_en, imgi_en, lce_en, lcei_en, lsci_en, unp_en, bnr_en, lsc_en, sl2_en, c24_en, cfa_en, c42_en, nbc_en, seee_en, imgo_en, img2o_en, cdrz_en, mdp_sel, interlace_mode |
| `0x06c–0x080` | `sw.`: log_en, src_width, src_height, tpipe_irq_mode, tpipe_width, tpipe_height |
| `0x084–0x0cc` | imgi_stride, lcei_stride, lsci_stride, bnr.bpc_en, lsc.sdblk_{width,xnum,last_width,height,ynum,last_height}, lce.{bc_mag_kubnx, slm_width, bc_mag_kubny, slm_height}, nbc.anr_{eny,enc,iir_mode,scale_mode}, seee.se_edge |
| `0x0d0–0x0ec` | imgo.{stride, crop_en, xoffset, yoffset, xsize, ysize, mux_en, mux} |
| `0x0f0–0x11c` | cdrz.{input_crop_w/h, output_w/h, luma h/v integer and subpixel offsets, h/v algorithm, h/v coeff_step} |
| `0x120–0x13c` | img2o.{stride, crop_en, xoffset, yoffset, xsize, ysize, mux_en, mux} |
| `0x140`, `0x144` | cfa.bayer_bypass, sl2.sl2_hrz_comp |

Values that matter, found by running:

| Field | Values |
| --- | --- |
| `top.scenario` | Must be 3 (the memory-to-memory scenario); 0 fails with `UNDER_MIN_TILE_FUNC_NO` |
| `top.cam_in_fmt` | 0 = RAW8, 1 = RAW10, 2 = RAW12 (the IMGI byte widths follow) |
| `top.mode` | 0 |
| IMG2O | Must take `img2o_mux_en=1, img2o_mux=1` (fed from C42). With mux 0, the CDRZ output, every attempt failed with `OUTPUT_DISABLE_INPUT_FUNC_CHECK_ERROR`: CDRZ seems to feed only the MDP link, which needs `tile_main_platform` with an MDP config (not tried) |
| NBC, SEEE | Add no TDR words |
| BNR + LSC | Add 6 TDR words per tile and widen the overlap |

**Working buffer.** Its size is `58592 + 168 × max_tiles` bytes. It starts with
`TILE_REG_MAP_STRUCT` (980 bytes): the config copied in and the per-tile results.
`tile_copy_config_by_platform` **clamps the tile width to 768** and forces `tpipe_height` to 8192
and `irq_mode` to 2.

**`ISP_TPIPE_DESCRIPTOR_STRUCT`**, as filled in:

| Word | Content |
| --- | --- |
| `[0]` | The TDR words |
| `[1]` | Per-tile records, 6 words each: in_xs, in_xe, in_ys, in_ye, ?, first TDR word |
| `[2]` | TDR words used |
| `[3]` | TDR capacity (36 a tile) |
| `[4]` | Words a tile |
| `[5]` | Tiles |
| `[6]` | Tile capacity |
| `[7]` | Tiles across |
| `[9]` | Tiles down (probably) |

Return value 1 is `ISP_TPIPE_MESSAGE_OK`.

**TDR format.** It is a sequence of 32-bit words per tile. The words present depend on the
enabled blocks, in this order:

1. **CTRL:** `TDR_EDGE[3:0]` (bit 0 left, 1 right, 2 top, 3 bottom), `TILE_IRQ[4]`,
   `LAST_IRQ[5]`, `PIX_ID[11:8]`, `CDP_TDR_EDGE[15:12]`, `CTRL_CDRZ_EN[16]`, `RSP_EN[17]`,
   `IMGO_EN[18]`, `IMG2O_EN[19]`, `MDP_CROP_EN[20]`.
2. `IMGI_TILE_OFFSET_ADDR[27:0]` (bytes).
3. `IMGI_TILE_XSIZE[15:0]` (bytes − 1) | `YSIZE[28:16]`.
4. If IMGO: offset, size and x/y offset.
5. If IMG2O: `IMG2O_TILE_OFFSET_ADDR`, then `XSIZE[13:0]` (bytes − 1) | `YSIZE[28:16]`, then
   (with crop) `XOFFSET` | `YOFFSET`.
6. If LCE: LCEI and LCE words.
7. `UNP_STARTBIT_OFFSET[3:0]` | `UNP_ENDBIT_OFFSET[19:16]`.
8. If LSC: LSCI offset and size, then `LSC_TILE_XSIZE`/`YSIZE`, `XOFST`/`YOFST`, `XNUM`/`YNUM` and
   `LWIDTH`/`LHEIGHT`.
9. If SL2: `SL2_TILE_XOFF`, `YOFF`.
10. If CDRZ: input and output sizes, luma and chroma integer and subpixel offsets.
11. `CTRL_MDP_{X,Y}{START,END}`.

The field masks are from `tile_tdr_isp_fprint_dump`.

**Result for 1600x1200 RAW10 → IMG2O YUV422** (defaults of `tpipe_test`: IMGI, UNP, CFA, C42,
IMG2O; stride 2000 in, 3200 out). There are **3 tiles, 6 words each, 18 words**:

| Tile | Input x | CTRL | IMGI offset | IMGI size | IMG2O offset | IMG2O size | UNP |
| --- | --- | --- | --- | --- | --- | --- | --- |
| 0 | 0–83 | `0x0008002d` (edges L,T,B) | 0 | 106 B × 1200 | 0 | 160 B (80 px) | end 8 |
| 1 | 74–841 | `0x0008002c` (T,B) | 92 | 962 B | 160 | 1516 B (758 px) | start 4, end 4 |
| 2 | 832–1599 | `0x0008002e` (R,T,B) | 1040 | 960 B | 1676 | 1524 B (762 px) | 0 |

The table:

- covers the full width (80 + 758 + 762 = 1600 output pixels);
- overlaps input tiles by 10 px for the demosaic (2 px with `cfa.bayer_bypass`);
- builds from the right edge, so the leftover tile comes first;
- splits nothing vertically.

**One tile does not span 1600 px.**

- The 768 clamp is in the library: `sw.tpipe_width=1600` still gives 3 tiles.
- A 768-wide source is a single tile; 769 is rejected.
- 800 gives 2 tiles; `tpipe_width=400` gives 5.

So the hardware's line buffers are almost certainly 768 px (MT6582's limit), and a 1600-wide
develop needs the TDR, or three single-tile passes programmed one after another. Either way, the
table is small and its computation is understood. For a fixed format it can be generated once
with the library. It is also easy to compute ourselves: tile edges, a 10 px overlap, RAW10 byte
offsets (⌊10·x/8⌋) and the UNP bit phase (start bit = 10·x mod 8; the end-bit encoding, 8/4/0 in the
table above, is still to be pinned down). So the
closed tile driver is **not** a dependency at run time.

### C. What remains, and the revised plan

The tiling library cannot supply the per-pixel blocks. Status of each:

| Block | Status after stage 1 |
| --- | --- |
| Black level (OBC) | Not located. It may be part of the first gain block (its offset words read zero), or a separate block. Find it by writing in stage 2. |
| White balance (PGN/RPG) | Two candidate gain-and-clip blocks at `0x5c0` and `0x654` (unity Q9, 12-bit clip). |
| Demosaic (CFA) | Defaults present at `0x800–0x84c`. They can probably stay as reset. `cfa.bayer_bypass` exists for a first test. |
| CCM (G2G) | `0x920–0x934`, identity in Q9: almost certainly the matrix. |
| Gamma (GGM) | LUT not in the first page. Next read-only probe: `0x15005000–0x15007fff` while streaming. |
| RGB→YUV (G2C) | `0xa00–0xa14`, BT.601 full range in Q9. Limited range means scaling by 219/255 and 224/255 and an offset of 16, whose register is not yet seen. |
| Enables | EN1/EN2/DMA_EN bit assignments for pass 2: unknown. `CLK_EN` implemented bits `0x9efd` give a hint. |
| Start | Unknown bit. The completion bits are known: `INT_STATUS` bit 14 `PASS2_DON` and bit 15 `TPIPE_DON`. |
| TDR fetch | `TDR_BASE_ADDR 0x204`, `TDR_EN 0x054[31]`, and the "tile setting" `0x050`. Which M4U port TDRI reads through is unknown (likely IMGI's). |

**Revised estimate.** The first IMG2O output is now about 2–3 weeks: the tile table and IMGI/IMG2O
programming are known, so only the enable and start bits are black-box. Color is about 2–3 weeks:
G2G and G2C are located, the gains are located, and OBC and GGM remain. The MDP I420 route is
2–3 weeks as before, unless IMG2O YUYV plus a NEON 4:2:2→4:2:0 step is enough (under 1 ms).
Integration is 1–2 weeks. **Total: about 6–10 weeks, down from 2–3 months.** The main risk is now
the start and enable bits, not the tiling.

**Stage 2 plan** (writes only to the CAM window; a test build; with the rescue environment at hand):

1. **One more read-only probe:** `0x15005000–0x15007fff` while streaming, to find the GGM LUT and
   whatever else holds the rest of `isp_reg.h`'s 0x6100 bytes.
2. **DT:** add `M4U_PORT_CAM_IMGI` and `M4U_PORT_CAM_IMG2O` to the camera node's `iommus`. These
   ports are on larb2, not the display's larb0.
3. **A test kernel module, or a debugfs path in `mt8163-cronos-camera.c`**, used only when pass 1
   is stopped:
   - Allocate IMGI, holding one captured RAW10 frame, plus IMG2O (1600×1200×2) and an 18-word TDR
     buffer, with the DMA API.
   - Program FMT_SEL scenario 3 and sub-mode 0 (scenario `[2:0]` and sub-mode `[6:4]` are the
     candidates).
   - Set the IMGI base, size and stride `0x230–0x240`, the IMG2O base `0x320` and its size and
     stride, `TDR_BASE_ADDR 0x204` and `TDR_EN 0x054[31]`.
   - Enable IMGI/UNP/CFA/C42/IMG2O.
   - Search the start bit in `CTL_START 0x000`, and poll `PASS2_DON` and `TPIPE_DON` (the module
     owns the read-clear status then).
   - Bound every attempt with `SW_CTL` reset (`camera_isp.c:2177-2264`).
4. **First with `cfa.bayer_bypass`, then with CFA.** Compare IMG2O against the Go develop
   (`camera.go`) with G2G at identity. Then bisect the gain blocks, black level, G2C limited-range
   coefficients and the GGM LUT against test patterns.
5. **Only then** choose IMG2O + NEON 4:2:0 or the CAMIN→PRZ→WDMA MDP route (ungating the MM
   clocks).

### Files

- `../isp-user/`:
  - `README.md`
  - `regprobe/` (Go)
  - `harness/tpipe_test.c`
  - `stubs/`
  - `tools/{build.sh,run.sh}`

  MIT. No vendor code: libdpframework is linked against at build time and loaded from the unit's
  `/vendor/lib` at run time.
- Sources for this section:
  - `regprobe` dumps and `tpipe_test` runs on a cronos unit, kept outside the repo
  - the unit's `/vendor/lib/libdpframework.so`, disassembled with binutils
  - Amazon's GPL `camera_isp.c` (debug-dump labels at `:1641-1690`; tile-dump list at `:735-1000`)
  - `cmdq_mdp.c:157-184`
  - our clock driver's MM and IMG gate bit numbers (`0000-mt8163-linux-7.2.patch:3395-3408`,
    `:3804-3846`)

## Stage 2: pass 2 runs (2026-10-18)

Stage 2 asked for one pass-2 run that produces a picture: a RAW10 1600x1200 frame in through IMGI,
YUV422 out through IMG2O, from a tile table we generate, compared with echod's CPU develop of the
same frame. Tools: `../isp-user/` (`kmod/isp2test.c`, `tools/pass2.sh`, `isp2cmp/`, and `regprobe`
with `-hi`, `-save`, `-exposure`, `-gain`).

**Verdict: pass 2 works.**

- The whole frame develops in **5.4 ms**: three tiles from our own table, demosaiced, BT.601.
  echod's I420 develop takes 14.1 ms of four cores for the same frame.
- Against echod's bilinear develop of the same frame (linear tone, BT.601 full range):
  - **50.3 dB Y, 50.5 dB Cb and 50.1 dB Cr** on a captured frame
  - **49.8 / 52.8 / 52.1 dB** on the photographic half of a synthetic frame
  - 27.8 / 33.2 / 28.1 dB on its test-pattern half: zone plate and 1-8 px stripes, where the ISP's
    demosaic and bilinear disagree, as expected
- No seams show at the tile edges.
- Nothing faulted except two IOMMU read faults that the enable-bit search provoked: a
  command-queue enable with no queue base (below). The unit never hung.

What it does not do yet: black level, white balance, color matrix and gamma are found or half
found, but not programmed; the chroma comes out signed; and the output is 4:2:2.

### How the hardware was reached

- **No DT change.** The module `isp2test` finds the camera node (`amazon,cronos-camera`) and
  borrows its device:
  - runtime PM on the device: the ISP power domain;
  - runtime PM on its larb: larb2 and its clocks;
  - its four imgsys clocks, through `clk_bulk_get_all`.

  It allocates the buffers with `dma_alloc_coherent` on that device, so they get IOVAs in the
  shared IOMMU domain: IMGI 2.4 MB, IMG2O 3.84 MB plus a guard, a 64 KiB tile table and a 1 MiB
  scratch buffer.
- **The IMGI and IMG2O ports are not in the node's `iommus`**, so larb2 left them untranslated:
  `SMI_LARB_MMU_EN` (`0x15001f00`) read `0x1`, IMGO only. While it holds the hardware, the module
  sets it to `0x3f`, every larb2 port translated. A stray DMA address then faults in the IOMMU
  instead of reaching memory. `off` restores the old value.
  - Both IOMMU faults of the stage named `larb=2 port=3`: the command queue reads through the
    IMGI port.
  - For a driver, the clean way is the DT: add `M4U_PORT_CAM_IMGI` and `M4U_PORT_CAM_IMG2O` to
    the camera node's `iommus`, and mtk-smi then sets the bits itself.
- **Registers.** All writes were in the camera node's CAM window (`0x15004000-0x15007fff`), plus
  that one larb2 word. The imgsys page was only read.
- **Pass 1 stopped first**, with the hold file. Every run was bounded:
  - a 20 µs poll of the read-clear status words, for at most 0.1-0.5 s;
  - then the CAM soft reset (`SW_CTL` 5, 4, 0).

  A wrong setting showed up as a timeout, `DMA_ERR` (INT bit 30) or an IOMMU fault, never as a
  hang.

### The probe of `0x15005000-0x15007fff` (read only, streaming)

| Range | Content |
| --- | --- |
| `0x5000-0x523c` | 144 words, all 32 bits random at power-up: an SRAM table. Two 16-bit fields per word (R and B?) |
| `0x5300-0x553c` | 144 words, 16 bits used: the matching single-channel table (G?) |
| `0x5800-0x5d9c` | 360 words of 24 bits: the **PCA** hue/saturation LUT (proved below) |
| `0x5e04` | `0x00ff0200` |
| `0x6000-0x7fff` | reads zero |

The two 144-entry tables have the shape of MediaTek's gamma (GGM: RB and G). They are writable
(read back). But no enable bit tried made the output follow them, so they are unconfirmed.

### The pass-2 register map, as found

How it was found:

- Writing all-ones and reading back gave each control register's implemented bits.
- Single-bit sweeps on the running pipeline gave their meaning.
- A tile table of marker values gave the destination of each word.
- Offsets are from `0x15004000`.

| Offset | Implemented | Meaning found |
| --- | --- | --- |
| `0x000` START | | **bit 0 `PASS2_START`**: reads 1 while pass 2 runs, clears itself at the end. Pass 1 runs from the TG, not from here |
| `0x004` EN1 | `0x64f7bbad` | bit 20 **UNP** (needed for anything to flow); bit 21 **CFA** (the demosaic); bit 11 **PGN**, the gains at `0x880-0x88c`; bit 23 **G2G**, the matrix at `0x920-0x934`; bit 3 blacks the picture out with the reset values (OBC?); bits 5, 8 and 9 stall the run (LSC wants LSCI; 8 and 9 unknown). The rest change nothing. Pass 1's bits 0 (TG1) and 12 (PAK) are known |
| `0x008` EN2 | `0xb883c01f` | bit 0 **G2C**; bit 1 **C42**; bit 3 **PCA** (LUT `0x5800`; an all-zero LUT is a no-op); bit 23 `UV_CRSA` (GPL); bits 27-29 are command-queue enables. Bit 27 makes pass 2 fetch commands from CQ base `0x0ac`, an IOMMU fault at 0. The rest change nothing |
| `0x00c` DMA_EN | `0x4ab` | bit 0 IMGO, bit 7 **IMGI**, bit 10 **IMG2O**. Bits 1, 3 and 5 are presumably LSCI, ESFKO and AAO |
| `0x010` FMT_SEL | `0x0d07ff77` | `0x00011103`: scenario 3, `CAM_IN_FMT` [11:8] 1 (RAW10), and the TG1 bits as pass 1 sets them. `CAM_OUT_FMT` [15:12] does not affect IMG2O |
| `0x018` SEL | `0x8243f8fc` | **bit 6: take the tile sizes from the tile table.** Bit 5 (`PASS2_DB_EN`) gives `DMA_ERR` here; bits 12-14 stall |
| `0x01c` | `0x07ff007f` | 0 |
| `0x020` INT_EN | `0xdfbbf41f` | **The status bits latch only when enabled.** We write `0x5fbbf41f` (bit 31 left alone) |
| `0x024` INT_STATUS | | bit 14 `PASS2_DON`, bit 15 `TPIPE_DON` (tile mode), bit 30 `DMA_ERR`. `0x048` DMA_INTX bit 1 is IMG2O done |
| `0x050` TILE | `0xfff3ffff` | Tile word 0 lands here, without bits 18 and 19. Even without a table it must hold the edges and `LAST_IRQ`, or the run ends in `DMA_ERR` |
| `0x054` TCM_EN | `0xfc910383` | **bit 31 `TDR_EN`**. Bit 27 stops the offset and UNP words loading. No other bit had a visible effect |
| `0x0a8-0x0bc` | 6×32 | command-queue bases |
| `0x204/0x208/0x20c` | | TDRI base, offset, size (bytes − 1). The hardware rewrites `0x20c` during a run (`0x1f` after it) |
| `0x210/0x214` | 32/16 bits | another read DMA, base and size; unknown |
| `0x230-0x24c` | | IMGI base, offset, xsize (bytes − 1), ysize, stride, -, CON, CON2 |
| `0x26c-0x284` | | LSCI |
| `0x288-0x2fc` | none | (FLKI, VIPI and VIP2I are not on this chip) |
| `0x300-0x33c` | | IMGO and IMG2O: base, offset, xsize, ysize, stride, CON, CON2, crop |
| `0x35c-0x368` | | unknown, two base and size pairs |
| `0x36c-0x384`, `0x388-0x3a0` | | ESFKO, AAO |
| `0x5c0`, `0x654` | | gain blocks that **do not affect pass 2** with any EN bit (pass 1's?) |
| `0x880-0x88c` | | **PGN**: clips `0x880/0x884`, four Q9 gains `0x888/0x88c` (EN1 bit 11; ×2 doubled the output) |
| `0x920-0x934` | | **G2G**: `{C00,C01} {C02,ofs} {C10,C11} {C12,ofs} {C20,C21} {C22,ofs}`, Q9, identity at reset (EN1 bit 23; ×0.5 halved the output) |
| `0x948` | | UNP: start bit [3:0], end bit [7:4] (loaded from the table) |
| `0xa00-0xa14` | | **G2C**: coefficients as in stage 1. The offsets sit in [26:16] of `0xa04` (Y, 11 bits), `0xa0c` (Cb) and `0xa14` (Cr), 10 bits each, signed, in quarters of an 8-bit code |
| `0xa1c` | bit 0 | moves chroma by about one code; left 0 |
| `0x160/0x164` | | debug port: `0x9000-0x900e` read the IMGI, CFA, YUV and OUT stages; `0x5000/0x5100` TDR; `0x6000` CQ |

### The output format

IMG2O writes **YUYV**: Y0, Cb, Y1, Cr, two bytes a pixel, at the stride given. The chroma is
**two's complement around 0**, not offset by 128:

- G2C's chroma offset is signed. 512 reads as −512 and the result clips at −128.
- None of these gave unsigned chroma: FMT_SEL's output-format field, the IMG2O stride's spare
  bits, `0xa1c`, or `UV_CRSA`.

For now the consumer flips bit 7 of each chroma byte, for free in the 4:2:2→4:2:0 step.

The Bayer phase is `PIX_ID`, in the tile word (MediaTek's order: 0 B, 1 Gb, 2 Gr, 3 R). The
OV02B10 needs 3. With 0, red and blue swap and the chroma comes out in YVYU order.

The luma is on average 0.4 code above echod's rounding.

### The tile table, ours

`isp2cmp tdr` builds the table and reproduces the vendor tile driver's 18 words bit for bit
(stage 1's `tpipe_test` output). The rules:

- Tiles are at most 768 input pixels, cut from the right edge.
- An inner edge costs 6 input pixels on the tile's left and 4 on its right. This gives input 0-83,
  74-841 and 832-1599, and output 0-79, 80-837 and 838-1599.
- IMGI reads 16-bit aligned:
  - the offset is ⌊10·x / 16⌋·2 bytes;
  - the UNP start bit is 10·x − 8·offset;
  - the end bit is the last pixel's end bit modulo 16 (8, 4 and 0 here);
  - the size is rounded up to whole 16-bit words.

  This settles stage 1's open "end-bit encoding".

Two things differ from the vendor's table to run the whole frame on one start:

- **`LAST_IRQ` only on the last tile.** The vendor's sets it on every tile (`irq_mode` 2). Then
  the hardware stops after tile 0 with `PASS2_DON` and has written 80 columns.
- **`PIX_ID` 3.**

How the words load:

| Word | Destination | Condition |
| --- | --- | --- |
| 0, CTRL | `0x050` | `TDR_EN` |
| 1, 3, 5: offsets and UNP | `0x234`, `0x324`, `0x948` | `TDR_EN` |
| 2, 4: IMGI and IMG2O sizes | `0x238`, `0x328` | **only with SEL bit 6** |

Without SEL bit 6 every tile uses the frame's sizes, and the run either fails with `DMA_ERR` or
writes one tile.

### The recipe (`tools/pass2.sh`)

After `on` and a soft reset, as written:

1. `0x000` 0; `0x150` (CLK_EN) `0x1ffff`; TG `0x410/0x414` 0; `0x020` `0x5fbbf41f`.
2. The unused DMA bases (LSCI, IMGO, ESFKO, AAO) point at the scratch buffer. The CQ bases are 0.
3. `0x010` `0x00011103`; EN1 `0x00300000`; EN2 `0x3`; DMA_EN `0x480`; `0x01c` and `0x078` (MUX_SEL2)
   0.
4. IMGI: `0x230` base, 0, 1999, 1199, 2000. IMG2O: `0x320` base, 0, 3199, 1199, 3200.
5. G2C offsets 0; `0xa1c` 0.
6. The table: `0x050` `0x32f`; `0x204` base, `0x208` 0, `0x20c` 71; `0x054` `0x80000000`;
   `0x018` `0x40`.
7. `0x000` = 1. Wait for INT_STATUS bit 14 (bit 15 comes too).

It produced the same bytes after a reboot as before it. The guard bytes after the frame stayed
untouched.

### Results

| Input | Develop compared | Y | Cb | Cr |
| --- | --- | --- | --- | --- |
| Captured frame (a dark room) | echod `Full` (bilinear), linear tone | 50.30 dB | 50.49 | 50.12 |
| Captured frame | echod `I420` cell color, linear tone | 48.68 | 47.40 | 47.22 |
| Synthetic, photo half | echod `Full`, linear | 49.82 | 52.75 | 52.10 |
| Synthetic, photo half | echod `I420` cell color | 43.93 | 47.21 | 49.15 |
| Synthetic, test-pattern half | echod `Full` | 27.83 | 33.20 | 28.11 |
| Synthetic, test pattern, CFA off (bit 21 clear) | echod `Full` | 23.85 (Bayer mosaic) | | |

About the comparison:

- The reference is `isp2cmp ref`, a port of echod's `tables()`, `developRow` and `i420Rows`
  color. Its tone is set linear (black 0, gains 1, white 2046, gamma 1), and it converts with
  BT.601 full range, G2C's reset matrix.
- The captured frame is a dark room: the room's light was off. Its values are low, so its PSNR is
  flattered.
- The synthetic frame is `isp2cmp mkraw`:
  - the left half is a macOS desktop picture (Big Sur Coastline's thumbnail), scaled;
  - the right half is color bars, a gray ramp, a zone plate, and 1-8 px black/white and
    red/cyan stripes;
  - it is mosaiced RGGB in linear light, with sensor-like red and blue responses (0.55, 0.7) and a
    pedestal of 16.
- The pictures (ISP output, reference, and the difference ×8) were checked by eye. They are kept in
  scratch, not the repo.

Timing: 5.39 ms a frame, all three tiles. A single 768-px tile took 2.56 ms. Pass 2 runs at about
350 Mpixel/s.

### What remains

| Item | Status | Next step |
| --- | --- | --- |
| Black level | EN1 bit 3 blacks out with reset values; registers not found (`0x4f0` holds `0x0f3c14b4`, `0x10`) | Bisect `0x4e0-0x55c` with bit 3 on, against a gray ramp |
| White balance | **PGN found** (EN1 11, `0x888/0x88c`, Q9) | Map each gain to its Bayer channel with the color bars |
| Color matrix | **G2G found** (EN1 23, `0x920-0x934`) | echod has no matrix today; leave at identity, or add one later |
| Gamma | Tables at `0x5000`/`0x5300` writable; enable not found among EN1/EN2 | Look for a GGM control (`0x5e00-0x5ffc`, `0x8a0-0x8ec`'s curve-shaped words), then decode the 144 knots |
| Limited range | Coefficients ×219/255 and ×224/255, Y offset +16 (64 in the 10-bit field): straightforward | Chroma +128 does not fit the signed output; flip bit 7 in the 4:2:0 step |
| 4:2:2 → 4:2:0 | CPU: one pass over 3.84 MB, under 1 ms with NEON, a few ms in Go | Or the MDP: `MDP_CAMIN → PRZ0 → WDMA` (needs the MM clocks and MMSYS routing) |
| Driver | | Move the module's work into `mt8163-cronos-camera.c` (or a V4L2 m2m node); add the two `iommus` ports to the DT; echod's `I420` takes pass 2's output |

**Updated estimate.** Stage 2 took a day of unit time, against the 2-3 weeks the first IMG2O
output was budgeted. What is left:

| Work | Estimate |
| --- | --- |
| Tone: black level, gains, gamma enable and LUT format, to match echod's leveling | 1-2 weeks; GGM is the risk |
| I420: CPU 4:2:2→4:2:0 with the chroma flip | 2-3 days |
| I420 through the MDP route instead | 2-3 weeks |
| Driver and echod integration | 1-2 weeks |

**Total: about 3-5 weeks**, down from 6-10.

### Files (stage 2)

- `../isp-user/kmod/isp2test.c` and `Makefile` (GPL-2.0)
- `../isp-user/tools/kmod.sh`, `tools/pass2.sh`
- `../isp-user/isp2cmp/` (Go): `mkraw`, `tdr`, `ref`, `show`, `psnr`
- `../isp-user/regprobe/main.go`: `-hi`, `-save`, `-exposure`, `-gain`

## Stage 3: in production (2026-10-18)

Stage 3 asked for the H.264 stream to be developed on pass 2, with a picture at least as good as
echod's CPU develop, and echod falling back to the CPU on any error.

**Verdict: done, at a cost in frame rate.** The tone is echod's to within a code. The picture is
better than the CPU's I420 on natural images, mostly in the chroma. The driver runs pass 2 beside
the capture. But pass 1 and pass 2 cannot move data at the same time, so pass 2 has to fit in the
sensor's vertical blanking. While the stream develops on the ISP, the driver lengthens the
sensor's frame to make room: the stream runs at 25.5 frames a second instead of 29.2, on about
a third of the CPU.

### The tone, found

All in the CAM window; offsets from `0x15004000`. The pipeline is 12 bits wide: a 10-bit sample
enters UNP as `x * 4`.

| Block | Registers | Found |
| --- | --- | --- |
| OBC (black and per-channel gain) | `0x500-0x50c` offsets, `0x510-0x51c` gains; EN1 bit 3 | 13 bits each. The offset is signed, on the 12-bit scale, added first; the gain is Q9 (at most 16x), then a clip to 4095. Channel order B, Gb, Gr, R (index 0 is blue even with `PIX_ID` 3). Bit 3 "blacked the picture out" in stage 2 because the gains reset to 0 |
| PGN (second gain) | `0x888/0x88c` gains, `0x880/0x884` clips, `0x890/0x894` offsets; EN1 bit 11 | Same channel order, two Q9 gains a word. Takes the stretch beyond OBC's 16x (dark frames) |
| GGM (gamma) | LUT `0x1000` (R in `[15:0]`, B in `[31:16]`) and `0x1300` (G); enable **`0x1600` bit 0** | 144 entries at the 12-bit inputs 0, 8, …, 512 (64 entries), 528, …, 1024 (32), 1056, …, 2048 (32), 2176, …, 4096 (16). Each entry is the output (10 bits, `[9:0]`) and the rise to the next entry (`[15:10]`, 6 bits, unsigned); the ISP interpolates linearly. With the rise 0 the curve is a staircase. `0x1600` is not cleared by the CAM soft reset, and the SRAM keeps its contents |
| G2C (BT.601) | `0xa00-0xa14` | Video range: echod's integer coefficients doubled (Y 132/258/50, Cb −76/−148/224, Cr 224/−188/−36 in Q9), Y offset 64 (quarters of a code), chroma offset 0 (the output stays signed) |

The GGM output is 10 bits and G2C takes 1020 as the CPU's 255 (with 1023, the ISP's luma came
out one code high at white). The 6-bit rise caps the first entry of a curve steeper than about
1/2 (gamma 1/2.7 rises 101 there). The driver starts that entry higher instead, which lifts only
what is at or below black.

echod's tone maps directly. echod levels a channel as `clamp((2x − black) · gain / (white − black))`
on its 11-bit scale, then applies `^gamma`. On the ISP's 12-bit scale `X = 4x`, that becomes:

- OBC offset `−2·black`;
- the channel's gain times the stretch `2048 / (white − black)`, in Q9: OBC up to 16x, PGN for
  the rest, 256x in all;
- the curve `1020 · (k/4096)^gamma` at the 145 knots k.

The ISP demosaics between OBC and GGM, so the leveling happens before the demosaic and the gamma
after it. echod does the same.

Checked on a 10-bit ramp (all four sites equal, gains 1, a real frame's black, white and gamma):
1530 of 1600 pixels have exactly echod's luma, the rest ±1 (the curve's interpolation), mean
difference 0.008. Only at the black knee does the ISP's demosaic smooth the step echod's
per-pixel reference has. `echod/internal/hardware/camera/isp_cronos_test.go` models the ISP's
leveling and holds it to within a code of echod's tables.

### Pass 1 and pass 2 together

The camera captures continuously, so pass 2 has to run beside pass 1. They share EN1, EN2,
DMA_EN, FMT_SEL and SEL. What was found:

- **Pass 2 fails with any extra DMA enabled.** With IMGO (pass 1's port) set in DMA_EN, it ends
  in `IMG2O_ERR` + `DMA_ERR` after the first tile, even with the TG stopped. The same happens with
  ESFKO or AAO. No bit of TCM_EN, SEL or `0x01c` rescues it. Scenario 3 and submode 0 are the only
  FMT_SEL values under which pass 2 starts at all.
- **Turning IMGO off under pass 1 breaks pass 1's frame.** Pass 1's registers are live.
- **SEL bit 4 (PASS1_DB_EN) double-buffers pass 1's registers.** Pass 1 takes them at a frame's
  first line. Pass 2 uses the values as written. So pass 2 can have the registers while pass 1
  keeps what it took. A 10 ms gap in IMGO_EN with bit 4 set no longer harms a frame.
- **That alone is not enough.** Pass 2 and an IMGO that is actually writing still collide
  (`DMA_ERR`). A frame whose first line comes while pass 2 holds the registers takes pass 2's
  settings and is lost.

  The stage-3 frame-repeat check found this. A slot IMGO did not write comes back byte for byte
  three frames later. An earlier check, which only compared brightness bands, had missed it.
- **The TG's frame counter (`0x44c[23:16]`) moves when a frame's last line is in.** The next
  first line (SOF, INT bit 12) follows after the sensor's blanking: 0.79 ms at the 30-a-second
  frame (1235 lines), 6.4 ms at 1441 lines. One line is 27.0 µs.

So pass 2 runs in the blanking. It starts right after the counter moves and must be done, with
pass 1's values back, before the next first line. With SEL bit 4 set for the window and the
sensor's frame at 1440 lines (38.9 ms, 25.7 a second), it ran clean: 80 runs in a row with no
SOF inside a window, no error, and none of 343 captured frames repeated.

The two do not need double buffering to be correct when the window is in the blanking, but
without it pass 1 would see pass 2's enables (TG1 off, scenario 3) live.

**A wedge outlives a warm reboot.** One bad run, with an empty tile table, left pass 2 started
and the CAM unable to capture. Neither the CAM soft reset nor imgsys `IMG_SW_RST` (`0x1500000c`,
CAM bits 12-14) cleared it, and nor did the next warm reboot. The second reboot did. The ISP power
domain is never switched off: genpd's sync_state is pending, and the command line has
`pd_ignore_unused`. The driver's table and settings are fixed and tested, so this needs a
programming error to happen. The driver still stops using pass 2 after one failure, until the
stream restarts.

The old capture had a race of its own: about one frame in eight was a stale slot. The thread
looked at the TG counter every 0.5–1 ms. The next slot's address has to be set within the 0.79 ms
between the counter and the first line, and when the thread was late, IMGO wrote the frame into
the slot being handed out. The thread now looks every 20–40 µs from 2 ms before a frame is due, and runs at
`SCHED_FIFO` low priority (`sched_set_fifo_low`). With that, `camwatch` saw 0 repeats in hundreds
of frames at 30 a second. The real-time priority also stopped the rare late window: without it, a
3-minute stream had 4 frames dropped for a first line inside the window; with it, none.

### The driver (`patches/mt8163-cronos-camera.c`)

- **DT:** the camera node's `iommus` add `M4U_PORT_CAM_IMGI` and `M4U_PORT_CAM_IMG2O`
  (`config/cronos-techo5.dtsi`). mtk-smi then translates those larb2 ports itself, and the TDRI
  fetch goes through IMGI's port.
- **uAPI: `CRONOS_IOC_DEVELOP`** (`_IOWR('V', BASE_VIDIOC_PRIVATE + 0, struct cronos_develop)`,
  344 bytes, the same layout for 32- and 64-bit callers), on the camera's own video node. It
  takes:
  - a user pointer to the packed RAW10 frame;
  - a user pointer for the I420 out;
  - the black offset (12-bit scale);
  - four gains (R, Gr, Gb, B in 1/512);
  - the 145-point curve.

  It returns the ISP's time. It is handled outside the V4L2 core's lock, so a development does
  not hold up reads.
- **Buffers:** allocated on the first call. IMGI (2.4 MB) and IMG2O (3.84 MB) are non-coherent,
  cached and synced; the tile table is coherent. The table is the stage-2 one (`isp2cmp tdr`),
  static.
- **Streaming:**
  1. The ioctl copies the frame in and writes the registers pass 1 never reads: OBC, PGN, G2C,
     the curve, the DMA addresses.
  2. It hands the run to the capture thread and waits, up to 400 ms.
  3. At the next counter tick the thread runs pass 2 inside the SEL-bit-4 window. It does this
     only if the frame that just ended was at least 38.4 ms long. Then it puts pass 1's values
     back.
  4. If a first line came during the window (INT bit 12), the thread drops that frame. On an
     error or a 15 ms timeout, it resets the CAM and sets pass 1 up again (`camera_pipeline` with
     the current slot), and drops two frames.
  5. The ioctl then turns IMG2O's YUYV into I420. It averages the two lines' chroma, with the
     chroma biased to 128 in 16-bit lanes, four pixels a step. It writes through a two-line
     buffer and `copy_to_user`; writing straight to the caller was slower.
- **Not streaming:** the ioctl powers the ISP itself and runs pass 2 directly.
- **Frame length:** while it develops, the sensor's frame is held at 1440 lines at least
  (`sensor_exposure`). It goes back to the sensor's own one second after the last development.

Costs, measured in the driver:

| Step | Time |
| --- | --- |
| Copy the frame in | 1.4–1.6 ms |
| Cache clean and invalidate | 0.5–0.9 ms |
| Pass 2 | 5.5 ms |
| Out (sync, conversion, copy to the caller) | 8.6 ms idle, 12.2 ms with the camera, display and encoder all using memory: the A53's memory bandwidth |

### echod

- `hardware/camera/isp_cronos.go`: `Frame.ISPI420` maps the frame's tone and calls the ioctl on a
  node it opens once. ENOTTY or EINVAL means an old kernel. On any error it logs once and turns
  itself off for good. `TECHO5_CAMERA_ISP=0` keeps frames on the cores.
- `feature/camstream/h264.go`: `vencEncoder.prepare` tries `ISPI420` first, then `Frame.I420`.
- `hardware/camera/isp_cronos_device_test.go` (`hwdevice`): live frames on both paths, with times
  and PSNR.

### Results

Picture (`isp2cmp`: `tone`, `ctl`, `i420`, `conv`, `cmp` and `dev`, run on the unit). Each
synthetic frame is compared with **the scene itself**: `mkraw -truth`, developed with the frame's
own tone, as a perfect demosaic would. Both develops use echod's tone; "CPU" is echod's
`Frame.I420`.

| Frame | Region | CPU Y / Cb / Cr (dB) | ISP Y / Cb / Cr (dB) |
| --- | --- | --- | --- |
| Coastline photo, sensor noise (read 1.5, shot 0.2) | photo half | 42.13 / 42.98 / 42.97 | **43.37 / 49.84 / 48.57** |
| Succulents (night, gamma 1/2.7), noise | photo half | 37.33 / 37.73 / 37.88 | **37.82 / 45.28 / 42.45** |
| Coastline, no noise | photo half | 43.69 / 45.16 / 45.69 | **44.34 / 51.06 / 49.74** |
| Coastline, noise | test-pattern half | **22.91** / 24.41 / **26.17** | 21.47 / **27.98** / 25.68 |
| Coastline, no noise: zone plate | | 20.89 / 22.52 / 23.42 | **21.66 / 43.08 / 42.35** |
| Coastline, no noise: 1-8 px black/white stripes | | **18.32** / 18.92 / 23.31 | 14.80 / **20.20 / 23.87** |

On natural images the ISP is ahead, by up to 1.2 dB in luma and 5–7 dB in chroma. On test
patterns the ISP's luma is softer: an edge spreads over 3 pixels where echod's per-pixel luma
keeps it in 1–2. Seen as a PNG crop, the same stripes show echod's I420 with strong
yellow/blue fringes and zipper patterns, and the ISP's nearly without. On the 1-pixel stripes,
the ISP gives an even gray; echod gives an alternating pattern that is wrong.

Noise-free and with gamma 1/2.7, the ISP's luma on the succulents was 33.4 dB against the CPU's
40.2. Every pixel at the synthetic frame's pedestal sits exactly at black, where the steep
curve's first entry is lifted. Real noise spreads them out (the noisy row above).

On real frames there is no truth, so the two develops are compared with each other. Live in a
dark room (white 1251 of 2047, gamma 1/2.2), ISP against CPU: Y 35.6, Cb 32.8, Cr 33.5 dB, the
ISP 0.6 codes brighter on average. The ISP clips each sample at black before the demosaic
averages it, where the CPU averages first. By eye the tone is the same and the ISP's noise is
finer, with less color blotching. The comparison pictures are kept in scratch, not in the repo.

Stream (`feature/camstream` `TestDeviceH264Rate`, `HW_MAXSHUT=1220`, 20 s, test binary on the
unit, CPU from `time`):

| Develop | Pictures a second | Bitrate | CPU (user + sys) |
| --- | --- | --- | --- |
| ISP, final kernel (#23, thread at `SCHED_FIFO`), 180 s | 25.5 | 10.2 Mbit/s | 49.8 + 87.3 s over 183 s = 0.75 cores |
| ISP, kernel #22 (thread not real-time), 20 s and 180 s | 24.6 | 9.9 Mbit/s | about 1.1 cores |
| CPU (`TECHO5_CAMERA_ISP=0`), 20 s | 29.2 (27.8 on a second run) | 11.7 Mbit/s | 38.8 + 7.5 s over 23 s = 2.0 cores |

That is about 29 ms of CPU a frame on the ISP against 72 on the cores. Most of what is left on the ISP
path is the kernel's copy in and conversion out, plus the stream's own read and pipe copies, which
are the same on both paths. No pass-2 warning came in any of the runs.

### What remains

- **Frame rate:** 25.7 a second at most while the ISP develops, since a 5.4 ms pass has to fit in
  the blanking. Faster would need any of these:
  - pass 2 beside an active IMGO, which this stage could not find (the vendor's command-queue and
    double-buffer flow, CQ format `{(n−1)<<16 | 0x4000+offset, data}` / `0xfc000000`, works as a
    register loader, but SEL bit 5 PASS2_DB_EN only gives `DMA_ERR`);
  - a shorter line;
  - the MDP route.
- **CPU:** the copy in could go, since pass 2 could read the frame's own DMA slot. But echod
  reads frames with `read()`, which carries no sequence number.
- **CDRZ/MDP scaling, BNR, LSC and NR** are unused.

### Shipped (2026-10-18)

- Kernel #23 is on the Show 5's boot partition. The previous one is in `/data/boot-prev.img`.
- Slot a holds rootfs v1.2.19-mainline.12 (64-bit) and has committed good.
- With the camera running, the installed kernel's develop gave output bit-identical to the stage-2
  recipe's.

### Files (stage 3)

- `../patches/mt8163-cronos-camera.c`: pass 2, `CRONOS_IOC_DEVELOP`, the capture thread's timing
- `../config/cronos-techo5.dtsi`: the camera node's IMGI and IMG2O IOMMU ports
- `../isp-user/isp2cmp/develop.go`: `tone`, `ctl`, `i420`, `conv`, `cmp`, `dev`
- `../isp-user/isp2cmp/main.go`: `mkraw -truth -noise -shot`
- `../isp-user/kmod/isp2test.c`: `run … TOFF TCLR TSET`, `vs`, `wait`
- `echod/internal/hardware/camera/`:
  - `isp_cronos.go`
  - `isp_cronos_test.go`
  - `isp_cronos_device_test.go`
  - `camera_spot.go` (a stub)
- `echod/internal/feature/camstream/h264.go`
- `echod/internal/feature/display/camtest_path.go`

## Stage 4: the MDP makes the I420 (2026-10-19)

Stage 4 asked the MDP (RDMA, RSZ, WDMA, WROT at `0x14001000`-`0x14006fff`) to make the stream's
I420 from pass 2's output, in place of the conversion on the cores (stage 3's "Out" step: the
sync, the 4:2:2 to 4:2:0 conversion and the copy to the caller), without a worse picture.

**Verdict: done for the luma, and the chroma's layout; the cores still average the chroma.** The
MDP runs on mainline from the camera driver, memory to memory, with no firmware and no GCE: RDMA
reads IMG2O's YUYV, RSZ1 passes it through, WROT writes the luma straight into the caller's
buffer and the chroma as 4:2:2 planes. The cores then average each two chroma lines into the
caller's 4:2:0 planes. The output is the cores' I420 to the bit. The kernel's CPU for a frame went
from about 8.1 ms to about 5.3 ms; the stream's own process uses 0.69 cores instead of 0.76, at the
same 25.2-25.3 pictures a second.

The chroma cannot go all the way on the MDP because IMG2O's chroma is two's complement (stage 2,
"The output format"). Every color matrix of the MDP (RDMA's sixteen, WROT's) takes 128 as no
color, and WROT's own 4:2:0 filter averages the two lines' bytes as they are. That average is
right only where the two lines' chroma have the same sign, after a flip of bit 7, and right
without the flip where they differ: no fix after the fact can tell the two apart. So the flip has
to come before any averaging, and only the cores can do it.

### The route

The task allowed two routes:

- **A, the direct link**: pass 2 into `MDP_CAMIN`, then PRZ0 and WDMA or WROT, tile by tile in
  the sensor's blanking, with the ISP's MDP crop and the CDRZ in the tile table. MediaTek's own
  camera preview took this path, so its link probably carries chroma the MDP takes. DpFramework's
  `DpIspStream` builds this path, but run offline (below) its tile calculation stops with
  `ISP_MESSAGE_TILE_FUNC_CANNOT_FIND_LAST_FUNC_ERROR` for every configuration tried: its
  configuration struct is 0x170 bytes, 0x28 more than the tile driver's, and those fields are
  unknown. Without that program the ISP's MDP-side bits (`0x0d8` bit 23, `0x01c` bit 23, the
  CDRZ, `MDP_CROP`) would be black-box work inside the 6 ms window.
- **B, memory to memory**: pass 2 writes YUYV as before, then an MDP job. DpFramework's
  `DpBlitStream` builds it, and the capture worked first time. **This is the route taken.**

### Reading the program out of DpFramework (`isp-user/harness/dpcap.c`)

`dpcap` runs the unit's own `libdpframework.so` under the vcodec runtime (on the unit, or offline
under qemu-arm) and fakes `/dev/mtk_cmdq`. The executable defines `open()` and `ioctl()` and is
linked with `--export-dynamic`, and the bionic linker finds those before libc's. The library:

1. asks for the device tree's data (`CMDQ_IOCTL_QUERY_DTS`, 3764 bytes): 511 event numbers, which
   dpcap gives back as their own index, and the mutex base last;
2. submits each job as `CMDQ_IOCTL_ASYNC_EXEC` (nr 20): a list of `struct op_meta` from Amazon's
   GPL `drivers/misc/mediatek/mdp/mdp_def_ex.h`. Each op is a write with a mask, a poll or an event
   wait. Each register is an engine index (`ENGBASE_*`, `cmdq/v2/mt8163/cmdq_engine.h`: MMSYS,
   RDMA0, RSZ0, RSZ1, WDMA, WROT0, TDSHP0, MUTEX, IMGSYS, CAMSYS) and an offset.

`dpcap blit YUYV 1600 1200 I420 1600 1200` gives 141 ops: RDMA0 → RSZ1 (bypassed) → WROT0 in
two tiles, 1024 and 576 pixels wide, with mutex 6, single SOF, and waits on
`CMDQ_EVENT_MDP_RDMA0_EOF` (52) and `CMDQ_EVENT_MDP_WROT_WRITE_EOF` (61). The I422 output differs
in three values (WROT's control and buffer size). `DP_COLOR_ENUM` is mainline mdp3's
`MDP_COLOR()` without the compression bit: YUYV `0x281045`, I420 `0x6c0848`, I422 `0x680849`.

### On the unit (`isp-user/kmod/mdptest.c`)

`mdptest` borrows the camera's device for buffers in the IOMMU domain every multimedia master
shares, enables the MDP's clocks in mmsys and larb0's MDP ports, and lets a script write the MDP
blocks, MMSYS's MDP routing words (`0x01c-0x02c`, `0x044-0x054`) and mutex 6's words, and pulse the
MDP's reset bits; nothing of the display's. It ran beside echod and the display. Findings:

- **Power and translation.** The MM domain and larb0 are on for the display. CG_CON0 had the
  MDP's clocks gated (`0xffe06bfc`); with them on, all the blocks read. larb0's `MMU_EN`
  (`0x14016f00`) read `0x3` (the display's OVL0 and RDMA0 only): mtk-smi writes it when larb0
  resumes, and the display never lets it suspend. Setting bits 14 (RDMA) and 16 (WROT) is enough;
  IOVAs from the camera's device work for them. A WROT plane deliberately sized past its buffer
  faulted in the IOMMU (`larb=0 port=16`), nothing else.
- **The captured program works as is**, replayed with CPU writes and the GCE events replaced by
  polls. Luma: every byte equals IMG2O's. Chroma (I420 out): the top line of each pair, unchanged.
- **WROT's filters**, `VIDO_MAIN_BUF_SIZE` (`0x008`), which DpFramework leaves alone:

  | Bits | Value | Effect |
  | --- | --- | --- |
  | [6:4] (4:2:0, vertical) | 0 | the first line of each pair |
  | | 1 | (1, 2, 1) / 4 over the line above, the first and the second |
  | | 3 | (a + b + 1) / 2 of the pair: the cores' arithmetic exactly |
  | | 5, 6, 7 | the second line |
  | [2:0] (horizontal) | 1, 2, 4 | filtered; 0, 3, 5-7: none |

  With I422 output the horizontal bits must be 0, or the chroma moves by one sample.
- **No signed chroma anywhere.** RDMA's `TRANSFORM_0` (`0x200`, enable bit 16, matrix [27:24]) and
  WROT's `VIDO_MAT_CTRL` (`0x084`) were swept on a ramp of chroma bytes: every setting is
  monotonic in the unsigned byte, with 128 as no color.
- **When it is done.** `VIDO_INT` (`0x01c`) and the mutex's `INTSTA` do not tell. What does:
  RDMA's `MON_STA_1` (`0x408`) state [18:8] back to `0x100` (idle), and WROT's debug word
  `0x1400` (select at `0x018`, read at `0x0d0`), whose [15:0] counts the line being written and
  is 0 when the tile is out. The 1024-pixel tile takes 3.4 ms, the 576 one 1.9: 5.4 ms a frame.

### The driver (`patches/mt8163-cronos-camera.c`)

- **DT** (`config/cronos-techo5.dtsi`): the camera node adds RDMA, RSZ1 and WROT as its fourth to
  sixth `reg`; the clocks `mdp-rdma`, `mdp-rsz1`, `mdp-wrot`; their three mmsys resets; and
  `mediatek,mmsys`, `mediatek,mutex` and `mediatek,mdp-larb` (larb0). A node without
  `mediatek,mdp-larb` keeps the cores' conversion.
- **Per frame**, after pass 2: the caller's luma plane is pinned and mapped (one run of IOVA, its
  start on a cache line, else the cores do the frame); larb0 is held (and with it the MM domain),
  the three clocks on, larb0's two `MMU_EN` bits set; the frame and the two tiles are written as
  captured, the waits polled (30 ms at most); WROT writes the luma into the caller's buffer and the
  4:2:2 chroma into a kernel buffer. The cores then average and flip the chroma on NEON (`SRHADD`,
  then bit 7: the cores' formula to the bit), eight lines a step, into the caller's U and V.
- **Checks**: the last line of each tile in each plane is set beforehand to a value the ISP never
  writes (luma 0, chroma `0x80`), and must have changed. Any error or timeout resets the three
  engines (mmsys resets) before the pages are released, logs once, and the cores make the I420 until
  the stream restarts, from IMG2O, which is still there.
- **Parameters** (`/sys/module/mt8163_cronos_camera/parameters/`): `mdp` (1; 0 has the cores make
  the I420, for tests), `times` (the last development's steps, in microseconds: frame in, luma
  mapped, MDP, unmapped, chroma, whole out).
- IMG2O's per-frame cache clean is gone: the cores only ever read it, after an invalidate.

### Results

Quality: `isp2cmp dev` on the same frame with `mdp` 1 and 0, on the installed kernel. A synthetic
frame with sensor noise and a real frame from the camera (a dark room): **byte-identical**
(`cmp`) both times.

Time on the cores for one frame, streaming (`times`, µs, typical):

| Step | Cores (`mdp=0`) | MDP |
| --- | --- | --- |
| Frame in (copy, cache clean) | 1750-1850 | 1700-1850 |
| Luma pages pinned and mapped | – | 800-1300 |
| MDP (the cores sleep) | – | 5400 |
| Pages unmapped, chroma invalidated | – | 375-410 |
| Chroma to 4:2:0 for the caller | – | 2100 (2400 without NEON) |
| Whole out, wall | 6200-6400 | 8700-9000 |

So the kernel's CPU a frame is about 8.1 ms on the cores' path and about 5.3 ms with the MDP.
Kernel #23 also cleaned IMG2O each frame (0.5-0.9 ms).

Stream (`feature/camstream` `TestDeviceH264Rate`, `HW_MAXSHUT=1220`, the test binary on the unit,
echod stopped), on the installed kernel (#28):

| Develop | 180 s: pictures a second | Process CPU (user + sys) | Whole system busy |
| --- | --- | --- | --- |
| MDP | 25.3 | 51.5 + 74.7 s = 0.69 cores | 237.5 s |
| Cores (`mdp=0`) | 25.2 | 50.5 + 88.3 s = 0.76 cores | 248.0 s |

Two 20-second runs each on the test kernel: the whole system busy for 29.9 and 29.4 s with the MDP,
31.4 and 31.0 s on the cores; kernel #23 31.2 and 31.1 s. The rate is the same either way: pass 2
in the blanking still sets it. No MDP or pass-2 warning came in any run. The IOMMU faults seen
around the runs are older than this stage: larb0 port 0 at boot (the display's handover) and
larb3 port 4 when the encoder stops at a test's end, the same on kernel #23.

### What remains

- **The chroma on the MDP**, which needs it unsigned before WROT. Candidates: the direct link
  (route A), if its chroma is unsigned, with WROT's filter 3 doing the average in hardware; or an
  ISP bit that makes IMG2O's chroma unsigned (the CDRZ's output, or `0x0d8`/`0x01c` bit 23, which
  DpFramework sets for the link). That would save about 2.5 ms a frame more.
- **The frame in**: IMGI could read the caller's frame where it is, as WROT writes its luma. The
  mapping costs about what the copy does (0.8-1.3 ms for the luma's 1.9 MB), so the gain is small.

### Files (stage 4)

- `../patches/mt8163-cronos-camera.c`: the MDP job, the chroma on NEON, `mdp` and `times`
- `../config/cronos-techo5.dtsi`: the MDP's registers, clocks, resets, mmsys, mutex and larb0
- `../isp-user/harness/dpcap.c`, `tools/build.sh`, `tools/run.sh` (`PROG=dpcap`)
- `../isp-user/kmod/mdptest.c`, `kmod/Makefile`, `tools/kmod.sh`

## Stage 5: route A, pass 2 straight into the MDP (2026-10-19)

Stage 5 asked for the direct link (route A): pass 2 into `MDP_CAMIN`, the chroma made 4:2:0 on the
MDP if the link carries it unsigned. The quality had to stay byte-identical, and the result had to
be integrated with route B as the fallback.

**Verdict: done, and byte-identical.** The link carries the chroma around 128. WROT's own 4:2:0
filter 3 then gives the cores' I420 to the bit, so the whole I420 is made in hardware, written
straight into the caller's buffer. The driver uses it by default and falls back to route B on any
error. The kernel's CPU time per frame drops by about 2 ms. Two things are unchanged: the stream
(25.4 pictures a second) and, within noise, the whole system's CPU time.

### DpFramework's ISP stream, made to run offline (`isp-user/harness/dpcap.c`)

The 0x28 bytes past the tile driver's 0x148 belong to `DpWrapper_ISP`. They are read in its
`onConfigFrame`, `initTilePath` and `onConfigTile`, with the struct copied to the wrapper at
`+0x204`. They hold buffers, not tile settings:

| Offset | Use |
| --- | --- |
| `0x148` | kept by `initTilePath` (wrapper `+0x200`) |
| `0x150` | the ISP command queue's base, written to CQ1/2/3's base (`0x0ac`/`0x0b0`/`0x0b4`) |
| `0x15c` | the tile table's address for the ISP: TDRI's base (`0x204`), plus each tile's offset |
| `0x160` | where the library writes the tile table (a CPU pointer; NULL crashed the worker) |
| `0x168` | which command queue (0, 1, 2) |
| `0x16c` | a flag; non-zero calls the command's virtual `+0x5c` |
| `0x14c`, `0x154`, `0x158`, `0x164` | not read by these three functions |

The tile calculation's `ISP_MESSAGE_TILE_FUNC_CANNOT_FIND_LAST_FUNC_ERROR` was something else:

- MDP's input function (10003) names ISP function 296 (the MDP crop) as its source.
- The ISP's function list only reaches 296 through the resampler (292, RSP).
- So **`top.rsp_en` (`0x1c`) must be 1**, besides `top.mdp_crop_en`.

`tpipe_test` now also runs `tile_main_platform` with an `MDP_TILE_CONFIG_STRUCT` (`mdp.N=value`).
Its dump names the 23 words:

1. source and tile sizes;
2. the engines' enables (ISP, RDMA0, PRZ0, PRZ1, TDSHP0, WROT0, WDMA);
3. the MMSYS routing words (MOUT and SEL);
4. `disable_isp_direct_link` (the byte at `0x58`).

With RSP on and a tile-table pointer given, `dpcap isp` prints the whole program. Options:

- `DPCAP_SIZE=WxH` sets the size.
- `DPCAP_ROT` sets the rotation. 180 makes the library choose WROT instead of WDMA (PRZ1 then).

The program, per tile:

1. The MDP's tile registers.
2. The routing (ISP_MOUT 1 or 2 to PRZ0/PRZ1, PRZ's MOUT and SEL, WDMA/WROT SEL).
3. The mutex single-started and polled.
4. TDRI's base set to the tile's entry.
5. **`0x0d8` bit 23, then a poll of `0x01c` bit 23**, then the MDP's end-of-frame event.

The program's ISP side never sets EN1/EN2/DMA_EN: those come from the camera HAL's command queue,
which we do not have.

### The ISP side, from MediaTek's MT6582 camera sources

The library's own dumps call the platform "MT6582", and a public MT6582 Android tree has the camera
HAL that fills that command queue (`mtkcam/core/imageio`, `PostProcPipe.cpp`, `isp_function.cpp`,
`cam_path_pass2.cpp`, `isp_reg.h`). Its register map matches this ISP's (CAM `0x15004000` +
offset):

- `0x01c` is **`CAM_CTL_PIX_ID`**: PIX_ID [1:0], CDP_EDGE [19:16], **MDP_SEL [20]**,
  ISP_FRAME_DONE_SEL [23]. `0x0d8` is its SET register: DpFramework's "kick" only sets
  ISP_FRAME_DONE_SEL. The HAL always sets MDP_SEL.
- `0x008` EN2: G2C [0], C42 [1], CDRZ [17], **UV_CRSA [23]** (the resampler, CRSP). The HAL always
  sets UV_CRSA ("mdp direct-link") and clears CDRZ for MT6582 ("TEST_MDP").
- `0x054` TCM_EN: TCM_CTL [0], TCM_IMGI [1], TCM_CDRZ [16], TCM_UNP [20], TCM_RSP [26],
  TCM_LOAD [27], CQ_APB_2T [28], TILE_STALL [29], TDR_EN [31]. The HAL writes `0x98100003` for a
  tiled pass 2.
- `0x110`/`0x114` are **CAM_CTL_CROP_X/Y**: MDP_XSTART [12:0], MDP_CROP_EN [15], MDP_XEND [28:16];
  the rows likewise. The tile table does not load them: the CPU writes each tile's columns.
- `0x018` CAM_CTL_SEL: TDR_SEL [6] (the tile sizes from the table), PASS2_DB_EN [5], PASS1_DB_EN [4].
- The tile word's control bits: TILE_EDGE [3:0], TILE_IRQ [4], LAST_TILE [5], CDP edges [15:12],
  CDRZ_EN [16], RSP_EN [17], IMGO_EN [18], IMG2O_EN [19], MDP_CROP_EN [20].

### On the unit (`isp-user/kmod/isp2test.c` and `mdptest.c`, `isp-user/tools/link.sh`)

Both harness modules were loaded together, with the capture stopped. The captured program ran with
two changes: the GCE's events became polls, and mutex 6 took the place of 5. With only those, the
ISP ran through its stages (the debug port counted the whole tile out) and then waited, while WROT
wrote nothing. What it took, one by one:

| Change | Effect |
| --- | --- |
| `CAM_CTL_PIX_ID` = MDP_SEL \| 3 | none alone |
| EN2 + UV_CRSA | pass 2 completes, but the MDP still gets nothing |
| TCM_EN `0x98100003` (TCM_CTL, TCM_IMGI, TCM_LOAD, CQ_APB_2T) | **WROT writes**: 640x480, luma equal to IMG2O's |
| WROT filter `[6:4]` = 3 (outside DpFramework's write mask) | the chroma is **(a + b + 1) / 2 of the unsigned bytes** |
| CROP_X/Y per tile, TDR_SEL (`0x018` = `0x40`, not `0x50`) | the whole frame, three tiles |

What was ruled out on the way:

- Each implemented bit of MUX_SEL and MUX_SEL2 (`0x074`/`0x078`).
- Each of EN1, EN2 and DMA_EN's unknown bits.
- The PRZ1 selections.
- The CDRZ (on or off).
- `0x0d8` before or after the start.

The PRZ0 → WDMA path, the one DpFramework picks for rotation 0, wrote nothing in any of these
trials. The PRZ1 → WROT path, the rotation-180 program run without rotation, is the one that works.
WROT's matrix (`0x084` `0x81`, as DpFramework sets it for the link) maps to video range a second
time: it must stay 0.

**IMG2O and the link together stall.** With IMG2O enabled as well, every tile times out. The
first tile's WROT output is right, and IMG2O's output is partial. So the fallback cannot get IMG2O
from the same run.

Results: one start a tile, from the table DpFramework makes without the CDRZ. The tiles are 762,
756 and 82 pixels wide, taking 2.58, 2.55 and 0.33 ms: 5.46 ms against route B's pass 2 of 5.40 ms.
The output was compared with the cores' conversion of IMG2O's YUYV (bit 7 flipped, (a + b + 1) / 2):

| Frame | Y | U | V |
| --- | --- | --- | --- |
| A captured frame (a dark room), linear tone | 0 bytes differ | 0 | 0 |
| The synthetic frame (photo, bars, zone plate, stripes), linear tone | 0 | 0 | 0 |

### The driver (`patches/mt8163-cronos-camera.c`)

- **DT**: the camera node adds the link's gate, `mdp-camin` (`CLK_MM_CAM_MDP`), to its clocks, and
  `MT8163_MMSYS_RST_CAM_MDP` to its resets. A node without `mdp-camin` keeps route B.
- **Before pass 2** (in the ioctl):
  - The caller's whole I420 is pinned and mapped as one run of IOVA, marked, and checked after.
    The marks are the last line of each tile in each plane, set to 0, a value video range never
    writes.
  - The MDP is powered, reset, and set up for the frame: RSZ1 passed through, WROT writing the
    three planes with filter 3.
- **Pass 2** (`run_pass2_link`, in the blanking as before), per tile:
  1. The MDP's tile: routing CAMIN → RSZ1 → WROT, the offsets and sizes, the mutex started.
  2. The tile's control word and MDP crop columns.
  3. TDRI at the tile's entry, then the start.
  4. A poll for PASS2_DON.
  5. A poll for WROT's line counter at 0.

  The registers are put back for pass 1 afterwards (CROP_X/Y, TCM_EN, PIX_ID, EN2).
- **Fallback**: any error (pass 2, the MDP, a mark left) stops and resets the MDP, releases the
  pages, and runs pass 2 again for route B in the same call. The link is then not tried until the
  stream restarts. Tested by clearing ISP_MOUT_EN from `mdptest` during a development: "MDP link:
  -110; route B until the stream restarts", and the I420 was identical.
- **Parameters**: `link` (1; 0 keeps route B, for tests), `mdp` (0 still means the cores),
  `times` (on route A: frame in, I420 mapped, pass 2 + MDP, unmapped, 0, the same).

### Results (test kernel #36)

**Quality**: `isp2cmp dev` (the driver's ioctl, echod's tone). On the synthetic and the captured
frame, route A, route B (`link=0`) and the cores (`mdp=0`) gave the same bytes (`cmp`).

**One development**, the camera stopped (`isp2cmp dev -n 5`, the ISP powered up and down each
time):

| | Route A | Route B |
| --- | --- | --- |
| Call | 9.7-12.0 ms | 17.6-19.4 ms |
| `times` (µs): in, map, ISP/MDP, unmap, chroma, out | 1641, 1421, 5586, 623, 0, 623 | 1917, 1284, 5474, 805, 3017, 10583 |

**Streaming** (`camstream` `TestDeviceH264Rate`, `HW_MAXSHUT=1220`, echod stopped):

| | Route A | Route B |
| --- | --- | --- |
| Rate (60 s, four runs) | 25.4, 25.4, 25.3, 25.5 | 25.5, 25.4, 25.3, 25.4 |
| Kernel per frame (`times`: in + map + unmap + chroma) | 1.8 + 0.9 + 0.4 = **3.1 ms** | 1.8 + 0.7 + 0.4 + 2.1 = **5.0 ms** |
| Process (test + encoder), user + sys over 60 s | 21.4 + 25.6, 21.2 + 25.0 s | 18.3 + 26.3, 18.2 + 26.7 s |
| Whole system busy (60 s) | 8201, 8143 ticks | 7992, 8007 ticks |
| The same, 120 s, with cpu0's mean frequency | 15960 ticks at 1150 MHz | 15870 ticks at 1146 MHz |

The kernel saves about 1.9 ms of CPU a frame, and the system time falls by 1.4 s a minute. But the
process's user time rises by about 3 s a minute, and the whole system's busy time comes out the
same within 1%. The I420 now reaches the encoder through echod's pipe (`vencpipe`) with none of it
in the caches: route B's cores had just written the chroma. The rest of the user-time rise is not
explained. The rate does not change: pass 2 in the blanking still sets it.

"Pass 2 ran into a frame" came once in about 7,600 route-A frames (none in a 120 s run of each
route). Route A's pass 2 takes about 0.15 ms longer than route B's in the 6.3 ms blanking.

### What remains

- **Zero copy to the encoder**: develop straight into the encoder's input buffer instead of a heap
  buffer that echod then writes down a pipe. That is where route A's saving would show in the
  whole system.
- **IMG2O beside the link**, for a fallback from the same run: it stalls today (above).
- **The frame in**: IMGI reading the caller's RAW where it is (unchanged from stage 4).

### Files (stage 5)

- `../patches/mt8163-cronos-camera.c`: `link_table`, `link_tiles`, `run_pass2_link`, `link_*`, the
  fallback, `link`
- `../config/cronos-techo5.dtsi`: `mdp-camin`, the CAM_MDP reset
- `../isp-user/harness/dpcap.c`: the wrapper's extra fields, `DPCAP_SIZE`, `DPCAP_ROT`, the tile
  table printed
- `../isp-user/harness/tpipe_test.c`: `mdp.N=` (`tile_main_platform` with an MDP config)
- `../isp-user/tools/link.sh`: route A on the unit through `isp2test` and `mdptest`
