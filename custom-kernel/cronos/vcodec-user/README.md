# vcodec-user: MT8163 vendor video codec libraries on mainline + Alpine

This directory runs Amazon's 32-bit Android 11 codec stack for the MT8163
(`libvcodecdrv.so`, `libvcodec_utility.so`) on our mainline kernel with an
Alpine aarch64 rootfs. A small test program calls the driver API
(`eVDecDrv*`/`eVEncDrv*`) directly, with no OMX and no binder. The kernel side
is a `/dev/Vcodec` driver; its interface is specified below.

Status: in use on the 2nd gen Show 5 (2026-10-18). Decode is bit-exact against
ffmpeg (H.264 720p High, 2.9 ms a frame; H.265 Main since 2026-10-19, 2.4 ms at 720p
and 7.1 ms at 1080p); encode makes 1600x1200 High in about 20 ms.
echod's video player and RTSP stream use the two filters below.

## Layout

| Path | What |
| --- | --- |
| `include/vcodec_shim_uapi.h` | **The new kernel ioctl** (`VCODEC_SHIM_ALLOC`) |
| `include/val_types_public.h` | MTK VAL types, userspace 32-bit layout (offsets verified) |
| `include/vdec_drv_if_public.h` | Reconstructed decoder API (each field tagged [V] verified / [P] public header) |
| `include/venc_drv_if_public.h` | Reconstructed encoder API (same tagging) |
| `stubs/` | Replacement `libion`, `libion_mtk`, `libm4u`, `libbwc`, `liblog`, `libcutils`, plus empty `libutils`/`*_sa.ca7` placeholders |
| `stubs/fakevcodec.c` | Test only: an `LD_PRELOAD` fake `/dev/Vcodec` used for qemu runs |
| `harness/` | `vdec_test`, `venc_test`, `smoke_test` |
| `tools/build.sh` | Docker build: fetches dependencies, compiles, writes `out/staging` |
| `tools/fetch-deps.sh` | Streams the NDK sysroot and the Android 11 runtime into `.cache/` |
| `tools/qemu-test.sh` | Runs the staging tree as an armv7 container (binfmt qemu) |
| `tools/check-symbols.py` | Checks that every BIND_NOW import in the staging tree resolves |
| `tools/run-chroot.sh`, `tools/run-direct.sh` | On-device launchers (copied into the staging tree) |
| `tools/re/` | The disassembly helpers used for the reverse engineering |

## Build

You need Docker. It runs natively on arm64 or amd64, and nothing large is
kept on disk:

```sh
# vendor-lib = the device's /vendor/lib (libvcodecdrv, libvcodec_utility,
#              libvcodec_oal, libmtk_drvb are the ones used)
tools/build.sh /path/to/vendor-cronos/lib          # -> out/staging
tools/qemu-test.sh smoke_test                      # optional offline check
```

`fetch-deps.sh` downloads two things into `.cache/`, which is gitignored and
can be moved with `CACHE=`:

- The **NDK r25c sysroot** (85 MB extracted), streamed out of the zip. That is
  bionic headers, API-30 stub libraries, crt objects and compiler-rt. Debian's
  clang-14/lld do the compiling, so the x86_64 NDK toolchain is never needed.
- The **Android 11 armv7 runtime** from the single layer of
  `redroid/redroid:11.0.0` (arm64), pinned by digest. That image is an AOSP 11
  build with 32-bit support. The 750 MB layer is streamed and filtered down to
  `linker`, `libc.so`, `libm.so`, `libdl.so`, `ld-android.so` and `libc++.so`.
  For reference it also keeps AOSP `liblog`/`libcutils`/`libbase`/`libutils`/
  `libion`, which are not staged. Licenses are Apache-2.0 and BSD (bionic).
  Fallback: pull the same files from any Android 11 armv7 or arm64 device
  (`/apex/com.android.runtime/bin/linker`,
  `/apex/com.android.runtime/lib/bionic/*`, `/system/lib/{ld-android,libc++}.so`).

The staging tree (about 3.5 MB):

