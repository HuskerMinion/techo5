# MCDI: deeper CPU idle on mainline (offline reverse engineering)

Status: offline analysis only. Nothing has run on a unit yet. Date: 2026-10-18.

## Verdict

**It is not impossible, and the AP side of the protocol is small.** On Amazon's split, the Fire OS
kernel (not BL31) owns several SSPM mailbox words. The one that matters most is the MCDI
"available CPU" mask. Mainline never writes it, which fits the wedge: the core parked in WFI with
its GIC interface off, and the SSPM never woke it.

Confidence:

- **AP-side protocol: high.** It was decompiled from the Fire OS kernel's `mtk_mcdi.c`, and it
  matches upstream TF-A's `sspm_reg.h` for MT8183 word for word.
- **That the missing avail mask is *the* cause of the wedge: medium.** The SSPM firmware's main
  body is encrypted, so its side is inferred, not read.
- **That the SSPM's MCDI task is running at all on mainline: unknown.** Phase 1 below settles it
  in a few milliseconds, safely.

The note `research/tfa-devapc-psci.md` that the DTS comment and README cite does not exist in the
tree. The BL31 facts below come from the decrypted BL31 in the session scratchpad
(`tfa/dec/atf_dram.ann.S`).

## Sources

| What | Where | Notes |
|---|---|---|
| Fire OS kernel | `scratchpad/vpu/boot/Image` | Linux 4.4.146, **32-bit ARMv7** (FOS6, build Sep 2026), `mcdi_v1/mtk_mcdi.c` + `mtk_mcdi_governor.c` built in. kallsyms recovered with `kallsyms-finder` (vmlinux-to-elf). Disassembled with capstone (`scratchpad/mcdi/dis.py`). |
| Fire OS DT | `scratchpad/vpu/boot/fos3.dts` (athena-proto), fos6 (athena-evt) | idle states, `mediatek,sspm`, `mediatek,mt8183-mcdi` |
| Amazon BL31 | `scratchpad/tfa/dec/atf_dram.bin` (runs at 0x54600000) | old MediaTek ATF (`plat_affinst_suspend`, `plat/mediatek/mt8183/plat_pm.c`) |
| SSPM firmware | `p14-sspm_1.img` | `tinysys-sspm`, built Dec 12 2018. Section 1 (0x200): log strings. Section 2 (0x16d4, 0xc854 bytes): plain code. Section 3 (0xdf40, 0x6b430 bytes, type 0x63): **encrypted** (entropy 7.99 bit/byte). The MCDI controller is not readable. |
| Upstream reference | TF-A `plat/mediatek/mt8183/drivers/mcdi/mtk_mcdi.c`, `include/sspm_reg.h`, `plat_pm.c` | the Chromebook (kukui) BL31, which drives the same mailbox |

## Hardware interface (all NS-accessible: Fire OS writes them from EL1)

### SSPM mailbox 3 = MCDI mailbox, `0x10480000`, 32-bit word per slot

There is plain shared SRAM, polled by both sides. There is no doorbell and no IPI: the
`mbox3_ctrl` at 0x10481000 is never touched for MCDI. In the Fire OS kernel,
`sspm_mbox_write(3, slot, &v, 1)` is `memcpy_to_sspm(0x10480000 + 4*slot)`.

