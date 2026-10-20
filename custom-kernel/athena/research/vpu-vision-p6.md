# The MT8183 "VPU" (Vision P6) on athena: can mainline drive it?

Read-only research from the eMMC dumps of one unit (`cam_vpu1-3`, `vendor`, `lk`, `boot`), the Fire OS
4.4 kernel's strings and device tree, and MediaTek's MT6771 VPU driver (same VPU generation, same
firmware protocol, same fixed addresses; published in many vendor trees, e.g.
`drivers/misc/mediatek/vpu/mt6771/vpu_{hw,reg}.{c,h}`). Nothing was run on a device.

## Verdict

**Feasible, and simpler than the README assumes.** We can drive the stock firmware and its algorithms
with no Cadence or MediaTek toolchain:

- The `cam_vpu*` images are **plain, unencrypted Xtensa ELF files, not stripped, with full DWARF**
  (symbols, enums, structs, source paths). The only protection is an RSA signature at the end that
  **LK** checks before it copies the images into `vpu_binary`. Nothing checks it again after that.
  TF-A plays no part, and the DSP has no secure boot: the normal-world kernel writes the reset vector
  and releases the core.
- The host protocol can be read straight from the firmware's DWARF (`VPU_CMD_*`, the `INFO00..39`
  struct) and matches MediaTek's MT6771 driver exactly.
- Fire OS's own normal-world kernel drives the VPU registers (`vpu_core0@0x19100000`), so DEVAPC
  under the Fire OS TF-A this unit runs must allow it.
- We do not need the EMI-protected `vpu_binary` copy. Linux can read the images from the partitions
  (or from files) and put them in ordinary reserved DRAM at the addresses the firmware is linked for.

Using the stock **face detector** (`vpu_lib_dlfd_vpu0`) is the hard part. Booting the core and
running a kernel are not. The DSP returns raw CNN feature maps. Decoding the boxes and landmarks,
building the image pyramid and laying out the buffers is done on the CPU by Fire OS's
`vendor/lib/libdnn_fd.so` (`mtkdl::IvpNetVPU0`, a SqueezeNet RPN; 57 KB of ARM32). That library
would have to be reverse-engineered or replaced. The NeuroPilot runtime (`apu_lib_apunn`) is
further off still: it runs command streams built by an NNAPI HAL compiler. (Since done: section 16 runs
apunn's convolutions from Linux, byte-exact, the person detector's in 2.55 ms.)

## 1. What the `cam_vpu` partitions hold

Every partition has the same layout:

| offset | content |
|---|---|
| 0x000 | MTK partition header: magic `0x58881688`, length L, name `cam_vpuN`; extended header `0x58891689` (header size 0x200) |
| 0x200 | `vpu_image_header` (0xd38 bytes; offsets below are from 0x200) |
| +0x000 | version `0x17110109`, then a date word, builder string `"syl"` |
| +0x028 | header_size `0xd38` |
| +0x02c | image_size (`0x18e7d8` in vpu1, 0 in vpu2 and vpu3) |
| +0x030 | segment count, then 50 x `{u32 core_mask, offset, dst_addr, length, file_size}` |
| +0x41c | algorithm count, then 50 x `{u32 core_mask, offset, length, char name[32]}` |
| +0xd38 | the ELF files, back to back (offsets are relative to 0x200) |
| 0x200+L-0x10c | trailer: `"_VPUx_VER:"`, 2 bytes `01 02`, then a 256-byte RSA-2048 signature |

`core_mask` is 0x61, 0x62 or 0x64, that is core 0, 1 or 2. MT8183 has two cores; the core 2 entries
are unused. In vpu1, some stale bytes from an older, longer image remain after L. A second
`_VPUx_VER:` at 0x194184 belongs to them.

**Toolchain and core config.** Every ELF has `e_machine` 94 (Xtensa) and a `.xtensa.info` section
that reads: `CORE_NAME="MVPU6_0413_Prod"`, `HW_VERSION="LX7.0.4"`, `HW_CONFIGID0=0xc0f3dbfe`,
`HW_CONFIGID1=0x21069682`, `ABI=0` (windowed), and TIE checksums
`7d6bb054/fd38296b/dd559a2e/9b955ab2`. The toolchain is XCC 12.0.4 (RG-2016.4), or 12.0.7
(RG-2017.7) for apunn. The vector code needs the core's TIE (the Vision P6 FLIX instructions), which
is not public. A generic Xtensa disassembler (capstone 6) mis-decodes most of it.

**cam_vpu1: the main program.** Three copies, one per core:

- Core 0: reset vector at `0x7da00000`; main program at `0x7db00000..0x7db13e88` (DDR); DRAM0 data
  at `0x7ff00000` (0x6c0 bytes); IRAM text at `0x7ff40000` (0x674 bytes: `m_load_seg`,
  `m_load_seg_dma`, the algorithm loader).
- Core 1 uses `0x7e300000` and `0x7e400000`; core 2 uses `0x7ec00000` and `0x7ed00000`.

These are exactly MT6771's `VPU_MVA_RESET_VECTOR`, `VPU_MVA_MAIN_PROGRAM`, `VPU2_*` and `VPU3_*`
values. The sources are `vpu_main/main/main.c` (`main_vpu`), plus `core/driver/{dma,interrupt,param,
register,wdt}`, `core/loader/{mloader,libloader_pil}` (the Xtensa PI-library loader), `vpu_metlog.c`
and `vpu_decrypt` (60 bytes, a stub). There are 2383 symbols.

**cam_vpu2: the algorithms** (`kernel lib`, linked at `0x7de00000` for core 0 and `0x7e700000` for
core 1). The table names do not always match what the code is:

| name | what it is (from the ELF's source paths) |
|---|---|
| vpu_flo_d2d_k14, k15, k19 | guided-filter bokeh/depth stages (pregfastds, pregfastp1, gfbkpart2) |
| vpu_flo_d2d_k3 | warp perspective |
| vpu_flo_d2d_k5 | **SLT self-test kernel**: ports `d2dk5ip0`/`d2dk5op1`, properties version, wrk_buf_size, support_format, param1-5 |
| vpu_flo_d2d_k7 | corner Harris (VP6) |
| vpu_flo_d2d_lap | Laplacian |
| vpu_lib_d2d_canny / harris / histogram | Canny edges, Harris corners, histogram |
| vpu_lib_d2d_kidmaperf | DMA performance test (the only core-2 entry) |
| vpu_lib_d2d_swnr | software noise reduction (IIR, blending, pixel diff) |
| **vpu_lib_dlfd_vpu0** | **deep-learning face detector with landmarks** (`libfd_vpu0_landmark`, `cnnrt`, `libxi/cnn`); ports `DLFD_input`, `DLFD_pyramid`, `DLFD_output`; weights built in (`setup_weight`, 0xc50a4-byte ELF) |
| **apu_lib_apunn** | NeuroPilot NN runtime (`vp6-ann`): conv, depthwise/dilated conv, deconv, pool, softmax, concat, pad, PReLU/ReLU, logistic, l2norm, resize, requant, ...; this ELF has no symbol table |

**cam_vpu3** holds `vpu_flo_d2d_ksample`, a sample kernel, one copy for each core.

In vendor: `lib/libvpu.so` (the `VpuStream`/`VpuAlgo`/`VpuRequest` user API over `/dev/vpu`),
`lib/libdnn_fd.so` (the face-detection host side, `vendor/mediatek/proprietary/hardware/libdnn/fd/sqzrpnfd-vpu.cpp`),
`lib/libcamalgo.vpunr.so`, and `bin/hw/android.hardware.neuralnetworks@1.1-service-apunn`.

## 2. Signatures, who checks them, and the host protocol

- **LK** (`platform/mt8183/mt_vpu.c` in `lk.img`) reads `cam_vpu1/2/3`, prints
  `vpu %s verification pass/fail` and asserts on failure. The check is
  `amzn_lk_verify_image_maybe` (RSA-PSS with the prod key; the eng key is tried only on
  non-production devices), using the `_VPUx_VER:` trailer. LK also checks the header version and
  size, copies everything into `vpu_binary`, protects it with `emi_mpu_set_protection`, and adds
  `bin-phy-addr`/`bin-size` to the `mediatek,vpu_core0` DT node.
  **Never modify these partitions.** LK would assert and the unit would not boot.
- **TF-A** is not involved. **The VPU has no boot ROM check.** The host writes
  `XTENSA_ALTRESETVEC` and drops RUN_STALL, and the core runs whatever is at that address.
- **Can Linux start a core with the stock firmware and use its algorithms? Yes.** The protocol,
  from the firmware's DWARF (`_VPU_CTL_STRUCT_`, 40 registers, read through `pCtl`):

  - `INFO00` holds the state: 0 NOT_READY, 1 READY, 2 IDLE, 4 BUSY, 8 ERR, 16 TERMINATED.
  - `INFO01` holds the command: `DO_EXIT 0x00`, `DO_LOADER 0x01`, `DO_DECRYPT 0x02`,
    `DO_PASS1_DL 0x10`, `DO_PASS2_DL 0x11`, `DO_D2D 0x22`, `SET_DBG 0x40`, `SET_MPU 0x41`,
    `SET_FTRACE_LOG 0x42`, `GET_SWVER 0x81`, `GET_ALG_ATTR 0x82`.
  - The other INFO registers carry arguments and results:

    | register | used for |
    |---|---|
    | INFO05 | port_num |
    | INFO06 | port_struct_ptr |
    | INFO07, INFO08 | info_prop ptr, length |
    | INFO09, INFO10 | info desc count, desc ptr |
    | INFO11 | settings desc count |
    | INFO12 | set_desc_ptr, `src_data_addr` (the loader's algorithm address) or `buffer_num` |
    | INFO13 | `src_data_size` or `buffer_struct_ptr` |
    | INFO14, INFO15 | settings buffer ptr, size; or `vpu_clk_khz` |
    | INFO16 | vpuif_clk_khz |
    | INFO17, INFO18 | notify / log counters |
    | INFO19 | `pbuf_iram_data` |
    | INFO20 | `sw_version` |
    | INFO21, INFO22 | log buffer address, size |
    | INFO23 | timestamp |
    | INFO29 | host version (the firmware prints `Host version should be 0x%08x or later`) |

  - D2D buffers are `struct vpu_buffer` (64 bytes): `port_id u8, format u8, plane_count u8,
    width, height, planes[3]{stride, length, u64 ptr}`.
  - The IRAM image at INFO19 is `IRAM_DATA_HEADER {u32 num; {ofst, dst_addr, size}[20]}`, followed
    by the data: the 0x7ff00000 and 0x7ff40000 segments of cam_vpu1.

  The driver's sequence for each command:

  1. Write the arguments, `INFO01 = cmd`, `RUN_STALL = 0`, `CTL_XTENSA_INT = 1`.
  2. Wait for the VPU's interrupt, or poll `XTENSA_INT` bit 0.
  3. Check that `INFO00` is READY or IDLE, then set `RUN_STALL = 1`.
  4. Clear the interrupt with `XTENSA_INT = 1`.

  `GET_ALG_ATTR` makes each algorithm describe its own ports and properties, so most of each
  algorithm's interface can be found at run time.

## 3. Registers, power, clocks, memory path, protection

Addresses come from the Fire OS device tree (`fos*.dts`, extracted from `boot.img`). Register offsets
come from MT6771's `vpu_reg.c`, whose memory names match the Fire OS kernel's own strings
(`VPUSYS_COREA_DMEM_0_128KB`, `..._IMEM_256KB`, `_CONTROL`, `_DEBUG`).

- Fire OS DT: `vpu_core0@19100000` (size 0x94000, GIC SPI 292, `0x124`, flags 8) and
  `vpu_core1@19200000` (SPI 293). `ipu_conn@19000000`, `ipu_adl@19010000`, `ipu0@19180000` and
  `ipu1@19280000` are the clock syscons, and mainline has the same four.
- The core window (core 0; core 1 is +0x100000). The AP sees the DSP's local memories directly:

  | AP address | VPU address | content |
  |---|---|---|
  | 0x19100000 | 0x7ff00000 | DMEM0, 128 KB |
  | 0x19120000 | 0x7ff20000 | DMEM1, 128 KB |
  | 0x19140000 | 0x7ff40000 | IMEM |
  | 0x19180000 | 0x7ff80000 | CTRL (the firmware's `g_VPU_CTL_BASE = 0x7ff80100`, `g_VPU_ADL_BASE = 0x7ff80200`) |
  | 0x19190000 | | DEBUG |

  CTRL registers, as offsets from 0x19180000:

  | offset | register |
  |---|---|
  | 0x00 / 0x04 / 0x08 | CG_CON / SET / CLR: bit 0 JTAG, bit 1 AXI_M, bit 2 IPU. These are the bits mainline's `clk-mt8183-ipu0` gates. |
  | 0x0c | SW_RST: bit 0 HW, bit 1 AXI_M, bit 2 APB, bit 4 B_RST, bit 8 D_RST, bit 12 OCDHALTONRESET |
  | 0x110 | CTRL: bit 23 RUN_STALL, bit 19 STATE_VECTOR_SELECT (use ALTRESETVEC), bit 17 PIF_GATED, bit 26 PBCLK, bit 31 P_DEBUG, bits 29:27 SRAM_CONFIGURE, PRID in bits 16:1 |
  | 0x114 | XTENSA_INT (VPU to AP) |
  | 0x118 | CTL_XTENSA_INT (AP to VPU) |
  | 0x11c | CTL_XTENSA_INT_CLR |
  | 0x12c | INT_MASK |
  | 0x13c / 0x140 | AXI_DEFAULT0 / 1: bits 0:22, 0:27, 1:4 and 1:9 route requests through the M4U; 0:28 is pre-ultra |
  | 0x150..0x1cc | XTENSA_INFO00..31 |
  | 0x1d0.. | DEBUG_INFO |
  | 0x1f8 | XTENSA_ALTRESETVEC |

- **Power (mainline):** `MT8183_POWER_DOMAIN_VPU_TOP` (12) is a child of DISP. Its clocks are
  `topckgen MUX_IPU_IF` and `MUX_DSP`, plus `ipu_conn` gates 0-5. Its children are `VPU_CORE0` (13,
  clock `MUX_DSP1`) and `VPU_CORE1` (14, `MUX_DSP2`). Fire OS splits each core domain into
  "dormant" and "shutdown" MTCMOS states; mainline turns on both.
- **Clocks Fire OS enables for core 0:** `top_dsp/dsp1/dsp2/ipu_if_sel`; `ipu_core0` jtag, axi_m
  and ipu; `ipu_adl_cabgen`; `ipu_conn` dap_rx, apb2axi, apb2ahb, ipu/ipu1/ipu2_cab1to2, cab3to3,
  cab2to1, cab3to1_slice, ipu, ahb, axi, isp, cam_adl, img_adl; `mmsys gals_ipu2mm`, `gals_ipu12mm`,
  `gals_comm0/1`, `smi_common`. Every one of these exists in mainline (`mt8183-clk.h`), and
  `CONFIG_COMMON_CLK_MT8183_IPU_*` and `_MMSYS` are already `=y` in our 7.2 config.
- **Memory path:** VPU0's AXI masters sit on **larb2** (`M4U_PORT_IMG_IPUO`, `IPU3O`, `IPUI`, in the
  ISP domain). VPU1's sit on **larb3** (`CAM_IPUO`, ..., in the CAM domain). Fire OS maps
  `vpu_binary` through the M4U at the fixed addresses above. MT6771's driver has a
  `BYPASS_M4U_DBG` mode instead: leave the AXI_DEFAULT "via M4U" bits clear, and the VPU's 32-bit
  addresses are physical. Mainline leaves larb ports that no device has attached with their MMU
  bit off. So bypass is the minimal route: put the images at **PA 0x7da00000+** in a `no-map`
  region. Core 0 needs reset vector, main program and lib area (0x7da00000..0x7e300000), plus room
  for buffers; MT6771 reserves addresses up to 0x82600000, including `SHARED_DATA` at 0x7f500000.
  The clean alternative is the M4U with a reserved-memory `iommu-addresses` fixed mapping.
- **DEVAPC:** the Fire OS kernel (normal world) maps and reads the whole 0x19100000 window
  (register dumps, DMEM/IMEM stack dumps), so the AP may access it. Unlike the SCP problem in the
  README, here the AP drives the core itself.
- **EMI MPU:** it guards only LK's `vpu_binary` copy at 0xbb140000. The plan above never touches
  that. One thing to check on the unit before choosing PA 0x7da00000: that no LK-placed carve-out
  is there. Fire OS DT has a dynamically placed 80 MB `secure-carveout`; look at
  `/sys/firmware/devicetree/base/reserved-memory` and `/proc/iomem`.

## 4. First experiment (smallest steps, each one observable)

A throwaway platform driver, or a debugfs module, in the 7.2 tree, with a DT node like this:
`power-domains = <&spm MT8183_POWER_DOMAIN_VPU_CORE0>` (plus ISP for larb2), the clocks listed
above, `interrupts = <GIC_SPI 292 IRQ_TYPE_LEVEL_LOW>`, and `memory-region` set to a `no-map`
region at 0x7da00000. Map 0x19100000 with plain `devm_ioremap`, because `ipu_core0`'s clock
driver already claims 0x19180000.

1. **Power and registers only.** Power on VPU_TOP and CORE0 and enable the clocks. Read
   CTRL+0x110 (CTRL, PRID), 0x1f8 (ALTRESETVEC) and INFO00. Write and read back a word at
   0x19100000 (DMEM0). This proves power, clocks and DEVAPC.
2. **Load.** Read `cam_vpu1` from `/dev/mmcblk0p15` (or a file). For each core-0 segment with
   `dst < 0x7ff00000`, copy `img[0x200 + offset]`, `length` bytes, to `PA dst`.
   Build `IRAM_DATA_HEADER{num=2, {ofst, 0x7ff00000, 0x6c0}, {ofst, 0x7ff40000, 0x674}}` plus the
   data at a free PA, for example 0x7dd00000 inside the main-program area, which ends near
   0x7db14000.
3. **Boot**, following MT6771's `vpu_hw_boot_sequence`:
   1. Set `ALTRESETVEC = 0x7da00000`.
   2. In CTRL, set bits 31, 26, 19, 23 and 17, and clear bits 29:27.
   3. Pulse SW_RST bit 12; pulse bits 4 and 8.
   4. Clear CTRL bit 17. Leave AXI_DEFAULT0 and 1 bits 22, 27, 4 and 9 **clear** (bypass); set
      bit 28.
   5. Clear RUN_STALL (bit 23). Wait for the interrupt or poll `XTENSA_INT`; expect
      `INFO00 = 1` (READY).
   6. Set RUN_STALL, then write `XTENSA_INT = 1`.

   On a timeout, dump DMEM0 at 0x19100000 (`mtk_stack_dump` sits at VPU 0x7ff002c0) and
   DEBUG_INFO.
4. **Hand-shake.** Send `SET_DBG (0x40)`: INFO19 = the IRAM blob's PA, INFO21/22 = a 64 KB log
   buffer, INFO23 = a timestamp, INFO29 = host version. Then trigger as in section 2.
   Afterwards, read the firmware's log text from the INFO21 buffer and **INFO20 = sw_version**
   (`GET_SWVER 0x81` also returns it). The required host version is not yet known: MT6771 uses
   `0x18070300`, and the firmware's error message prints the value it wants.
5. **First algorithm.** Copy `vpu_flo_d2d_k5` (the SLT kernel) from `cam_vpu2` into DRAM. Then:
   1. `DO_LOADER (0x01)` with INFO12 = its PA, INFO13 = its length, INFO15/16 = clock in kHz.
   2. `GET_ALG_ATTR (0x82)` with INFO06/07/10/12 pointing into a work buffer. Read back the port
      and property descriptors.
   3. `DO_D2D (0x22)` with INFO12 = 2 buffers (port `d2dk5ip0` in, `d2dk5op1` out), INFO13 = the
      `vpu_buffer` array, INFO14/15 = the settings blob.

   Then try `vpu_lib_d2d_histogram` or `canny`, whose results are easy to check. Only after that
   `vpu_lib_dlfd_vpu0`, together with reverse-engineering the input and pyramid layout and
   `bbox_decode` from `libdnn_fd.so`.

Risks and unknowns:
- The MT8183 offsets are taken from MT6771; step 1 confirms them.
- Whether this unit's TF-A allows larb2/3 M4U bypass. Fall back to the M4U with an
  `iommu-addresses` fixed map.
- VCORE/DSP DVFS: start at the boot default mux rates and pass them in INFO15/16.
- The required host version (step 4).

## Working files (scratch, not in the repo)

a scratch directory outside the repo (`vpu/`):
- `elf/*.elf`: the 33 ELF files, with `.syms` symbol lists and `.src` source lists.
- `boot/fos*.dts`: the Fire OS device trees, and `boot/Image`, the Fire OS 4.4 kernel.
- `vendor/`: the vendor partition.
- `ref6771/`: MediaTek's MT6771 driver.
- `extract.py`, `syms.py`, `dwarf_types.py`: the parsers.

## Results on the unit (2026-10-18)

Run with `VPU_PROBE=1 custom-kernel/athena/build.sh` (then patches/vpu-probe.py, now patches/vpu.py with `VPU_DEBUG=1`: a throwaway driver,
debugfs `techo5-vpu/{power,regs,mem}`, the core's power domain and Fire OS's 28 clocks, a no-map
region at 0x7da00000), from the test slot only. Tools: `custom-kernel/athena/tools/vpu/`.

1. **Power, clocks, DEVAPC: fine.** With the core powered, CTRL (0x19180110) reads `0x40820000`
   (RUN_STALL and PIF_GATED set), CG_CON `0xfffffff8`, ALTRESETVEC `0x50000000`. **Do not touch
   DMEM/IMEM while PIF_GATED (CTRL bit 17) is set**: the read stalls the bus and the unit resets
   (the lockup guards reboot it in about a minute).
2. **Boot: works.** `mkregion.py` + `vboot.sh` (ALTRESETVEC 0x7da00000; CTRL bits 31, 26, 19, 23, 17
   set and 29:27 clear; the SW_RST pulses; PIF ungated; AXI_DEFAULT0 = bit 28 only, the M4U
   bypassed; RUN_STALL cleared): INFO00 = 1 (READY) at once, the PC in the main program's idle loop
   (0x7db020f3). The core reads and writes DDR at physical addresses: the bypass holds.
3. **Commands: work.** `vcmd.sh` writes the INFOs and INFO01, clears RUN_STALL and raises
   CTL_XTENSA_INT. The firmware only takes a command once its last interrupt has been acknowledged
   by writing **1** to XTENSA_INT (0x80114). XTENSA_INT itself never read back as set: completion
   shows in the log and in the INFOs. `SET_DBG` (0x40; INFO19 = the IRAM blob, INFO21/22 = a 64 KiB
   log buffer, INFO29 = 0x18070300) loads the IRAM blob and logs:
   `VPU_SW_BUILD_DATE(0x23081617) VPU_SW_VERSION(0x16122608) VERSION_VPU_PORT_ST(0x0103b7f3)`;
   INFO20 = 0x23081617.
4. **Algorithms: not yet.** `DO_LOADER` (0x01) with INFO12/13 = cam_vpu2's `vpu_flo_d2d_k5` ELF (at
   PA 0x7dc00000) and INFO15/16 = 312000 is taken (`CMD:(0x1)`), raises no error, and leaves INFO00
   at READY where MediaTek's driver expects IDLE. `GET_SWVER` (0x81) and `GET_ALG_ATTR` (0x82) then
   log `ERROR:CMD_ID`, though the firmware's own enum has those values. The loader is Cadence's PIL
   (position-independent library) loader (`m_loader_pil`, `m_loader_pil_validate_dynamic`,
   `struct xtlib_pil_info`), entered from `vpu_loader(_VPU_CTL_STRUCT_ *, _VPU_DL_STRUCT_ *)`.
   **Next:** find what `vpu_loader` takes from the INFOs (it may want a descriptor, or the library
   packaged by xt-pkg-loadlib rather than the raw ELF), with an Xtensa disassembler for its scalar
   code (capstone 6 decodes the core's base ISA); then GET_ALG_ATTR's port structures from DWARF,
   then DO_D2D.

## Loader and D2D protocol (reverse-engineered)

Offline, from cam_vpu1's core 0 program (`cam_vpu1_00_f38.elf`: DWARF line table, symbols, and the
base-ISA part of the code), the k5 and ksample ELFs, and Fire OS's own MT8183 kernel (the ARM32
`boot/Image`, VA = 0xc0008000 + file offset). Scratch work: `scratchpad/vpu-loader/` (`dis3.py` is a
disassembler that understands the core's CONST16 and narrow branches and annotates line numbers;
`main_vpu.dis`, `fos_hw.s`).