```
system/bin/linker            AOSP 11 linker (unmodified)
system/bin/{vdec,venc,smoke}_test
system/lib/libc.so libm.so libdl.so ld-android.so libc++.so   AOSP
system/lib/liblog.so libcutils.so libutils.so libion.so        ours
system/lib/libfakevcodec.so                                    ours, test only
vendor/lib/libvcodecdrv.so libvcodec_utility.so libvcodec_oal.so libmtk_drvb.so   vendor, unmodified
vendor/lib/libion_mtk.so libm4u.so libbwc.so                   ours
vendor/lib/lib{mp4enc_sa,vc1dec_sa,vp8enc_sa}.ca7.so libhevce_sb.ca7.android.so   empty placeholders
vendor/etc/vcodec.prop       "Android properties" for the libs
run-chroot.sh run-direct.sh
```

There is no `ld.config.txt` on purpose. With no linker config, the Android 11
linker puts everything in one default namespace that searches `/system/lib`,
`/odm/lib`, `/vendor/lib` and then `LD_LIBRARY_PATH`. VNDK namespaces and
`libvndksupport` are not involved.

## Run on the device

Copy `out/staging` to the device, for example
`rsync -a out/staging/ <device>:/opt/vcodec/`. Then, as root, use either launcher:

```sh
# chroot: binds /dev, mounts /proc, and binds $DATA onto /data
DATA=/root/vtest /opt/vcodec/run-chroot.sh smoke_test          # alloc path only, no HW
DATA=/root/vtest /opt/vcodec/run-chroot.sh vdec_test -o /data/out.nv12 /data/in.h264
DATA=/root/vtest /opt/vcodec/run-chroot.sh venc_test -s 1280x720 -f nv12 -b 4000000 \
        -o /data/out.h264 /data/in.nv12      # without in.nv12: generated test pattern

# or no chroot: the linker is run explicitly with LD_LIBRARY_PATH
/opt/vcodec/run-direct.sh vdec_test -o /tmp/out.nv12 /tmp/in.h264
```

`vdec_test` options:

- `-f nv12|i420|yv12|blk`: output format. `blk` writes the raw hardware tile
  format. The others detile in software, cropped to the display size.
- `-n N`: stop after N output frames.
- `-p N`: number of output buffers.
- `-l`: no-reorder (low-latency) mode.
- `-q`: quiet.

Per access unit it prints the `eVDecDrvDecodeEx` time. The summary gives the
average, minimum and maximum, frames per second, and the time spent on output.

`venc_test` options:

- `-s WxH` (required): picture size.
- `-f nv12|i420|yv12`: input layout.
- `-b` bitrate in bit/s, `-r` frame rate, `-g` I-frame period in frames.
- `-P baseline|main|high`: profile.
- `-H`: SPS/PPS before every IDR.
- `-n`: number of frames.

The input YUV must be tightly packed; it is re-laid out into 16-aligned
buffers. The tool prints per-frame encode time, size and keyframe flag.

Environment variables:

| Variable | Effect |
| --- | --- |
| `VCODEC_LOG=V\|D\|I\|W\|E` | Vendor log level, printed to stderr (default I). The libs log routine things at E. |
| `VCODEC_SHIM_DEBUG=1\|2` | Shim tracing: allocations, IOVAs, property lookups |
| `VCODEC_SHIM_CACHED=1` | Ask for cacheable buffers (needs kernel support; see below) |
| `VCODEC_PROPS="k=v;k=v"` | Property overrides, on top of `/vendor/etc/vcodec.prop` |
| `VCODEC_DEV` | Allocation device (default `/dev/Vcodec`) |
| `VCODEC_SHIM_FAKE=1` + `LD_PRELOAD=/system/lib/libfakevcodec.so` | No kernel at all (qemu tests) |

Useful properties:

- **`vendor.mtk.vdec.h264.log=1`** writes a full trace of the decoder to
  `/data/mtklog/h264.internal.log.txt` (create `/data/mtklog` first). The trace
  covers SPS/PPS/slice parsing and **every register access**, as lines like
  `RISCWrite_VLD(66, 32'h101); /* base 0x16021000 ... */`. That is the raw
  material for a mainline V4L2 stateless driver later.
- `vendor.mtk.vdec.h265.log=1` does the same for H.265, to
  `/mnt/sdcard/TID_<tid>_Handle_<h>.h265.internal.log.txt` (a tmpfs on `/mnt` in `unshare -m`
  makes room for it on the read-only root).
- `vendor.mtk.venc.h264.showlog=1` and `vendor.mtk.venc.drvinfo=1` turn on
  encoder logging.

Under qemu, with the fake device, both flows run as far as the hardware:

- The decoder parses the stream and programs VDEC with our IOVAs, then gives
  up because the fake "barrel shifter ready" bit never sets.
