# isp-user: probing MT8163 ISP pass 2 from Linux

Tools for moving the Show 5 2nd gen's RAW10 develop onto ISP pass 2. The findings are in
`../research/mt8163-isp-pass2.md` ("Stage 1 on the unit", "Stage 2: pass 2 runs", "Stage 3: in
production"). The production path is the camera driver's `CRONOS_IOC_DEVELOP`
(`../patches/mt8163-cronos-camera.c`); these tools found it and check it.

Stage 1 (read-only):

- **`regprobe/`** (Go, arm64, static): read-only snapshots of the CAM (`0x15004000`), imgsys
  (`0x15000000`) and MMSYS (`0x14000000`) register pages through `/dev/mem`, optionally while it
  streams the camera itself. It prints every word that reads non-zero and marks the ones that change
  between snapshots.
- **`harness/tpipe_test.c`** (armv7 bionic): runs MediaTek's pass-2 tile driver,
  `tpipe_main_platform` in the unit's own `/vendor/lib/libdpframework.so`, on a configuration given on
  the command line. It prints the tile table (TDR) and the descriptor, and decodes the table with the
  library's own `tile_tdr_isp_fprint_dump`.

Stage 2 (pass 2 runs; it writes the CAM window, and larb2's MMU_EN while it holds the hardware):

- **`kmod/isp2test.c`** (GPL-2.0, out-of-tree module): borrows the camera node's power domain,
  clocks, larb and IOMMU domain, allocates the pass-2 buffers there, and exposes them and a small
  command interface (register writes and reads in the CAM window, a bounded start-and-poll, the
  debug port) in debugfs under `isp2test/`. Used only while the capture is stopped.
- **`tools/pass2.sh`**: the register recipe that develops one frame (RAW10 1600x1200 in, YUYV out,
  three tiles from a tile table), through `isp2test`.
- **`isp2cmp/`** (Go, host): makes synthetic RAW10 test frames, generates the tile table (`tdr`,
  bit-exact with the vendor tile driver's), develops a frame on the CPU the way echod does (a port of
  its `Full`/`I420` color), converts the ISP's output to PNG and reports PSNR.

The vendor library is never copied into this directory or into `out/`: it is linked against at
build time and loaded from `/vendor/lib` on the unit. Stage 1's tools write no register.

## Layout

| Path | What |
| --- | --- |
| `regprobe/main.go` | The register reader (`-stream`, `-n`, `-interval`, `-o`, `-mmsys-only`, `-hi` for CAM `0x15005000-0x15007fff`, `-save` a frame, `-exposure`/`-gain`) |
| `harness/tpipe_test.c` | The tile-driver harness; `cfg_fields[]` is the recovered `ISP_TPIPE_CONFIG_STRUCT`; `mdp.N=value` runs `tile_main_platform` with an `MDP_TILE_CONFIG_STRUCT` (the ISP-to-MDP path) |
| `harness/dpcap.c` | Stage 4: runs DpFramework's `DpBlitStream` (or `DpIspStream`) with `/dev/mtk_cmdq` faked, and prints the MDP register program it submits. Stage 5: the ISP stream runs too (`28=1`, RSP on), with `DPCAP_SIZE=WxH` and `DPCAP_ROT`, and prints its tile table |
| `stubs/libsync.c`, `stubs/empty.c` | Stand-ins for the four libraries libdpframework needs that the vcodec runtime lacks |
| `tools/build.sh` | Docker build of the stubs and the harness |
| `tools/run.sh` | On-device launcher (the Android 11 runtime from `../vcodec-user`) |
| `kmod/isp2test.c`, `kmod/Makefile` | The pass-2 harness module |
| `kmod/mdptest.c` | Stage 4: the MDP harness module (RDMA/RSZ/WDMA/WROT, memory to memory, beside the display) |
| `tools/kmod.sh` | Docker build of the modules against the `techo5-k6` kernel tree |
| `tools/pass2.sh` | On-device: one pass-2 develop through the module |
| `tools/link.sh` | Stage 5: route A on the unit, pass 2 straight into the MDP (CAMIN → RSZ1 → WROT, I420), through `isp2test` and `mdptest` |
| `isp2cmp/main.go` | `mkraw` (with `-truth`, `-noise`, `-shot`), `tdr`, `ref`, `show`, `psnr` |
| `isp2cmp/develop.go` | Stage 3: `tone` (echod's for a frame), `ctl` (the isp2test commands for it), `i420` (echod's CPU I420, or the scene's with `-truth`), `conv` (the ISP's YUYV to I420, as the driver does), `cmp` (PSNR of two I420s, PNGs), `dev` (the driver's `CRONOS_IOC_DEVELOP`, timed). Builds for the host or the unit (arm64) |

## Build

The ARM side needs Docker, an image with `arm-none-eabi-gcc` (the repo's `techo5-kbuild` has one;
set `IMAGE=` to use another), and the NDK sysroot that `../vcodec-user/tools/fetch-deps.sh` puts in
`../vcodec-user/.cache`. It does not use the NDK's clang: the harness declares the dozen libc
functions it calls itself, and links against bionic's stubs and crt objects with GNU ld.

```sh
scp -O unit:/vendor/lib/libdpframework.so /some/scratch/vendor/   # outside the repo
tools/build.sh /some/scratch/vendor                                 # -> out/bin/tpipe_test, dpcap, out/lib/*.so

# regprobe, from the repo root
docker run --rm -v $PWD:/r -v techo5-gomod:/go/pkg/mod -v techo5-gocache:/root/.cache/go-build \
  -w /r/custom-kernel/cronos/isp-user/regprobe -e GOOS=linux -e GOARCH=arm64 -e CGO_ENABLED=0 \
  golang:1.26 go build -trimpath -o /r/custom-kernel/cronos/isp-user/out/bin/regprobe .
```

The four stand-in libraries:

- `libsync.so`: the fence calls, which fail cleanly.
- `libgralloc_extra.so`, `libhardware.so`, `libnativewindow.so`: empty.

libdpframework imports only 22 symbols outside bionic. The other 18 (`liblog`, `libcutils`,
`libion`, `libion_mtk`) are the vcodec runtime's own stand-ins.

## Run on the unit

The image already carries the Android 11 runtime at `/usr/local/lib/techo5-vcodec`. Copy `out/bin`,
`out/lib` and `tools/run.sh` to a directory on the unit, for example `/tmp/isp-user`. Then:

```sh
DIR=/tmp/isp-user /tmp/isp-user/tools/run.sh                       # defaults: 1600x1200 RAW10 -> IMG2O
DIR=/tmp/isp-user /tmp/isp-user/tools/run.sh -o /tmp/isp-user/r sw.tpipe_width=400 top.cfa_en=0 cfa.bayer_bypass=1
LOG=D DIR=/tmp/isp-user /tmp/isp-user/tools/run.sh ...             # the library's own log, with config and errors
```

`-o DIR` writes the following to DIR:

- `config.bin`
- `desc.bin`
- `work.bin`: the whole working buffer. The tile register map is its first 980 bytes.
- `tdr.bin`
- `tdr.txt`: the library's decode of the tile table.

Exit status 0 means the library returned `ISP_TPIPE_MESSAGE_OK`.

### regprobe

```sh
touch /run/techo5/hold; kill $(pidof techo5)        # echod must not hold the camera
/tmp/isp-user/bin/regprobe -stream -n 10 -o /tmp/regs.txt
rm /run/techo5/hold                                 # echod comes back on its own
```

The tool reads the CAM and imgsys pages only once frames are arriving: outside a stream the ISP
power domain is off, and a read there would stall the bus. It never touches CAM `0x020–0x04c`,
which holds the interrupt status words that clear on read, or the write-only `0x080–0x094`. The
MDP blocks (`0x14001000–0x14006fff`) are left out on purpose: `MMSYS_CG_CON0` shows their clocks
gated.

## Pass 2 on the unit (stage 2)

```sh
tools/kmod.sh                                            # -> out/kmod/isp2test.ko (kernel #15's tree)
# host, isp2cmp built for the host or run in golang:1.26:
isp2cmp mkraw -o syn.raw -photo some.png                 # or a frame saved with regprobe -save
isp2cmp tdr -o tdr.bin                                   # 3 tiles, PIX_ID 3 (red first), LAST_IRQ on the last
# unit (copy isp2test.ko, tools/pass2.sh, syn.raw, tdr.bin to /tmp):
touch /run/techo5/hold; kill $(pidof techo5)             # stop the capture
mount -t debugfs none /sys/kernel/debug
insmod /tmp/isp2test.ko
sh /tmp/pass2.sh /tmp/syn.raw /tmp/tdr.bin /tmp/out.yuv  # prints the run: DONE after ~5.4 ms
rmmod isp2test; umount /sys/kernel/debug; rm /run/techo5/hold
# host:
isp2cmp ref -raw syn.raw -o ref.yuv                      # echod's develop, linear tone, BT.601 full range
isp2cmp psnr -a ref.yuv -b out.yuv -signed               # the ISP's chroma is two's complement
isp2cmp show -yuv out.yuv -signed -o out.png
```

`isp2test`'s `ctl` takes one command a line (or several separated by `;`): `on`, `off`, `reset`
(the CAM soft reset), `w OFF VAL...` (consecutive words; `@imgi`, `@img2o`, `@tdr`, `@aux` with an
optional `+N` stand for a buffer's IOVA), `wm OFF CLEAR SET`, `r OFF [N]`, `ri OFF [N]` (imgsys,
read only), `st` (the read-clear status words), `dbg SEL...` (the debug port),
`run OFF VAL MS [MASK [TOFF TCLR TSET]]` (write the start register, optionally one masked write
right after it, then poll the status until a bit of MASK, default PASS2_DON, or MS milliseconds),
`vs` (wait for the TG's frame counter to move: a frame's last line is in), `wait OFF MASK MS` (poll
a register until a bit of MASK), `fill BUF BYTE`, `us N`, `clear`. Results go to `log`. Offsets are relative to `0x15004000` and limited to the camera node's
CAM window (`0x000-0x3ffc`). While powered, every larb2 port is put in translated mode, so a stray
DMA address faults in the IOMMU (`dmesg`) rather than reaching memory.

## License

MIT, as the rest of the repository, except `kmod/`, which is GPL-2.0 like the kernel it is built
into. MediaTek's library is not included and is not covered.

## Stage 3 on the unit

With the stage-3 kernel the driver develops frames itself; `isp2cmp` checks it and compares:

```sh
isp2cmp mkraw -o syn.raw -photo p.png -truth syn.lin -noise 1.5 -shot 0.2   # a frame and its scene
isp2cmp dev -raw syn.raw -o isp.i420 -n 20      # the driver's develop, with echod's tone for the frame
isp2cmp i420 -raw syn.raw -o cpu.i420           # echod's CPU develop
isp2cmp i420 -raw syn.raw -truth syn.lin -o truth.i420
isp2cmp cmp -a truth.i420 -b isp.i420 -x1 800   # PSNR against the scene, photo half
```

Beside a running capture, `dev` waits for a blanking (the driver lengthens the sensor's frame for it).
`isp2cmp ctl` writes the same tone as isp2test commands, for the stage-2 recipe (`tools/pass2.sh`).

## Stage 4: the MDP

`dpcap` prints what DpFramework would program for an MDP job; nothing is written anywhere:

```sh
PROG=dpcap DIR=/tmp/isp-user /tmp/isp-user/tools/run.sh blit YUYV 1600 1200 I422 1600 1200
```

Each line is a write (`W address value mask`), a poll (`P`) or a GCE event wait (`E event N`; the
numbers are `cmdq_event_common.h`'s, 52 RDMA0's end of frame, 61 WROT's). It also runs offline: the
vcodec runtime's armv7 tree (`../vcodec-user/tools/qemu-test.sh` builds one) with
libdpframework.so, the stand-ins and `bin/dpcap` added, as a `linux/arm/v7` container under qemu.

`mdptest` replays such a program on the hardware, beside the display and a running echod:

```sh
tools/kmod.sh                                 # -> out/kmod/mdptest.ko
# unit:
mount -t debugfs none /sys/kernel/debug; insmod /tmp/mdptest.ko
D=/sys/kernel/debug/mdptest
cat frame.yuyv > $D/yuyv                      # 1600x1200 YUYV in
echo on > $D/ctl                              # the MDP clocks, larb0's RDMA/WDMA/WROT ports translated
cat blit.ctl > $D/ctl; cat $D/ctl             # the program, its log
cat $D/i420 > out.i420; rmmod mdptest
```

`ctl` takes `on`, `off`, `w ADDR VAL [MASK]` (absolute addresses; `@yuyv`, `@i420`, `@aux` with an
optional `+N` stand for a buffer's IOVA; with a mask, the other bits are kept, as the GCE does),
`r ADDR [N]`, `p ADDR VAL MASK MS` (poll until the masked word equals VAL), `rst [BITS]` (the MDP's
mmsys resets), `fill BUF BYTE`, `us N`, `clear`. Writes are limited to the MDP blocks
(`0x14001000-0x14006fff`), MMSYS's MDP routing words and mutex 6. The buffers are the camera
device's, in the IOMMU domain the display shares: a stray address faults there (`dmesg`). The
recipe the driver uses came out of these two; `../research/mt8163-isp-pass2.md`, "Stage 4", has it.