**How the code was read.** XCC packs most scalar code into FLIX bundles that no public tool decodes.
Instruction length is set by the low nibble of the first byte: 0-7 is 3 bytes, 8-0xd is 2 bytes, 0xe
is a 16-byte bundle and 0xf an 8-byte bundle. This sweep lands exactly on 292 of the 302 function
ends. Everything outside bundles decodes with capstone. Constants are loaded with CONST16 pairs, so
register addresses and string pointers can be read directly. In the common "const16 aN, imm16"
bundle slot, bits 3:0 of the immediate are in bits 3:0 of byte 1, and bits 10:4 are in bits 7:1 of
byte 2. That is enough to recover the register a bundle writes (for example `a4 f6` = 0x1b4,
`a8 f6` = 0x1b8, `a4 e2` = 0x114). Branch offsets inside bundles were **not** decoded. Below, VPU
addresses `0x7ff801xx` are the AP's `0x191801xx` (`vr 0x801xx`).

### 1. The main loop and dispatcher (`main_vpu`, 0x7db05840, main.c)

- Before the loop: `parameterSet(BUSY)` (main.c:526), then iDMA and interrupt init.
- Loop top (main.c:553-566): `parameterSet(READY)`, then `interruptToMCU(0)`, then `interruptWait`
  (WAITI), then `parameterSet(BUSY)`. `parameterSet(state)` (0x7db022e8) writes **INFO00 = state**
  and **INFO17 = 0x100**. The values come from the `movi a10, N` slot of the bundles
  (`0fa1c002…` = 1, `0fa4c002…` = 4).
- Then (main.c:569-599) it snapshots the INFO registers into a local `_VPU_CTL_STRUCT_ vpu_ctl`
  (INFO01, 02, 03, 04, 05, 06, 07, 08, 09, 10, 11, 12, 13, 14, 15, 16, 19, 21, 22) and logs
  `CMD:(0x%x)`. It writes INFO25 (main.c:603), then dispatches on `vpu_ctl.info01` (main.c:605,
  five bundles at 0x7db05ea2..0x7db05ef2).
- The case bodies, from the line table and the padded branch targets:

  | INFO01 | body | what it does |
  |---|---|---|
  | 0x00 DO_EXIT | main.c:957, 0x7db05fe0 | `return 0` (the loop condition at main.c:950 also tests `cmd != 0`) |
  | 0x01 DO_LOADER | main.c:610-645, 0x7db05ff5 | `vpu_loader(&vpu_ctl, &vpu_dl)`. If it returns non-zero, logs `error:vpu_loader`. Then `xthal_icache_all_invalidate()` |
  | 0x22 DO_D2D | main.c:667-731, 0x7db060ea | MPU legal regions (only after SET_MPU), then `tm_mgr(&vpu_ctl, &vpu_dl)` |
  | 0x40 SET_DBG | main.c:735-856, 0x7db061fb | log buffer = INFO21/22; logs build date and versions; INFO20 = build date; host check on INFO29 (see below); loads the IRAM blob at INFO19 |
  | 0x41 SET_MPU | main.c:859-873, 0x7db06832 | `MPU_reset(INFO05)` |
  | 0x42 SET_FTRACE_LOG | main.c:876-894 (fall-through) | `vpuMetTagInit(INFO05, INFO06)` |
  | anything else | main.c:935-950, 0x7db05b77 | logs `ERROR:CMD_ID(0x%X)` and loops |

  There is **no case for 0x02 (DECRYPT), 0x10/0x11 (PASS1/2_DL), 0x81 (GET_SWVER) or 0x82
  (GET_ALG_ATTR)**. Source lines 646-666 and 895-934 contain no code (compiled-out cases), and
  `vpu_decrypt` is never called. That is why 0x81 and 0x82 log `ERROR:CMD_ID`: the enum has them,
  but this firmware build does not handle them. Fire OS's kernel still sends 0x82
  (`vpu_hw_get_algo_info`, VA 0xc0859b00: `vpu_write_field(0x5a=INFO01, 0x82)`, INFO06/07/08/10/12,
  then it reads INFO05/09/11), and the firmware ignores it. The same kernel says
  `DO NOT SUPPORT VPU_IOCTL_GET_ALGO_INFO`. So **take an algorithm's ports and properties from its
  ELF** (below).
- **States.** INFO00 is READY (1) while waiting and BUSY (4) while running a command. The firmware
  never writes IDLE or ERR, so **INFO00 = READY after DO_LOADER is the normal result**.
  MediaTek's `vpu_check_postcond` accepts READY as well as IDLE. A failed load is **not** reported
  in INFO00.
- **SET_DBG host check.** If INFO29 <= 0x180702ff, the firmware logs `Host version is too old` and
  spins in `j .` (0x7db06a3c) for good. Use 0x18070300 or later.
- **Debug traces.** INFO25 (0x801b4) gets stage codes: 0x0a00 at the end of DO_LOADER, 0x0b00 at the
  start of D2D, 0x0d00 and 0x1400 at its end, 0x1e00 and 0x1f00 for SET_DBG, 0x2800 and 0x2900 for
  SET_MPU, 0x3200 for SET_FTRACE_LOG. INFO26 (0x801b8) gets bits ORed in as a command progresses.
  The codes are read from the `movi a11` slots and are only partly certain. Dump INFO23-31 after
  each command.
- **Completion.** Fire OS (load_algo at VA 0xc0856c8c, enque at VA 0xc08575fc) waits for the IRQ,
  then checks field 0x28 = **DONE_ST (0x8010c) bit 7 PWAITMODE**: the core is back in WAITI. It
  retries up to 5 times, then sets RUN_STALL and reads INFO00. Without the IRQ:
  1. **Write INFO00 = 0 before triggering.**
  2. Wait for INFO00 == 1 and DONE_ST bit 7 = 1.

  XTENSA_INT (0x80114) still has to be acknowledged (write 1) before each command, because
  `interruptToMCU` (0x7db0215c) reads it and writes it.

### 2. `vpu_loader(pCtl, pDL)` (0x7db052f0, vpu_loader.c)

- Line 105-109: `funcFLO_start = funcFLO = 0` (VPU 0x7db12fd0 and 0x7db12fd4, in DDR).
- Line 140: `src = VPUReadReg(INFO12)`. INFO12 is the only argument it reads. INFO13 (length) is
  never used: the loader parses the ELF itself. INFO15/16 only end up in the snapshot.
- Line 147: `m_loader_pil_validate_dynamic(src)` returns 0 only for a valid ELF magic **and**
  `e_type == ET_DYN`.
  - **ET_DYN path** (line 149, `m_loader_pil`): Cadence's split PI-library loader. It mallocs code
    and data from the heap (0x7db13e88..0x7de00000) and is the only path that can make vpu_loader
    fail (`error:vpu_loader`).
  - **Otherwise, the static path** (line 156-160): `m_loader(src)` sets up the iDMA object
    (`gpObjDma` methods +0xc, +0x18, +0x24), then calls `m_load_static_lib(src, …)`.
- `m_load_static_lib` (0x7db0523c, vpu_mloader.c:647-692):
  1. `m_verify_magic`. On failure it returns error code 1.
  2. If `e_type == ET_DYN`, it returns error code 3.
  3. For each program header with `p_type == PT_LOAD` (32-byte phdrs from `e_phoff`), it calls
     `m_load_seg(phdr, src + p_offset, p_paddr)`. That function lives in **IRAM at 0x7ff40180** and
     queues copy and zero-fill nodes ("bss exceed max" if too many).
  4. It runs the copies with `m_load_seg_dma()` (IRAM 0x7ff4032c, iDMA), then returns `e_entry`.

  The entry is stored in `funcFLO_start`. The static path then writes INFO25. It always returns 0,
  so **a bad static image fails silently**.
- **Format.** Use the algorithm ELF exactly as stored in cam_vpu2/3. Every stock algorithm is
  `ET_EXEC` with absolute `p_paddr`. No xt-pkg-loadlib packaging and no descriptor struct. Take the
  copy built for the core: core_mask 0x61 is linked at 0x7de00000, 0x62 at 0x7e700000.
  - `vpu_flo_d2d_k5` is algo 4 of cam_vpu2: offset 0x200 + 0xdb0f0, length 0xe1d0. It is
    byte-identical to `elf/cam_vpu2_04_db2f0.elf`.
  - `vpu_flo_d2d_ksample` is algo 0 of cam_vpu3: offset 0x200 + 0xd38, length 0xaf12.
- **Where the segments go.** The core puts them at their link addresses:
  - k5: DDR 0x7de00000 (0xc60 bytes) plus bss 0x7de00c60 (0x28); DMEM0 0x7ff00000 (0x4000);
    DMEM1 0x7ff20000 (0x4100).
  - ksample: the same, plus IMEM 0x7ff40800 (0x33e).

  Nothing has to be allocated in advance. The DDR kernel area 0x7de00000..0x7e000000 just has to be
  backed, as it is in the no-map region. Note that an algorithm's DMEM0 image overwrites the
  firmware's own `.dram0.data` (0x7ff00000..0x7ff006c0: `dmaObj`, `mtk_stack_dump`). This is by
  design. The firmware's IRAM (0x7ff40000..0x7ff40674) is kept, because algorithms' IMEM starts at
  0x7ff40800.
- **Constraints.**
  - SET_DBG must have loaded the IRAM blob before DO_LOADER, because the static loader runs from
    IRAM.
  - Do not send SET_DBG again after DO_LOADER: it rewrites DMEM0 0x7ff00000..0x6c0.
  - Put the ELF at a 4-byte-aligned PA (the header is read with l16ui/l32i), and preferably a
    4 KiB-aligned one. The stock ELFs' `p_offset`s are 64-byte aligned, so the iDMA sources stay
    aligned.
  - Keep it outside 0x7de00000..0x7e000000 (the kernel area) and preferably outside the heap.
  - The main stack is the top of DMEM1, growing down from `__stack` = 0x7ff40000 (sentry
    0x7ff20000). Leave DMEM1 alone above the algorithm's segment.
