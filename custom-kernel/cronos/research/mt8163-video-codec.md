# MT8163 hardware video codec on the mainline kernel

Status: done (2026-10-18): H.264 decode and encode run on the hardware; H.265 decode since
2026-10-19. See "Results" and "H.265" at the end, and the README.

## Short answer

The MT8163 has a hardware H.264 decoder, an H.265 decoder, and an H.264 encoder, which sit in
the same MediaTek VDEC/VENC IP family as the MT8173's. Unlike the MT8173, Fire OS does **not**
use a coprocessor (VPU) for video. Fire OS drives the hardware **from the AP**. The closed
32-bit userspace library `libvcodecdrv.so` parses the bitstream and writes the codec registers
itself. It reaches them through a small kernel character device, `/dev/Vcodec`, which handles
locking, interrupt waits, buffers and register mmap. Mainline has no driver for this model:
every mainline `mtk-vcodec` platform depends on VPU or SCP firmware.

- **MT8173 VPU firmware reuse:** unlikely to work. The decoder is a long shot, and the encoder
  is unsafe (details below).
- **Most practical path:** a small new kernel driver that provides the `/dev/Vcodec` interface,
  plus the vendor's armv7 bionic libraries loaded on Alpine through a bionic loader shim. This
  gives H.264 decode and encode through MediaTek's private C API, not through V4L2. It is
  roughly 3–6 weeks of focused work, and fragile.
- **Clean route:** a mainline V4L2 stateless driver, reverse-engineered from the library's
  register writes. This is months of work.

Prerequisites are already proven on this kernel. Power domains, `vdecsys`/`vencsys` clocks,
SMI larbs 1 and 3, and the M4U all probe. The VENC-domain JPEG encoder at `0x17003000` already
works as `/dev/video0` (`mtk-jpeg-enc`).

## 1. Hardware inventory

The table below comes from Amazon's `sources/amazon-kernel/arch/arm64/boot/dts/mediatek/mt8163.dtsi`.

| Block | Vendor node / compatible | Reg | IRQ | Clocks / power |
| --- | --- | --- | --- | --- |
| VDEC global con | `vdecsys@16000000` (`mt8163-vdecsys`), `vdec_gcon@16000000` | `0x16000000` (0x1000 / 0x10000) | – | provides `CLK_VDEC_CKEN`, `CLK_VDEC_LARB_CKEN` |
| SMI larb1 (VDEC) | `larb@16010000` | `0x16010000` | SPI 213 | VDEC_CKEN, LARB_CKEN; `POWER_DOMAIN_VDEC` |
| VDEC core | `vdec@16020000` (`mt8163-vdec`) | `0x16020000`, size `0x10000` (one window) | SPI 212 | `CLK_TOP_VDEC_SEL`; VDEC domain |
| VENC global con | `vencsys@17000000` | `0x17000000` | – | `CLK_VENC_CKE0..3` |
| SMI larb3 (VENC) | `larb@17001000` | `0x17001000` | SPI 207 | CKE1, CKE0; `POWER_DOMAIN_VENC` |
| H.264 encoder | `venc@17002000` (`mt8163-venc`) | `0x17002000`, size `0x1000` | SPI 206 | VENC domain (clocked from `mm_sel`; MT8163 has no `venc_sel`) |
| JPEG encoder | `jpgenc@17003000` | `0x17003000` | SPI 208 | CKE2; already used by mainline |
| MD32 coprocessor | `md32@10020000` (`mt8163-md32`, disabled) | `0x10020000`+0x38000 (TCM), `0x10058000`, `0x10059000` | SPI 174 | `CLK_INFRA_SCPSYS` |
| MDP | `mdp_rdma/rsz0/rsz1/wdma/wrot/tdshp` | `0x14001000`–`0x14006000` | SPI 178–183 | MM domain |

The vendor DT gives the VDEC a single 64 KiB window. Mainline's `mt8173.dtsi` splits the same
address range into `misc 0x16020000`, `ld 0x16021000`, `top 0x16021800`, `cm 0x16022000`,
`ad 0x16023000`, `av 0x16024000`, `pp 0x16025000`, and `hwd/hwq/hwb/hwg 0x16026800–0x16028400`,
with IRQ SPI 204. The MT8163 decoder is at the **same base address** as the MT8173's. The
encoder is not: MT8163 has it at `0x17002000`, MT8173 at `0x18002000`, with a second VP8
encoder at `0x19002000`.