| slot | addr | name (TF-A `sspm_reg.h`) | who writes on Amazon | meaning (as used) |
|---:|---|---|---|---|
| 0 | 0x10480000 | CLUSTER_0_CAN_POWER_OFF | **kernel** (`mcdi_cluster_off`) | 1 = the LITTLE cluster may be powered off; written just before PSCI 0x01010001 |
| 1 | 0x10480004 | CLUSTER_1_CAN_POWER_OFF | **kernel** | same for the big cluster |
| 2 | 0x10480008 | BUCK_POWER_OFF_MASK | **kernel**, boot: 0 | rails the SSPM may turn off; 0 = none |
| 3 | 0x1048000c | CLUSTER_0_ATF_ACTION_DONE | BL31 | 1 on a cluster-level suspend (`0x54603d9c`), 0 on resume (`0x54603dbc`) |
| 4 | 0x10480010 | CLUSTER_1_ATF_ACTION_DONE | BL31 | same |
| 5 | 0x10480014 | BOOTADDR | BL31 (`0x54603d88`, once) | BL31 warm entry for SSPM-powered-on cores |
| 6 | 0x10480018 | PAUSE_ACTION | **kernel** (`mcdi_task_pause`) | **1 = pause, 0 = run** (value, not upstream's BIT(1)) |
| 7 | 0x1048001c | AVAIL_CPU_MASK | **kernel** (`update_avail_cpu_mask_to_mcdi_controller`) | bit n = CPU n online/managed. **Mainline: nobody writes it.** |
| 8 | 0x10480020 | CPU_CLUSTER_PWR_STAT | SSPM | bits 0-7 = CPU on, bit 16/17 = cluster 0/1 on (kernel compares it with `avail | avail_cluster<<16`) |
| 9 | 0x10480024 | ACTION_STAT | SSPM | not read by Fire OS |
| 10 | 0x10480028 | CLUSTER_0_CNT | SSPM | cluster-off counter (heart-beat log reads it) |
| 11 | 0x1048002c | CLUSTER_1_CNT | SSPM | same |
| 12 | 0x10480030 | CPU_ISOLATION_MASK | **kernel**, boot: 0 | bit n = CPU n excluded (0xff is never written) |
| 13 | 0x10480034 | PAUSE_ACK | SSPM | follows slot 6: 1 when paused, 0 when running |
| 14 | 0x10480038 | PENDING_ON_EVENT | SSPM | nonzero = a CPU power-on is pending (`is_cpu_pwr_on_event_pending`) |
| 15 | 0x1048003c | PROF_CMD | - | unused by Fire OS |
| 16 | 0x10480040 | DRCC_CALI_DONE | BL31 (`0x54603df8`) | set to 1 on big-cluster resume |
| 17/18 | 0x10480044/48 | HP_CMD / HP_ACK | nobody on Amazon | upstream TF-A hotplugs through the SSPM. Amazon's BL31 does not (it has only the 6 SSPM accesses listed here). |

### SSPM config registers, `0x10440000`

| addr | name | writer |
|---|---|---|
| 0x104400d8 | ACAO_INT_SET: bit n+8 = STANDBYWFI irq for CPU n | BL31 on every state-id-1 suspend (`0x54603dd8`) |
| 0x10440204 | ACAO_WAKEUP_EN: bit n | BL31 only via SiP SMC `0x82000263`/`0xC2000263` with x1=0, x2=n. **The Fire OS kernel never issues it** (no 0x263 SMC id in the image). |

### MCDI SYSRAM, `0x0011B000`, 0x800 bytes (`mediatek,mt8183-mcdi`)

Fire OS only `memset(base+8, 0, 0x7f8)` at `subsys_initcall` (`mcdi_sysram_init`). It is otherwise a
profiling/footprint area (LK dumps it as `SYS_MCDI_DATA`). Nothing in it is configuration the SSPM
needs from the AP.

### SPM, for observing only

`0x10006180` PWR_STATUS: CPU power bits {9,10,11,12,16,17,18,19} for CPU0-7 (TF-A
`clst_single_pwr`). CPU7 is bit 19.

## What each side does

### BL31 (Amazon), PSCI CPU_SUSPEND with state-id 1 (`plat_affinst_suspend`, 0x54606554)

1. Saves the GIC redistributor and disables the CPU interface (`mt_gic_cpuif_disable`). From here
   no IRQ reaches the core; only a power cycle by the SSPM brings it back.
2. Enables SPMC auto-off for this CPU (`0x5460513c`) and sets the MCUCFG boot address.
3. Writes slot 5 = warm entry, once.
4. For state-id 1: ACAO_INT_SET = BIT(8+cpu). At cluster level (0x01010001, BL31 is the last CPU
   of the cluster): slot 3+cluster = 1, then disables CCI/SCU.
5. On resume (0x546063d4): re-enables the GIC, slot 3+cluster = 0, and slot 16 = 1 for cluster 1.

Amazon's BL31 never touches slots 6, 7, 12, 17 or 18. Upstream TF-A does: `mcdi_init()` writes
`avail = 0x01`, `on_finish` sets the CPU's bit, `off` clears it, and `check_mcdi_ctl_stat()` probes
the PAUSE and HP handshakes at boot and disables MCDI if the SSPM does not answer. That is why
mainline cpuidle works on kukui with no AP-side MCDI code, and why it cannot work here without
some.

### Fire OS kernel (AP side)

**Boot**

1. `subsys_initcall mcdi_sysram_init`: of_iomap `mediatek,mt8183-mcdi` (0x11B000), zero +8..+0x800.
2. `late_initcall mcdi_init`:
   - `register_cpu_notifier` ×2 (below).
   - `mcdi_governor_init` → `mcdi_avail_cpu_cluster_update` → **slot 7 = cpu_online_mask** (0xff
     with all 8 up).
   - `mcdi_status_init` → enable; `set_mcdi_buck_off_mask(0)` → **slot 2 = 0**.
   - **slot 12 = 0**.
   - It sends no IPI and does no pause/unpause. Slot 6 is assumed 0.
3. The governor returns "WFI only" for the first 10 s of uptime, then prints
   `MCDI bootup check: PASS`. That is a delay, not a handshake.

**CPU hotplug**

- `mcdi_cpu_callback` (UP_PREPARE/DOWN_PREPARE): stops the AP governor and wakes all CPUs.
- `mcdi_cpu_callback_leave_hotplug` (ONLINE/DEAD/FAILED): **slot 7 = new online mask**, then
  resumes the governor.

**Idle entry** (`mcdi_enter(cpu)`, the cpuidle `enter` of the "mcdi" state of `mt67xx_acao_v3`)

The governor (`mcdi_governor_select`) picks a state:

- **1: CPU off.** `mcdi_cpu_off`: `mtk_enter_idle_state(2)` = `cpu_pm_enter` +
  `arm_cpuidle_suspend(2)` = PSCI CPU_SUSPEND **0x00010001** + `cpu_pm_exit`. No mailbox write.
  For residency results 2..5 it uses index 3 (0x01010001) without slot 0/1.
- **2: cluster off** (this CPU is the last one up in its cluster; slot 8 is checked by
  `other_cpu_off_but_cluster_on` / `mcdi_cpu_cluster_on_off_stat_check`). `mcdi_cluster_off`:
  **slot (cpu<4 ? 0 : 1) = 1**, then PSCI **0x01010001**. Nothing in the kernel writes it back
  to 0. Presumably the SSPM consumes it (unverified).
- **3/4/5: SODI/dpidle/SODI3** (SPM firmware paths). They pause the SSPM MCDI task: slot 6 = 1,
  spin on slot 13 == 1. Afterwards slot 6 = 0, spin on slot 13 == 0.

`mtk_enter_idle_state` re-pends some wake IRQs after resume only for index > 3 (SODI/dpidle). That
is irrelevant to MCDI.

**Debug.** `mcdi_heart_beat_log_dump` prints slots 10/11 as `mcdi_cnt_cluster`.

### SSPM (inferred, encrypted)

On a STANDBYWFI irq from a CPU that is available and not isolated, and while the task is not
paused, the SSPM lets the SPMC power the CPU off. It then watches that CPU's GIC wake request and
powers the CPU on into slot 5's address. At cluster level it also needs slot 0/1 = 1 plus slot
3/4 = 1. It reports power state in slot 8 and counts cluster-offs in slots 10/11.

## Why mainline wedged (2026-10-17)

Mainline used the stock params on every CPU, both CPU-level and cluster-level, with nothing on the
AP side:

- **Slot 7 (avail) was never written.** It was most likely 0: LK and BL31 never write it, and
  upstream BL31 initializes it itself.
- Slots 2 and 12 were never written either.
- The CAN_POWER_OFF slots were never set for cluster-level requests.

A core whose bit is not in the avail mask is, for the SSPM, an offline CPU: Fire OS clears the
bit only after hotplug-off. So the SSPM most plausibly ignored it, or powered it off and never
powered it back on. Either way the core sat with its GIC interface disabled, and nothing could
wake it.

The other possibility is that the SSPM's MCDI task is not running or not answering under mainline.
Phase 1 below tests that directly.

## What a mainline driver must do

This is a "MediaTek MCDI AP" shim, about 100 lines. No IPI, no SSPM driver and no SPM firmware are
needed.

**At boot (before cpuidle can pick a deep state on any CPU)**

1. `ioremap(0x10480000, 0x100)`. Optionally zero 0x0011B008..0x0011B7FF as Fire OS does.
2. **Liveness check**, as upstream TF-A's `check_mcdi_ctl_stat`:
   - write slot 6 = 1, poll slot 13 == 1 for ≤ 5 ms;
   - write slot 6 = 0, poll slot 13 == 0 for ≤ 5 ms.

   If either times out, do not enable deep idle (e.g. skip registering, or set every state's
   `disable`).
3. slot 2 = 0 (no buck off), slot 12 = 0 (no isolation), **slot 7 = cpu_online_mask**.

**CPU hotplug.** A cpuhp state: on online (after the CPU is up), set its bit in slot 7. On
teardown/dead, clear its bit. Optionally pause around the transition as Fire OS's governor stop
does.

**Per idle entry**

- CPU-level: nothing; plain PSCI 0x00010001 (mainline `cpuidle-psci` as is).
- Cluster-level (later): write slot `cluster` = 1 right before PSCI 0x01010001. Mainline
  `cpuidle-psci` has no hook for this. It needs a small `enter` wrapper, or a DT state used only
  with OSI/PC where the kernel knows it is the last CPU. Also keep `local-timer-stop` and the
  `systimer` broadcast (CONFIG_MTK_TIMER=y, node enabled from `mt8183.dtsi`).

Leave buck-off, SODI, dpidle and suspend alone: they need the SPM firmware (`p13-spmfw`) and the
SSPM sleep IPIs.

## Concrete safe first experiment (one big core, CPU-level only)

**Prerequisites**

- Lockup guards as built today: softlockup/hardlockup panic, `panic_on_rcu_stall=1`, `panic=5`,
  MEDIATEK_WATCHDOG.
- `/dev/mem` with `STRICT_DEVMEM=y` and `IO_STRICT_DEVMEM` unset: MMIO not claimed by a driver is
  readable. Nothing in mainline claims 0x10480000.
- A `devmem` (busybox) or Python with `mmap`.

### Phase 0: read-only

Use the current image, `cpuidle.off=1`.

```sh
for s in $(seq 0 18); do printf '%2d %s\n' $s $(devmem $((0x10480000 + 4*s)) 32); done
devmem 0x10006180 32        # SPM PWR_STATUS
```

What to look for:

- Slot 7: expect 0 (the smoking gun).
- Slot 8: if the SSPM tracks power, about 0x000300ff with all cores on.
- Slots 6/13: expect 0/0.
- Slot 12: expect 0.
- Read it twice and note anything that changes.

### Phase 1: is the SSPM MCDI task alive?

Still `cpuidle.off=1`. This handshake has no idle-path risk: no core is in MCDI.

```sh
devmem 0x10480018 32 1; sleep 0.01; devmem 0x10480034 32   # expect 1
devmem 0x10480018 32 0; sleep 0.01; devmem 0x10480034 32   # expect 0
```

If slot 13 never follows slot 6, the SSPM's MCDI controller is not running on mainline. Stop:
deep idle is then impossible without whatever Fire OS-side SSPM bring-up is missing. Leave slot 6
at 0.

### Phase 2: program it like Fire OS

```sh
devmem 0x10480008 32 0; devmem 0x10480030 32 0
```

### Phase 3: one big core

Make a DTS change:

- Every CPU but cpu7 (`cpu@103`) gets `/delete-property/ cpu-idle-states;`.
- cpu7 gets `cpu-idle-states = <&CPU_SLEEP>;` (0x00010001 only, never a CLUSTER_SLEEP state).

On the command line, drop `cpuidle.off=1` and add `maxcpus=7` so that cpu7 stays offline at boot.

Then:

```sh
echo 1 > /sys/devices/system/cpu/cpu7/cpuidle/state1/disable   # if the dir exists while offline;
                                                                # else hold /dev/cpu_dma_latency = 0
devmem 0x10480008 32 0; devmem 0x10480030 32 0
echo 1 > /sys/devices/system/cpu/cpu7/online
devmem 0x1048001c 32 0xff                                       # avail = online mask
echo 0 > /sys/devices/system/cpu/cpu7/cpuidle/state1/disable   # (or close the latency fd)
```

Then watch:

- `/sys/devices/system/cpu/cpu7/cpuidle/state1/{usage,time,rejected}` rising.
- Slot 8 bit 7 and PWR_STATUS bit 19 toggling.
- Slots 10/11 unchanged (no cluster-off).
- A `taskset -c 7` periodic job (e.g. `sleep 0.01` loop) still being scheduled.

Kill switch: write 1 to `state1/disable`.

If cpu7 never returns, the RCU stall (about 21 s) or the buddy hard-lockup detector panics and
`panic=5` reboots. The other 7 cores keep the unit usable until then. A panic that cannot stop cpu7
still reboots via PSCI SYSTEM_RESET, and the watchdog backs that up.

### Phase 4 (only after phase 3 runs for hours)

1. All big cores at CPU level.
2. Then the LITTLE ones.
3. Then a driver doing the boot/hotplug writes in-kernel instead of `maxcpus`.
4. Last, cluster-level with the CAN_POWER_OFF write, which needs the `enter` wrapper.

## Open questions

- SSPM-side semantics are inferred (encrypted body). In particular: whether CAN_POWER_OFF (slot 0/1)
  is cleared by the SSPM, and whether ACAO_WAKEUP_EN (0x10440204, SiP 0x263) must be set by someone.
  Fire OS never calls that SiP, so presumably not.
- Amazon's PAUSE uses values 1/0, unlike upstream's BIT(1). Use 1/0.

## Results on the unit, 2026-10-18

- Phase 0: slot 7 (AVAIL_CPU_MASK) read 0, as predicted; slot 8 0; slot 9 2; slot 16 1.
- Phase 1: the SSPM's MCDI task answers: PAUSE_ACK (13) follows PAUSE (6) both ways within 10 ms.
- `patches/mcdi.py` (`athena-mcdi.c`) does the AP's side at boot: the handshake (else
  `disable_cpuidle()`), slots 2 and 12 zero, slot 7 kept to the CPUs being up from each CPU's
  hotplug prepare step (0xff once all are up). A build with `MCDI_CPUS=7` gives every core
  CPU_SLEEP (the PSCI cpuidle driver fails its probe if any core has none) and holds the others in
  WFI with a resume latency of 0.