- **Checking a load** (do it while stalled):
  - `vr 0x20000…` (DMEM1) must equal the ELF bytes at file offset 0x4fc0, 0x4100 bytes (k5).
  - debugfs `mem` at offset 0x400000 (PA 0x7de00000) should equal the ELF at 0x340, 0xc60 bytes.
  - INFO25 should be 0x0a00.

  Treat the DDR reads as indicative only. The firmware's MPU maps 0x7da00000-0x7fffffff with memory
  type 0xf7, which is probably write-back cacheable, so the core's DDR stores, such as
  `funcFLO_start`, may sit in its dcache. iDMA writes do reach DRAM.

### 3. D2D: what the firmware and the algorithm read

- **`tm_mgr`** (0x7db035c0) fills a `VPU_LIB_INFO_STRUCT kInfo` with `printf_func = vpu_sprintf`
  (which writes to the SET_DBG log buffer) and the MET hooks. Then
  `funcFLO = funcFLO_start(&kInfo)`, so the algorithm's `_start` runs on **every** D2D.
  - k5's `_start` (0x7de003bc) sets `kInfo.swVersion = 0x16122608`. It copies its port and property
    descriptors only if `port_struct_ptr != NULL`, then logs `VERSION:SW(%x),PORT(%x)` and
    `exit _start d2d_k(%x)`.
  - It returns `kernelProcess_ktm` (0x7de00510).
- **`tm_process`** (0x7db03670) clears a second `kInfo` and sets:
  - `buffer_num = INFO12`
  - `buffer_struct_ptr = INFO13`
  - `set_buf_ptr = INFO14`
  - `set_buf_size = INFO15`

  It then calls `funcFLO(&kInfo)`. The host side matches: Fire OS's `vpu_hw_enque_request` writes
  INFO01 = 0x22, INFO12 = buffer count, INFO13 = PA of the `vpu_buffer` array, INFO14/15 =
  settings PA and length (field indices 0x5a, 0x65-0x68).
- **Structures** (DWARF, little-endian):
  - `VPU_LIB_INFO_STRUCT` (algorithm's view, 60 bytes): swVersion +0, printf_func +4,
    version_vpu_port_struct +8, port_num +0xc, port_struct_ptr +0x10, inf_prop_ptr +0x14,
    inf_prop_length +0x18, inf_num_prop_des +0x1c, inf_desc_ptr +0x20, set_num_prop_des +0x24,
    set_desc_ptr +0x28, buffer_num +0x2c, buffer_struct_ptr +0x30, set_buf_ptr +0x34,
    set_buf_size +0x38. The firmware's copy is 96 bytes and adds met_log_enable/level and 7 MET
    function pointers.
  - `vpu_buffer` (64 bytes): port_id u8 +0, format u8 +1, plane_count u8 +2, width u32 +4,
    height u32 +8, planes[3] at +0x10. Each plane is 16 bytes: stride u32 +0, length u32 +4,
    ptr u64 +8. The algorithm uses only the low 32 bits of ptr.
  - `vpu_port` (35 bytes): id u8, usage u8, dir u8, name[32].
  - `vpu_prop_desc` (44 bytes): id u8, type u8, access u8, pad, offset u32, count u32, name[32].
    The ELFs store offset = 0, and the host computes offsets as MediaTek's
    `vpu_calc_prop_offset` does: a running sum of size(type) × count, with type 1 = int32.
- **The algorithms' interfaces (from their ELFs):**

  | | k5 (`slt.c`, `d2d_flo.c`) | ksample |
  |---|---|---|
  | ports | 0 `d2dk5ip0` (input), 1 `d2dk5op1` (output) | 0 `i_U16_ImgG` (usage 0, dir in), 1 `o_S16_ImgOut` (usage 0, dir out) |
  | info props | version, wrk_buf_size, support_format[10], reserved[64] (int32) | same |
  | settings | param1..param5, int32 each, offsets 0/4/8/12/16 (20 bytes); param5 is read-write | `S32_Wight`, `S32_Height`, int32 at 0/4 (8 bytes) |

  How `kernelProcess_ktm` works:
  1. It walks `buffer_num` × 64-byte buffers and picks them by `port_id`: 0 is the source, 1 the
     destination.
  2. It logs `src image=`, `dst image=` and `param1=… param5=` from the settings.
  3. It moves data with iDMA 2D: source to `dmem_src` (0x7ff03000, 4 KiB), a 9-vector `xb_vec2Nx8`
     kernel, then `dmem_dst` (0x7ff23000, 4 KiB) back to the destination.

  The tile geometry is set inside FLIX bundles and was not decoded. A 4096-byte image (64×64 Y8)
  can't overflow the 4 KiB DMEM buffers. After the run, the descriptors it used are in DMEM1 at
  0x7ff24000 (`vr 0x24000`, `DMA2DDesc[8]`).
- **`format`.** k5 is not seen to check it. Use MediaTek's enum: 0 DATA, 1 IMG_Y8, 2 YV12, 3 NV21,
  4 YUY2. This enum is from MediaTek's public uapi, not from this firmware.

### 4. Recipe (shell, debugfs `regs`/`mem`)

The debugfs `mem` offset is PA − 0x7da00000; the region is 0x1600000 bytes. Core 1's area is free,
so everything new goes at 0x7e800000 and up:

| PA | mem offset | content |
|---|---|---|
| 0x7dd00000 | 0x300000 | IRAM blob (mkregion.py, as now) |
| 0x7e800000 | 0xe00000 | k5 ELF, 0xe1d0 bytes, verbatim (`dd if=p16-cam_vpu2.img bs=1 skip=$((0x200+0xdb0f0)) count=$((0xe1d0))`) |
| 0x7e900000 | 0xf00000 | log buffer, 64 KiB (INFO21/22) |
| 0x7ea00000 | 0x1000000 | `vpu_buffer[2]`, 128 bytes |
| 0x7ea01000 | 0x1001000 | settings: 5 × int32 (for example 1, 2, 3, 4, 5) |
| 0x7eb00000 | 0x1100000 | input, 64×64 bytes (a ramp `i & 0xff`) |
| 0x7eb10000 | 0x1110000 | output, 4096 bytes, pre-filled with 0xaa |

`vpu_buffer[0]` at 0x7ea00000:

| offset | bytes | value |
|---|---|---|
| +0x00 | `00 01 01 00` | port 0, format Y8, 1 plane |
| +0x04 | u32 | 64 (width) |
| +0x08 | u32 | 64 (height) |
| +0x0c | u32 | 0 |
| +0x10 | u32 | 64 (stride) |
| +0x14 | u32 | 4096 (length) |
| +0x18 | u32 | 0x7eb00000 (ptr, low) |
| +0x1c | u32 | 0 (ptr, high) |
| +0x20..+0x3f | | zero |

`vpu_buffer[1]` at 0x7ea00040 is the same with port_id 1 (`01 01 01 00`) and ptr 0x7eb10000.

Command steps. Each one is a `vcmd.sh` call, changed to write INFO00 = 0 before triggering and to
wait for INFO00 == 1 with DONE_ST (0x8010c) bit 7 set.

1. `vboot.sh`. Expect INFO00 = 1.
2. SET_DBG: `vcmd.sh 0x40 19=0x7dd00000 21=0x7e900000 22=0x10000 23=0 29=0x18070300`. Expect
   INFO20 = 0x23081617 and the version lines in the log.
3. DO_LOADER: `vcmd.sh 0x01 12=0x7e800000 13=0xe1d0 15=312000 16=312000`. Expect INFO00 = 1, log
   `CMD:(0x1)`, nothing else, and INFO25 = 0x0a00. Check DMEM1 against the ELF (section 2).
4. DO_D2D: `vcmd.sh 0x22 12=2 13=0x7ea00000 14=0x7ea01000 15=20`. Expect these log lines:
   - `CMD:(0x22)`
   - `VERSION:SW(16122608),PORT(…)`
   - `exit _start d2d_k(7de00510)`
   - `[kernelProcess_ktm]:js_test.version[…]`
   - `+:SLT`
   - `buffer select`
   - `src image=0x7eb00000` and `dst image=0x7eb10000`
   - `param1=1, … param5=5`
   - `-:SLT`

   Expect INFO25 = 0x1400. The output at 0x7eb10000 should no longer be 0xaa.
5. For ksample, load cam_vpu3's algo 0 the same way (0xaf12 bytes). Use ports 0 and 1, and an
   8-byte settings blob {width, height}, with 15 = 8.

Open points:
- The compare constants of the dispatcher (the bundles at 0x7db05ea2) are not decoded. The case
  list rests on the case bodies, the line-table gaps and the `ERROR:CMD_ID` seen on the unit.
- k5's tile size and its exact operation are not known.
- The VPU's DDR may be cached (MPU type 0xf7). Fire OS reuses one buffer array without visible
  cache maintenance, which suggests it is safe. If a second D2D seems to see stale descriptors,
  use new addresses.

### 5. First D2D run on the unit: the k5 hang (analysis, 2026-10-18)

**What the unit showed.** SET_DBG, DO_LOADER and the DMEM1 check all passed. Then the D2D:
- The log reaches `param1=1 … param5=5`, then stops. There is no `-:SLT`.
- After 10 s: INFO00 = 4 (BUSY), DONE_ST = 0, INFO25 = 0x0c01, INFO26 = 0x13bd, INFO27 = 0x7dd007c0,
  INFO28 = 0x5d004136.
- Debug registers: DEBUG_INFO00 = 0xc000, then 0xc400; DEBUG_INFO01 = 0x00200040;
  DEBUG_INFO02 = 0x2000; DEBUG_INFO05 = 0x7de007b3.
- The output buffer is untouched.

**The DEBUG_INFO registers.** Fire OS's register and field tables in its kernel (data at Image
offsets 0x157d7f8 and 0x157e03c) name them. **0x801d0 is not the PC.**

| register | offset | content |
|---|---|---|
| DEBUG_INFO00 | 0x801d0 | P_DEBUG_DATA (status in bits 23:16) |
| DEBUG_INFO01 | 0x801d4 | P_DEBUG_INB_PIF in bits 7:0 |
| DEBUG_INFO02 | 0x801d8 | P_DEBUG_INST |
| DEBUG_INFO03 / 04 | 0x801dc / 0x801e0 | P_DEBUG_LS0_STAT / LS1_STAT |
| **DEBUG_INFO05** | **0x801e4** | **P_DEBUG_PC** (Fire OS also lays PSLVERR, P_FAULT_INFO_VALID, P_FATAL_ERROR and DOUBLE_EXCEPTION_ERROR fields over this register) |
| DEBUG_INFO06 | 0x801e8 | bit 0 TRIG_OUT_IDMA |
| DEBUG_INFO07 | 0x801ec | P_FAULT_INFO |
| "DBG10" | 0x801f8 | ALTRESETVEC (0x7da00000 is correct) |

So 0xc000 and 0xc400 are PDebug *data-bus* values, not code addresses. The earlier "PC in the idle
loop 0x7db020f3" was also read at 0x801d0, so it was a data value. Change `vcmd.sh` to read the PC
from 0x801e4.

**Where the core stopped.**
- **PC = 0x7de007b3.** This is k5 `kernelProcess_ktm` (slt.c:255), the instruction right after the
  **second** `DMATriggerStart` (`call8` at 0x7de007b0, which starts the DMEM1 → DDR output transfer)
  has returned. The instruction is `l32i.n a8, a4, 0`: a DDR load of `printf_ptr` (0x7de00c7c),
  needed for the `-:SLT` printf.
- So the core is **stalled on a bus access**. It is not looping, and it has not taken an
  exception: the firmware's exception handlers (`exp_handlder_m`, 0x7db05584) would set INFO00 = 8
  (`parameterSet(ERR)`) and dump to DMEM1.
- **INFO25 = 0x0c01** is written by `tm_process` (tm_process.c:113) just before it calls the
  algorithm's process function. 0x0c02 would mean it returned (line 117); 0x0d00 and 0x1400 follow
  in main.c. So the core never came back out of k5.
- **INFO26** is the firmware's ORed progress mask.
- **INFO27 and INFO28** are left over from SET_DBG: the IRAM segment's source (blob + 0x7c0) and
  its first word (`entry a1,0x20`). They are not related to the hang.

**What k5 does with the DMA.** k5's `DMATriggerStart` (0x7de00328) programs Cadence iDMA over the
ERI bus at 0x110000:
1. CONTROL (+0x14) = arg, then CONTROL = 0 (this looks like a reset pulse)
2. SETTINGS (+0x00) |= …
3. DESC_START (+0x08) = first descriptor
4. DESC_INCR (+0x10) = count
5. CONTROL = 1

It returns at once. `DMAReadStatus` (status at ERI +0x40) is never called from
`kernelProcess_ktm`, so **k5 does not visibly wait for either transfer** (no visible call or base-ISA
loop; a wait hidden inside the undecoded bundles is not ruled out). The only kernel with a proper DMA
driver is ksample. Its `dmaif.c` `waitDone` (0x7de003dc) polls the status until it changes, and on
status 5 (ERROR) it logs `[ERROR] DMA status = 0x%08x, error code = 0x%08x` and `stop here`, then
spins.

**Likely causes**, in order. These are inferred; none is confirmed.
1. **The output transfer (iDMA write to DDR) never completes**, and the core's next DDR access waits
   behind it on the shared AXI path (Fire OS's register table has a CABGEN 2to1 slice). Writes by
   iDMA to DDR had not been exercised before this run:
   - DO_LOADER's iDMA writes went to DMEM. Its DDR segment may have been written by the CPU:
     `m_load_seg_dma` also calls `m_memcpy`.
   - The first D2D transfer only reads DDR.

   One difference from MediaTek's boot: `vboot.sh` **overwrites** AXI_DEFAULT0 with `1<<28` and
   AXI_DEFAULT1 with 0. MT6771's `vpu_hw_boot_sequence` only sets bits (22 and 27 in AXI_DEFAULT0,
   4 and 9 in AXI_DEFAULT1, 28 for pre-ultra) and keeps the reset values. Those registers hold the
   iDMA's write attributes: AWUSER_IDMA bits 9:5, AWDOMAIN_IDMA 23:19, AWFLUSH_IDMA 25:24,
   AWULTRA_IDMA 27:26.
2. **k5 resets the iDMA (CONTROL pulse) while the first transfer may still be outstanding**,
   because it never waits. An AXI transaction killed mid-flight can hang the port.
3. A bad descriptor (wrong destination, size or pitch) taken from the buffer fields. The log printed
   the right `dst image`, which makes this less likely.

The k5 descriptors are still in DMEM1 at 0x7ff24000 (`DMA2DDesc[8]`, 32 bytes each; 0x7ff24020 is
the second one, if k5 used index 1). Each holds control +0, src +4, dst +8, bytes per row +0xc,
src pitch +0x10, dst pitch +0x14, rows +0x18. **Read `vr 0x24000`..`0x2403f` after a hang.** The
first transfer's data is at `vr 0x3000` (`dmem_src`, 4 KiB) and should hold the input ramp if it
completed.

### 6. Corrected recipe

**Boot (vboot.sh).**
1. Before writing, print the reset values of 0x8013c and 0x80140.
2. Then do what MediaTek does:
   - `AXI0 = (AXI0 & ~(1<<22 | 1<<27)) | 1<<28`
   - `AXI1 = AXI1 & ~(1<<4 | 1<<9)`

   This keeps the M4U bypassed and the other attributes at their defaults.
3. If the core hangs, power the VPU off and on (debugfs `power` 0, then 1), then rerun `vboot.sh`.

**A. ksample first.** It has a real DMA driver and reports DMA errors in the log.

Load it:
- ELF: cam_vpu3 algo 0, offset 0x200 + 0xd38, 0xaf12 bytes, copied verbatim to PA 0x7e800000 (mem
  offset 0xe00000).
- `vcmd2.sh 0x01 12=0x7e800000 13=0xaf12 15=312000 16=312000`. Expect INFO25 = 0x0a00.
- Check that `vr 0x20000`+0x1000 equals ELF[0x2640:0x3640] (DMEM1) and that `vr 0x40800`+0x33e
  equals ELF[0x3640:0x397e]. IMEM holds `kernelProcess_ktm` at 0x7ff40800.

How it works:
- Ports: 0 is `i_U16_ImgG` (input), 1 is `o_S16_ImgOut` (output). It takes `planes[0].ptr`
  (+0x18) of each buffer.
- Settings: `{int32 width, int32 height}` (`S32_Wight`, `S32_Height`).
- It copies through 4 KiB DMEM tiles (`DRAM0_ADDR` 0x7ff00500 and `DRAM1_ADDR` 0x7ff20000), 64 rows
  per step, with the width rounded up to 64.
- So keep **one tile = at most 4 KiB under every reading**: 32×32 pixels, 2 bytes each. That is
  64-byte rows and 2048 bytes.

Memory layout:

| PA | mem offset | content |
|---|---|---|
| 0x7ea00000 | 0x1000000 | `vpu_buffer[0]` |
| 0x7ea00040 | 0x1000040 | `vpu_buffer[1]` |
| 0x7ea01000 | 0x1001000 | settings: `20 00 00 00 20 00 00 00` (width 32, height 32; 8 bytes) |
| 0x7eb00000 | 0x1100000 | input: 2048 bytes, u16 ramp (`i`, little-endian), then zeros to 4 KiB |
| 0x7eb10000 | 0x1110000 | output: 4 KiB of 0xaa |