The M4U ports are already in mainline's `include/dt-bindings/memory/mt8163-larb-port.h`:

- **VDEC (larb1):** `VDEC_MC/PP/VLD/AVC_MV/PRED_RD/PRED_WR/PPWRAP`.
- **VENC (larb3):** `VENC_RCPU/REC/BSDMA/SV_COMV/RD_COMV/CUR_LUMA/CUR_CHROMA/REF_LUMA/REF_CHROMA`.

### Is there a VPU?

No video VPU exists on this chip. The MD32 does sit at the MT8173 VPU's address:

- The vendor GCE table names `scp_sram_base = 0x10020000` and `scp_base = 0x10050000`.
- These are exactly the MT8173 VPU's `tcm` and `cfg_reg` addresses.

However, the MD32 firmware Fire OS ships, `vendor/firmware/md32_p.bin` (23,888 B) and
`md32_d.bin` (24,235 B), is a power-management helper. It is a "MD32 LK" image with DVFS/EMI
shuffling, SPM sleep, I2C semaphores, a watchdog and a touch-GPIO IPI. It contains no codec code.

### The Fire OS codec stack

The stack comes from the vendor tar's `vendor/lib/`. Everything is **32-bit ARM**
(armeabi-v7a, bionic). There is no `lib64` codec code.

- **OMX wrappers:** `libMtkOmxVdecEx.so` and `libMtkOmxVenc.so`, on top of `libMtkOmxCore.so`
  and `mtk_omx_core.cfg`.
- **`libvcodecdrv.so` (945 KB):** the actual codec driver. Its build path is
  `vendor/mediatek/proprietary_mt8163/hardware/libvcodec/mt8163/...`.
  - It contains register-level HALs: `H264_HAL_WriteVLD/WriteVLDTOP/WriteAVCVLD/WriteMISC`,
    `H264_HAL_SetSPSAVLD/SetPPSAVLD/SetSHDRAVLD1/SetPRefPicListReg`, `H265_HAL_*`
    (HEVC VLD, UFO), and `H264_HAL_WaitISR`.
  - It has register-trace debug strings, for example `RISCWrite_VLD(%d, 32'h%x); /* base ... */`,
    covering `SYS/MISC/VLD/VLD_TOP/AVC_VLD/AVC_MV/HEVC_VLD/MC/PP/UFO/GCON/CRC`. These block names
    match mainline MT8173's VDEC reg-names.
  - It contains the string "8163/8167/8173 PDN setting", which shows that one code base covers
    all three chips.
  - It embeds VENC RCPU microcode (`ucH264RCPUCode_6797`, `ucH264mt8137_2017_0911_normal`).
  - `H264_GetHWRegBase` (disassembled at `0x53994`) selects the encoder MMIO base by chip ID:
    `0x17002000` (MT8163), `0x18002000` (the MT8173-style map), `0x1701e000`, `0x15009000`.
    It then mmaps that base through the kernel driver ("mmap VENC_HW_BASE"). This is the same
    H.264 encoder IP at a different base address per chip.
- **Formats the hardware does not cover:** `libvp8dec_sa.ca7.so`, `libvp9dec_sa.ca7.so`,
  `libvc1dec_sa.ca7.so`, `libmp4enc_sa.ca7.so`, `libvp8enc_sa.ca7.so` and
  `libhevce_sb.ca7.android.so` are CPU (Cortex-A7-tuned) software codecs. This means
  **VP8 and VP9 decode are not in hardware on MT8163**, unlike on MT8173.
- **Decoders listed in `media_codecs_mediatek_video.xml`:** AVC, HEVC, MPEG2/4, H263, VC1
  and DivX/XviD.