- With the mask written, CPU-level idle no longer wedges anything: cpu7 entered `cpu-sleep` some
  1400 times in its first minutes (13 s in it), woke for its timers, and the unit stayed healthy.
- But nothing is powered down: SPM PWR_STATUS bit 19 (cpu7) was set in all 300 samples, and the
  SSPM never reports a CPU state in slot 8. The state is a PSCI suspend that parks the GIC interface
  and comes back, no cheaper than WFI. The power-down is the SPM's work (its PCM firmware,
  `p13-spmfw`, which Fire OS's kernel loads and mainline does not); with the SSPM only told which
  CPUs it may manage, there is nothing to sequence it. So builds keep `cpuidle.off=1` (no saving to
  take, and no new risk), and the driver only keeps the mask as Fire OS does. Next step for real
  savings: load and drive the SPM firmware, a project of its own.

## SPM firmware and the missing power-down (offline, 2026-10-18)

Question: does the AP kernel have to load the SPM's PCM firmware (`p13-spmfw`) before MCDI can
power a core off, as the previous section supposed?

### Verdict

**No: on athena the AP kernel does not load the SPM firmware, and the CPU-level MCDI path does not
use it.** LK loads `spmfw` and BL31 parses it. The Fire OS kernel only names the DRAM type and, for
Vcore DVFS, asks BL31 to start the PCM. Neither Fire OS's `mcdi_cpu_off` nor BL31's state-id-1
suspend touches the SPM's PCM. A core's power switch is the SPMC (the per-core power controller,
whose SPM-side registers are `MPx_CPUy_PWR_CON`), and the SSPM drives it.