`vpu_buffer[0]`, 64 bytes:
```
00 01 01 00  20 00 00 00  20 00 00 00  00 00 00 00   port 0, fmt 1, 1 plane, w 32, h 32
40 00 00 00  00 08 00 00  00 00 b0 7e  00 00 00 00   stride 64, length 2048, ptr 0x7eb00000
00 … (32 zero bytes: planes 1-2)
```
`vpu_buffer[1]` is the same with byte 0 = `01` and ptr = `00 00 b1 7e` (0x7eb10000).

Run it: `vcmd2.sh 0x22 12=2 13=0x7ea00000 14=0x7ea01000 15=8`.
- **Expected log:**
  - `vpu_lib_d2d_ksample`
  - `DRAM0_ADDR(7ff00500)`
  - `DRAM1_ADDR(7ff20000)`
  - `buffer num(2)`
  - `src image=0x7eb00000`
  - `dst image=0x7eb10000`
  - `S32_Wight=32, S32_Height=32`
  - `DMA transfer time = N cycles`

  Then INFO25 = 0x1400, INFO00 = 1, and the output equals the input over the copied area.
- **If the log ends with `[ERROR] DMA status = 0x00000005, error code = X` / `stop here`:** the iDMA
  reports a bus error; record X. The core then spins in `j .` (PC near 0x7de00483), not on a bus
  stall.
- **If it stops with no error line and P_DEBUG_PC (0x801e4) shows a DDR load or fetch:** it is the
  same bus stall as with k5.

**B. Isolating the DDR write (k5 again).**
- Move only the output into the core's own memory: `vpu_buffer[1].ptr = 0x7ff30000`. That is DMEM1,
  above k5's segment and the descriptors and well below the stack, which grows down from 0x7ff40000.
  It is `vr 0x30000` on the AP.
- Pre-fill 4 KiB there through debugfs `regs` (dd at offset 0x30000) while the core is stalled.
- If `-:SLT` now appears and the D2D returns (INFO25 = 0x1400), the hang is the iDMA write to DDR.
  Then compare the AXI_DEFAULT1 reset value with what `vboot.sh` wrote, and check larb2's port setup
  for the VPU write ports in mainline.

Unchanged from section 4:
- The stock ELFs are loaded verbatim.
- INFO12 = number of buffers, INFO13 = buffer array, INFO14/15 = settings address and length.
- Buffers are 64 bytes each; planes[0].ptr low word at +0x18.
- The whole region is no-map, so there is no AP cache problem.
- The VPU MPU covers 0x7da00000-0x7fffffff (memory type 0xf7) for the core's own accesses. iDMA
  does not go through the MPU, and no MPU region needs setting up for 0x7eb00000; SET_MPU is off by
  default.

### 7. ksample ran but wrote nothing: its tile loop never ran (analysis, 2026-10-18)

**What the unit showed.** ksample with 32×32 completes cleanly (INFO25 = 0x1400, `DMA transfer time =
2745 cycles`, no DMA error). The input ramp arrives in DMEM0 at 0x7ff00500 (`DRAM0_ADDR`), but the
destination is never written, whether it is in DDR or in DMEM1.

**Why.** Read from ksample's `kernelProcess_ktm` (IMEM 0x7ff40800, `test/main_proc.c`). The loop
bound itself sits in undecoded bundles.
- main_proc.c:57-67 picks the buffers by port_id: 0 is the source, 1 the destination (from
  planes[0].ptr, saved at sp+0x10).
- :77 reads the settings `{W, H}`.
- :80-81 calls `dmaif_getDMACtrl()->init()`.
- :94 starts the **first** `load` (DDR source → `DRAM0_ADDR`), then :103 `waitDone`.
- **Only after that, the tile loop (main_proc.c:106-151):**
  - :117-119 `load` of the next tile, only if one remains (`bge` at 0x7ff409f3; row count
    `movi a14, 0x40`, so tiles are **64 rows**)
  - :129 `waitDone`
  - :132 `mtkMemoryCopyAligned(DRAM0 → DRAM1)`
  - :137 `store` (ctrl+0x10) of the tile to the destination
- :154 `waitDone`, :157-159 `ccount` timing and the log line.

`store` is called only inside the loop. Both tile buffers are 4 KiB (`DRAM0_ADDR` 0x7ff00500,
`DRAM1_ADDR` 0x7ff20000), which is exactly 64 rows × 64 bytes. ksample therefore expects
**H to be a multiple of 64** (tiles = H / 64, rounded down) and a 64-byte row (`addi a9, a3, 0x3f`
rounds up to 64). With H = 32 there are zero tiles: the first tile is loaded and nothing is stored.
That matches every observation. ksample is a copy kernel, not a read-only benchmark. Its output
should equal its input: u16 input, s16 output, `mtkMemoryCopyAligned`.

**What this means for k5.** iDMA reads from DDR work, and so do the core's stores to DDR (the log
buffer). **No transfer from DMEM to DDR has run yet.** ksample with H = 64 is the clean test of that
direction, and it goes through a DMA driver that waits and reports errors:
- If ksample's store lands, k5's hang comes from k5's own DMA handling: no visible wait, and a
  CONTROL reset pulse on every trigger.
- If it does not, iDMA writes to DDR are blocked on this platform (AXI/larb2 write port), and k5
  simply hung on them.

### 8. Next recipe: a full round trip with ksample

Everything is as in section 6, except:
- Settings at 0x7ea01000: `20 00 00 00 40 00 00 00` (W 32, H 64), with INFO15 = 8.
- `vpu_buffer[0]`, `vpu_buffer[1]`: height 64, length 4096 (`00 10 00 00` at +0x14), stride 64.
  The ptrs stay 0x7eb00000 and 0x7eb10000:
```
buf0 @0x7ea00000: 00 01 01 00  20 00 00 00  40 00 00 00  00 00 00 00
                  40 00 00 00  00 10 00 00  00 00 b0 7e  00 00 00 00  + 32 zero bytes
buf1 @0x7ea00040: same, byte 0 = 01, ptr = 00 00 b1 7e
```
- Input: 4096 bytes, a u16 ramp 0..2047. Output: 8 KiB of 0xaa. Afterwards, check that the first
  4096 bytes equal the input and the next 4 KiB are still 0xaa (to catch overruns).

Then:
1. **H = 128** (two tiles, 8 KiB in/out, length `00 20 00 00`): tests the second-tile `load` path.
2. **The destination in DMEM1** (ptr 0x7ff30000, AP `vr 0x30000`): if the DDR destination stays
   untouched but this one fills, the iDMA write to DDR is the problem.
3. **k5 again** with its destination at 0x7ff30000 (section 6 B), then with a DDR destination,
   only after ksample's DDR store has worked.

A kernel that does real work is the next step after the round trip. The candidate is
`vpu_lib_d2d_histogram` (cam_vpu2 algo 9, offset 0x200 + 0x1e7c88, 0x1e2de bytes). Its ports and
settings must be read from its ELF the same way; not done yet.

## Results on the unit, 2026-10-18 (kernel with VPU_PROBE=1)