- **Encoders listed in the same file:** AVC, HEVC (software), MPEG4 and H263.
- **Kernel interfaces the libraries use:**
  - `libvcodec_utility.so` → `/dev/Vcodec`, with ioctls `VCODEC_LOCKHW/UNLOCKHW`,
    `VCODEC_WAITISR`, `VCODEC_INC/DEC_{DEC,ENC}_EMI_USER`,
    `VCODEC_ALLOC/FREE_NON_CACHE_BUFFER`, `VCODEC_PWR_USER`, plus mmap of HW bases.
  - ION through `libion`/`libion_mtk.so` → `/dev/ion`.
  - `libm4u.so` → `/proc/m4u` (MVA allocation and port config).
  - `libbwc.so` → `/dev/MTK_SMI` (bandwidth scenario).
  - `libmtk_drvb.so` → `/proc/device-tree/chosen/atag,devinfo`.
  - `libdpframework.so` (MDP color conversion) → `/dev/mtk_cmdq` and `/dev/mdp_sync`.
- **No kernel modules for video:** `vendor/lib/modules/` holds only Wi-Fi/BT and netfilter
  modules. The `/dev/Vcodec` driver is built in (MediaTek's
  `drivers/misc/mediatek/videocodec/`). The partial `sources/amazon-kernel` tree does not
  include it.

## 2. Compared with mainline

The mainline `mtk-vcodec` driver in `drivers/media/platform/mediatek/vcodec/` supports two
firmware-mediated designs:

- **MT8173:** stateful, using the VPU (`mediatek,mt8173-vpu`, `vpu_p.bin`/`vpu_d.bin`).
- **MT8183 and later:** stateful or stateless, using SCP.

In both designs the kernel exchanges IPI messages and the coprocessor firmware programs the
codec registers. The kernel only touches `VDEC_SYS` and the MISC IRQ register; the code is in
`mtk_vcodec_dec_drv.c` (`VDEC_HW_ACTIVE_ADDR`, `VDEC_IRQ_CFG`/`CLR`). There is no MT8163 or
MT8167 compatible.

Could mainline's MT8173 driver plus MT8173 VPU firmware drive MT8163?

- **For it:**
  - The VDEC is at the same base, `0x16020000`, with the same sub-block names, and the vendor
    library shares code across 8163/8167/8173.
  - The MD32 sits where MT8173's VPU sits.
  - The `mtk_vpu.c` cfg offsets (`HOST_TO_VPU 0x24`, `VPU_TO_HOST 0x1c`, `PC 0x60`,
    `WDT 0x84`) look like MediaTek's MD32 register layout.
  - The TCM window (0x38000) can hold MT8173's 96 KiB PTCM plus 32 KiB DTCM at `+0x18000`.
- **Against it:**
  1. The MT8163 MD32's cfg block is at `0x10058000`, not `0x10050000`. Its TCM split and
     DRAM-window registers (`PMEM/DMEM_EXT*`) are unverified.
  2. The MT8173 firmware is built for the MT8173 VDEC revision. That revision decodes VP8 and
     VP9 in hardware, which the MT8163 cannot, so H.264 register semantics may also differ.
  3. The firmware's encoder code would write the MT8173 VENC at `0x18002000`. On MT8163 that
     address is the connectivity (Wi-Fi/BT) subsystem (`conn_peri_base = 0x18000000`), so the
     encoder must never be pointed at this firmware.
  4. There is no source to debug a hang. The MD32 may also not have the same bus view of VDEC
     and DRAM.

Verdict on this path: a long shot for H.264 decode only. Its main use would be as a quick
experiment. Do not build on it.

The MDP (RDMA/RSZ0/RSZ1/WDMA/WROT/TDSHP at `0x14001000`–`0x14006000`) has a different layout
from MT8173's (RDMA0/1, RSZ0–2, WDMA, WROT0/1). Mainline `mtk-mdp` also depends on the VPU.
It is not worth pursuing. Color conversion and scaling are cheaper on the CPU or GPU at
480×960.

## 3. Running the vendor libraries on Alpine

The vendor libraries are armeabi-v7a, which matches the Alpine **armv7** userspace this build
already uses. They are bionic binaries, so running them needs three things:

1. **A bionic loader on musl.** libhybris is the usual tool, but it targets glibc. The
   dependencies `libc++`, `libcutils`, `liblog` and `libutils` can be taken from an Android
   armv7 build or stubbed. Calling the libraries directly, without OMX, avoids
   `libbinder`/HIDL and `libui`/`gralloc`. The entry points are `eVDecDrvCreate/Init/Decode`
   and `eVEncDrvCreate/...` from MediaTek's `vdec_drv_if_public.h`/`venc_drv_if_public.h`,
   which `libvcodecdrv` exports. For H.264 decode alone, `libMtkOmxVdecEx`, `libdpframework`
   and `libmhalImageCodec` are not required.