- The encoder produces SPS/PPS on the CPU (`67 4d 00 1f ...`), programs VENC,
  then times out.

## What replaces what

| Vendor lib | Imports used by the codec libs | Replacement |
| --- | --- | --- |
| `libion.so` (AOSP) | `ion_free`, `ion_share` | `stubs/libion.c`: the buffer table, backed by `VCODEC_SHIM_ALLOC` |
| `libion_mtk.so` | `mt_ion_open`, `ion_alloc_mm`, `ion_custom_ioctl`, `ion_mmap`, `ion_munmap`, `ion_share_close` | `stubs/libion_mtk.c` |
| `libm4u.so` (`/proc/m4u`) | 12 `MTKM4UDrv::` methods (mangled names verified) | `stubs/libm4u.cpp`: port config succeeds as a no-op; `m4u_alloc_mva` maps a VA to the IOVA if the VA is one of our buffers |
| `libbwc.so` (`/dev/MTK_SMI`) | `BWC::Profile_Change`, `VideoRecordSize_Set`, `Video{De,En}codeCodec_Set` | `stubs/libbwc.cpp`: no-ops |
| `liblog.so` | `__android_log_print` | stderr |
| `libcutils.so` | `property_get`, `atrace_*`, `ashmem_*` | properties from a file and the environment, atrace disabled, ashmem through memfd |
| `libutils.so` | nothing (NEEDED only) | empty |
| `lib*_sa.ca7*.so` | nothing (NEEDED only; dlopen()ed for SW MPEG4/VC1/VP8/HEVC) | empty |
| `libmtk_drvb.so` | `sec_drv_base_check`, `sec_drv_info_update` | **kept, unmodified** (see Risks) |
| `libvcodec_oal.so` | nothing (NEEDED only) | kept, unmodified |

How `libvcodec_utility` uses ION (from the disassembly), for
`eVideoMemAlloc` with memory types 0 to 2, which is how libvcodecdrv allocates
everything for H.264 (type 1, align 128/512/4096):

1. `fd = mt_ion_open("val_public")`
2. `ion_alloc_mm(fd, size, align, flags=3 /*cached*/, &handle)`
3. `ion_share(fd, handle, &share_fd)`
4. `va = ion_mmap(fd, 0, size, RW, MAP_SHARED, share_fd, 0)`
5. `ion_custom_ioctl(fd, ION_CMD_MULTIMEDIA, {CONFIG_BUFFER, handle, module 17=VDEC/34=VENC})`
6. `ion_custom_ioctl(fd, ION_CMD_SYSTEM, {GET_PHYS, handle} -> addr)`

The address from the last step becomes `pvMemPa` and is programmed into the
hardware, so it must be the IOMMU address. Cache maintenance goes through
`ION_SYS_CACHE_SYNC`, by handle or "all". The shim turns it into
`DMA_BUF_IOCTL_SYNC` on the dma-buf, and does nothing for write-combined
buffers.

## Kernel interface needed (`/dev/Vcodec`)

### New: `VCODEC_SHIM_ALLOC`, `0xc0205601` = `_IOWR('V', 0x01, struct vcodec_shim_alloc)`

```c
struct vcodec_shim_alloc {   /* 32 bytes, same layout on arm32/arm64: compat = pass-through */
	__u64 size;     /* in: bytes; out: actual (PAGE_SIZE multiple) */
	__u64 iova;     /* out: VDEC/VENC device address, must be < 4 GiB */
	__u32 align;    /* in: 0 or power of two; <= PAGE_SIZE must always work */
	__u32 flags;    /* in: bit0 CACHED (optional, -EOPNOTSUPP ok), bit1 VENC hint */
	__s32 fd;       /* out: dma-buf fd, O_RDWR|O_CLOEXEC */
	__u32 reserved; /* must be 0 */
};
```

- The memory is zeroed. It is mapped into the codec IOMMU domain (larb1 VDEC
  / larb3 VENC; a single shared domain is fine) and exported as a dma-buf.
- `mmap(fd, 0..size)` must work repeatedly and with dup()ed fds.
- Without `CACHED` the CPU mapping is write-combined (for example
  `dma_alloc_wc` + `dma_mmap_wc`), and no cache operations are needed. **This
  is the default the shim asks for.**
- With `CACHED`, implement `begin_cpu_access`/`end_cpu_access` so that
  `DMA_BUF_IOCTL_SYNC` invalidates or cleans.