**Second finding: "never power-gated" is not established, and Amazon's BL31 argues against it.**
For a power-down CPU_SUSPEND, BL31 runs TF-A's `psci_power_down_wfi`. If the WFI ever returns,
that function parks the core in an endless WFI loop. A core can come back to Linux only by a warm
reset into BL31's entry point. So every `cpu-sleep` entry that Linux counted as `usage` (not
`rejected`, see below) was a core that the SSPM/SPMC reset. That is consistent with the wedge
before the avail mask: the SSPM ignored the core, and the core sat in that WFI loop. The 300
PWR_STATUS samples that showed bit 19 set may be biased: an unpinned sampler can run on cpu7
itself. Stage 1 below settles this before anything new is written.

Confidence:

- LK + BL31 load and parse the firmware, and the kernel does not: **high** (strings and call
  graph below).
- The CPU-level MCDI path does not use the PCM: **high** for the AP and BL31 code, **medium** for
  the encrypted SSPM firmware.
- Cores are already being reset on each counted entry: **medium-high**. This depends on the
  cpuidle counter that was read, and on BL31 having no platform `pwr_domain_pwr_down_wfi` hook.
  The hook would come through the plat-compat layer, which has none; ops+48 is null-checked at
  0x107dec, but the ops table was not found in the image.
- Whether the PCM must be *running* (Vcore DVFS mode) for the SSPM to gate a core: **unknown,
  probably not**. Stage 2 tests it, and only if stage 1 shows no gating.

### Evidence

**Partitions on hand** (`backups/<unit>/partitions/`, SHA256SUMS alongside):
`p13-spmfw.img` (1 MiB, 0x9007 bytes used) and `p14-sspm_1.img`; also `p18-lk.img`, `p19-lk2.img`,
`p3-tee1.img`, `p4-tee2.img`, `p20-boot.img` and the rest. `custom-kernel/athena/out/` holds no
partition dumps. Scratch: `tfa/dec/atf.bin` (BL31 SRAM part at 0x100000, TF-A `lib/psci`) and
`atf_dram.bin` (MediaTek platform code at 0x54600000), `vpu/boot/Image` (Fire OS kernel),
`mcdi/ksyms.txt`, and `spmref/` (fetched today: upstream TF-A `plat/mediatek/mt8183` spm, spmc
and mcdi sources, coreboot `soc/mediatek/mt8183/spm.c` and `soc/spm.h`, and coreboot blobs'
`pcm_allinone_lp4_{3200,3733}.bin`, `sspm.bin` and `license.txt`).

**`p13-spmfw`** is a MediaTek partition with header `spmfw`, holding three `2MPS` sub-images:
`pcm_allinone_lp3_1866.bin`, `pcm_allinone_lp4_3200.bin` and `pcm_allinone_lp4_3733.bin`, all
`pcm_allinone_v1.17.2_20180829`. **Amazon's lp4_3200 is byte-identical to coreboot's
`pcm_allinone_lp4_3200.bin`** (found at partition offset 0x318e). Its lp4_3733 has the same version
string but different bytes. The blobs license allows redistribution for MediaTek-chipset devices,
outside the kernel tree, and forbids reverse engineering. Coreboot's `sspm.bin` (26 KiB, entropy
6.9) is public too, but under that license it was not analyzed.

**Who loads it.**