2. **A kernel `/dev/Vcodec` driver** for Linux 7.2. This is a rewrite rather than a port: a
   misc device with:
   - lock/unlock and `WAITISR` on SPI 212/206;
   - power-on through `pm_runtime` on the VDEC/VENC domains plus larb1/larb3 and the
     `vdecsys`/`vencsys` clocks, all of which already exist in mainline;
   - mmap of `0x16000000`/`0x16020000`/`0x17002000`;
   - an alloc ioctl returning DMA buffers with M4U IOVAs.

   The ioctl numbers and structs must match MediaTek's `val_types_public.h`. Get them from an
   Amazon GPL kernel drop for an MT8163 device; the Echo Show 5 or Fire HD 8 2018 drop should
   include `drivers/misc/mediatek/videocodec/`. They can also be recovered from
   `libvcodec_utility.so`.
3. **Shims for ION, `/proc/m4u` and `/dev/MTK_SMI`.** The library expects ION handles plus
   physical or MVA addresses from `libm4u`. The simplest approach is to replace `libion`,
   `libion_mtk`, `libm4u`, `libbwc` and `libmtk_drvb` with small musl-free stub `.so` files
   that call the new `/dev/Vcodec` alloc ioctl.

Effort: about 1 week for the kernel driver, 1–2 weeks for the loader and shims, and 1–2 weeks
of debugging (buffer alignment, cache maintenance, UFO/NBM modes, chip-ID detection through
`atag,devinfo`). The result is a non-V4L2, MediaTek-private API. It works for an in-house
application but not for GStreamer or FFmpeg without a wrapper.

The alternative is a clean mainline V4L2 stateless H.264 driver. It would use the existing
`V4L2_CID_STATELESS_H264_*` controls, with the AP programming VLD/AVC_VLD/MC/PP directly. No
MediaTek SoC has one today. It would mean reverse-engineering `H264_HAL_*` with the help of the
`RISCWrite_*` trace strings, which the library emits when its log level is raised. One way is
to run the library from step 3 against a `/dev/Vcodec` that logs: it records register
sequences per frame, which then become the driver. Effort: 2–4+ months.

## 4. Verdict and first experiments

**Most promising path:** AP-driven, reusing the vendor libraries. In order:

1. A minimal mainline `/dev/Vcodec` driver.
2. A bionic loader and stubs on Alpine armv7.
3. A test harness that calls `libvcodecdrv`'s `eVDecDrv*` and `eVEncDrv*` directly.

Start with the **H.264 encoder**. The camera is already on V4L2, the encoder needs no
reference-frame management from the caller, and the base address is confirmed at `0x17002000`
by both the DT and the library. Optionally, use the register traces later to grow a proper
V4L2 driver.

**First low-risk experiment** (read-only once powered; nothing runs on the MD32):

1. Write a throwaway module or DT node bound to the existing `larb@16010000` and
   `larb@17001000`. It calls `pm_runtime_resume_and_get()` on them, which powers the VDEC and
   VENC domains and the larb clocks. It also enables `CLK_TOP_VDEC_SEL`, and `mm_sel` for VENC.
2. `ioremap` the following windows and **only read** them, logging the first 0x100 words of
   each:
   - `0x16000000` (VDEC_SYS)
   - `0x16020000` (MISC)
   - `0x16021000` (VLD)
   - `0x16021800` (VLD_TOP)
   - `0x16024000` (AVC_VLD)
   - `0x17002000` (VENC)
3. Expect non-0xffffffff and non-hanging reads with sane reset values. This confirms power,
   clocks and the decoder's sub-block layout against mainline MT8173's reg-names.
4. In parallel, obtain Amazon's GPL kernel source for an MT8163 product and extract
   `drivers/misc/mediatek/videocodec/` to pin down the `/dev/Vcodec` ABI.

Only if a cheap answer to the VPU question is wanted: with `CLK_INFRA_SCPSYS` enabled, read
`0x10058000`+{0x0, 0x8, 0x1c, 0x24, 0x60, 0x84} and compare with the `mtk_vpu.c` cfg layout
before trying any firmware load. Do not let MT8173 firmware run encoder IPIs.