- Release happens in the dma-buf `release()`; there is no FREE ioctl.
  `'V'` cannot collide with the vendor ioctls, which use `'M'`.

### Vendor ioctls libvcodec_utility issues

All of these are `_IOW('M', nr, unsigned int)` = `0x40044dNN`. The argument
is a pointer to the 32-bit struct shown. "H.264 flow" marks what the fake
device actually saw during the qemu runs.

| nr | Name | Arg (32-bit layout) | H.264 flow |
| --- | --- | --- | --- |
| 0x0b | WAITISR | `VAL_ISR_T`, 96 B: handle, size, eDriverType, isrfn, rsv, rsvsize, u4TimeoutMs @24, u4IrqStatusNum @28, u4IrqStatus[16] @32 (out) | yes. Any failure reads as a timeout. |
| 0x0d | LOCKHW | `VAL_HW_LOCK_T`, 32 B: pvHandle, size, pvLock, u4TimeoutMs @12, rsv, rsvsize, eDriverType @24, bSecureInst @28 | yes |
| 0x33 | UNLOCKHW | `VAL_HW_LOCK_T` | yes |
| 0x27 / 0x28 | INC / DEC_PWR_USER | `HAL_POWER_T`, 24 B: pvHandle, size, eDriverType @8, fgEnable @12 (char), rsv, rsvsize | yes, in eValInit/DeInit |
| 0x15 / 0x16 | INC / DEC_ENC_EMI_USER | `unsigned int *` (out: counter) | yes for the encoder (driver types 5, 24, 28) |
| 0x17 / 0x18 | INC / DEC_DEC_EMI_USER | `unsigned int *` | yes for the decoder |
| 0x22 / 0x23 | ALLOC / FREE_NON_CACHE_BUFFER | `VAL_MEMORY_T`, **96 B, userspace layout** (see below) | not seen for H.264; used by `VCodecDrvMemAllocAligned_NC` (HEVC/MPEG4 SW enc) |
| 0x24 | SET_THREAD_ID | `VAL_VCODEC_THREAD_ID_T` (76 B) | exported, rarely used |
| 0x20 / 0x21 | INIT / DEINITHWLOCK | `VAL_VCODEC_OAL_HW_REGISTER_T` | no |
| 0x29, 0x30, 0x31, 0x32 | CPU loading info / core loading / core number / CPU OPP limit | see kernel source | no (the driver tolerates failure) |
| 0x34 | VCODEC_MB | `unsigned int *` | in `eHalCmdProc` (a barrier) |

For 0x22/0x23, `VAL_MEMORY_T` is 96 bytes. All pointer and size fields are
8-byte "ext64" unions, which **differs from the `COMPAT_VAL_MEMORY` in
Amazon's driver**:

| Offset | Field |
| --- | --- |
| +0x00 | u4MemSign |
| +0x04 | eMemType |
| +0x08 | u4MemSize |
| +0x10 | pvMemVa |
| +0x18 | pvMemPa (out) |
| +0x20 | eAlignment |
| +0x28 | pvAlignMemVa |
| +0x30 | pvAlignMemPa |
| +0x38 | eMemCodec |
| +0x3c | i4IonShareFd |
| +0x40 | pIonBufhandle |
| +0x48 | pvReserved |
| +0x50 | u4ReservedSize (the vendor driver stores the kernel VA here) |
| +0x58 | i4IonDevFd |

After 0x22, userspace calls
`mmap64(NULL, u4MemSize, RW, MAP_SHARED, VCodec_fd, offset = pvMemPa)`. So
return a page-aligned IOVA (WC memory) in `pvMemPa`, and have `mmap` look up
that offset in the NC-buffer list **before** the register windows.

### `mmap` of `/dev/Vcodec` (registers, offset = physical address)

`eHalInit` maps the following windows, with `PROT_RW`, `MAP_SHARED`. Lengths
below a page are rounded up to a page by the kernel.

