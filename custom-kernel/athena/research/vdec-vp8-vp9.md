# VP8 and VP9 decoding on the MT8183 (athena)

Status (2026-10-19): no hardware path (below), so VP8 and VP9 play on the cores. The kernel still offers
H.264 only. The image's ffmpeg has the two decoders, and echod plays them where it does ("What changed").

## Verdict

**There is no hardware VP8 or VP9 decoder to drive.** No software on or for this unit programs the
VDEC for either codec:

- **The SCP firmware has H.264 only.** The one that runs is linux-firmware's
  `mediatek/mt8183/scp.img`, patched for our proxy (`patches/scp-vdec-proxy-fw.py`), not Amazon's.
  It identifies as `kukui_scp_v2.0.13324-280b9fce97` (2022-04-06), ChromeOS's kukui build, the
  latest published.
- **Fire OS decodes VP9 in software on the cores.** Its only hardware candidate is a missing library,
  and its VP9 hardware codec entry is commented out.
- **Fire OS's "hardware" VP8 is libvpx on the cores** too, with at most a deblocking assist.

An AP-driven VP9 driver, the way the Show 5 drives its decoder, needs a reference that programs the
VP9 blocks: a library to trace, or firmware to read. Neither exists for the MT8183. Whether the
silicon has VP9 at all is not documented. A driver written from no reference would take months,
with no certainty that the block exists. VP8/VP9 should stay on the cores: ffmpeg's decoders,
which the image's ffmpeg does not include today (below).