- **LK** (`p18-lk.img` strings): `load spm`, `[spmfw] addr=0x%llx size=0x%x`, an mblock
  `SPM-reserved`, `[spmfw] before smc call` / `after smc call`. LK reads `spmfw` into reserved
  DRAM and makes the SMC.
- **BL31.** SiP 0x82000228 (`MTK_SIP_KERNEL_SPM_ARGS`, dispatched at 0x546078d4) goes to
  `spm_args` (0x54602158), a switch on x1:
  - cmd 0: store the DRAM-type index.
  - cmd 1: `spm_config_spmfw_base(x2, x3)` (0x54601fe8, "use spmfw partition", "spmfw address is
    illegal"), then `spm_load_pcm_firmware` (0x546013f4). That function only parses the three
    images in place (descriptor pointers into the DRAM copy) and sets the ready flag at
    0x5460e750 once all three parse. It does not touch the PCM.
  - cmds 2-8: suspend, SODI and dpidle enter/leave.

  SiP 0x8200021b returns that ready flag (0x54601568).
- **Fire OS kernel** (`spm_module_init`, c14387c0): `mt_secure_call(0x82000228, 0, __spmfw_idx, 0)`,
  which is cmd 0 only. `spm_load_firmware_status` = SMC 0x8200021b. `spm_dvfsfw_init` and
  `spm_go_to_vcorefs` use SMC 0x82000220. In BL31 that handler (0x546039a8) programs DVFS
  registers (0x100066b0/b4, 0x10006620, 0x1000661c, 0x10006690/98, 0x10006460/70). If
  PCM_REG15 (0x1000613c) is 0, it also runs `spm_code_swapping` → reset → kick, which starts the
  PCM from the DRAM copy. That is the only place the PCM is started outside system suspend,
  SODI and dpidle.

So **the PCM IM is (re)fetched from the LK-loaded DRAM copy at every kick**. That DRAM must
survive under mainline. The DTS comment says LK adds atf/tee/sspm reservations itself; whether it
adds `SPM-reserved` too is a stage-0 probe.

**The CPU power path does not use the PCM.**

- Upstream TF-A `mtspmc.c` (`spmc_init`, `spm_poweron_cpu`, `spm_poweroff_cpu`) switches a core
  through `SPM_MPx_CPUy_PWR_CON` (0x10006208 + 4n). The SPM block is in SPMC mode
  (`BYPASS_SPMC` 0x100062b4 = 0). PCM_* registers are never touched.
- Amazon's BL31 has the same `mtspmc.c` ("SPM: enable SPMC mode" at 0x546051b8 writes
  POWERON_CONFIG_EN = 0x0b160003; "spmc: power on/off core").
- `plat_affinst_suspend` (0x54606554) makes no SPM-firmware call for state-id 1.
- Fire OS `mcdi_cpu_off` is `mtk_enter_idle_state(2)` with no SPM access.

**Correction to "What each side does".** 0x5460513c is `mcucfg_init_archstate(cluster, cpu, 1)`,
not "enable SPMC auto-off": its table at 0x5460ba00 is {0x0c53003c, 0x0c532208} = MCUCFG
MP0_MISC_CONFIG3 / MP2_CPUCFG. The SPMC write on a state-id-1 suspend is 0x54605080. It **sets**
BIT(1) `SW_NO_WAIT_Q` in MCUCFG_SPARK[cpu]: 0x0c531c30 + 4·cpu for the LITTLE cores, 0x0c532430 +
8·cpu for the big ones (cpu7: 0x0c532448). The other hook, 0x5460507c, is an empty `ret`. Nothing
in BL31 clears that bit. In upstream TF-A, setting it is `spm_disable_cpu_auto_off`. So on Amazon's
design the SPMC does not cut power by itself on WFI, and the SSPM must sequence it through
`PWR_CON` (inference, medium).

**Why `usage` means "was reset".**

- On the BL31 side (`atf.bin`): `psci_cpu_suspend_start` calls 0x10c524 for a power-down state.
  0x10c524 is `dsb; wfi; bl 0x10cadc`, and 0x10cadc is `wfi; b 0x10cadc`.
- On the Linux side: when a power-down PSCI call returns directly, arm64 `cpu_suspend()` turns
  the 0 into -EOPNOTSUPP, cpuidle-psci returns -1, and `cpuidle_enter_state` counts it as
  `rejected`. Only a resume through `cpu_resume` (a warm reset) counts as `usage`.

### Registers to read from Linux (read-only)

All of these are NS-readable: Fire OS reads PCM_IM_PTR, PCM_REG*_DATA and SPM_SW_FLAG from EL1
(`spm_vcorefs_dump_dvfs_regs`), and the earlier PWR_STATUS reads worked. With `IO_STRICT_DEVMEM`
unset, scpsys claiming 0x10006000 does not block `/dev/mem`. Use 32-bit reads only. Do not touch
PCM_IM_HOST_RW_PTR/DAT (0x10006038/3c): reading the IM needs a write.