| Block | Offset | Length |
| --- | --- | --- |
| VDEC_SYS | `0x16000000` | 0x100 |
| MISC | `0x16020000` | 0x200 |
| VLD | `0x16021000` | 0x400 |
| VLD_TOP (+0x800 in the same page) | `0x16021000` | 0x1000 |
| MC | `0x16022000` | 0x1000 |
| AVC_VLD | `0x16023000` | 0x1000 |
| AVC_MV | `0x16024000` | 0x300 |
| HEVC_MV | `0x16024000` | 0x1000 |
| PP | `0x16025000` | 0x1000 |
| VP8_VLD | `0x16026000` | 0x1000 |
| VP6_VLD / VP8_VLD2 | `0x16027000` | 0x1000 |
| HEVC_VLD | `0x16028000` | 0x1000 |
| VENC (encoder only) | `0x17002000` | 0x1000 |

So allow `0x16000000` (1 page), `0x16020000`–`0x16028fff` and `0x17002000`.
Amazon's driver also allows `0x19002000`, `0x7fff000` and `0x10000000`, which
none of these paths uses.

The decoder uses these mappings from two separate fds: `eValInit` opens one
and `eHalInit` opens one per instance. `mt_ion_open` and the shim each open
`/dev/Vcodec` too. Allow several opens per process.

### Other paths and properties the libs touch

All of these are optional. A missing file is handled.

- `/proc/device-tree/chosen/atag,devinfo` and
  `/sys/bus/platform/drivers/dev_info/dev_info` are eFuse words, read only by
  `eV{De,En}cDrvQueryCapability`. If missing, it reports "limit to 720p",
  which the harnesses ignore.
- `/proc/mcdi/sodi_en`, `/sys/power/cpufreq_state` and
  `/sys/bus/platform/drivers/mem_bw_ctrl/concurrency_scenario` are written by
  the EMI/DVFS helpers. Failures are logged only.
- libvcodec_oal reads `/proc/cpuinfo` and cpufreq/cputopo sysfs.
- Dump paths: `/data/vdec/*`, `/data/mtklog/*`, `/sdcard/dump_reg_*`, all only
  when enabled by a property.
- Properties: libvcodecdrv reads about 50 of them (the full list is in the header
  comments and `stubs/libcutils.c`: `vendor.mtk.vdec.*`, `vendor.mtk.venc.*`,
  `vendor.mtk.vcodec.{720p,1080p}`, `ro.vendor.mtk_*tee*`,
  `ro.vendor.mtk_sec_video_path_support`, `ro.build.type`, and so on).
  libvcodec_utility reads `vendor.mtk.vdec.{prof,regdump}`. All default to
  off, and TEE/secure paths stay off.

## Notes on the codec API (details in the headers)

- **Decoder:**
  - `eVDecDrvDecode` is a stub; use `eVDecDrvDecodeEx(h, &rb, 0, NULL)`.
  - The output-buffer callback (`SetParam 23`) is mandatory.
  - `eVDecDrvInit` only parses the SPS, so the harness sends the SPS/PPS
    again in front of the first access unit. Without that, decoding fails with
    "Cannot find PPS".
  - Picture N is decoded when access unit N+1 arrives (the lib looks ahead
    one AU), so EOS (`DecodeEx(h, NULL, ...)`) is needed to get the last
    frames.
  - Address structs inside the API are 24 bytes: VA at +0, PA at +8, size at
    +16. Input must be 64-aligned and output 512-aligned (VA and PA).
  - Output is always MediaTek block format (MM21-like), with
    `Ysize = ALIGN16(w) * ALIGN32(h)` and CbCr at `+Ysize`.
- **Encoder:**
  - Only device addresses go to the hardware. Chroma is derived from the luma
    address, `bufW` and `bufH`, so a frame must be one contiguous buffer.
  - NV12 = 6, I420 = 3, YV12 = 5. NV21 is rejected.
  - Bitrate is in bit/s and the frame rate is Q16.
  - SPS/PPS come from `Encode(opt 1)`.
  - The chip is hard-coded as MT8163 (`QueryCapability(CHIP_NAME)` = 19,
    register base 0x17002000).

## Pull from the device

Nothing is strictly required. The four libraries above are already in
`vendor-cronos/lib`, and the `*_sa.ca7` codecs are replaced by empty
placeholders because libvcodecdrv imports nothing from them. Optional:

- `/proc/device-tree/chosen/atag,devinfo` from the Android/Fire OS kernel,
  only to make `QueryCapability` report 1080p.
- The real `libvp8dec_sa.ca7.so`, `libvp9dec_sa.ca7.so` and friends, only if
  software VP8/VP9 is ever wanted.

## Risks and open points

1. **Untested on hardware.** The offline runs stop at the first register read
   that needs a real response.