The earlier statement in the README ("the decoder could do them, but the SCP firmware ... has no
handler") had the firmware right. The hardware half was a guess.

## Evidence

### The SCP firmware (kukui_scp_v2.0.13324)

- Strings: `vdec_h264`, `venc_h264`, `h264_hal_*`, MDP, ISP. There is no `vp8` or `vp9` string
  anywhere in the image.
- The decoder service task (SRAM 0x2f204, Thumb) loops on a message queue. It indexes a handler
  table at 0x5bd68 (bss 0x5b6f8 + 0x670) by the message's codec byte, and logs `vdec handler %d not
  exists.` for an empty slot. Its init stores one entry, slot 0 = 0x2dc51 (the H.264 handler).
  Nothing else in the image writes the table: its address appears in no literal pool, and the
  other stores at offset 0x674 use other bases.
- Mainline's stateless VP8 (`vdec_vp8_req_if.c`) and VP9 (`vdec_vp9_req_lat_if.c`, which has a
  single-core path for MT8186) use the newer IPI pair `SCP_IPI_VDEC_LAT` / `SCP_IPI_VDEC_CORE` and
  the MT8186/MT8192 firmware's message layout. MT8183's firmware implements `SCP_IPI_VDEC_H264`
  only. The image is kukui's own build, so the kukui Chromebooks presumably decode VP8/VP9 on the
  cores too (an inference from the firmware, not checked on a Chromebook).
- Other SoCs' SCP firmware with VP9 cannot run here: it is built for different cores and a
  different memory map, and has its own IPI layout.

### Fire OS (`backups/<unit>/partitions/p12-vendor.img`, `p20-boot.img`)

- `etc/media_codecs_mediatek_video.xml`: `OMX.MTK.VIDEO.DECODER.VPX` (VP8, up to 1920x1088) is on.
  `OMX.MTK.VIDEO.DECODER.VP9` is **inside a comment**, so Android picks `OMX.google.vp9.decoder`
  (libvpx). `media_codecs_performance.xml` lists both MTK names, which does not enable them.
- `lib/libvcodecdrv.so` (MediaTek's AP-side codec driver, the one the Show 5 uses) has
  `gVP9_DRV_IF`, and its VP9 path `dlopen`s `/vendor/lib/libvp9dec_sa.ca7_2core.so`. **That file is
  not on the unit.** It also has a `VP9 driver disable perService control` switch.
- `lib/libvp9dec_sa.ca7.so` is a libvpx-derived **software** decoder: `vp9_convolve8_*`,
  `vp9_loop_filter_*`, NEON idct objects built with armasm, `vp9_sw_dec_*`. Its imports are libc,
  `VCodecOALInit`, `VCodecQueryCPUInfo`, `PowerHal_Wrap_scn*` (CPU boost) and `thha_score`, with
  no device access.
- `lib/libvp8dec_sa.ca7.so` is MediaTek's libvpx port (`-DMT6572_VP8_SERIES`, `VP8SwDecApi.c`,
  `vp8_dx_iface.c`). It has `VP8LockHW` / `VP8WaitISR` / `VP8DECHWDBStart` wrappers (a hardware
  deblocking assist on older SoCs, reached through the OAL), and the decode itself is libvpx.
- The Fire OS kernel's `/dev/Vcodec` (`vcodec_lockhw`, 0xc05d3fd0) accepts the decoder types 2, 6,
  10, 21, 23, 25, 26 and 29 (VP9_DEC is 23). That is MediaTek's shared driver table: nothing in the
  kernel image names VP9.

### Side finding: hardware H.265

Fire OS lists `OMX.MTK.VIDEO.DECODER.HEVC` (and `.secure`), and `libvcodecdrv.so` holds `gH265_DRV_IF`
with 92 `H265_HAL_*` strings, an AP-driven HEVC decoder like the Show 5's (`custom-kernel/cronos/
research/mt8163-video-codec.md`, "H.265"). The Show 8 could get hardware H.265 the Show 5's way: a
`/dev/Vcodec` driver for the MT8183's VDEC (Fire OS's ABI is in its kernel: `vcodec_*` at
0xc05d16ac-0xc05d4da4), the vendor libraries under the Show 5's bionic loader (`vcodec-user/`), and
echod's `hw_vcodec.go`. It would share the VDEC with the SCP-proxied H.264 path and need exclusion
between the two. Not attempted here.

## Software VP8/VP9 on the cores

Before this change the image's ffmpeg (`tools/linux/build-ffmpeg.sh`) had no VP8 or VP9 decoder, and
echod refused a WebM/VP8/VP9 source on both units. Timing of ffmpeg 8.1.2's decoders on the
Show 8 (a static aarch64 build with only the VP8/VP9 decoders, clips from libvpx at realtime speed,
`-benchmark`, output discarded):

| clip (10 s, 30 fps, 300 frames) | 1 thread | 4 threads | cores used at 30 fps |
| --- | --- | --- | --- |
| VP9 1280x720, 2.2 Mbit/s | 2.52 s (119 fps) | 1.13 s (265 fps) | 0.26-0.33 |
| VP9 1920x1080, 5.9 Mbit/s | 5.76 s (52 fps) | 2.34 s (128 fps) | 0.58-0.70 |
| VP8 1280x720, 2 Mbit/s | 1.08 s (277 fps) | 0.45 s (660 fps) | 0.11-0.13 |

("Cores used" is user plus system time over the clip's 10 s. The scheduler may put the threads on the
A53s or the A73s.) So the cores play VP9 1080p30 with room to spare. VP8/VP9 playback for echod is
an ffmpeg build change (add the `vp8`/`vp9` decoders and parsers; the Matroska demuxer, which reads
WebM, is already there) plus echod's routing to the software path, not decoder work.

## What changed

- `tools/linux/build-ffmpeg.sh`: the `vp8` and `vp9` decoders and parsers (the Matroska demuxer
  already reads WebM; the decoder pulls in `vp9_superframe_split` itself). `build-ffmpeg-arm64.sh`
  takes the same flags, now pulls the arm64 `alpine:3.24` first, and refuses a container that is
  not aarch64. A local tag pulled for arm/v7 stays arm/v7 under `--platform linux/arm64`; the build
  failed in ffmpeg's aarch64 assembly that way. Built: `bin/techo5-ffmpeg-arm64`, sha256 `9d587969…`.
  The Show 5's armv7 ffmpeg was not rebuilt (that script runs under WSL).
- echod (`feature/video`, `probe.go`, `proc.go`): VP8 and VP9 are usable only where the image's
  ffmpeg has both decoders, found by a message each alone carries, as H.265 is found. They always
  take the software path. The Show 5's hardware path keeps to the codecs its bundle names, and the
  Show 8's to H.264. A refusal names what the device plays (`H.264, H.265, VP8, VP9 and MPEG-4
  only`). Tests in `pure_test.go`.

Checked on the Show 8 with the new ffmpeg (echod running, rootfs mainline.17/18, three 20 s clips:
VP8 720p, VP9 720p and VP9 1080p30, libvpx-encoded, Opus sound):

- **Bit-exact:** each clip's 600 frames as YUV 4:2:0 hash the same as libvpx's decode (the reference
  decoder) on the Mac: `398b22e5…`, `adb9d15b…`, `0419a5bf…`.
- **Real time:** the player's own command (args.go: `-threads` 3/4, `fps`, `scale` fast_bilinear to
  1280x720, `pad` to 1280x800, RGBA, the sound filter), paced with `-re`, ran each 20 s clip in
  19.6-19.7 s. The whole unit was busy 1.66, 1.86 and 2.54 cores against 0.69 at idle. So playback
  costs about 1.0 (VP8 720p), 1.2 (VP9 720p) and 1.85 cores (VP9 1080p).
- echod's `TestDeviceSWPlay` could not be used: its decoder runs fenced off loopback and the device's
  addresses (`fence.go`), and the clips had nowhere else to come from.
- A rootfs with both (v1.2.19-mainline.19, kernel hw11) was built. It was **not installed**: the
  session's permission check refused the install.

## Estimate, if the hardware question is ever reopened

1. Find out whether a VP9 block exists: a read-only probe of the VDEC's register space (the H.264
   path's windows, plus the offsets the MT8173's VP9 uses) with the domain powered. Half a day; a
   "yes" alone would not give the programming model.
2. Without a reference trace, a VP9 hardware driver means reverse engineering an undocumented
   block from scratch: months, with an uncertain end. The cheaper source of a trace would be a
   device with the same VDEC and a hardware VP9 library (for MediaTek phones, `*_2core.so`). None
   is on hand.