| addr | name | what the value means |
|---|---|---|
| 0x10006000 | POWERON_CONFIG_EN | 0x0b16000x: BL31's spmc_init ran (SPM regs writable) |
| 0x100062b4 | BYPASS_SPMC | 0 = SPMC mode (expected) |
| 0x10006180 / 0x10006184 | PWR_STATUS / _2ND | CPU0-3 bits 9-12, CPU4-7 bits 16-19, cluster tops bit 8 / 15. Both clear = core off |
| 0x10006208 + 4n, 0x1000621c + 4n | MP0/MP1_CPUn_PWR_CON | bit0 RST_B, 1 ISO, 2 PWR_ON, 3 PWR_ON_2ND, 4 CLK_DIS. cpu7 = 0x10006228. PWR_ON = 0 means someone (the SSPM) asked for off |
| 0x10006204 / 0x10006218 | MP0/MP1_CPUTOP_PWR_CON | cluster switches (for later) |
| 0x10006174 | CPU_IDLE_STA | bits 10-17 = STANDBYWFI of CPU0-7 (cpu7 bit 17). WFI set together with PWR_STATUS set = parked in WFI with power on |
| 0x10006178 | PCM_FSM_STA | low 23 bits 0x108490 = PCM fresh from reset. Bits 7-9 IM_STATE = 4: IM loaded. Bit 21 PCM_KICK, bit 22 IM_KICK |
| 0x10006018 / 0x1000601c | PCM_CON0 / PCM_CON1 | PCM clock and IM mode bits (key 0x0b16 in the top half when written) |
| 0x10006020 / 0x10006024 | PCM_IM_PTR / PCM_IM_LEN | nonzero: an IM fetch was set up (DRAM address and length of the firmware) |
| 0x10006100 … 0x1000613c | PCM_REG0 … PCM_REG15_DATA | **REG15 is the PCM program counter: 0 = PCM not running** (BL31 tests exactly this at 0x54603b30). Changing between reads = running |
| 0x10006104 | PCM_REG1_DATA | 1 with REG15 = 0: firmware loaded but not running (coreboot's code-swap check) |
| 0x10006600 / 0x10006604 | SPM_SW_FLAG / SPM_SW_DEBUG | PCM flags of the last scenario, firmware debug footprint |
| 0x10006608 … 0x1000661c | SPM_SW_RSV_0..5 | 0x1000661c low half 0x2000 and 0x10006620 bit1 = BL31's Vcore DVFS init ran (0x546039a8 case) |
| 0x100066a4 | SPM_DVFS_LEVEL | current DVFS level when the DVFS firmware runs |
| 0x10006158 / 0x1000615c | SPM_IRQ_STA / SPM_WAKEUP_STA | last PCM IRQ / wake reasons |
| 0x100068c0 … 0x100068e0 | SPM_PC_TRACE_CON / G0..G7 | PCM PC trace, if the hardware trace was enabled |
| 0x10480000 + 4·slot | MCDI mailbox | slots 8 (power stat), 9, 10/11 (cluster counters), 13, 14, as before |
| 0x0011B000 … 0x0011B7FF | MCDI SYSRAM | LK dumps it as SYS_MCDI_DATA. Diff it before and after an idle run: anything the SSPM writes there is a footprint of its MCDI work |

Optional, riskier: MCUCFG 0x0c532448 (cpu7 SPMC0, BIT(1) set after the first suspend), 0x0c531c30
+ 4n, 0x0c53003c, 0x0c532208. BL31 has `sip_mcusys_write` with a range check, so MCUCFG may be
secure-write-only. A read is probably fine, but an abort from `/dev/mem` is a SIGBUS at best and
an SError panic at worst. Read these last, in their own run.

The SiP status SMC 0x8200021b (returns 1 = all three firmwares parsed) is read-only, but it needs
a few lines of kernel code (`arm_smccc_smc`). Use the SMC32 ID exactly as Fire OS does.

### Staged plan

**Stage 0: read-only state, current image, `cpuidle.off=1`.**

1. Dump the table above twice, one second apart.
2. `ls /proc/device-tree/reserved-memory/`, then `cat /proc/iomem | grep -i reserved`. Look for
   LK's `SPM-reserved` node, and check that it holds PCM_IM_PTR's address if that is nonzero.

Expected: POWERON_CONFIG_EN 0x0b160003, BYPASS_SPMC 0, cpu7 PWR_CON with PWR_ON set, PCM_REG15 = 0
(no one sent 0x82000220 on mainline).

If the SPM DRAM copy is **not** reserved, fix that before stage 2 or any system-suspend work.
Mainline could overwrite it, and BL31 would then kick garbage into the PCM. MCDI is not affected.

**Stage 1: is cpu7 really never gated? Read-only, the existing `MCDI_CPUS=7` build.**

- Give cpu7 one periodic job with a long period (`taskset -c 7` loop of `sleep 0.1`). That makes
  the expected residency about 100 ms.
- Run a sampler **pinned to cpu0**: a C or Python `mmap` loop reading 0x10006180, 0x10006184,
  0x10006228, 0x10006174 and slot 8 every 10-50 µs for 10 s, counting each distinct tuple.
- Read cpu7's `state1/usage`, `rejected`, `time`, `above` and `below` before and after. Diff the
  MCDI SYSRAM.

Outcomes:

- **A. Bit 19 / PWR_ON drops while `usage` rises:** CPU-level MCDI already works and the previous
  section's negative result was a sampling artefact. Next: measure the saving at the wall
  (adapter meter, all cores at CPU level vs `cpuidle.off=1`), then the phase-4 roll-out.
- **B. `usage` rises, PWR_STATUS never drops, CPU_IDLE_STA bit 17 is set while idle:** the core
  is reset but not powered off (an odd SSPM policy), or the counters lie. Read the MCUCFG SPMC
  registers (optional row). Then go to stage 2.
- **C. Only `rejected` rises:** BL31 returns from CPU_SUSPEND, so it has a WFI-return path after
  all. The idle state is a costly WFI. Stop and re-read the BL31 ops table.

**Stage 2 (only for B): start the PCM as Fire OS does, with nothing loaded by the AP.**

A small built-in module, `cpuidle.off=1`, at `late_initcall`:

1. SMC 0x8200021b → expect 1. If 0, stop: LK did not hand the firmware over.
2. SMC 0x82000228 (0, idx, 0). idx comes from Fire OS's `__spm_check_dram_type` logic: the DDR
   type and frequency (LP4 at 3200 or 3733, LP3 at 1866). Sending the same value Fire OS would is
   harmless.
3. SMC 0x82000220 with Fire OS's arguments from `spm_dvfsfw_init`: x1 = 0 (DVFS register init),
   then `spm_go_to_vcorefs`'s x1 = 1 (kick). Recover the x2 values from those two functions
   before use.
4. Re-read stage 0: PCM_REG15 must now move, and PCM_FSM_STA must show IM_STATE = 4.

Then repeat stage 1.

Risks:

- The PCM then runs the Vcore/DRAM DVFS firmware. It can move Vcore through the PMIC wrapper and
  DRAM through DVFSRC.
- Kukui runs this very firmware build (the byte-identical lp4_3200) from boot under mainline, so
  the state is known to be livable. But on athena BL31's DVFS init writes registers whose meaning
  was not checked here.
- A hang here happens after init, so the lockup guards and `panic=5` cover it. Have the A/B slot
  ready anyway.

Do **not** load the PCM from the AP directly (coreboot's PCM_IM_HOST_RW sequence). It is
NS-writable, but it would desynchronise BL31's dyna-load state and its later kicks.

**Stage 3: cluster level, after CPU level is proven on all cores.**

Write slot `cluster` = 1 right before PSCI 0x01010001 (an `enter` wrapper), and expect slot 3/4
(BL31), slots 10/11 (counter) and PWR_STATUS bit 8/15 to move. Unchanged from phase 4.4; nothing
in this section suggests the PCM is involved.

### Open questions (added)

- The exact semantics of `SW_NO_WAIT_Q` = 1 on Amazon's SPMC, and who, if anyone, clears it.
- Whether LK reserves `SPM-reserved` in the DT it passes to mainline (stage 0).
- The SSPM's real power-off policy (encrypted on athena; the public coreboot `sspm.bin` is under a
  no-RE license).

## Re-measured on the unit, 2026-10-18 (sampler pinned to cpu0): the cores do power off

The earlier "nothing is powered down" was the sampler's doing: an unpinned shell loop of `mmio`
reads ran on cpu7 too, which was then awake for every read. Measured again with a Go sampler pinned
to cpu0 (`/dev/mem`, a tight loop for 10 s, 4.27 million samples) while a thread pinned to cpu7 woke
every 100 ms, on the `MCDI_CPUS=7` build:

- cpu7's PWR_STATUS bit 19 was clear in 86% of the samples (3.68 M), PWR_STATUS_2ND bit 19 in all
  of them, and its MPx_CPUn_PWR_CON (0x10006228) had PWR_ON clear as often as PWR_STATUS: the core is
  powered off between its wakes, by the SPMC, with no PCM involved (PCM_REG15 = 0, PCM_FSM_STA
  0x2108490: no program running).
- The SSPM reports it: mailbox slot 8 nonzero in 86% of the samples.
- `cpu-sleep`: usage +3319 and rejected +1326 in the 10 s.
- POWERON_CONFIG_EN read 0x3 and BYPASS_SPMC 0 (SPMC mode).

So CPU-level idle with the AP's side of MCDI (`athena-mcdi.c`) is real power-gating; the SPM's PCM
firmware is not needed for it (the offline section above was right on that). Next: every core
(`MCDI_CPUS=0,...,7`), soaked under load, then the default; cluster-level later.

### All cores, soaked (2026-10-18)

`MCDI_CPUS=0,...,7`: every core in `cpu-sleep`, about 75% of all core time in it at idle (363 s of
deep sleep a minute summed over the eight cores). 15 minutes with three hardware decodes and a
40-second RTSP stream in the middle: no lockup, RCU stall or warning. The SoC's temperature
(thermal_zone0) settled at 37.2-37.4 C, against 38.4-38.7 C over the same 15 minutes on the WFI
kernel right after (same room, same load pattern without the decode and stream). It is the build's
default now (custom-kernel/athena/build.sh).

## Cluster level, and SoC-level deep idle (SODI), 2026-10-19

Question: can the SoC go deeper than MCDI while idle with the screen on (MCUSYS off, DRAM in
self-refresh, bus and infra clocks gated: MediaTek's SODI or deep idle)? And do the Show 5's limits
(`custom-kernel/cronos/research/mt8163-cpuidle.md` §9: echod woke the CPU about 2,900 times a second,
and its secure firmware restored the arch timer wrongly after a power-down) hold here?

### Verdict

**Not worth building, and nothing was shipped for it.** The SoC is seldom idle as a whole with echod
running: at random instants all seven cores other than cpu0 were powered off 1.4-9.7% of the time
(below; more soon after a boot). Those windows mostly last 0.4-6.4 ms, against a SODI round trip of
about 1.5 ms on the Show 5 plus its entry. So SODI could hold the SoC a few percent of the time. With
the panel in video mode, DRAM keeps serving the display, so the gain over the cluster power-down
already in place would be MCUSYS and some infra clocks for those few percent. On top of that, SODI
here needs a protocol mainline lacks entirely (below): 2 to 4 weeks of reverse engineering. The arch timer
question was not reached; a mainline kukui suspends through the same kind of SPM power-down with the
arch counter as its clocksource, so it is probably not the blocker here.

Two findings came out of it:

- **The cluster-level states have been in use all along.** `patches/mcdi.py` meant to give the cores
  CPU_SLEEP only, but its guard ("already patched?") looked for `patches/mcdi.py` in the DTS, and the
  DTS's own comment names the script. So the override was never appended. Every image so far has run
  mt8183.dtsi's two states on every core: CPU_SLEEP (0x00010001) and CLUSTER_SLEEP0/1 (0x01010001).
  The cluster state powers the cluster off, with no CLUSTER_n_CAN_POWER_OFF write (the mailbox's slots
  0 and 1 read 0): the cluster's PWR_STATUS bit clears and the SSPM's cluster counters (slots 10, 11)
  advance. It ran eight hours stable before this check (cpu0: 10.5 M cluster-sleep entries,
  17,700 s in it). So the 1.2 °C measured on 2026-10-18 already includes cluster power-down. The
  script and the comments now say so. The default build still gives both states (the built image is
  unchanged), and only a subset build (MCDI_CPUS=7) gives CPU_SLEEP alone, behind a marker of its own.
- **cpufreq churns, but limiting it saves nothing measurable.** schedutil changes the big cluster's
  speed 130-190 times a second, and each change costs `sugov:4` about 100 context switches (the
  voltage steps of vproc and vsram). That is most of the 16,700 arch-timer interrupts a second. With
  `rate_limit_us` 10000 (the Show 5's setting) both halve, but the SoC was no cooler (below). It was
  not shipped.

### Measurements (kernel hw11, rootfs v1.2.19-mainline.15, echod running, screen on)

Sampler `clsample` (Go, pinned with `sched_setaffinity`, `/dev/mem` `PROT_READ` of SPM 0x10006000
and the MCDI mailbox 0x10480000). It reads PWR_STATUS | PWR_STATUS_2ND: CPU bits 9-12 and 16-19,
cluster tops bits 8 and 15.

| sampler | seen | result |
| --- | --- | --- |
| busy loop on cpu0, 10 s (13 M samples) | the big cluster | top off 33.6%; cpu4-7 off 59-80% each |
| busy loop on cpu4, 10 s | the LITTLE cluster | top off 38.3%; cluster-0 counter +1,719 (170 a second) |
| `nanosleep` 2-10 ms on cpu0, 40 s (6,444 samples) | all others at a random instant | big cluster top off 19.3%; **all 7 other cores off 5.2%** |
| Go `time.Sleep` 2-10 ms on cpu0 / cpu4 | the same | all 7 others off 1.4% / 2.9% |
| `nanosleep` on cpu0 / cpu4, 7 minutes after a boot (rootfs mainline.17) | the same | all 7 others off 9.7% / 8.5% |
| busy loop on cpu0, 20 s, length of each "all 7 others off" window | | 2,656 windows (19% of the time): 54 under 100 µs, 277 at 0.1-0.4 ms, 1,444 at 0.4-1.6 ms, 859 at 1.6-6.4 ms, 22 at 6.4-26 ms |

cpu0's own idle at the same time (cpuidle, since boot): 2.7 M WFI entries averaging 38 µs (the
interrupts), and 130 k cluster-sleep entries averaging 2.1 ms.

A busy sampler keeps its own cluster up, so it sees the other cluster's best case. The sparse ones
are closer to the truth. They also count instants when cpu0 itself was idle, which SODI needs too,
so 5-10% is an upper bound on SODI's share of the time even at no entry cost.

Wake sources, per second over 10 s (`/proc/interrupts`, threads' `ctxt_switches`):

| source | rate | note |
| --- | --- | --- |
| arch_timer, all cores | 16,000 | cpu7 about 9,600 |
| `sugov:4` / `sugov:0` switches | 14,400 / 3,600 | 131 / 13 frequency changes |
| `11240000.mmc` (MT7663S Wi-Fi, SDIO) | 3,400 | all on cpu0; `mt76-sdio-txrx` 2,400 switches, `mt76-sdio-status` 750 |
| IPI function calls | 3,300 | |
| echod's `sysmon` thread (`hrtimer_nanosleep`) | 3,200 | Go's sysmon polls every 20 µs while a thread is in a blocking syscall; echod's ALSA reads and writes are blocking `ioctl`s (`lib/alsa`) |
| timer@10017000 (broadcast) | 180 | |

schedutil `rate_limit_us` A/B, both policies, ABAB, 60 s to settle then 240 s each:

| rate_limit_us | busy | arch_timer irq/s | freq changes/s | sugov switches/s | cluster-sleep (core-%) | thermal_zone0 |
| --- | --- | --- | --- | --- | --- | --- |
| 1000 (default) | 8% | 16,674 | 155 | 13,532 | 588 | 37.82 °C |
| 10000 | 8% | 8,492 | 66 | 5,288 | 564 | 37.96 °C |
| 1000 | 12% | 15,087 | 190 | 13,603 | 558 | 37.95 °C |
| 10000 | 8% | 9,630 | 99 | 6,364 | 558 | 38.21 °C |

The temperature drifted up through the run (morning), and the 10 ms windows were no cooler than
their neighbors. echod restarted once (cleanly) inside the first 10 ms window.

### What SODI would take on athena

- **BL31 has the entry.** Amazon's BL31 steers CPU_SUSPEND by state ID (`plat_affinst_suspend`,
  0x54606554). Sid 2/3 goes to SODI (0x5460250c) and sid 4 to deep idle (0x54602708), each a PCM
  path that needs the SPM firmware. LK loads that firmware and BL31 parses it (SiP 0x228;
  0x8200021b reads "ready"). SiP 0x21a/0x222 write the pwrctrl fields.
- **The Fire OS kernel does much more than the Show 5's.** Its symbols (`mtk_idle`, `spm_sodi`)
  show `spm_go_to_sodi` → `spm_sodi_pre_process`, `spm_sodi_notify_sspm_before_wfi` /
  `_after_wfi` (SSPM IPIs over `sspm_ipi_send_*`, `spm_to_sspm_command`), and
  `spm_sodi_pcm_setup_before_wfi`. It also hooks the display (`spm_sodi_set_vdo_mode`,
  `primary_display_sodi_rule_init`: this panel is in video mode), the GPU (`ged_sodi_start`,
  `mtk_gpu_sodi_entry`) and the eMMC (`idle_lock_by_ufs`, `ufs_cb_before_xxidle`), plus a
  clock-gate condition check. Mainline has no SSPM IPI driver, and the SSPM firmware's main body is
  encrypted, so the IPI protocol would have to be read out of the Fire OS kernel alone.
- **Conditions.** Fire OS's idle conditions keep SODI out while the MSDC clocks run. MSDC1, the
  Wi-Fi SDIO host, interrupts 3,400 times a second at idle, so under Fire OS's own rules SODI would
  rarely apply with Wi-Fi up either.

### What would move idle

Not SODI but fewer wakeups. The likely ones, none tried here:

1. The Wi-Fi SDIO driver's interrupt and worker rate at idle.
2. echod's blocking ALSA `ioctl`s, which keep Go's sysmon polling. Non-blocking PCM descriptors
   waited on through the runtime poller would let sysmon back off to 10 ms.
3. A cpufreq table or governor that changes the big cluster's voltage less often. The rate limit
   alone did not show a saving.

The cluster-level state already powers each cluster off for about a third of the time.