- SET_DBG, DO_LOADER and D2D all work from Linux (`tools/vpu/vboot.sh`, `vcmd2.sh`, which clears INFO00
  and waits for READY with DONE_ST's PWAITMODE, and reads the PC at 0x801e4). AXI_DEFAULT0/1 reset to 0
  here; the boot keeps them and only sets pre-ultra.
- k5 loads (DMEM1 byte-identical to the ELF) and its D2D reaches its process function, then hangs
  in its own DMA handling (section 5); not pursued.
- ksample, the full round trip: DDR -> iDMA -> DMEM -> core -> DMEM -> iDMA -> DDR, byte-exact. It
  stores only for a width of 64 or more (its loop is over 64-unit tiles in both directions; at 32 it
  loads once and stores nothing, which looked like dead iDMA writes): 64x64 stride 64 copies all
  4096 bytes exactly and touches nothing past them; at 256x64 stride 512 it copies the first 32 rows,
  so it counts the width in bytes.
- iDMA writes to DDR, core stores to DDR and iDMA reads all work in the no-map region with the M4U
  bypassed.

### 9. Survey of the stock algorithms, and what to use from echod (offline, 2026-10-18)

Read from each core-0 ELF: its source list, `port_prop_descs` (35-byte `vpu_port`), the setting
names, its DMEM buffers and its log strings. Script: `scratchpad/vpu-loader/survey.py`. Port usage
is 0 for an image and 1 for data; dir is 0 for in and 1 for out.

| algo (cam_vpu2 index) | what it is | ports | settings (int32 unless noted) | notes |
|---|---|---|---|---|
| 0 `vpu_flo_d2d_k14` | guided filter, pre-downsample (`pregfastds`, VP5) | 0 `i_U16_W` in, 1 `o_U16_WDown` out, 2 `i_TileData` (data, in) | `S32_roiWidth`, `S32_roiHeight`, `S32_Stride` | TM tiling framework. It **needs host-built tile data** (`ERROR:tile data size(%d!=%d)`) |
| 1 `vpu_flo_d2d_k15` | guided filter, pre-pass 1 (`boxSum`, `BoxSumSquare`) | 0 `i_U16_WDown`, 1 `i_U16_WVarDn` in; 2 `o_U16_A`, 3 `o_U16_B` out; 4 `i_TileData` | `F32_Scale` (float), `S32_RX`, `S32_RY`, `S32_Width`, `S32_Height`, `S32_Stride`, `S32_BlkSize` | Bokeh pipeline stage. Tile data required |
| 2 `vpu_flo_d2d_k19` | guided filter, part 2 (`gfbkpart2`) | 0-3 in (`d2dk19ip0..3`), 4-5 out, 6 data | `S32_RX`, `S32_RY`, `S32_Width`, `S32_Height`, `S32_Stride` | Same |
| 3 `vpu_flo_d2d_k3` | warp perspective (TM framework with padding) | 0 `d2dk3ip0`, 1 `d2dk3op1`, 2 `d2dk3ip2` | param1..param5 (k5's SLT-style harness) | Its DMA layer logs alignment errors (`invalid psrc/pdst alignment`, `invalid numBytesPerRow alignment`) |
| 4 `vpu_flo_d2d_k5` | SLT self-test | 0 in, 1 out | param1..5 | Hung on the unit (section 5) |
| 5 `vpu_flo_d2d_k7` | Harris 5×5 "max power pattern" diag (`d2dk7ip0`, `op1`, `ip2`) | | | A power/stress test, not useful |
| 6 `vpu_flo_d2d_lap` | **Laplacian / pyramid level** (`xv1D5TapDownSampleX`, `xv1D3TapUpSampleY`, `xvLaplacian`, `Padding`) | 0 `i_U8_LapIn` (image, in), 1 `o_U16_Lap_down` (data, out), 2 `o_U16_Lap_up` (data, out), 3 `i_TileData` (data, in) | `S32_Width`, `S32_Height`, `S32_Stride`, `S32_nLevel` | TM framework, but logs **`Bypass Tile data`**: it tiles by itself when no tile-data buffer is given. ROI log: `S32_roiWidth=%d , S32_roiHeight=%d , S32_Stride=%d` |
| 7 `vpu_lib_d2d_canny` | Canny edges, a benchmark harness (`test/canny.c`) | 0 src, 1 wkb (work), 2 dst (`wkb image=`) | `S32_Scale`, `S32_Offset` | Fixed DMEM tiles `memSrcTile` and `memDstTile`, 0x1b4d0 each (1928 × 58). Built for **1920-wide** frames. Ends with "Performance Pass/Fail" |
| 8 `vpu_lib_d2d_harris` | Harris corners, a harness | 0 src, 1 dst | `S32_Scale`, `S32_Offset` | `DRAM0/1Buffer` 0x1e000 = 1920 × 64 tiles |
| 9 **`vpu_lib_d2d_histogram`** | 256-bin histogram plus equalization LUT (`xvHistogram(src, u32 hist[256], u8 lut, scratch, W, H)`, `xvHistogramEqualization_LUT`) | 0 src, 1 dst (port_id is checked in `main_proc.c:50`) | 3 settings; logs `S32_Scale`, `S32_Offset` | `InputMEM` 57600 bytes (= 1920 × 30 rows per tile); `U32Dst` 256 × u32; `U8Dst` 1 KiB. Logs `start_y,end_y`, `Loop Count`, cycles. "Performance" check against 1920 |
| 10 `vpu_lib_d2d_kidmaperf` | iDMA benchmark (ksample's sibling, with sizes in the log) | 0 `i_U16_ImgG`, 1 `o_S16_ImgOut` | `S32_Wight`, `S32_Height` | DMA test only |
| 11 `vpu_lib_d2d_swnr` | software noise reduction (IIR down/pass, blending, pixel diff, transpose) | none published: 0 the work buffer, 1 and 2 the U and V planes (§18) | `vpu_st_sett_swnr`, in the ELF's DWARF (§18) | Production. Its host side is `libcamalgo.vpunr.so`. Recovered and in use: §18 |
| 12 `vpu_lib_dlfd_vpu0` | CNN face detector with landmarks | `DLFD_input`, `DLFD_pyramid`, `DLFD_output` | | Production. Its host side is `libdnn_fd.so` (section 1) |
| 13 `apu_lib_apunn` | NeuroPilot NN runtime | | | Needs NNAPI-compiled command streams |
| cam_vpu3 `vpu_flo_d2d_ksample` | tile copy | 0 in, 1 out | `{W, H}` | Proven: it copies when W ≥ 64, with width in bytes |

**Which algorithms matter.**
- **Production kernels** (the real value for echod): the **face detector** and **swnr**. Both need
  their Fire OS host libraries reverse-engineered first, so they are not a quick win. (swnr since done:
  §18.)
- **Bokeh stages** (k14, k15, k19): need host-made tile data.
- **Test harnesses** (k3, k5, k7, canny, harris, histogram, kidmaperf, ksample): run out of the box
  on fixed DMEM tiles sized for 1920-wide frames.

The two most useful that run without any host library:
1. **`vpu_lib_d2d_histogram`.** A luma histogram gives auto-exposure, low-light and scene-change
   statistics for echod.
2. **`vpu_flo_d2d_lap`.** One Laplacian-pyramid level per call. Its Laplacian energy is a focus or
   blur metric, and the downsampled level is a cheap ½-scale image (for motion or presence
   detection). Its tiling handles arbitrary sizes.

**Limits.**
- The vector code can't be decoded, so the exact output layout of both is **inferred** from symbols
  and DMEM sizes. Each recipe below therefore pre-fills the outputs with 0xaa and compares against
  a few candidate CPU references.
- Width is in bytes. Keep rows and strides multiples of 64 bytes: the DMA layer checks the
  alignment of addresses and of bytes per row.
- Camera widths: 1456 → stride 1472; 2112 is already a multiple of 64.

#### Recipe H: histogram (cam_vpu2 algo 9, offset 0x200 + 0x1e7c88, 0x1e2de bytes)

Load it as k5 and ksample were loaded: copy to 0x7e800000, then
`DO_LOADER 12=0x7e800000 13=0x1e2de 15=312000 16=312000`. Check that `vr 0x00500` and up hold
`InputMEM` after the run.

| PA | content |
|---|---|
| 0x7ea00000 | `vpu_buffer[0]`, the source |
| 0x7ea00040 | `vpu_buffer[1]`, the destination |
| 0x7ea01000 | settings: `01 00 00 00  00 00 00 00  00 00 00 00` (Scale 1, Offset 0, 0; 12 bytes, INFO15 = 12) |
| 0x7ec00000 | source: 1920×1080 Y8, stride 1920 (2,073,600 bytes, up to 0x7edfa400). First test: every byte = `(x + y) & 0xff` |
| 0x7eb10000 | destination: 8 KiB of 0xaa |

```
buf0: 00 01 01 00  80 07 00 00  38 04 00 00  00 00 00 00   port 0, Y8, 1 plane, 1920 x 1080
      80 07 00 00  00 a4 1f 00  00 00 c0 7e  00 00 00 00   stride 1920, len 2073600, ptr 0x7ec00000
buf1: 01 00 01 00  00 01 00 00  01 00 00 00  00 00 00 00   port 1, DATA, 256 x 1
      00 04 00 00  00 20 00 00  00 00 b1 7e  00 00 00 00   stride 1024, len 8192, ptr 0x7eb10000
```

Run `D2D 12=2 13=0x7ea00000 14=0x7ea01000 15=12`.
- **Expected log:** `vpu_lib_d2d_histogram`, `buffer num(2)`, `src image=0x7ec00000`,
  `dst image=0x7eb10000`, `S32_Scale=1, S32_Offset=0`, `start_y,end_y:…`, `Loop Count:N`, the
  processing / DMAIn / DMAOut cycle counts, `CPP = …`, then Pass or Fail.
  N should be about 1080/30 = 36: the rows per tile are 57600 / W.
- **Output check:**
  - Look for a u32[256] that equals `hist[v] = count(px == v)` over the whole image (or over the
    last tile only, if it does not accumulate across tiles).
  - Look for a 256-byte equalization LUT: `lut[v] = round(255 × cdf[v] / (W × H))`, at
    dst + 0x400 or at dst. The two stores at main_proc.c:132 and :141 write U32Dst (1 KiB) and
    U8Dst.
  - Everything past what they write must stay 0xaa.
- **Then camera geometry:** 1456×1088 with stride 1472 (57600 / 1472 = 39 rows per tile).
  Settings and destination stay the same.

#### Recipe L: Laplacian level (cam_vpu2 algo 6, offset 0x200 + 0x10ed08, 0x49308 bytes)

This one has an IMEM segment (0x7ff40800, 0x408d bytes). Check it after DO_LOADER the same way as
ksample's.

| PA | content |
|---|---|
| 0x7ea00000 | three `vpu_buffer`s |
| 0x7ea01000 | settings `{W, H, Stride, nLevel}`: `80 02 00 00  e0 01 00 00  80 02 00 00  01 00 00 00` (640, 480, 640, 1; 16 bytes, INFO15 = 16) |
| 0x7ec00000 | input: 640×480 Y8, stride 640 (307,200 bytes). Use a smooth gradient plus a few sharp squares |
| 0x7eb00000 | `o_U16_Lap_down`: 1 MiB pre-filled with 0xaa |
| 0x7ed00000 | `o_U16_Lap_up`: 2 MiB pre-filled with 0xaa |

```
buf0: 00 01 01 00  80 02 00 00  e0 01 00 00  00 00 00 00   port 0 i_U8_LapIn, 640 x 480
      80 02 00 00  00 b0 04 00  00 00 c0 7e  00 00 00 00   stride 640, len 307200
buf1: 01 00 01 00  40 01 00 00  f0 00 00 00  00 00 00 00   port 1 o_U16_Lap_down, 320 x 240
      80 02 00 00  00 58 02 00  00 00 b0 7e  00 00 00 00   stride 640 (u16), len 153600
buf2: 02 00 01 00  80 02 00 00  e0 01 00 00  00 00 00 00   port 2 o_U16_Lap_up, 640 x 480
      00 05 00 00  00 60 09 00  00 00 d0 7e  00 00 00 00   stride 1280 (u16), len 614400
```

Leave port 3 out on purpose: the log should then say `Bypass Tile data`. Run
`D2D 12=3 13=0x7ea00000 14=0x7ea01000 15=16`.
- **Expected log:** `VPU_SW_VERSION[…]`, `TM_TILE_PROC_PARAMS`, `RT_TILE_PROC_PARAMS`,
  `src image` / `dst image` lines, `Bypass Tile data`, and the ROI line.
- **Check:** first measure how much of each output changed from 0xaa. That gives the real output
  sizes and strides. Then compare with these candidate references:
  - down = a 5-tap binomial `[1 4 6 4 1]/16` blur, decimated by 2 in x and y, possibly kept scaled
    (u16 = sum before the shift).
  - up = a 3-tap upsample of down back to W×H; or the Laplacian `in − up`, offset or signed in u16.
- If no output changes, check the log for a `dmaIF[…] ERROR …` line. Its alignment messages name
  the field at fault.

### 10. What a proper kernel driver needs

A real driver (in place of the debugfs probe) needs the following.
- **Power and clocks:** VPU_TOP plus CORE0 (and ISP for larb2), with the 28 Fire OS clocks.
- **Firmware:**
  - Parse cam_vpu1/2/3: the 0x200 MTK header, the `vpu_image_header` segment table and algorithm
    table. Read them from the partitions or as `request_firmware` files; never write them.
  - Place the core-0 segments and the IRAM blob in a fixed reserved region: no-map at 0x7da00000,
    or an IOMMU-fixed mapping at the same IOVAs through larb2.
- **Boot:**
  - Run MT6771's `vpu_hw_boot_sequence`, keeping the AXI reset values.
  - Then send SET_DBG with a log buffer and host version ≥ 0x18070300, and expose the log through
    debugfs.
- **Commands:**
  - A single in-flight command per core, under a mutex. Write the INFOs, ack XTENSA_INT (write 1),
    clear RUN_STALL, raise CTL_XTENSA_INT.
  - Completion comes from the VPU IRQ (GIC SPI 292, level). In the handler, ack XTENSA_INT, then
    confirm INFO00 == READY and DONE_ST bit 7 (PWAITMODE), as Fire OS does.
  - On timeout, read P_DEBUG_PC (0x801e4) and the log, then do a full core reset and reboot.
    INFO00 never reports errors, so success means the expected log line or output.
- **Algorithms:** cache the loaded one (DO_LOADER only on a change). Keep a per-algorithm table of
  ports and setting layouts taken from the ELFs (GET_ALG_ATTR does not exist in this firmware).
- **Buffers (dma-buf import):**
  - The VPU uses 32-bit addresses, so buffers need a physically contiguous or IOMMU-mapped 32-bit
    address. If larb2's ports bypass the IOMMU, that means CMA below 4 GiB.
  - **The `vpu_buffer` array and the settings blob must sit inside the VPU's MPU window,
    0x7da00000-0x7fffffff.** The core (not the iDMA) reads them, and the firmware's MPU only maps
    that range. Image planes are moved by the iDMA and may be elsewhere.
  - Cache maintenance on the AP side: `dma_sync` before and after each command.
- **uAPI:** a small ioctl, roughly `{algo name, buffers[≤ N]{port, fmt, w, h, planes[3]{stride,
  len, dmabuf fd, offset}}, settings bytes}`, returning on completion or with a fence.

### 11. The histogram exception, decoded (from the unit's DMEM dump and log, 2026-10-18)

**Inputs.** `scratchpad/vpu/dmem-hist-exception.bin` (the regs window 0..0x40000, read after the
error) and `vlog-hist.bin`.

**What the firmware did.**
- `exp_handlder_m` (main.c:229-326) ran. It wrote a dump at the start of DMEM1 (0x7ff20000):
  sp = 0x7ff3fc20, MEMCTL = 0x00ff0000, …, EPC1 = 0x7de01027 at +0x10.
- It then called `parameterSet(8)`, which is why **INFO00 = 8 (ERR)**, raised the IRQ, and sat in
  `interruptWait`, which is why the PC reads 0x7db020f3.
- After that comes `j .` (0x7db05731), so **the core does not come back: power-cycle and reboot
  it.** INFO25 stays 0x0c01 because the exception hit inside the algorithm.

**XTOS UserFrame at a2 = 0x7ff3fc60** (DMEM1 offset 0x1fc60):

| field | value |
|---|---|
| pc | 0x7de01027 |
| ps | 0x00060330 |
| sar | 0x18 |
| a2 | 0x7de01868 (`g_TMDMA`, the DMA controller) |
| **a3** | **0x7ea01000 (`set_buf_ptr`)** |
| a4 | 0x7ff20400 (`U32Dst`) |
| **a5** | **0x438 = 1080** |
| **exccause** | **6 = IntegerDivideByZero** |

**The faulting instruction.** 0x7de01027 is `quos a5, a5, a7` at histogram main_proc.c:97, the
"Loop Count" just after `start_y,end_y` is logged at :95. The dividend is 1080 (the image height)
and the divisor a7 = 0.

**The kernel's locals** (its sp is 0x7ff3fcc0; frame base = sp + 0x90, `srcImg` at fbreg −128):
- sp + 0x10 = srcImg 0x7ec00000 and sp + 0x14 = dstImg 0x7eb10000, both correct.
- sp + 0x28 = start_y = 0 and sp + 0x2c = end_y = 0.
- start_y and end_y come from `start_y = (X / s) × s` and `end_y = Y / s` (`quos` and `mull` at
  0x7de00ff4..0x7de00ffa), with a non-zero step s.

**What histogram is missing.** The only inputs we left at 0 are **`settings[1]` (S32_Offset, also
logged as 0) and `settings[2]` (unnamed)**, plus unused plane fields. The buffer fields arrived:
height 1080 is in a5 and the pointers are right. The consistent reading is that the third int32
setting is the **end row (ROI height)** of the band this call processes:
- start_y = S32_Offset aligned down to the step;
- end_y comes from `settings[2]`;
- the divisor at :97 is a band height derived from them (end_y − start_y, or `settings[2]` itself).

`settings[2] = 0` gives end_y = 0 and a division by zero. The Scale/Offset pair is the start row and
step used to split a frame between the two VPU cores (`start_y,end_y`).

This is an inference: the arithmetic sits in undecoded bundles. It fits all four observed values
(start_y = 0, end_y = 0, a5 = H, a7 = 0).

**Corrected recipe H.** Everything as in section 9, but with settings
`01 00 00 00  00 00 00 00  38 04 00 00` (Scale 1, Offset 0, End 1080), still INFO15 = 12.
- Expect `start_y,end_y:0,1080`, and probably `0,1080` again if the step is 1. Then `Loop Count:N`
  and the cycle lines.
- If it faults again (INFO00 = 8): read EPC1 at DMEM1 + 0x10 (`vr 0x20010`) and the UserFrame at
  `vr 0x3fc60` (pc, ps, sar, vpri, a2, a3, a4, a5, exccause).
  - A divide by zero at 0x7de01027 again means the divisor is not `settings[2]`. Try
    `{30, 0, 1080}` (step = rows per tile) next.
  - A different EPC narrows it down further.
- Before each retry: power-cycle, `vboot.sh`, SET_DBG, DO_LOADER.

### 12. Histogram output format and settings, from the {1,1080,0} run (offline, 2026-10-18)

From `hout-c.bin` (the 8 KiB destination), `dmem-hist-c.bin` (DMEM/IMEM after the run) and
`h-src.bin`. Analysis scripts are inline Python; the numbers are reproducible from those files.

**Destination layout** (the two `store`s at main_proc.c:132 and :141 write `U8Dst`, then `U32Dst`,
1 KiB each):

| dst offset | content |
|---|---|
| 0x000-0x0ff | **u8 LUT[256]**, the histogram-equalization LUT (`xvHistogramEqualization_LUT`). Here all 0xff (saturated, see below) |
| 0x100-0x3ff | zero (the rest of the 1 KiB `U8Dst`) |
| 0x400-0x7ff | **u32 CDF[256]**: the cumulative histogram, CDF[v] = number of px ≤ v |
| 0x800- | untouched (0xaa) |

The final copy of both arrays stays in DMEM1: `U8Dst` at 0x7ff20000 (`vr 0x20000`) and `U32Dst`
at 0x7ff20400 (`vr 0x20400`). The CDF in the destination is a snapshot taken **one loop before the
end**: dst[0x400] = 347587, while the DMEM final is 386924. **Histogram = first difference of the
CDF.**

**Settings semantics.** These fit all five runs ({1,0,0}, {30,0,1080}, {1080,0,1}, {1,1080,0} and
the 24-byte one):
- `settings[0]` ("S32_Scale" in the log) = **start row**, rounded down to the 30-row DMA tile
  (57600 / W): 1 gives 0, 30 gives 30.
- `settings[1]` ("S32_Offset") = **end row**.
- `settings[2]` is not used.
- `Loop Count = 15 × H / (end_y − start_y)`, where H is `vpu_buffer[0].height`: 15 for
  {0, 1080}, and −540 for {30, 0}. A difference of 0 gives the IntegerDivideByZero of section 11.
- **Each loop histograms rows [start_y, end_y) and accumulates.** So the kernel processes 15
  frames' worth of rows: CDF[255] in DMEM = 31,104,000 = 15 × 1920 × 1080, exactly. This is a
  benchmark harness (`Performance Pass` against a CPP threshold), which is also why the LUT
  saturates to 0xff: it is scaled by W × H but fed 15 × W × H counts.
- The 24-byte settings blob faulted because INFO15 / `set_buf_size` evidently has to be 12. Keep
  INFO15 = 12.

**Accuracy.** Over the near-uniform `(x+y)&0xff` frame, CDF differences divided by 15 match the
CPU histogram to within 1-7% for bins 1..254. Bin 0 is about 3× too high and bin 255 about 1.5× too
high: extra zeros and 0xff values are being counted, probably tile padding or stale DMEM. This pattern
cannot show *which* rows are counted, so the next run must use a row-coded image.

**Corrected recipe H** (buffers, source and destination as in section 9):
1. **Settings:** `00 00 00 00  38 04 00 00  00 00 00 00` (start 0, end 1080), INFO15 = 12.
2. **Source pattern:** use **`src[y][x] = y & 0xff`**. The reference is then
   `hist[v] = 1920 × #{y < 1080 : (y & 0xff) == v}`: 1920 × 5 = 9600 for v < 56, and
   1920 × 4 = 7680 for v ≥ 56. Any extra or missing rows show up as whole multiples of 1920.
3. **Single pass.** Optionally set **`vpu_buffer[0].height = 72`**, so that
   15 × 72 / 1080 = 1 and the log shows `Loop Count:1`. Keep the stride (1920), length and ptr of
   the full frame: H only appears in the loop-count formula and the CPP. If Loop Count is 1, the
   CDF should equal the true CDF, and the LUT should be a real equalization curve:
   `lut[v] ≈ 255 × CDF[v] / (W × H)`.
   With H = 1080 instead, divide the CDF by 15.
4. **Read the result:**
   - CDF from `vr 0x20400` (1 KiB, final), or dst + 0x400 (one loop earlier).
   - LUT from dst + 0x000.
   - `hist[0] = CDF[0]`, `hist[v] = CDF[v] − CDF[v−1]`.
5. **Banding:** `{s, e}` histograms only rows [s, e). The dual-core split is `{0, 540}` on core 0
   and `{540, 1080}` on core 1. Keep s a multiple of 30 rows, or more generally of 57600 / W.
6. **Camera frames:** 1456 × 1088 with stride 1472, settings `{0, 1088}`. The tile is
   57600 / 1472 = 39 rows. For a single pass, use height = 1088 / 15, not an integer. Use 15 loops
   and divide by 15, or pick end − start as a multiple of 15.

### 13. Histogram on the unit (2026-10-18)

Settings {0, 1080, 0} (start row, end row), INFO15 = 12, source `y & 0xff` at 1920x1080. The loop
count stays 15 whatever `vpu_buffer[0].height` says (72 or 1080): it is a fixed benchmark repeat, so
the counts are 15 frames' worth. 137k processing cycles and 244k DMA-in cycles for the 15 passes,
under a millisecond a frame. The final CDF in DMEM1 (`vr 0x20400`) totals exactly
15 x 1920 x 1080; the histogram from it is right to about 1%: 0.82% of the pixels land in bin 0
instead of their own (most bins a fraction of a percent short, bins 58 and 252 some 23%), the
harness's tile edges most likely. The LUT at dst saturates (scaled for one frame, fed 15). Good
enough to show the VPU doing image work from Linux; not exact enough to ship as is.

### 14. A driver instead of the debugfs probe (2026-10-18)

`/dev/techo5-vpu` (now `patches/vpu/techo5_vpu.c`, `techo5_vpu_uapi.h`) does in the kernel what
`vboot.sh` and `vcmd2.sh` did: BOOT copies a firmware image into the region and runs the boot
sequence (AXI defaults kept) and SET_DBG; LOAD runs DO_LOADER on an ELF; D2D writes the vpu_buffer
array and the settings into the region and runs the job, the images being in the data area from
0x7eb00000 that the device maps. A command is done at READY with PWAITMODE; ERR (an exception) marks
the core as needing a boot. echod's `hardware/vpu` builds the firmware image from the unit's
`cam_vpu1` partition (the same bytes as `mkregion.py`) and finds algorithms by name in `cam_vpu2/3`.
On the unit: booted in 5 ms; ksample's 64x64 copy in 0.46 ms, byte-exact, nothing written past it.

### 15. A neural network on the VPU: person detection (offline, 2026-10-18)

The question: can the VPU run echod's person detector (`feature/person`, TensorFlow's person detection
example, `assets/person_detect.tflite`) so that it leaves the CPU?

(Superseded by section 16: apunn's format is now known, and the model's convolutions run on the VPU,
byte-exact. The CPU cost measured there is also about three times this section's estimate.)

**Verdict: no, and it is not worth trying.** The model costs the CPU about 1-2% of one core at two
looks a second. It only runs while presence has seen movement. A VPU port would save at most that.
Each of the three ways to get there costs weeks, and none of them is likely to work.
- **apunn** needs a private per-operation command format. The Android HAL builds it on the ARM side,
  operation by operation, and it is documented nowhere. apunn also takes only uint8 per-tensor
  quantization, and this model is int8 per-channel.
- **Our own code** can only be scalar base-ISA code, because the Vision P6 vector ISA is not public.
  Scalar code would be about 10-20× slower than the A73.
- **dlfd** is a face detector. echod already has one in hardware (mtk-fd).

Leave the model on the CPU. If its cost ever matters, write a NEON kernel for depthwise convolution
in `lib/tflite` (below). That costs days, not weeks.
Confidence: high on the CPU cost and on the recommendation. Medium on the apunn format details, which
come from strings and from dlfd's DWARF, not from decoded code.

Inputs:
- `backups/<unit>/partitions/p16-cam_vpu2.img` (algorithms 12 and 13, core 0) and `p12-vendor.img`
  (`lib/libdnn_fd.so`, `lib/libvpu.so`, `lib/libneuropilot_hal_utils.so`,
  `bin/hw/android.hardware.neuralnetworks@1.1-service-apunn`), read with a small ext4 reader.
- The model and `echod/internal/lib/tflite`.

Scratch (not in the repo): `scratchpad/vpu15/` holds `extract.py`, `elf.py`, `ext4.py`, a DWARF v2
struct dumper (`dwarf.py`), a TFLite flatbuffer walker (`tfl.py`), and the extracted `out/*.elf`
and `host/*`.

#### 15.1 The model, and what it costs today

The model, from walking its flatbuffer:
- Input: 96×96×1 int8. Then 13 depthwise-separable blocks. The first layer is a depthwise 3×3 with
  stride 2 and depth multiplier 8, from 1 channel to 8. Then AVERAGE_POOL 3×3, a 1×1 CONV down to 2,
  RESHAPE and SOFTMAX. 31 operators in all.
- **7.16 M MACs per inference:**

  | | MACs | |
  |---|---|---|
  | 1×1 convolutions | 6.19 M | 14 layers, 8 to 256 input channels |
  | depthwise 3×3 | 0.96 M | |

- **208 k weights**, int8 with **per-channel** scales (one scale per output channel in every
  conv). Biases are int32.

So MobileNet v1 0.25 at 96×96 is indeed about 7 MMAC.

**How echod runs it.** `lib/tflite` does **not** use int8 kernels. It dequantizes the weights when
the model loads and runs the model in float32 (commit bee71da7; `interp.go` "a quantized model is run
in float").
- 1×1 convs go through `vec.Dot`, the NEON FMLA assembly in `lib/vec/dot_arm64.s`. There is one call
  per output element, about 124 k calls, each 8-256 long.
- Depthwise convs are plain Go scalar loops (`kernels_nn.go`). The first layer takes the slower
  `c/mult` path.

No timing on a device exists (`BenchmarkScore` in `person_test.go` has not been run on one), and
there is no Go toolchain here to run it. The estimate below is a cycle model:

| | per Dot call | per 1×1 output (besides Dot) | per depthwise MAC | ms per inference | share of one core at 2/s |
|---|---|---|---|---|---|
| A73 @ 2.0 GHz | ~15 cycles + 0.4/MAC | ~12 cycles | ~2.5 cycles (8 on layer 1) | ≈ 5 | ≈ 1% |
| A53 @ 2.0 GHz | ~25 cycles + 1.0/MAC | ~18 cycles | ~4.5 cycles (14 on layer 1) | ≈ 9 | ≈ 2% |

- The float weights add 0.8 MB of memory traffic per inference, under 0.5 ms.
- Allow 2× either way: **3-15 ms an inference, 0.5-3% of one core, or 0.1-0.4% of the 8-core SoC.**
  And only while presence reports movement.
- **That is tiny.** Moving it to the VPU gains at most about 10 ms of CPU a second, while somebody is
  moving in the room.
- To check it, run `go test -bench Score ./internal/feature/person` on the unit, pinned with
  `taskset` to a big core, then to a little one.
- If it ever matters, there are two cheap CPU fixes, both far cheaper than any VPU route:
  - Vectorize the depthwise loops (channels are contiguous, so this is an AXPY over channels).
    That roughly halves the time.
  - Run int8 with SMLAL. The A73 and A53 are ARMv8.0, without SDOT.

#### 15.2 apunn: what it executes, and could we feed it

What the DSP side is (`apu_lib_apunn`, cam_vpu2 algo 13, core 0 copy):
- **This ELF has neither a symbol table nor DWARF.** It has section headers, `.rodata` strings, an
  `.xt.prop` table and a `.comment` section with one compile line per source file. XCC
  12.0.7/RG-2017.7, `-O2 -g3`, with the debug info stripped afterwards.
- Text is 0xf52b0 bytes at 0x7de016b0. The entry is 0x7de01790, ET_EXEC, linked at 0x7de00000 like
  every other algorithm, so our LOAD would take it.
- Its sources:
  - `vp6-ann/main.c` and `apu_drv.c`.
  - **`vp6-ann/xrp-dsp/xrp_dsp.c`**.
  - `vp6-ann/operations/*.c`, one wrapper per op: conv, depthwise_conv, dilated and
    depthwise_dilated conv, deconv, pool, softmax, logistic, RELU, PRELU, concat, pad, slice, mean,
    l2norm, eltwiseOp, transpose4D, depthnspace, batchnspace4D, mtk_reorder, mtk_requant and
    mtk_resize_{bilinear,nearest}.
  - Cadence's XI CNN library: `xicnna/libxi/cnn/src/{convA_MOW, depthwise_conv_MOD, fc, avgpoolA,
    maxpoolA, softmaxA, …}.c`, MediaTek's `libxi/mtk_cnn`, and `libcommon` (cnnrt, cnnarena, iDMA).
- Its strings:
  - The op enum `XFL_CONV_2D … XFL_RESIZE_NEAREST`, 27 ops.
  - The version tag `XTENSA_ANN_V1`.
  - XRP's `process_command`, `find_cmd_ns`, `xrp_device_dispatch`,
    `%s, xrp_request_valid failed!! flags = 0x%x` and `dsp_buffer[%d].flags`.
  - The XI error table.

What the host side is (`android.hardware.neuralnetworks@1.1-service-apunn`, 234 KB, stripped
ARM32):
- Sources: `vendor/mediatek/proprietary_mt8183/hardware/vpu_nn/{XtensaDriver, XtensaExecutor,
  APUCustomOP}.cpp` and `operations/src/*.cpp`.
- It links `libvpu.so` and opens the VPU algorithm `apu_lib_apunn` through it (`[inst] create vpu
  algo[%s] fail`).
- **It is the XRP host, carried over D2D:**
  - It allocates `in_data`, `out_data` and a `dsp buffer` array, and calls
    `Xtensa xrp_run_command_sync`.
  - Before anything else it sends `XFL_XTENSA_ANN_VERSION` (`Xtensa ANN version mismatch!`) and
    `XFL_LOCAL_MEM_INFO`, which reports the DMEM banks (`numBanks > 1`, `areBanksContiguous`).
- **There is no offline compiler.** At model preparation, each NNAPI operation's `kernelSetup` does
  the work on the ARM:
  - checks shapes and types;
  - computes the quantization parameters (`failed re-quantization setup`);
  - plans the tiles against the DMEM banks (`needs 2 banks`, `min_tile_W` / `max_tiles_W` /
    `bast_tile_W`);
  - reorders the coefficients into the VP6 layout (`coefficients reordering failed`);
  - fuses PAD into CONV.

  The executor then issues **commands op by op** (`CMD op:`, `op count` / `optimize op count`),
  with temporaries in XRP buffer groups.

So the "command stream" is a series of XRP commands, one per (fused) op. Each command's `in_data`
carries an `XFL_*` opcode and that op's parameter block. The tensors in it are described as Cadence
XI tiles. dlfd, built from the same `libxi/cnn` and `cnnrt`, has full DWARF for those structures:
- `xi_tile3D` (68 bytes: pBuffer, bufferSize, pData, dim1/2/3 sizes, pitches, locations and edges,
  `dataOrder`) and `xi_tile4D` (80 bytes).
- `xi_cnn_data_order`: WHD, DWH, WHDN, NWHD, NDWH, DWHN.
- `xi_cnn_conv_params` (6 bytes): `stride u8, accumShift u8, outputScale u16, outputShift u8,
  flags u8`.
- Also `xi_cnn_eltwise_params` and `xi_cnn_softmax_params`.

`xi_cnn_conv_params` holds **one fixed-point output scale per layer**. The op set covers everything
this model needs: depthwise conv with a multiplier, 1×1 conv, average pool, reshape and softmax.

Is the format public?
- The **XRP framing is**: Cadence's open-source XRP (github.com/foss-xtensa/xrp) has the host API
  (`xrp_api.h`, `xrp_run_command_sync`), the DSP side (`xrp_dsp.c`) and the shared command layout
  (`struct xrp_dsp_cmd`: flags, in/out data sizes, buffers, a 16-byte namespace id).
- The **per-op payload is not.** It is MediaTek and Cadence code that lives only in the vendor tree
  and in this binary.
- MediaTek's MT6771 kernel driver (`drivers/misc/mediatek/vpu/mt6771`) is only the transport we
  already reimplemented. It knows nothing of NN.
- `apusys` and `mdla` are later chips (MT6779 on) with an MDLA accelerator and a different
  firmware. Nothing carries over.
- The NeuroPilot SDK documents the TFLite and Neuron APIs above the HAL, not the APU command format.

Could we hand-build a stream for this model? In principle yes. In practice the work and the risks
are large:
1. **Reverse-engineer `kernelSetup`** for 5 ops (DEPTHWISE_CONV, CONV, AVERAGE_POOL, RESHAPE,
   SOFTMAX). That means recovering, from stripped ARM32 C++ (146 surviving `_Z` symbols, mostly
   HIDL and std):
   - the tiling planner;
   - the coefficient reorder for the `MOD` and `MOW` kernel variants;
   - the parameter-block layouts;
   - the XRP-over-D2D framing. Which D2D ports and buffers carry `in_data`, `out_data` and the
     buffer array has to be read from `libvpu.so` and the HAL.
2. **Quantization.** The HAL is NNAPI 1.1. That version has only `TENSOR_QUANT8_ASYMM`: uint8 with
   one scale per tensor. Per-channel int8 arrived in NNAPI 1.2. This model would have to be
   requantized to per-tensor uint8, and MobileNet v1's depthwise layers lose much of their accuracy
   that way. That loss is the reason per-channel quantization exists (Krishnamoorthi 2018,
   "Quantizing deep convolutional networks for efficient inference"). The alternative is retraining
   with quantization-aware training.
3. **Debugging blind.** We cannot decode the vector code (section 1). A wrong field gives garbage or
   an exception, never a message. The CPU model would be the only reference.
4. **Not obviously faster.** There are about 30 ops, each a D2D round trip. ksample's whole D2D took
   0.46 ms (section 14). Command overhead alone could reach several ms, about what the A73 needs
   for the whole model. The arithmetic itself is negligible on the P6: 7.2 MMAC at the 256 8-bit MACs
   a cycle Cadence quotes for Vision P6, at a few hundred MHz, is under 0.1 ms at full use and
   perhaps 0.5 ms on layers this small.

**Effort:** 4-8 weeks, with low confidence of an accurate result, to save about 1% of a core.

#### 15.3 Our own code on the P6: is there a toolchain?

- **Vector (TIE) code: no.** The core's ISA lives in the TIE files and Cadence's generated
  `xtensa-modules.c` and `libisa` tables. Only the configuration's licensee has them.
  - The ELFs carry only identifiers: `.xtensa.info` gives `CORE_NAME="MVPU6_0413_Prod"`, the
    CONFIGID words and four TIE checksums.
  - `.xt.prop` is Cadence's property table: address ranges flagged as instructions, literals, data,
    alignment or branch targets. It shows where instructions are, which helped the length sweep in
    the loader section. It says nothing about what they encode.
  - So the FLIX/IVP vector instructions cannot be recovered from the firmware, short of decoding
    the encodings statistically from XCC output. That would take months.
- **Scalar base ISA: possible.**
  - Upstream GCC and binutils target Xtensa through a "core overlay" (`xtensa-config.h` plus the ISA
    tables). Public overlays exist only for other cores: the ESP32 LX6 and LX7 cores, dc233c, and
    others.
  - A hand-made overlay with this core's base options would do: windowed ABI, CONST16 loads (GCC's
    `-mconst16`), MUL32, DIV32 (`quos` is used), loops and density, all of which are visible in the
    code.
  - That overlay could build an ET_EXEC algorithm at 0x7de00000 with the `_start` /
    `VPU_LIB_INFO_STRUCT` contract of section 3. Upstream LLVM's Xtensa backend is a second route.
- **Would scalar code beat the CPU? No.**
  - An int8 MAC in base-ISA code takes two loads, a 16-bit multiply and an add: about 3-4 cycles a
    MAC on the single-issue scalar pipe.
  - 7.2 M MACs is then about 25-30 Mcycles: **about 90 ms at 312 MHz, about 50 ms at 600 MHz.** The
    A73's float NEON path takes about 5 ms.
  - The weights (208 KB int8) would be read from DDR, or tiled into the 2 × 128 KB DMEM with our own
    iDMA code.
  - So it frees about 1% of a core at 10-20× the latency.
- **Effort:** 1-2 weeks for a toolchain and a loadable "hello" algorithm, plus about 1 week for the
  int8 kernels. That buys nothing echod needs. It could make sense only as a platform for some
  future always-on DSP job.

#### 15.4 The stock face detector (dlfd) as a person signal

What it is:
- The DSP side has full DWARF (`libfd_vpu0_landmark/src/{main_fd,layers_fd}.c`, XI CNN with
  `S8`/`S16` convs and residual adds).
- Its weights are built in.
- Its settings are `vpu_st_sett_dlfd {input_width, input_height, rotation, execution}` (16 bytes,
  from DWARF).
- It has two RPN heads (`Head1/2ProposalCoord`, `ProposalScore`, `LandmarkCoord`,
  `LandmarkVisible`, all DWH order).

What `libdnn_fd.so` does on the CPU:
- builds the image pyramid (`pyramid(%ld us)`, port `DLFD_pyramid`);
- queries the output sizes (`head0_coord`, `head0_logit`, `head1_*`, `landmark0/1_*`);
- decodes the boxes for each of four rotations (`decode_0/90/180/270`);
- merges the 1, 1/4 and 1/16 scales (`merge bbox_1_4`, `bbox_1_16`);
- runs NMS.

Reverse-engineering it means recovering, from 57 KB of stripped ARM32 C++:
- the pyramid geometry and buffer layout;
- the anchor and stride scheme and the box-regression decode;
- the score thresholds and NMS.

That is 1-3 weeks. The DWARF on the DSP side helps with the tensor shapes.

**Not worth it.** It finds faces, which the MT8183's hardware face detector already does in echod
(`feature/presence/faces.go`, through `hardware/facedetect`, once a second). The two would overlap
almost entirely. Nor does a face detector replace a person detector: a person turned away, far off or
half out of frame shows no face, and that is exactly the case `feature/person` exists for.

#### 15.5 Recommendation

1. **Keep person detection on the CPU.** Measure `BenchmarkScore` on the unit, on a big core and on
   a little one, to replace the estimate in 15.1.
2. If the number surprises (over about 20 ms), vectorize the depthwise kernel in `lib/tflite`
   first, or add an int8 SMLAL path.
3. Keep the VPU for what already runs without host libraries: histogram, and the Laplacian level
   (section 9).
4. Do not start on apunn or a P6 toolchain for this model.

### 16. apunn from Linux: one quantized conv, then the person detector's 27 (2026-10-18)

**Verdict: it works, byte for byte.** apunn runs CONV_2D and DEPTHWISE_CONV_2D from Linux through
`/dev/techo5-vpu` with no Fire OS library, and its output equals a CPU reference exactly in every test
(no ±1 LSB at all). The person detector's 27 convolutions run as **one command in 2.55 ms**, against
**16.9 ms** for the whole model on the CPU today. The cost is accuracy: NNAPI 1.1's uint8 takes one
weight scale per tensor, and the model's are per channel. On 27 pictures the person score moves by
0.05 on average and 0.14 at worst, and no decision at 0.5 changes.

Code: `echod/internal/hardware/vpu/apunn.go` (the command transport, and the HAL's kernelSetup for the
two convs, ported), `apunn_device_test.go` (`hwdevice`), `person_net_test.go` (the model converted
to uint8 per tensor, the CPU pipeline, the accuracy test). Scratch: `scratchpad/vpu16/` (`hal/hal.s`,
the annotated disassembly; `tools/annot.py`).

#### 16.1 How the HAL was read

The HAL (`android.hardware.neuralnetworks@1.1-service-apunn`, Thumb-2) is stripped, but it carries a
**`.gnu_debugdata` section** (Android's MiniDebugInfo: an xz-compressed ELF with a symbol table). It
names 349 functions, every kernelSetup and planner among them: `flkConv_kernelSetup`, `SetupConv2D`,
`SetupDepthwise`, `searchTileDims`, `AllocateLocalMemConv2D`, `setupDeviceMemoryLayout`,
`ConvReorderCoefficients`, `kernel_model_{qm24,qm32,1x1,MxN,GS_MOW}`, `xrp_intrinsic_queue_cmd`,
`xrp_create_vpu_instance`. With `arm-none-eabi-objdump` (in `techo5-kbuild`), those symbols put back
with `objcopy --add-symbol`, and a small annotator that resolves the PC-relative literals to strings
and the interworking stubs to PLT names, the code reads almost like source. `libvpu.so` exports its
C++ methods and its vtables are relocations, so its virtual calls resolve too.

#### 16.2 Transport: XRP in one D2D job

`xrp_create_vpu_instance` gets the algorithm `apu_lib_apunn` (it tries `apu_lib_custom` first),
allocates a 104-byte ION buffer for the command and adds it to a request **five times** as identical
`VpuBuffer`s: port 1, one plane, width = stride = length = the buffer's size, height 1. There are no
settings. Each command is one `enque`. One buffer is enough: we send one.

The command is Cadence's `xrp_dsp_cmd` plus two words of MediaTek's:

| offset | field |
|---|---|
| +0 | flags: 1 REQUEST_VALID, 4 REQUEST_NSID (5 sent); the DSP sets 2 RESPONSE_VALID (8 would be DELIVERY_FAIL) |
| +4, +8, +12 | in_data size, out_data size, buffer size (12 × buffers) |
| +16 | in_data: inline if ≤ 16 bytes, else its address |
| +32 | out_data: the same |
| +48 | the buffers: one `{flags, size, addr}` inline, or the address of an array of them; flags 1 read, 3 read-write |
| +64 | namespace id `de63dbbe-4a99-4889-9083-f07bf861097a` (also in apunn's ELF) |
| +80, +84 | 1, 0 (single core) |

The addresses are the core's physical addresses; everything sits in the driver's data area
(0x7eb00000), inside the firmware's MPU window. The first 64 KiB of the data area hold the command
(`Device.Command`), tensors go above.

**A command carries a whole model.** `XtensaExecutor::run` sends one command per inference: in_data is
an array of 152-byte operation records, out_data a 4-byte status (0 = done), the buffers every
tensor. A record is:

| offset | content |
|---|---|
| +0 | the XFL op: the order of apunn's `XFL_*` strings, CONV_2D 0, DEPTH_CONV2D 1, …; 27 LOCAL_MEM_INFO, 28 XTENSA_ANN_VERSION |
| +4..+131 | the op's parameters, as its kernelSetup wrote them |
| +132 | 6 × u16: the input tensors, as indices into the buffers (0xffff unused) |
| +144 | 2 × u16: the output tensors |
| +148 | the HAL's "skip" mark for fused ops (0) |

On the unit: VERSION answers `{0, "XTENSA_ANN_V1"}`; LOCAL_MEM_INFO answers `{0, banks 2,
contiguous 1, 124304, 118784}` (bytes free in DMEM0 and DMEM1). A command that does nothing takes
**0.29 ms** round trip. Booting the firmware takes 3-4 ms, loading apunn 3 ms.

apunn's ELF is 1.37 MB, the driver's LOAD takes 1 MiB. Only 0xf87e8 bytes are loaded (the program
headers end there: the section headers are not used by the loader), so `Device.Load` now sends the
ELF cut after its last loaded byte; the driver is unchanged. Also fixed: `vpu.Algorithm` stepped
through the algorithm table 40 bytes at a time instead of 44 (`{core, offset, length, name[32]}`),
which only found algorithm 0.

#### 16.3 CONV_2D and DEPTHWISE_CONV_2D parameters (`conv_params_t`, 81 bytes at record +4)

| offset | field |
|---|---|
| +0 | 81 |
| +4..+19 | the local-memory layout `{mode, mask, bytes in bank 0, in bank 1}` (below) |
| +20, +24, +28 | input C, W, H |
| +32, +36, +40, +44 | output C, W, H; batch N |
| +48, +52 | output multiplier (Q31) and right shift: NNAPI's `QuantizeMultiplierSmallerThanOne(in_scale × w_scale / out_scale)` |
| +56, +60, +64 | the output tile: channels, columns, rows |
| +68, +70 | KW, KH (u16) |
| +72 | stride (u8; the HAL reads stride W only, and accepts square strides) |
| +73 | flags: bit 0 activation fused; bit 1 loop order; bits 2-4 the kernel; bit 5 double-buffered coefficients |
| +74, +75 | KW/2 − pad left, KH/2 − pad top |
| +76, +77, +78 | zero points: input, weights, output |
| +79, +80 | the activation's range, as NNAPI's `CalculateActivationRangeUint8` |

Kernels (`conv_kernel_kind_e`): 1 "MOD" (1x1 and MxN, output channels in blocks of 64), 2 QM24 (24-bit
accumulators, allowed when `MayOverflowAccumulator24` says no channel's 128 × Σ|w − 128| reaches
2^23), 3 QM32, 4 the fallback (one output pixel a tile; the HAL uses it for non-constant weights),
5 GS_MOW (3x3 stride 1), 6 depthwise. `SetupConv2D` tries QM24 (if allowed), MOD and GS_MOW with
`searchTileDims` and keeps the cheapest by the `kernel_model_*` cycle models; if none fits, QM32; then
the fallback. `searchTileDims` takes the whole output as one tile if it fits, else walks channel tiles
(step 1, or 64 for MOD), with and without double-buffered coefficients, and row tiles from the full
height down, by model cycles + 1276 a tile. `AllocateLocalMemConv2D` sizes eight buffers (bias, input
×2, output ×2, coefficients ×2, scratch; each rounded to 64; the second copies only when there is more
than one tile) and `setupDeviceMemoryLayout` places them: with two banks, largest first into bank 0
while it fits, the rest into bank 1 (the mask says which); the DSP repeats the sizing from the same
parameters. `SetupDepthwise` uses channel tiles of 32 and tries row tiles from the full height down.
Everything except GS_MOW is ported (`apunn.go`), with the models' constants read from the binary.

Coefficients and bias (`ConvReorderCoefficients`):
- QM24/QM32: per output-channel tile, per block of 64 channels, two passes (pairs of `(kx, ci)`
  positions alternate between them), each row padded to a multiple of 4 and each kernel to 8; every
  weight stored as w − 128 (signed), one byte per output channel of the block.
- MOD: per tile, `[ky][kx][ci][channel]`, channels padded to 64, w − 128.
- Both fold the zero points into the bias: `bias + (zw·zi − 16384)·K + (128 − zi)·Σw` (K = KW·KH·Cin).
- Depthwise: weights unchanged, `[tile][ky][kx][channel]` padded to 64; bias unchanged.
- Fallback: both unchanged.

Tensors are NHWC uint8; the record's inputs are `{input, coefficients, bias}`, the output is one.
The HAL's `isSupported` limits depthwise to **a multiplier of 1** and strides 1, 2 or 4.

#### 16.4 Results on the unit (kernel with VPU_PROBE=1, `go test -tags hwdevice`)

Random weights, biases and inputs with NNAPI 1.1's quantization (in 0.02/120, w 0.004/131, out
0.05/100), compared with a CPU reference written from NNAPI's (gemmlowp's rounding). Output pre-filled
with 0xaa; nothing written past it in any run. Times are the best of 5 commands, round trip.

| layer | kernel, tile | differing outputs | time |
|---|---|---|---|
| conv 8x8x8 → 8, 3x3 | QM24 (chosen), whole | 0 of 512 | 0.34 ms |
| the same, forced MOD / QM32 / fallback | | 0 / 0 / 0 | 0.34 / 0.32 / 0.50 ms |
| conv 12x12x32 → 64, 1x1, RELU6 | all four kernels | 0 of 9216 | 0.33-0.37 ms (fallback 1.0 ms) |
| depthwise 16x16x16, 3x3, RELU6 | depthwise, whole | 0 of 4096 | 0.35 ms |
| conv 64x64x32 → 64, 1x1 (tiled) | QM24 [64,64,16]; MOD; QM32 | 0 of 262144 | 0.85 / 0.87 / 0.96 ms |
| conv 40x40x16 → 96, 3x3 (tiled) | QM24 [96,40,10]; MOD; QM32 | 0 of 153600 | 0.85 / 1.06 / 0.96 ms |
| conv 64x48x24 → 24, 3x3 stride 2 | QM24 [24,32,12]; MOD; QM32 | 0 of 18432 | 0.44 / 0.64 / 0.54 ms |
| depthwise 64x64x64 (tiled) | [64,64,13] | 0 of 262144 | 0.52-0.54 ms |
| depthwise 96x80x48 stride 2 | [48,48,10] | 0 of 92160 | 0.44 ms |

So about 0.3 ms is the command, and the arithmetic is fast: 13.8 M MACs (40x40x16 → 96, 3x3) in about
0.55 ms beyond it.

**The person detector.** `person_net_test.go` converts `assets/person_detect.tflite`: activations
convert exactly (uint8 = int8 + 128 at the same scale), weights are quantized again per tensor
(min/max, asymmetric), biases again at in_scale × the new w_scale. The first layer, a depthwise with
multiplier 8 from one channel, is not supported as such and becomes the equal CONV_2D 1 → 8. Its 27
convolutions then go to the VPU as one command (82 tensors, 27 records, about 210 KB of coefficients and biases placed
once); the average pool, the last 1x1 to two classes and the softmax stay on the CPU (2.3 K adds and
512 MACs).

- **The VPU's output equals the CPU's uint8 pipeline byte for byte**, layer by layer (a command
  each) and for the whole trunk, on all 27 pictures.
- **2.55 ms an inference** (the command round trip; the ARM sleeps in the driver's poll meanwhile).
  The CPU path, `BenchmarkScore` (echod's float interpreter), takes **16.9 ms** on the unit with
  echod running (no `taskset` there; GOMAXPROCS=1 gives 16.7 ms). At two looks a second that is
  3.4% of one core, against about 0.1% for the VPU path.
- **Accuracy of uint8 per tensor**, against the score echod computes today, on 27 pictures (12 crops
  of two 640x480 test pictures, one a portrait; 15 crops of three camera frames; scratch only): mean
  |Δ score| 0.051, worst 0.140, the same side of 0.5 in all 27. The six portrait crops score
  0.940-0.952 in float and 0.816-0.949 in uint8; the rest stay low in both. The per-tensor weight
  scales are coarse for some layers (layer 1, a depthwise: 0.23 a step), which is where the loss comes
  from.

#### 16.5 What is left for the whole network, and an estimate

(Done: items 1-3 shipped, section 17. Item 4 stays undone.)

1. **A real driver** in the production kernel (section 10; today the driver is only in VPU_PROBE test
   kernels). Boot once (4 ms), load apunn once (3 ms), keep the constants in the data area. 2-5 days,
   most of it the kernel side and its review.
2. **echod**: a VPU backend for `feature/person` that, when `/dev/techo5-vpu` exists, converts the
   model at start (the code in `person_net_test.go`, moved into the package), runs the trunk on the VPU
   and the tail on the CPU, and falls back to the CPU path on any error. 1-2 days with tests.
3. **Accuracy**: decide whether the per-tensor loss is acceptable (it changed no decision here; a
   bigger picture set from the unit's own camera should confirm). If not: cross-layer weight
   equalization before the per-tensor quantization (offline, no retraining), or quantization-aware
   fine-tuning. 1-3 days for equalization.
4. Optional: the pool and softmax on the VPU as well (`flkPool_kernelSetup`, `flk_softmax`), to save
   a few microseconds; not worth it.

Open points: GS_MOW (kernel 5) is not ported, so a 3x3 stride-1 CONV_2D the HAL would give to it
gets the next best kernel (the person model has none). DMEM layout mode 2 (two non-contiguous banks)
is ported but untested: this unit reports contiguous banks. The firmware's INFO20 read 0x23081617
after the first boot and 0x03081617 after later ones (driver log); harmless so far.

### 17. In production: the driver in every build, person detection on the VPU (2026-10-18)

**What shipped.**
- **Kernel** (`patches/vpu.py`, `patches/vpu/techo5_vpu.c`; `CONFIG_TECHO5_VPU=y` in every athena
  build): the section 14 driver, cleaned up as section 10 asked. `/dev/techo5-vpu` is a misc device,
  one opener at a time (EBUSY otherwise), root's alone: a job's XRP command names physical
  addresses the VPU's DMA then reads and writes, so the device is as good as /dev/mem. Power: the
  core's domain (runtime PM) and its 28 clocks are on from a BOOT until the last close, and also go
  off when a command times out or the firmware takes an exception (the next user boots again);
  the core is stalled before its clocks go. After probe the VPU is off (`runtime_status`
  `suspended`). The firmware still comes from user space (BOOT takes the image echod lays out from
  `cam_vpu1`), the region is the same no-map 22 MiB. The debugfs files (power, regs, mem, log) are
  only in a `VPU_DEBUG=1` build (`CONFIG_TECHO5_VPU_DEBUG`; `VPU_PROBE=1` still works), for
  `tools/vpu/`. Completion is still polled, sleeping 100 µs between looks (the IRQ is described in
  the DTS but unused): 0.25 ms of CPU time an inference, all in.
- **echod, `hardware/vpu`:** `ReadFirmware`/`ReadAlgorithm` read only the tables and the bytes
  needed (not the 15 and 32 MiB partitions); `OpenAPUNN` opens, boots, loads and checks apunn
  (about 40 ms, most of it reading the partitions); `PrepareNet`/`Place`/`RunNet` run a chain of
  convs as one command, its constants placed once.
- **echod, `feature/person` (`vpu.go`):** where `/dev/techo5-vpu` exists, the model is converted once
  (the section 16 conversion, plus equalization below), its 27 convs run on the VPU and the pool, the
  last 1x1 and the softmax on the CPU (14 µs). The VPU is opened at the first look, let go 30 s after
  the last one and when looking stops; any VPU error (or EBUSY) logs once and sends the model back to
  the cores until echod restarts. The float model stays loaded behind it.

**Equalization.** Plain per-tensor weights failed a picture that matters: the portrait's whole frame,
squashed to 96x96 as echod looks at it, fell from 0.79 to 0.24. The cause is the second layer, a
depthwise conv whose 8 channels reach 0.34 to 40.6 (median 0.94): over one scale most of its channels
get 3 steps. Cross-layer equalization (Nagel et al. 2019) between conv 0 (1 → 8), the depthwise and the
1x1 after it (two passes over the two pairs) fixes it. It is not free everywhere: equalizing only the
first pair moved the camera's empty-room scores up by 0.135 on average, the second pair alone moved
them down by 0.235 (RELU6's clip and the activations' fixed resolution see the scaled channels), the
two together balance; taking it down the whole chain helped nothing. So only the first three layers.

| per-tensor uint8 vs echod's float model | camera, 512 | portrait and room crops, 26 |
|---|---|---|
| plain: mean / worst \|Δ\| | 0.037 / 0.137 | 0.074 / 0.554 |
| plain: decided otherwise at 0.6 (at 0.5) | 0 (83) | 1 (1) |
| equalized: mean / worst \|Δ\| | 0.041 / 0.130 | 0.033 / 0.138 |
| equalized: decided otherwise at 0.6 (at 0.5) | 0 (69) | 0 (0) |

The camera set is 512 Luma(96, 96) pictures from the unit's camera, one every half second for four
minutes, of an empty room (scratch only, deleted). The model is unsure of that room: float scores
0.3-0.6, most 0.4-0.5, which is why so many cross 0.5; the judge's threshold is 0.6 (on the mean of
three looks), and none crossed that. The second set is the section 16 crops of the two 640x480 test
pictures plus their whole frames. On the unit the VPU's output equals the CPU's uint8 pipeline byte
for byte on all 538 pictures.

**Time and CPU, on the unit** (`BenchmarkDeviceVPUScore`, `BenchmarkDeviceCPUScore`, 400 looks each, echod running):

| | wall per look | CPU time per look | at two looks a second |
|---|---|---|---|
| VPU (27 convs) + tail on the CPU | 2.8 ms | 0.25 ms | 0.05% of a core |
| the float model on a core | 17.5 ms | 17.5 ms | 3.5% of a core |

Powering up (partitions read, boot, load, constants placed): about 40 ms, then 2.5 ms a look.

As echod runs it (`TestDeviceDetectorLooks`: the detector `load()` makes, a look every 500 ms for a
minute, echod held): on the VPU 120 looks took 118 ms of CPU time (0.2% of a core), on the cores 109
looks took 5.9 s (9.8%: 54 ms a look, the cores clocked down between looks, against the benchmark's
17.5 ms back to back). The VPU's `runtime_status` read `active` while looking and `suspended` once
let go.

**On the unit, in production** (rootfs v1.2.19-mainline.12 with this kernel, presence and person
detection turned on for the test): echod starts looking with the VPU off, and powers it only at the
first look, which needs presence to have seen movement. The room stayed empty and still for the
whole test (also at sensitivity 100), so echod's own run never got to a look; the path it would take
is the one `TestDeviceDetectorLooks` ran, in the same binary's code.

### 18. swnr, the stock noise reduction: recovered, and on the camera stream (2026-10-19)

**Verdict: it works, and it is in the stream.** swnr is MediaTek's "fast SWNR", a **chroma-only**
noise reduction: an edge-stopping recursive (IIR) filter run over the U and V planes of a 4:2:0
picture, then blended back toward the planes as they came where it changed them a lot. It does not
touch the luma: Fire OS left the luma to the ISP, and its library adds film grain to it on the cores.
On the unit's camera in a dark room (gain 15.5x) it takes the color noise from 10.3 to 3.2 levels
rms (PSNR +10 dB against the scene's mean over 24 frames), and the blotches of red and green over
everything gray are gone. One 1456x1088 picture is three jobs, 13.1 ms on the VPU, about 20 ms with
the cores moving the plane. echod now runs it on the Show 8's stream whenever the sensor's gain is
4x or more.

Code: `echod/internal/hardware/vpu/swnr.go` (the host side, ported), `vpu.go` (the device shared
between its users), `echod/internal/feature/camstream/nr.go` (when and how strongly),
`hardware/camstream`'s `SetDenoise` (where). Tests: `hardware/vpu/swnr_test.go`, `swnr_device_test.go`,
`hardware/camstream/nr_device_test.go` (the quality measurement), `feature/camstream/nr_test.go`,
`feature/person/vpu_device_test.go` (both users at once). Scratch, not in the repo:
`scratchpad/swnr/` (`tools/annot_arm.py`, the annotated `host/vpunr.ann`, `dtree.txt`).

#### 18.1 How it was read

- **The DSP side is not stripped.** cam_vpu2 algorithm 11 (core 0: offset 0x200 + 0x24f1b8, 0x42f4d
  bytes; core 1: 0x6feec0) carries a symbol table and DWARF built with `-g3`: sources
  `test/{main_proc.c, Blending.cpp, IIR_DOWN.cpp, IIR_PASS12.cpp, IIR_PASS34.cpp, PixelDiff.cpp,
  xiTranspose.cpp}`, the DMA layer, and every function's parameters and locals. The §9 survey missed it
  because it looked for the published port descriptors, and swnr publishes none. The settings struct
  is in the DWARF as `vpu_st_sett_swnr` (below); the stage functions are `mtkSWNRStage1..5`.
- **The host side** is `vendor/lib/libcamalgo.vpunr.so` (418 KB, ARM, stripped). Its C++ methods are
  exported, so `.dynsym` names nearly everything; `.gnu_debugdata` adds only a few locals. The code is
  ARM, not Thumb (the symbols' values are even), and annotates as in §16.1 (literal pools resolved to
  strings, PLT slots to their relocations). Source path
  `vendor/mediatek/proprietary_mt8183/hardware/libcamera_feature/libvpunr/mt8183/libnr/VPUIIR/VPUIIRCore.cpp`.
  Classes: `MTKNR` (the factory; `vendor.swnr.type` picks CPU or VPU), `AppIIR` and `IIRCore` (the same
  filter on the cores), `AppVPUIIR` and `VPUIIRCore` (on the VPU), `CCRCore` (chroma coring near
  neutral, cores), `HFGCore` (film grain on the luma, cores).
- **libvpu's request API**, from `libvpu.so`'s vtables (relocations in `.data.rel.ro`): `VpuStream`
  {getAlgo, acquire, release, enque(req, core), deque, flush, setPower, ...}, `VpuRequest`
  {addBuffer(port, buf), getProperty, setProperty(name, value), addBuffer(VpuBuffer&),
  setProperty(void*, len), setExtraParam}. `VpuBuffer` is {plane_count; planes[3]{fd, offset,
  stride, length}; format at +0x34 (clamped to 0-4); width; height; port_id at +0x40}, written into the
  64-byte `vpu_buffer` of §3 by `addBuffer`.

#### 18.2 What the host does (AppVPUIIR::NRInit/NRMain, VPUIIRCore)

- The picture is planar 4:2:0 (formats 2 and 4 accepted), its planes as three ION fds. Only U and V go
  to the VPU, each **W/2 x H/2 with a stride of W/2**.
- **Tuning**: ten int32s, Fire OS's `ANR_FASTSWNR_*` per ISO from the camera's NVRAM: K, STRENGTH, SEED,
  BLEND_W, BLEND_TH, DITHER_S, NTRL_TH_1_Y, NTRL_TH_2_Y, NTRL_TH_1_UV, NTRL_TH_2_UV. The VPU path reads
  the first six. The library carries one tuning of its own, `VPU_IIR_WORSE` = {300, 30, 0, 32, 5, 256,
  252, 254, 1, 3}, used when `vendor.swnr.load.worse` is set (the per-ISO NVRAM values were not looked
  for). CCR (30 values) and HFG (18) have their own `*_WORSE` tables; they run on the cores after the
  VPU when their tuning enables them.
- **Tables**, in the work buffer (`VPUIIRCore::IIRMain`):
  - weights, 256 bytes, by the difference d between neighbors:
    `w[d] = sat255((round(256·e^(-√2·d/(STRENGTH/10))) · round(65536·2^(-7.843848638152472/K))) >> 16)`.
    So K sets the most a pixel takes from its neighbor (251/256 at 300), STRENGTH/10 the scale in
    levels over which that falls.
  - blend, 512 bytes, by the change d (|ΔU| + |ΔV|): 0 for d < BLEND_TH, 64 − BLEND_W from 2·BLEND_TH,
    `2(64 − BLEND_W)(d − BLEND_TH)/d` between; all zero when BLEND_W is 64.
  - dither seeds, 4·W/2 bytes: a window (picked by SEED) of a 64 KiB random table in the library's
    `.rodata`. Only with DITHER_S ≠ 0; echod leaves the dither off rather than ship that table.
- **Jobs**: three a picture. `VPUInit` acquires nine requests but only three are used
  (`VPUStageAll`; `VPUStage0..4` are dead code): U with S32_Stage 5 on core 1, V with 5 on core 2 (in
  parallel), then 4 for both on core 2. Each job's buffers: the plane on port 1 (port 2 the V plane in
  the blend job), the work buffer on port 0 (format 0, width = stride = length, height 1). No `VpuPort`
  descriptors are involved: ports are just ids. `setExtraParam` asks for 1800 (planes) and 2300 (blend)
  MB/s of bandwidth, which `/dev/techo5-vpu` does not need.

#### 18.3 The interface

**Settings** (`vpu_st_sett_swnr`, 60 bytes, int32; the offsets into the work buffer, pl = W/2·H/2):

| # | field | plane jobs (U; V) | blend job |
|---|---|---|---|
| 0-2 | S32_Width, S32_Height, S32_Stride | W/2, H/2, W/2 | the same |
| 3 | S32_Stage | 5: the four passes over one plane | 4: the blend of both |
| 4 | S32_NeedBlend | BLEND_W ≠ 64 | the same |
| 5, 6 | S32_NeedDither, S32_NRSmooth | DITHER_S ≠ 0, DITHER_S | the same |
| 7 | S32_weight_tbl_offset | 2pl + 0x200 | the same |
| 8 | S32_iir_w_horz_offset | 2pl + 0x300; 3pl + 0x300 | (left over) |
| 9 | S32_src_offset | 0; pl (the plane as it came, kept for the blend) | (left over) |
| 10 | S32_iir_w_vert_offset | 4pl + 0x300; 5pl + 0x300 | (left over) |
| 11 | S32_sd_tbl_offset | 6pl + 0x300 + 2W | the same |
| 12, 13 | S32_src_u_offset, S32_src_v_offset | (not set: stack garbage in Fire OS) | 0, pl |
| 14 | S32_blend_tbl_offset | (not set) | 2pl |

**Work buffer**, (W/2)·(6·H/2 + 8) + 0x300 bytes (2.38 MB at 1456x1088): U and V as they came, the
blend table (0x200), the weight table (0x100), U's and V's horizontal weights, U's and V's vertical
weights, rows kept between tiles, the dither seeds.

**On the DSP** (from the functions' signatures; the arithmetic sits in FLIX bundles that were not
decoded): stage 1 copies the plane (`IIR_COPY`) and turns neighbor differences into weights through the
table, horizontally on transposed tiles (`PixelDiff_PASS1`, `xiTranspose_I8A`); stage 2 the vertical
weights (`PixelDiff_PASS2`) and the first vertical pass; stages 3-4 the other passes (`IIR_PASS1..4`:
across, back, down, up; the dither in pass 4); stage 5 (`Blending`) mixes filtered and original by
BLEND_W plus the blend table at |ΔU| + |ΔV|. DMEM: two 80 KiB tile buffers (`local_dmem0/1`), the
tables, and per-column state (`prev_ori_value_u`, 16 KiB); `sd_tbl` is 4096 words, so a plane is at
most 4096 wide.

**Constraint found on the unit: the planes must start on 4 KiB.** With the V plane right after U
(64-byte aligned, as the driver allows), the V job's output was garbage in bands of rows (rms error 22
against 4), whatever the settings; on 4 KiB boundaries, as ION's pages put them, both planes are right.

#### 18.4 Results on the unit (a Show 8 2nd gen, the production kernel's /dev/techo5-vpu)

- **Synthetic** (`TestDeviceSWNRSynthetic`: smooth ramps and sharp squares in U and V, Gaussian noise
  σ 4): rms against the clean planes 4.01 → 3.60 with `VPU_IIR_WORSE`, 2.21 at Strength 60, 1.55 at
  {K 600, Strength 100, BlendW 16, BlendTh 8}. Nothing written outside swnr's part of the data area.
- **Time**: 13.1 ms for the three jobs at 728x544 planes (about 4.4 ms each), whatever the tuning.
  `NV12` on the ISP's own plane: 20.5 ms. The rest is the cores: both the ISP's plane and the driver's
  data area are mapped uncached for them, and read at 80-400 MB/s (by core and clock), so each is read
  once in long runs, split or joined in cached memory, four bands side by side (34 ms before that).
  Since §18.6 the data area is mapped cached as well, and the planes are split into it and joined
  from it directly.
- **Quality** (`TestDeviceNRQuality`): 24 live frames of a still scene through the ISP at 1456x1088;
  the reference is their mean (a frame's error against it is its noise, plus whatever the filter takes
  of the picture; the mean keeps 1/√24 of the noise). The room was dark: gain 15.5x, the ISP's
  stretch 15x. rms in levels, over the whole plane, the flattest eighth of the 16x16 blocks and the
  busiest eighth:

  | | U: all / flat / busy | V: all / flat / busy |
  |---|---|---|
  | as the ISP made it | 10.3 / 10.2 / 10.3 (27.9 dB) | 11.1 / 11.2 / 11.2 (27.2 dB) |
  | `VPU_IIR_WORSE` {300, 30, 32, 5} | 10.1 / 10.1 / 10.1 | 11.0 / 11.1 / 11.1 |
  | {600, 150, 16, 10} | 7.3 / 7.1 / 7.3 | 8.1 / 8.1 / 8.2 |
  | {1000, 600, 16, 30} | 3.4 / 3.1 / 4.1 | 3.7 / 3.5 / 4.0 |
  | **{1000, 600, 8, 60}** (echod at the ceiling) | **3.3 / 2.8 / 4.4 (37.8 dB)** | **3.3 / 3.0 / 3.6 (37.8 dB)** |
  | {1000, 1000, 0, 100} | 3.2 / 2.7 / 4.8 | 3.2 / 2.9 / 3.5 |
  | {2000, 1000, 0, 10} | 9.6 / 9.5 / 9.6 | 10.5 / 10.4 / 10.5 |

  Fire OS's own tuning barely touches noise this strong: the weights die out within a few levels, and
  most changes are past 2·BLEND_TH, where the blend puts the noisy plane back. A high BLEND_TH is what
  matters. The busy blocks' error rising past the flat ones' as BLEND_W falls toward 0 is the filter
  starting to take color detail; {1000, 600, 8, 60} is the compromise. By eye (crops, scratch only):
  the confetti of red, green and blue is gone, a gray grain is left.
- **The luma, tried** (Y as swnr's "plane" in four strips of 272 rows): rms 32.5 → 24.5 at
  {1000, 1000, 16, 30}, at 4 × 19 ms. Too slow for the stream, and noise of 32 levels is beyond an
  edge-stopping filter anyway: not used.
- **The stream** (`TestDeviceH264Rate`): at the dark room's own exposure the sensor gives 15 frames a
  second, with and without the smoothing (every picture smoothed, 187 over the run). With the exposure capped (`HW_MAXSHUT=400`: 60 frames a second, gain at
  the ceiling), 59.9 a second without it and **23.2 with it**: the ISP and swnr one after the other on
  each frame. The test process's CPU at 15 a second: 74% of a core with it, 46% without. Between 4x
  and 6x (30 frames a second) it held the stream to 23.8; §18.6 brings that back to 30.
- **Two users** (`TestDeviceSharedAPUNNAndSWNR`, `TestDeviceVPUWithStreamNR`): apunn and swnr in turn on
  one open device, each loaded again when the other ran last (apunn 2.8 ms, swnr well under 1 ms):
  the convolutions stay byte-exact; a look takes 6.4 ms on average with swnr running at 15 a second
  (2.5 ms alone). The person detector's net takes the data area up to 0x80800; swnr's part starts at
  0x1f0000.

#### 18.5 In echod

- `hardware/vpu`: `Acquire` is a refcounted hold on one open device, booted by the first holder and
  closed by the last; `Register` and `Lock(name)` load a holder's algorithm when another ran since,
  and boot again after a failed job (the driver powers the core down then). apunn takes the data area
  from the bottom, swnr from the top, and `reserve` refuses an overlap. `OpenAPUNN` is a holder now,
  so person detection and the stream no longer lock each other out (EBUSY sent person detection to
  the cores for good).
- `hardware/vpu.Chroma`: `NewChroma(w, h)` lays out U, V and the work buffer at the top of the data
  area (3.2 MB at 1456x1088), `Planar` writes the tables when the tuning changes and runs the three
  jobs, `NV12` does an interleaved plane in place.
- `hardware/camstream`: `SetDenoise` runs a function on each developed picture's chroma plane before
  the encoder takes it (in place: the encoder reads the ISP's buffer). Since §18.6 it runs while the ISP
  develops the next frame.
- `feature/camstream/nr.go`: on from gain 4x until it falls under 3x (hysteresis); the tuning in a
  straight line from {600, 100, 24, 15} at 4x to {1000, 600, 8, 60} at the ceiling (only the ceiling
  was measured: the room was dark); the VPU taken at the first dim frame (20-45 ms), let go 10 s after
  the last one and when the stream stops; any failure turns it off until the stream starts again, said
  once. `camera.Frame` carries the gain it was asked for.

Not done: the camera page's JPEGs (developed on the cores into RGBA, so swnr would need a round trip
through YUV; a mean of a few frames would do more for a still); CCR and HFG.

#### 18.6 The stream at full rate: a cached data area, and swnr beside the ISP (2026-10-20)

Between 4x and 6x the sensor gives 30 frames a second, and swnr held the stream to 23.8 (the gain
pinned with `HW_GAIN`, `TestDeviceH264Rate`, 10 s each; 30.0 with `HW_NR_OFF`). A picture's
swnr took 24.6 ms in the stream: 13.2 ms on the VPU, the rest the cores. Reading the two planes back
from the uncached data area was the largest part of the rest: 12.5 ms on one core (64 MB/s), about
7 ms in four bands.

**A cached view of the data area** (`patches/vpu/techo5_vpu.c`). The MT8183's VPU does not snoop
the cores' caches, so a cached mapping needs cache maintenance around each job. The device's mmap at
offset 0 stays write-combined. At `TECHO5_VPU_MMAP_CACHED` (0x10000000) the same area maps cached,
and `TECHO5_VPU_SYNC` {offset, length, flags} does the maintenance on a range of it: `FOR_DEVICE`
cleans it to memory (`dcache_clean_poc`) before a job reads it, `FOR_CPU` invalidates it
(`dcache_inval_poc`) after a job wrote it, before the cores read it. The region is no-map, so it has
no linear-map alias for `dma_sync_*`. The kernel maps the data area cached itself (`memremap` WB, a
vmap alias) and maintains the caches through that alias; they are physically tagged, so this covers
the lines the user mapping brought in. The kernel's write-combined mapping now stops at the data area,
so the kernel holds no two mappings of one range with different attributes. In echod, `Device.Cached`
and `Device.Sync`; swnr's `Planar` cleans both planes before its jobs and invalidates them after, and
`NV12` splits straight into them and joins straight from them. apunn keeps the uncached view. A
kernel without the cached view refuses the mmap, and echod falls back to the uncached path.

| `TestDeviceSWNRCost` (one band, one core) | split | to the area | VPU | back | join |
|---|---|---|---|---|---|
| uncached view (`HW_UNCACHED=1`) | 2.1 ms | 0.4 ms | 13.5 ms | **8.1 ms** | 2.1 ms |
| cached view | (split into the area) | 0.7 ms | 13.6 ms | **0.85 ms** | (joined from it) |

The cores' clocks moved between the two runs, so the split and join differ by up to 2x. The read
back is the step that changed. `NV12` (four bands): 25.3-26.0 ms a picture uncached, 18.7-19.2 ms
cached (`TestDeviceSWNRCachedSame`).

**The output is unchanged.** `TestDeviceSWNRCachedSame` runs 4 noisy pictures at 4 tunings (Fire OS's
strongest to echod's at the ceiling) through the cached view, then the uncached view, then the cached
view again: all 16 are byte for byte the same each way. A synthetic NV12 run through `TestDeviceSWNRFile`
at the same 4 tunings gives the same bytes on this kernel as on hw13 with the code before the change.

**Not enough alone.** With the cached view the stream made 25.5 a second, with swnr at 21.4 ms a
picture. The ISP's development and the rest of the frame take about 17.8 ms, which leaves 15.5 ms of
the 33.3, and the VPU alone takes 13.5 ms.

**swnr beside the ISP** (`hardware/camstream`). The ISP has three pictures now. `Encode` develops frame
N while swnr runs on frame N-1 in the background and the encoder encodes N-2. A frame then costs the
longest of the three, not the ISP and swnr added together. `SetDenoise` takes a `start` called as
each picture is developed, after the work on the one before has ended. It returns that picture's
work, or nil for none. `streamNR.start` takes the frame's gain there, so the work running in the
background never sees the next frame's. The stream is a frame later from the first dim frame until it
stops. A stream that never dims is not delayed.

| `TestDeviceH264Rate`, 10 s | before | the cached view | and swnr beside the ISP |
|---|---|---|---|
| gain 4x, swnr on | 23.8 / s (24.6 ms) | 25.6 / s (21.3 ms) | **30.0 / s** (22.4 ms) |
| gain 6x, swnr on | 23.8 / s (24.5 ms) | 25.4 / s (21.5 ms) | **30.0 / s** (22.0 ms) |
| gain 15.5x at 30 a second | | | 29.9 / s (22.1 ms) |
| `HW_MAXSHUT=400` (60 a second) | 23.2 / s | | 49.6 / s (19.2 ms) |
| swnr off (`HW_NR_OFF`) | 30.0 / s | 29.9 / s | 30.0 / s |

At 6x, over 20 s, the test process took 116% of a core with swnr and 84% without.

**Person detection** shares the VPU and is unchanged: `TestDeviceVPU` (6 pictures byte-exact),
`TestDeviceDetectorLooks` (60 looks on the VPU, 0.29% of a core), `TestDeviceVPUWithStreamNR` (24 looks
byte-exact beside 53 pictures smoothed, 6.5 ms a look) and `TestDeviceSharedAPUNNAndSWNR` all pass on the
kernel and echod shipped in rootfs v1.2.19-mainline.36.