2. **Caches.** The libs ask for cached ION buffers and do sloppy maintenance:
   `VCodecDrvFlushCachedBuffer` passes an uninitialized handle, the VA/size
   fields are never set, and on MT8163 the encoder never invalidates the
   bitstream. The shim therefore defaults to **write-combined** buffers, which
   is correct but makes CPU reads of output frames slow (detiling cost shows
   up in vdec_test's "output" time). `VCODEC_SHIM_CACHED=1` is for
   experiments once the kernel supports it.
3. **IOVA must be below 4 GiB** and must be what VDEC/VENC see through the
   M4U. The larb ports must be in IOMMU mode (`iommus = <&iommu PORT>` for
   the VDEC_* / VENC_* ports).
4. **`VAL_MEMORY_T` compat layout** (0x22/0x23) differs from Amazon's
   compat struct; see the table above. NC buffers are probably unused by
   H.264.
5. **libmtk_drvb.** `eHalInit` runs MediaTek's "drvb" platform check through
   `libmtk_drvb.so`. On failure it deliberately corrupts the stack. The shipped
   `libmtk_drvb.so` already has `mtk_drvb_basechk` reduced to `return 0`
   (patched in this Lineage vendor image), so it passes without device-tree
   data and is kept unmodified. An unpatched copy would need
   `atag,devinfo` word[1] = hw code `0x8163`.
6. **`personality(PER_LINUX32)`.** The Android 11 linker aborts if this
   fails. It works on the A53 (AArch32-capable). It fails under qemu-user on
   Apple silicon, which is why `tools/qemu-test.sh` patches a copy of the
   linker; that copy is never staged.
7. **Pixel layout.** The chroma order in block mode (Cb first) and the
   meaning of the error status bit are inferred. Check with `-f blk` plus an
   offline detiler if the colors look swapped.
8. **WAITISR timeout semantics.** Any ioctl error is reported as a timeout,
   and the driver then resets the hardware and retries. Return 0 only when the
   IRQ fired, and fill `u4IrqStatus[]` if the driver asked for it
   (`u4IrqStatusNum`).

## The filters echod uses

- `harness/vcodec_dec.c` (`vcodec-dec [-c h264|hevc] -s WxH`): Annex-B H.264 (or H.265) on stdin,
  every picture to show as NV12 of WxH on stdout. H.265 is the same library's `gH265_DRV_IF`, with
  the same block-format output; a keyframe's access unit is taken a NAL unit at a time ("need more
  output buffer" until the slice is done, the same buffer handed in again: see the file's header). Its output frames are its own cached buffers (`VCODEC_SHIM_ALLOC_CACHED`), synced
  before they are read (the write-combined ones took 27 ms a 720p frame to read). Exit status 3: a
  stream the decoder does not take, for the caller to fall back to the cores.
  `VCODEC_DEC_TEST` feeds it wrongly to test the kernel's recovery from a hung decoder
  (`midslice`, `corrupt=N[,S[,K]]`, `badfb=K`, and `wedge=K,OFF,VAL`, which writes a decoder register
  at the K-th interrupt wait: `wedge=10,0x10c,0` hangs the decoder for good without the kernel's
  reset; `research/mt8163-video-codec.md`, "A hung decoder, reset").
- `harness/vcodec_enc.c` (`vcodec-enc -s WxH [-b bps] [-r fps] [-g gop]`): I420 frames on stdin, each
  frame's H.264 on stdout as a record (u32 length, u32 flags: bit 0 keyframe, bit 1 the parameter sets
  alone), keyframes with SPS/PPS in front; SIGUSR1 asks for a keyframe.

Both keep their output on a descriptor of their own and point descriptor 1 at stderr: the library
prints to stdout ("[Info] Flush buffer ...").

`tools/bundle.sh` packs what an image may carry into `bin/techo5-vcodec-arm.tar.gz`
(`/usr/local/lib/techo5-vcodec`: the Android runtime, the stand-ins, the two filters, and `etc/codecs`,
the codecs `vcodec-dec` takes, which echod reads (a bundle without it is H.264 only); never
MediaTek's libraries, which are the unit's own, in its `/vendor/lib`). The launch is the linker run
directly with `LD_LIBRARY_PATH=<dir>/system/lib:<dir>/shim:/vendor/lib`; it needs the usual world-usable
`/dev/urandom` (the runtime reads it as it starts), which the image's `boot.sh` makes sure of.