## Evidence index

- Vendor DT: `sources/amazon-kernel/arch/arm64/boot/dts/mediatek/mt8163.dtsi`. Lines ~514
  (md32), ~611–620 (GCE base table), 1592–1643 (vdec/venc/larbs).
- Mainline MT8173 reference: `linux-7.2.11/arch/arm64/boot/dts/mediatek/mt8173.dtsi`, lines
  564 (vpu), 1387 (vcodec_dec), 1467 (vcodec_enc_avc) and 1519 (vcodec_enc_vp8). Also
  `drivers/media/platform/mediatek/vpu/mtk_vpu.c`, lines 33–71.
- Ported MT8163 support already in the tree:
  - `arch/arm64/boot/dts/mediatek/mt8163.dtsi` (scpsys VDEC/VENC domains, `vdecsys`/`vencsys`,
    larb1/larb3, `mt8163-m4u`);
  - `drivers/pmdomain/mediatek/mt8163-pm-domains.h`;
  - `drivers/clk/mediatek/clk-mt8163-{vdecsys,vencsys}.c`.
- Running unit: larb1, larb3 and the iommu are bound. `17003000.jpeg-encoder` is `/dev/video0`
  and is runtime-suspended when idle. There is no MD32 node or firmware. The board DT
  `config/cronos-techo5.dtsi` holds the `jpgenc` node, which is the template for VENC.
- Vendor files: `vendor/lib/libvcodecdrv.so`, `libvcodec_utility.so`, `libvcodec_oal.so`,
  `libMtkOmxVdecEx.so`, `libMtkOmxVenc.so`, `*_sa.ca7.so`, `libm4u.so`, `libbwc.so`,
  `libion_mtk.so`, `libmtk_drvb.so`, `libdpframework.so`, `vendor/firmware/md32_{p,d}.bin`,
  and `vendor/etc/media_codecs_mediatek_video.xml`.

## Results (2026-10-18)

The path of section 3 works, faster than estimated:

- **Kernel:** `patches/mt8163-vcodec.c`, written fresh against the ABI of Amazon's GPL driver
  (`drivers/misc/mediatek/videocodec/mt8163` of android_kernel_amazon_mt8163, which also pinned the
  ioctl structs' 32-bit layouts). Power is mainline's runtime PM on the VDEC/VENC domains through the
  larbs the IOMMU links; buffers come from the DMA API at IOMMU addresses.
- **User space:** `vcodec-user/` (Android 11's runtime from an AOSP build, stand-ins for libion,
  libion_mtk, libm4u, libbwc, liblog and libcutils; no OMX). The decoder wants the PPS with the first
  access unit (Init parses only the SPS) and `eVDecDrvDecodeEx`, not `Decode`. Output is MediaTek's
  block format, detiled on the cores.
- **Measured:** decode 2.9 ms a 720p frame, bit-exact; encode 20 ms a 1600x1200 frame, High profile.
- **Pitfalls met:** the library prints to stdout (the filters keep their output on a descriptor of
  their own); the Android runtime reads /dev/urandom at start and mdev had left it root's alone
  (boot.sh now makes the usual nodes world-usable); the MMC hosts' numbers were not fixed and a
  kernel whose timing changed found its eMMC at mmcblk1 now and then (`cronos-techo5.dtsi` aliases).

## H.265 (2026-10-19)

The HEVC decoder is not a separate library. `libvcodecdrv.so` holds it next to H.264:
`eVDecDrvCreate(VDEC_DRV_VIDEO_FORMAT_H265 = 0x200)` dispatches to `gH265_DRV_IF`
(`Vdec_Drv_H265_*`, `h265_dec_*`, `H265_HAL_*`). The unit's `/vendor/lib` has no other HEVC decoder;
`libhevce_sb.ca7.android.so` is the software HEVC *encoder*. The driver type it locks and waits with
is 29 (`VAL_DRIVER_TYPE_HEVC_DEC`), which `mt8163-vcodec.c` already counted as the decoder's.

- **Hardware:** the same VDEC. Next to VLD, VLD_TOP, MC, PP and MV (0x16024000, as HEVC_MV), it
  programs HEVC_VLD at 0x16028000, inside the window `/dev/Vcodec` already maps. The same interrupt
  (SPI 212, MISC 0xa4 bit 16) and the same larb1 ports; no IOMMU faults, no new kinds of buffer
  (no `ALLOC_NON_CACHE_BUFFER`; its MV buffers come through `eVideoMemAlloc` like H.264's). No UFO: the
  library has UFO code, but nothing turns it on for MT8163. **No kernel change was needed.**
- **Output:** the same MediaTek block format as H.264 (16x32 luma tiles, 16x16 CbCr tiles, CbCr at
  `+Ysize`), although `GET_PIXEL_FORMAT` says 4. The buffers are aligned to the 64-pixel CTB:
  1280x720 is allocated 1280x768, 1920x1080 1920x1088 (`u4DramPicArea` = W x H x 1.5). The H.264
  detiler in `vcodec_dec` works unchanged.
- **Feeding it:** two differences from H.264.
  1. Access units split on H.265's two-byte NAL header (type in bits 1-6;
     `first_slice_segment_in_pic_flag` is the top bit of the third byte; VPS/SPS/PPS, AUD and prefix
     SEI start a picture, suffix SEI ends one).
  2. `DecodeEx` takes a keyframe's access unit a NAL unit at a time. Given VPS+SPS+PPS+SEI+IDR in one
     buffer it consumes the VPS, returns `NEED_MORE_OUTPUT_BUFFER` and keeps its offset; the next call
     must hand in the **same** buffer, until the slice is done. Handing in the next access unit
     instead applies the old offset to it ("non-I slice before first I slice", nothing decoded).
  `eVDecDrvInit` with the first access unit parses VPS/SPS/PPS as for H.264.
- **Debugging:** `vendor.mtk.vdec.h265.log=1` writes the register trace to
  `/mnt/sdcard/TID_<tid>_Handle_<h>.h265.internal.log.txt` (not `mtklog/`); on the read-only root,
  run in `unshare -m` with a tmpfs on `/mnt`.

Measured on the unit (x265 Main, 8-bit 4:2:0, B-frames, WPP; 90 frames each):

| | 720p | 1080p |
| --- | --- | --- |
| hardware time a frame (WAITISR) | 2.4 ms | 7.1 ms |
| `vcodec_dec` whole run, a frame (incl. detiling) | 8.9 ms wall, 5.8 ms CPU | 20.8 ms wall, 12.4 ms CPU |
| ffmpeg 8.1 on the cores, to NV12 | 23 ms wall, 32 ms CPU | 37 ms wall, 93 ms CPU |
| bit-exact vs ffmpeg (per-frame MD5 of NV12) | yes, 90/90 | yes, 90/90 |

Played at 30 fps through echod's code (`TestDeviceVcodecPlay`, ffmpeg copying the stream out with
`hevc_mp4toannexb`, frames not drawn), the whole unit was busy 1.11 cores at 720p and 1.48 at 1080p,
against 0.84 idle; on the cores (`TestDeviceSWPlay`, converted to a 1280x800 panel) 2.33 cores at
720p and 3.74 at 1080p, where 1080p fell behind (22.9 fps).

The image's ffmpeg had no HEVC at all (no decoder, parser, bitstream filter or raw muxer), so H.265
could not even be copied out; `tools/linux/build-ffmpeg.sh` now builds them in (the decoder is also
the fallback on the cores, and what probing needs to see the pixel format).

**A hang seen once.** In the first session of experiments (after runs that fed the decoder wrongly,
before the "same buffer again" rule), a 720p run stopped at its 17th picture: `WAITISR` timed out
(the library's timeout is 60 ms for HEVC, 200 for H.264), no interrupt came, and from then on every
decode, H.264 too, timed out until a reboot. Nothing in the kernel log (no IOMMU fault). It did not
come back in about 4,000 pictures afterwards (720p and 1080p, repeated runs, streams without their
keyframe, runs fed the old wrong way). Two things make such a hang stick: the library's own reset
(VLD 0x108) does not bring the block back, and the VDEC power domain never goes off, so nothing else
resets it. That is genpd keeping every domain that was on at boot on until the scpsys provider's
sync_state, which never comes on this kernel (`/sys/kernel/debug/pm_genpd`: `vdec on`, idle 0 ms).
If it shows up again, the fix belongs in `mt8163-vcodec.c`: power-cycle the decoder after a timed-out
wait, which needs the domain to be allowed off first.

## A hung decoder, reset (2026-10-19)

The hang above lasts until a reboot because nothing resets the decoder state that the library
cannot reach. `mt8163-vcodec.c` now resets the whole decoder after a timed-out wait. When a
`WAITISR` for the decoder times out, the driver:

1. powers the block down to its larb (interrupt off, clocks off, runtime PM put), then waits up to
   200 ms until larb1 is runtime-suspended;
2. pulses the TOPRGU's VDEC reset for 10 µs (`resets = <&watchdog MT8163_TOPRGU_VDEC_RST>`, through
   mainline's `mtk-wdt` reset controller);
3. powers the block up again.

larb1 sits in the decoder's subsystem. Its resume writes its port configuration again, so the IOMMU
enables come back with it (`mediatek,larb = <&larb1>` in the node). The wait then returns -2 as
before, and the library carries on. Two parameters under `/sys/module/mt8163_vcodec/parameters`:
`reset` (1; set it to 0 to turn the reset off for tests) and `resets`, which counts the resets
done.

Power-cycling the domain was the other option. But genpd keeps the VDEC domain on (`stay_on` until
the scpsys provider's sync_state, which never comes), and the reset makes a power cycle unnecessary.

**A hang made on purpose.** None of the wrong feedings reproduced the hang:

- Feeding the next access unit in the middle of an H.265 keyframe decodes nothing. The library
  rejects the slice ("non-I slice before first I slice").
- Corrupting bytes makes H.264 time out on the corrupted pictures, but the library recovers by
  itself.
- Corrupt H.265 ends in the library's FATAL, not a hang.
- A bad output buffer address faults in the IOMMU, and the decoder carries on.
- Killing the process in the middle of a picture changes nothing.

What does stick is `vcodec_dec`'s `VCODEC_DEC_TEST=wedge=K,OFF,VAL`. It writes a decoder register at
the K-th interrupt wait. At that point the library holds the hardware and the block is powered,
which it is not between pictures: MISC then reads 0x11 everywhere. A sweep that wrote 0 to each of
MISC's non-zero words found one register, **MISC `0x10c`** (`0x00001011` while running). After that
write, the picture times out. The library's own recovery ("VDEC HW break +") then waits for an
acknowledgement that never comes, and spins on a core with the hardware lock held. Every later
process that decodes does the same. This is the field failure, made on purpose.

Results on the unit: test kernel #33, the 720p H.264 and H.265 streams from `ffmpeg testsrc2`,
checked against ffmpeg's decode by the MD5 of the NV12:

| | reset off | reset on |
| --- | --- | --- |
| Wedged process | spins in "break" (killed after 40 s) | 1 reset (H.264) or 2 (H.265), then all 180 pictures, **bit-exact** |
| The next process, clean stream | spins too, on its first picture (killed after 40 s; 3 rounds of 3) | H.264 and H.265 bit-exact, no reset (3 rounds of 3) |
| H.264 with one corrupted access unit (3 seeds) | 1 timeout, recovered by the library | the same MD5s as with reset off: the reset changes nothing the library already recovers from |

No IOMMU faults came around the resets. The bit-exact decode after each reset also shows that larb1
came back translating: without its MMU enables, the decoder's addresses would have gone to memory
untranslated. The kernel logs each reset as `no interrupt in 200 ms (VDEC_SYS 00000111, MISC 0xa4
01101001): reset, N us`. A reset takes about 1.1 ms; kernel #36 repeated the test: the wedge, then
H.264 and H.265 bit-exact again.

The encoder keeps Amazon's own reset (`vc_enc_reset`). A TOPRGU VENC reset would also reset the JPEG
encoder, which mainline drives in the same subsystem.

## VP8 and VP9 on the cores (2026-10-19)

The MT8163 has no VP8/VP9 decoder (above): Fire OS's `libvp8dec_sa`/`libvp9dec_sa` are software, so
these codecs play on the cores, through ffmpeg's own decoders.

**Which ffmpeg.** The 64-bit rootfs (`build-rootfs.sh`, `T5_ARCH=aarch64`) ships `bin/techo5-ffmpeg-arm64`,
which `tools/linux/build-ffmpeg-arm64.sh` builds on a Mac in Docker (an arm64 Alpine container) with
`build-ffmpeg.sh`'s flags. Those flags have had `vp8`/`vp9` decoders and parsers since the Show 8's
change. Only the armv7 build (`build-ffmpeg.sh`, for the 32-bit image on the 4.9 kernel) runs under
QEMU in WSL. The Show 5's slots, mainline.22 and .23, both carry that build already
(`/usr/local/bin/techo5-ffmpeg`, sha256 `9d587969…`, ffmpeg 8.1.2), so nothing had to be rebuilt.
What was missing was a limit (below).

**Bit-exact.** Synthetic clips (`testsrc2` with temporal noise and a slow scroll, 20 s, Opus sound;
encoded with libvpx 'good', cpu-used 4, VP9 with 4 tile columns and row-mt), decoded on the unit
through `pipe:0` (the image's ffmpeg has no file protocol) to YUV 4:2:0, hash the same as libvpx's
own decode (alpine ffmpeg 8.1.2 `-c:v libvpx` / `libvpx-vp9`):

| clip | bitrate | sha256 of the frames, unit = libvpx |
| --- | --- | --- |
| VP8 1280x720p30 | 2.3 Mbit/s | `f21ebbf4…` |
| VP8 1920x1080p30 | 5.2 Mbit/s | `3c069bc7…` |
| VP9 1280x720p30 | 2.1 Mbit/s | `ea22afc9…` |
| VP9 1280x720p60 | 3.4 Mbit/s | `e54b7d81…` |
| VP9 1920x1080p30 | 4.7 Mbit/s | `3e72bfc0…` |
| VP9 1920x1080p60 | 7.0 Mbit/s | `ca1614dc…` |

**Speed** (mainline.23, kernel #36, echod running, 4 cores at 1.3 GHz). "Player" is echod's own
software-path command for this screen (args.go: `-threads` 3 to 720p and 4 above, `fps` to 30,
`scale` fast_bilinear to 852x480, `pad` 960x480, `transpose`, BGRA, and the sound filter to s16le),
fed the clip and with its output discarded:

| clip | decode only, 1 thread | decode only, echod's threads | player flat out | player at -re: wall, ffmpeg cores, unit cores |
| --- | --- | --- | --- | --- |
| VP8 720p30 | 96 fps | 235 fps | 78 fps | 19.5 s, 1.15, 1.65 |
| VP9 720p30 | 52 fps | 107 fps | 61 fps | 19.5 s, 1.39, 1.97 |
| VP9 720p60 | 56 fps | 115 fps | 83 fps (decoded) | 19.5 s, 1.92, 2.49 |
| VP8 1080p30 | 45 fps | 118 fps | 56 fps | 19.6 s, 1.57, 2.06 |
| VP9 1080p30 | 25 fps | 57 fps | 41 fps | 19.6 s, 2.15, 2.64 |
| VP9 1080p60 | | 68 fps | | **21.9 s**, 3.0 |

- VP9 1080p30 kept up for 200 s, played in 199.6 s (the 20 s clip ten times over, stream-copied).
  The cores stayed at 1.3 GHz, the temperature leveled at about 65 °C, and the cpuplug governor had
  all four cores up from its first reading during playback, 10 s in. That is about 2 cores, against 1.85 on the Show 8.
- VP9 1080p60 falls behind: it decodes at 68 fps alone, and the player took 21.9 s for 20 s.
- 1080p30 has 41 fps against the 30 it needs. A busier 1080p30 stream (a higher bitrate than
  these clips) could still fall behind; none was tried.

**The limit (echod, `feature/video`).** `vpx_show.go` sets `vpxMost` to 1920x1088x30 pixels a second
on the Show 5's mainline kernel. `ParseProbe` takes a VP8/VP9 stream past it as unusable: an HLS
stream picks a variant within reach, and a single stream is refused with "its video is vp9 at
1920x1080 and 60 frames a second, more than this device decodes in time (VP8 and VP9 up to 1080p at
30 frames a second)". 720p60 and 1080p30 play. H.264 and H.265 are not held to it (they have the
hardware decoder). Tests: `pure_test.go`.
