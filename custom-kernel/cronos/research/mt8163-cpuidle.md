# Deeper CPU idle on the 2nd gen Echo Show 5 (cronos, MT8163)

Status: sections 1-6 are the offline analysis (2026-10-18). Stage A (hotplug) runs on the units
(section 8). Stage B, SODI, is built and works on a unit, but stays off: section 9 (2026-10-19).

## Verdict

**The Show 8 approach (MCDI: CPU_SUSPEND power-down, a firmware powers the core off and back on)
does not carry over.** The MT8163 has no SSPM and no hardware SPMC. Its CPU power domains are
switched in software, through the SPM's PWR_CON registers. Only two things ever do that switching:

- the AP's kernel, for CPU hotplug, and
- the SPM's PCM firmware, which the AP's kernel loads itself, for SODI, deep idle and suspend.

The firmware never does it on its own. Amazon and MediaTek shipped **no MCDI firmware for
MT8163**: `IDLE_HAVE_MCDI` is 0 and the "mcidle" switch is off in the Fire OS/Lineage kernel. Under
Fire OS a busy-idle core stays in WFI. Idle cores are powered off by **hotplug**: the HPS governor
takes them offline, and Lineage's own bench dump shows only 2 of the 4 CPUs online.

So the realistic route to "idle cores are powered down" on mainline is the Fire OS one:

1. **Stage A (recommended, small): CPU hotplug with a real power-off.** Give the `mt-boot`
   enable method `cpu_can_disable`, `cpu_disable`, `cpu_die` and `cpu_kill`. `cpu_die` is PSCI
   CPU_OFF. `cpu_kill` waits for the core's STANDBYWFI, then runs Amazon's MTCMOS power-down sequence
   from the AP. Then add a load-based governor that takes CPUs 1-3 offline when idle. Today mainline
   cannot take a cronos core offline at all: `mt-boot` has no `cpu_die`, so `arch_cpu_is_hotpluggable()`
   is false.
2. **Stage B (large, optional): SODI** (CPU0 and the cluster off with the screen on). This needs
   the AP to load and kick the SPM PCM "sodi" firmware (GPL arrays in the 4.9 source), mask and clone
   IRQs into CIRQ, and call PSCI CPU_SUSPEND 0x02010000. It only applies when CPU0 is the only CPU
   online, so it builds on stage A.

**Do not** enable PSCI cpuidle with a power-down state (0x00010000 / 0x02010000) on its own. On
this firmware nothing would power the core off, and nothing would wake it: the same wedge as on
athena, but with no mailbox to fix it.

Confidence:

- **That the MT8163 has no firmware-driven per-core power-off (no MCDI): high.** The Fire OS/Lineage
  4.9 source compiles it out, in both branches checked. The kernel itself powers cores on for CPU_ON
  and off after CPU_OFF. The SODI and deep-idle firmware are AP-loaded arrays.
- **What Amazon's BL31 does in CPU_SUSPEND: not read, inferred (medium).** No cronos `tee1`
  image is on this machine, and upstream TF-A has no mt8163 platform. The inference is that the
  BL31 records entry points and flushes and parks in WFI, and the SPM PCM or the AP cuts the power.
  It rests on the kernel doing the power switching for CPU_ON/CPU_OFF itself.
- **That stage A works with this BL31: high.** Fire OS and Lineage 4.9 do exactly this (PSCI
  CPU_OFF, then the AP's MTCMOS power-down) all day, on the same tee1 that boots mainline.

## Sources

| What | Where | Notes |
|---|---|---|
| Mainline platform DT | `/src/linux-7.2.11/arch/arm64/boot/dts/mediatek/mt8163.dtsi` (via `./k6.sh`) | `psci { compatible = "arm,psci-0.2" }`, CPUs `enable-method = "psci"`, **no `idle-states`, no `cpu-idle-states`** |
| Our board DT | `mt8163-amazon-cronos-bootfix.dtsi` (`config/cronos-boot-fix.dtsi`), `mt8163-amazon-common.dtsi` | overrides PSCI to **0.1** (`"arm,psci"`, `cpu_suspend = 0x84000001`, `cpu_off = 0x84000002`, `cpu_on = 0x84000003`, `affinity_info = 0x84000004`), CPUs `enable-method = "mt-boot"`; `secmon@43000000` 0x30000 no-map (BL31) |
| Our boot method | `patches/mt8163-boot.c` | `mt8163_boot_ops`: `cpu_init`, `cpu_prepare`, `cpu_boot` only (PSCI CPU_ON plus SPM MTCMOS power-on). No `cpu_die`/`cpu_kill`/`cpu_can_disable` |
| Running config | `config/cronos.config` → `out/.config` in the build container | `# CONFIG_CPU_IDLE is not set`, `CONFIG_CPU_PM=y`, `CONFIG_ARM_PSCI_FW=y`, `CONFIG_HOTPLUG_CPU=y`, lockup panics, `PANIC_TIMEOUT=5` |
| Fire OS / Lineage 4.9 kernel | `github.com/amazon-oss/android_kernel_amazon_mt8163`, branches `cronos/lineage-18.1` and `3.18/cm-14.1` (sparse clones in the session scratchpad `ak/k`, `ak/k318`) | `drivers/misc/mediatek/base/power/mt8163/{mt_psci.c,mtcmos.c,mtcmos.h,mt_idle.c,mt_cpuidle.c,mt_spm*.c}`, `drivers/cpuidle/cpuidle-mt81xx_v2.c`, `base/power/hps_v1/` |
| Fire OS DT (stock) | `sources/amazon-kernel/arch/arm64/boot/dts/mediatek/mt8163.dtsi`; the stock cronos dtbs dumped in an earlier session (`cronos-stock/dt*.dts`) | `idle-states { entry-method = "arm,psci"; CPU_SLEEP_0_0 psci-suspend-param 0x0010000; CLUSTER_SLEEP_0 0x2010000 }`, `enable-method = "mt-boot"`, `atf-reserved-memory` 0x43000000 + 0x30000 |
| Lineage kernel configs | `docs/dumps/cronos-lineage-kernel-4.9.337.config`, `cronos-twrp-kernel-4.9.77.config` | `CONFIG_CPU_IDLE=y`, `CONFIG_CPU_IDLE_GOV_MTK=y`, `# CONFIG_ARM_CPUIDLE is not set`, `CONFIG_MTK_PSCI=y` |
| Lineage bench | `docs/dumps/cronos-lineage-18.1-bench.txt` | `processor : 0`, `processor : 1` only (cpu2/3 offline), `[hps_main]` kernel thread running |
| amonet (cronos) | `github.com/R0rt1z2/amonet` branch `mt8163-cronos` | `makedist.sh` ships `bin/tz.img`, which is not in git. That is the tee1 image (BL31 + TEE) and was **not fetched**. It is the place to read the BL31 from |
| Upstream TF-A | search of trustedfirmware.org | mt8173, mt8183, mt8186, mt8195 and others. No mt8163 platform was found |

Not available here: the cronos `tee1`/`tee2`/`lk` partitions. `backups/` only holds the athena set
(G091GG…), and for cronos (G091MK…) only `vendor-backup.tar` and `api.psk`. The cronos
`/vendor/firmware` has no SPM file (`md32_*.bin` and Wi-Fi only). On the MT8163 the SPM firmware
lives inside the kernel, not in a partition.

## 1. What mainline and our DT declare, and what runs today

- `mt8163.dtsi`: PSCI 0.2, `enable-method = "psci"`, one cluster (`cpu-map/cluster0/core0..3`),
  arch timer with `arm,no-tick-in-suspend`, a GPT (`mediatek,mt6577-timer` at 0x10008000) usable
  as a broadcast clockevent, CCI-400 at 0x10390000, GIC-400 at 0x10221000. **No idle states.**
- `cronos-bootfix.dtsi` turns it into Amazon's interface: PSCI **0.1** with explicit function IDs,
  and `enable-method = "mt-boot"`. The BL31's CPU_ON only records the entry point, so the kernel
  powers the core's MTCMOS itself (`mt8163-boot.c`, after Amazon's `mt_psci.c`/`mtcmos.c`).
- Mainline's `psci_0_1_init()` reads `cpu_suspend`, `cpu_off`, `cpu_on` and `migrate`, but **not
  `affinity_info`**. So `psci_ops.affinity_info` is NULL, and `cpu_psci_cpu_kill()` would return 0 at
  once without polling. Stage A must wait on the SPM's STANDBYWFI bit instead.
- `cpuidle-psci.c` (`psci_idle_init_cpu`) refuses any CPU whose `enable-method` is not `"psci"`.
  With `mt-boot`, `CONFIG_ARM_PSCI_CPUIDLE` would register nothing, even with idle states in the
  DT. A per-core CPU_SUSPEND experiment would need its own small cpuidle driver.
- **Current boot: no cpuidle at all.** `CONFIG_CPU_IDLE` is not set, so idle is the arch default
  `cpu_do_idle()`, which is WFI. The A53 clock-gates itself in WFI, so what is left to save is
  leakage, plus the cluster and SoC states. (`out/idletest/cronos-idle.config` adds
  `CPU_IDLE`/`ARM_PSCI_CPUIDLE` to a cronos config, but that directory holds an athena dtb and no
  result was found for it. On cronos it would have done nothing, for the reason above.)
- **Hotplug: not possible today.** `arch_cpu_is_hotpluggable()` is `ops->cpu_can_disable()`,
  which `mt-boot` lacks, and `op_cpu_disable()` needs `cpu_die`. So there is no
  `/sys/devices/system/cpu/cpuN/online` (probe P1 confirms it).

## 2. Amazon's secure firmware and how Fire OS powers CPUs

### BL31 / tee1

- The BL31 version was **not determined**: there is no tee1 image to read. What is known:
  - it speaks PSCI 0.1 with the SMC32 IDs 0x84000001-4. Fire OS and our DT both list them;
  - it lives at 0x43000000, size 0x30000;
  - the Lineage config has `MTK_ATF_LOGGER`, so it logs to a shared buffer, and
    `mcusys_smc_write` exists (MCUCFG 0x10200000 is secure-write);
  - `CONFIG_MTK_IN_HOUSE_TEE_SUPPORT=y`: MediaTek's own TEE in tee1.
- From the AP-side code, its PSCI implementation does **not** switch CPU power. CPU_ON is followed
  by the kernel's `spm_mtcmos_ctrl_cpu(cpu, STA_POWER_ON)` (`mt_psci_cpu_boot`). CPU_OFF is followed
  by the kernel's `spm_mtcmos_ctrl_cpu(cpu, STA_POWER_DOWN, chkWfi=1)` (`mt_psci_cpu_kill`). This is
  the old MediaTek "ATF 1.0" split (MT6735 family). The PSCI layer does cache maintenance, the GIC
  and the warm-boot address, and the SPM does the power.

### Fire OS idle (Lineage 4.9 `cronos/lineage-18.1`)

- Driver `cpuidle-mt81xx_v2.c` has five states: dpidle, SODI, MCDI, slidle and rgidle (WFI). The
  governor is `mtk_governor` → `mt_idle_select()`.
- `mt_idle.c`: `IDLE_HAVE_MCDI 0`, `idle_switch[] = {dpidle 1, soidle 1, mcidle 0, slidle 0,
  rgidle 1}`. `go_to_mcidle()` returns false. A core with other cores online always ends in
  `go_to_rgidle()`, which is a bare `wfi`.
- SODI and deep idle require `only_one_cpu_online()`, which is `spm_get_cpu_pwr_status() ==
  CPU0`, i.e. PWR_STATUS. They run `spm_go_to_sodi`/`spm_go_to_dpidle`:
  1. mask all IRQs except the SPM's and clone the GIC into CIRQ;
  2. `__spm_reset_and_init_pcm` and `__spm_kick_im_to_fetch`, which point PCM_IM_PTR at an array
     compiled into the kernel (`sodi_binary`, "pcm_sodi_v00.04_mt8163_20171227", 915 words;
     `dpidle_binary`, "pcm_deepidle_v00.04_mt8163_20171222_v9", 935 words);
  3. set the pwrctrl (`wfi_op = AND` over all `ca7_wfiN_en`) and kick the PCM;
  4. `mt_cpu_dormant()` → `arm_cpuidle_suspend(2)` → PSCI CPU_SUSPEND with idle state index 2. With
     the DT's `cpu-idle-states = <CLUSTER CLUSTER CPU CPU CPU>`, index 2 is the second entry,
     **0x02010000** (power-down, affinity level 2). The 32-bit path hard-codes `0x201 << 16`, the
     same value.

  The PCM firmware then powers CPU0 and the cluster off and on.
- No MCDI firmware for MT8163 is in either branch (`grep -ril mcdi` finds only the disabled
  `mt_idle.c` hooks). `base/power/mcdi/` is the SSPM-generation driver, for other SoCs.
- Hotplug: `hps_v1` (`[hps_main]`), period 100 ms, up threshold 80 % for 2 periods, down
  threshold 70 % for 20 periods (`mt_hotplug_strategy_platform.h`). It uses
  `device_offline()`/`device_online()`.

### Amazon's CPU MTCMOS power-down (to port, `mtcmos.c` `spm_mtcmos_ctrl_cpu1..3`)

1. `POWERON_CONFIG_SET = 0x0b16 << 16 | 1`.
2. Wait for `SLEEP_TIMER_STA` & `CA7_CPUn_STANDBYWFI` (bit 16+n). Amazon's wait is unbounded.
3. PWR_CON |= PWR_ISO; |= SRAM_CKISO; &= ~SRAM_ISOINT_B.
4. L1_PDN |= L1_PDN; wait for L1_PDN_ACK (bit 8) = 1.
5. PWR_CON &= ~PWR_RST_B; |= PWR_CLK_DIS; &= ~PWR_ON; &= ~PWR_ON_2ND.
6. Wait for PWR_STATUS and PWR_STATUS_2ND bit (9+n) = 0.

Power-on is the reverse, and `mt8163-boot.c` already does it (with bounded waits). The cluster
(CPUTOP, `spm_mtcmos_ctrl_cpusys0`) has its own sequence with L2_PDN 0x244. It is only safe when
every core of the single cluster is off, and on cronos CPU0 is never off outside SODI, deep idle and
suspend.

## 3. AP-side requirements on mainline

**Stage A (hotplug with power-off)**

- `mt8163_boot_ops`:
  - `.cpu_can_disable = cpu != 0`;
  - `.cpu_disable = cpu_psci_ops.cpu_disable` (it checks `psci_ops.cpu_off`);
  - `.cpu_die = cpu_psci_ops.cpu_die` (CPU_OFF 0x84000002, state 0x00010000; as in 4.9);
  - `.cpu_kill` = wait for STANDBYWFI bit 16+n (bounded, about 100 ms; `cpu_psci_cpu_kill` is a
    no-op here because PSCI 0.1 has no affinity_info), then the MTCMOS power-down above with bounded
    polls, under the same spinlock as power-on. If any wait times out: return an error, leave the
    domain as it is, and log PWR_CON, PWR_STATUS and SLEEP_TIMER_STA.
- `cpu_boot` on re-online: as now, PSCI CPU_ON, then MTCMOS power-on. Guard it: if PWR_STATUS
  still shows the core on (a failed kill), refuse with -EBUSY. A powered core with RST_B high would
  not restart from the new entry point.
- `CONFIG_HOTPLUG_CPU=y` is already set. No cpuidle config change is needed.
- A governor. Either a small userspace loop in the rootfs that reads `/proc/stat` and writes
  `cpuN/online`, with HPS-like hysteresis (offline after about 2 s under 70 %, online at 80 % for
  200 ms), or simply offline cpu3/cpu2 while the screen is off or idle. Keep it out of early boot.
- Likely side effect: arm64 kexec needs the secondaries offline, and this gives it `cpu_die`.
  Not checked.

**Stage B (SODI, cluster off, screen on): requirements, not a plan to do soon**

- Port `mt_spm_internal.c` (PCM reset/kick/init, wake status), `mt_spm_sodi.c` (the pwrctrl and
  `sodi_binary`), CIRQ (`mediatek,mt8163-sys_cirq` at 0x10204000, needed to latch wake IRQs while
  the GIC is off), and SPM IRQ0.
- Entry only with CPU0 alone online. PSCI CPU_SUSPEND **0x02010000** through `cpu_suspend()` with
  `cpu_pm_enter/exit`, and a broadcast timer, because the arch timer stops. The PCM firmware also
  touches the PMIC wrapper and DRAM (MEMPLL, DDR high-speed flags). That makes it far riskier than
  stage A.

**Ruled out**

- PSCI cpuidle with CPU-level power-down (0x00010000) and nothing else: no agent powers the core
  off or wakes it (inferred; BL31 not read).
- Borrowing an MCDI PCM firmware from the MT6735 family: not validated on MT8163, and MediaTek
  turned the feature off for this SoC. Low confidence that it would even run.

## 4. Register map (MT8163 SPM, base 0x10006000; the AP's kernel reads and writes it in both
Fire OS and our `mt-boot`, so it is NS-accessible)

| Address | Name | Use |
|---|---|---|
| 0x10006000 | SPM_POWERON_CONFIG_SET | key `0x0b16 << 16`, bit0 = register control (write; read is informative only) |
| 0x10006200 | CA7_CPU0_PWR_CON | bits: 0 RST_B, 1 ISO, 2 PWR_ON, 3 PWR_ON_2ND, 4 CLK_DIS, 5 SRAM_CKISO, 6 SRAM_ISOINT_B |
| 0x10006204 | CA7_DBG_PWR_CON | |
| 0x10006208 | CA7_CPUTOP_PWR_CON | the cluster |
| 0x10006218 / 0x1000621c / 0x10006220 | CA7_CPU1/2/3_PWR_CON | |
| 0x10006244 / 0x10006248 | CPUTOP_L2_PDN / L2_SLEEP | bit0 PDN, bit8 ACK |
| 0x1000625c / 264 / 26c / 274 | CA7_CPU0..3_L1_PDN | bit0 L1_PDN, bit8 L1_PDN_ACK |
| **0x1000660c** | **PWR_STATUS** | bit8 CPUTOP, **bits 9-12 CPU0-3**, bit13 DBG |
| 0x10006610 | PWR_STATUS_2ND | same bits |
| 0x10006600 / 0x10006608 | APMCU_PWRCTL / AP_STANBY_CON | |
| **0x10006720** | **SLEEP_TIMER_STA** | **bits 16-19 CPU0-3 STANDBYWFI**, bit24 CPUTOP STANDBYWFI |
| 0x10006310 / 0x10006314 | PCM_CON0 / PCM_CON1 | key `0x0b16 << 16`; IM_KICK, PCM_KICK, IM_SLAVE |
| 0x10006318 / 0x1000631c | PCM_IM_PTR / PCM_IM_LEN | where the PCM fetches its program (LK's or Fire OS's last load) |
| 0x10006380 … 0x100063bc | PCM_REG0 … REG15_DATA | REG15 = PCM PC; changing between reads = running |
| 0x100063c0 / 0x100063c4 | PCM_EVENT_REG_STA / PCM_FSM_STA | |
| 0x10006b00 / b04 / b08 / b0c | PCM_RESERVE / RESERVE2 / PCM_FLAGS / PCM_SRC_REQ | |
| 0x10006b30 … 0x10006b3c | CA7_CPU0..3_IRQ_MASK | SPM per-CPU wake IRQ masks |
| 0x10006910 / 0x10006918 | SLEEP_ISR_RAW_STA / SLEEP_WAKEUP_MISC | |
| 0x10006f00 … 0x10006f0c | SLEEP_CA7_WFI0..3_EN | PCM WFI inputs |

These are not to be touched from `/dev/mem`: MCUCFG 0x10200000 (secure-write, via `mcusys_smc_write`),
the GIC CPU interface, and the BL31 at 0x43000000 (no-map, secure).

## 5. Risks

- **A hang before init loops the one-shot test slot.** Stage A's code runs only on an explicit
  `echo 0 > cpuN/online` after boot. The boot-time bring-up path (`cpu_boot`) must stay
  byte-for-byte the same, so build the hotpluggable variant with the governor off and test by hand.
- **Lockup detectors and `panic=5`.** A core that never reaches STANDBYWFI makes `cpu_kill`
  spin. With bounded waits it fails instead. An unbounded wait (as in Amazon's code) would trip the
  softlockup panic on the CPU doing the offline. That reboots into the same kernel, which is fine
  because nothing offlines at boot.
- **Cutting power to a core that is not in WFI** (STANDBYWFI misread) corrupts it mid-flight. Its
  dirty L1 lines are lost, and the system hangs or crashes. Mitigations: the PSCI CPU_OFF has
  already flushed, Amazon waits on the same bit, and we add the check plus PWR_ISO first, as Amazon
  does.
- **Re-online after a failed kill:** see the `cpu_boot` guard. Without it, CPU_ON would "succeed" and
  the core would never come up. Mainline then times out with "failed to come online", which is not
  fatal.
- **IRQ affinity:** IRQs move off the offlined CPU (generic code). Check that per-CPU users
  (the arch timer PPI, the PMU's SPI 8-11 with `interrupt-affinity`, and the vcodec and Mali IRQs)
  cope. On arm64 they do generically.
- **cpufreq:** the policy covers all four CPUs (`opp-shared`), so offline CPUs only drop out of the
  load signal. Thermal's cpufreq cooling uses cpu0, which never goes offline.
- **Stage B** is in a different risk class: PMIC wrapper, DRAM and IRQ routing through CIRQ. A
  lost wake IRQ there is a silent hang with only CPU0 online. The RCU stall panic would catch it, but
  only if the stall detector itself is still running.

## 6. Staged plan

**Stage 0: read-only probes on the current image (no code change).**

- P1: `ls /sys/devices/system/cpu/cpu1/` (expect no `online`) and `cat /sys/devices/system/cpu/{online,possible}`.
  `ls /sys/devices/system/cpu/cpuidle` (expect missing).
- P2: `dmesg | grep -i -E "psci|mt-boot|smp"` (expect "Using PSCI v0.1 Function IDs from DT"
  and four CPUs up).
- P3: `/proc/device-tree/reserved-memory/` must hold `secmon@43000000`; `grep -i reserved /proc/iomem`.
- P4: from a sampler **pinned to cpu0** (`taskset -c 0`, mmap `/dev/mem`, one 4 KiB map of
  0x10006000), read these twice, one second apart:
  0x1000660c, 0x10006610, 0x10006720, 0x10006200, 0x10006208, 0x10006218, 0x1000621c, 0x10006220,
  0x10006244, 0x1000625c, 0x10006264, 0x1000626c, 0x10006274, 0x10006310, 0x10006314, 0x10006318,
  0x1000631c, 0x100063bc, 0x100063c4, 0x10006b00, 0x10006b08, 0x10006b0c, 0x10006608, 0x10006910,
  0x10006918, 0x10006f00-0x10006f0c.

  Expect PWR_STATUS bits 8-13 set, PWR_CON 0x...0d or similar (RST_B | PWR_ON | PWR_ON_2ND,
  ISO clear), PCM_REG15 frozen (no PCM running under mainline), and IM_PTR = whatever LK last set.
- P5: STANDBYWFI sanity. Keep cpu2 busy (`taskset -c 2 sh -c 'while :; do :; done'`), leave cpu3
  idle, and sample 0x10006720 for 5 s from cpu0. Expect bit 18 always 0 and bit 19 mostly 1. This
  shows that the bit `cpu_kill` will trust tracks WFI.

**Stage 1: one core, by hand.** Build with the stage A ops, no governor. After boot: `echo 0 >
/sys/devices/system/cpu/cpu3/online`. Check `dmesg` and P4: PWR_STATUS bit 12 and its 2ND bit clear,
CPU3_PWR_CON with RST_B and PWR_ON clear, L1_PDN ACK set. Then `echo 1 > …/online`: the bits come
back, and cpu3 runs a `taskset -c 3` job. Repeat 1000 times in a loop with a load on cpu0-2.

**Stage 2: all secondaries.** Cycle cpu1-3 in random order under load: decodes, the RTSP stream,
Wi-Fi traffic. Soak for 30 minutes, then a night with cpu1-3 offline. Measure at the adapter (WFI
vs. offlined) and the SoC temperature, as on athena.

**Stage 3: governor.** A rootfs service with HPS-like hysteresis and min_cpus = 1 (or 2 while
media plays). Default off for one release, then on.

**Stage 4 (optional): cluster level = SODI.** Read the BL31 from tee1 first: a read-only `dd` of
`mmcblk0p4` by the user, or amonet's `bin/tz.img`. Confirm what CPU_SUSPEND 0x02010000 does there.
Then port the SODI path behind a module parameter, entered only with one CPU online and the display
in video mode.

## 7. Stage 0 results (read-only, kernel #14)

- P1: `cpu1/` has no `online`; `online` and `possible` are `0-3`; no `cpuidle/`. As expected: the
  cores cannot be taken offline today, and idle is plain WFI.
- P2: `psci: Using PSCI v0.1 Function IDs from DT`; four CPUs up.
- P3: `secmon@43000000` is in the reserved memory, beside `ramoops@44410000`.
- P4 (a `/dev/mem` reader mapping the SPM page read-only, twice a second apart; no taskset in the
  image, so the sampler was not pinned):
  - PWR_STATUS and PWR_STATUS_2ND both `0x03effffa`: bits 8-12 set, the cluster and all four cores
    powered.
  - CPU0-3 PWR_CON (`0x10006200`, `208`, `218`, `21c`; and `220`) all `0x4d`: powered on and out of
    reset, as no core has ever been switched off.
  - L2_PDN and the L1_PDN registers `0`: no cache powered down.
  - PCM: REG15 (`0x100063bc`, its program counter) `0` and the FSM state `0x00048490`: no SPM program
    is running. LK leaves none loaded that runs (stage 4's question; the PCM_IM_PTR itself was not read).
  - SLEEP_TIMER_STA (`0x10006720`) `0x02f40000`: one STANDBYWFI bit set at that instant.
- P5: sampling STANDBYWFI from an unpinned sampler for 5 s with a spin loop started for cpu2 gave every
  core 26-44 % in WFI. The bits move, so they track WFI, but without taskset neither the load nor
  the sampler stayed on one core. Stage 1 should carry its own affinity (a static `taskset` or the
  sampler setting its own) before reading the per-core numbers.

## 8. Stage 1 results (kernel #15, mt-boot with cpu_kill)

- `cpu1/online` .. `cpu3/online` exist; cpu0 has none (`cpu_can_disable`).
- `echo 0 > cpu3/online`: PWR_STATUS and PWR_STATUS_2ND go from `0x03effffa` to `0x03efeffa`, bit 12
  clear: the core's domain is off, not just in WFI.
- 300 cycles, cpu1-3 in turn, offline then online, with two cores spinning: the core was powered
  off after every offline (300 of 300), no write failed, no `mt-boot` errors, all four back online.
- The vcodec and Wi-Fi modules built for #14 load on #15 (nothing they use changed); the daemon runs.
- Stage 3's governor is echod's `hardware/cpuplug`: on the 2nd gen Show 5 only, two cores always up,
  a spare core back after two seconds above 70 % busy, one taken down after twenty below 30 %.

## 9. Stage B: SODI (kernels #30-#32 in the test slot, 2026-10-19)

### The BL31 cannot be read

`mmcblk0p4` (PARTNAME `tee1`, 5 MiB) was read once, read-only, into the session scratchpad (not
the repo). It is encrypted: a 0x200-byte "ATF" header (size 0xb400), then an `EET KTM` ("TEE KTM")
header and ciphertext. Every 4 KiB block has an entropy of 7.95-7.96 bits per byte, and there is no
readable string in the whole image. The key is in the SoC (the preloader decrypts it), so
CPU_SUSPEND cannot be disassembled from the unit's image, and amonet's `tz.img` would be the same
image. What the BL31 does was found by running it (below).

### Read-only probes (kernel #28, `sodiprobe`, /dev/mem mapped `PROT_READ`)

- SPM at rest: `POWER_ON_VAL0/1` = `0` / `0x61015830` (Fire OS's defaults: LK sets the same);
  `PCM_CON0` `0`, `PCM_CON1` `0x6c00`, `PCM_IM_PTR` `0` with `IM_LEN` `0xff`: LK leaves no program, and
  the PCM is idle (`REG15` `0`, `FSM` `0x48490`). `CLK_CON` `0xa0040000`, `AP_STANBY_CON` `0x06b80000`,
  `PCM_SRC_REQ` `0x1bc`, `PCM_TIMER_VAL` `0xffffffff`, wake mask all set. `DUAL_VCORE_PWR_CON` `0`.
- The PMIC wrapper's DVFS command slots (`0x1000d0e8`-`0x124`) are all zero: nothing on mainline or
  in LK programs them. A DVFS kick would write 0 to MT6323 register 0 (CHR_CON0).
- CIRQ (`0x10204000`): all masked, `CON` `0x80000000`. GIC-400 `TYPER` `0xfc68` (288 interrupts). The
  SPM's IRQ0 (SPI 163) reads pending in the GIC: unrequested, its active-low line is not inverted by
  the sysirq.
- Clock gates: `INFRA_PDN_STA0` `0x2a00`, `STA1` `0x00400246`. Under `clk_ignore_unused` most of
  infra's gates are open: USB, I2C1, the PWMs, UART1-3, MSDC1, MSDC2 (Wi-Fi) and AP_DMA.
- The arch timer and GPT2 agree to 1.6 ppm with the cores running.

### What the SODI program does (`sodi_binary`, 915 words, read as data)

The PCM encoding is regular enough to follow: `0x18c0001f imm` loads r3 with an immediate (`0x19..`
loads from memory), `0x81..` tests one bit (bits 19:15 select the bit, 14:10 the register) and
`0xd8..` branches on it, `0xe8208000 addr val` stores, and `0xc0c0....` calls. The flags register
(`PCM_FLAGS`, loaded into r6) is tested for bits 0, 2, 6, 7, 9, 12, 13 and 24 only. Bit 3
(VCORE_DVS_DIS), BUCK_SEL and MAINPLL_PDN_DIS are never tested.

- **The main path** (entry at 0x201): with SCREEN_OFF set, it waits for the cores' WFI, then powers
  CPU0, CPUTOP and MCUSYS down (L1/L2 too) through their PWR_CON registers, toggles the infra
  SRAM/ROM sleep (`0x10006294`) and `DUAL_VCORE_PWR_CON` (`0x3101`, `0x101` after), and sets r0 bits
  1, 9, 17 and 23. Then it waits for a wake event, and undoes it all in reverse with settling waits.
  It ends every path by raising SW_INT0 (SPM IRQ0), which is what lets a core that is not powered
  down leave WFI. Unless CPU_PDN_DIS (bit 0) is set, it does the power-down. Unless CPU_DVS_DIS (bit
  9) is set, it also reads the sym827's VSEL over an I2C master at `0x10059800` (slave `0xc0`), stores
  it in `PCM_RESERVE2`, writes `0x94` and writes the saved value back on wake: CPU voltage scaling
  behind the back of mainline's cpufreq.
- **DRAM** is only touched from the APSRC event vectors (PC 0, 0x3a, 0x90, 0xac): DDRPHY MTCMOS
  (`0x10006240`), a DRAMC bit (`0x100040f4` bit 25), r0 bits 2, 8, 16, 22 and 26-31. The program
  enables those vectors only with MEMPLL_RESET (bit 7) or SODI_DIS (bit 6) set, and DDRPHY_PDN_DIS
  (bit 2) skips the DDRPHY sequence in them. The PMIC wrapper's DVFS slots are never kicked.
- Fire OS on cronos would hardly ever have run it. Its `soidle_condition_mask` keeps SODI out while
  any of these gates is open: MSDC0-3, SPI, AP_DMA, USB, I2C, the PWMs and UART1-3. The Wi-Fi SDIO
  host (MSDC2) holds its clock for as long as SDIO interrupts are on (`mtk-sdio.c`), so SODI waited
  on the clock whenever Wi-Fi was up. The likely reason for the mask: those masters cannot ask for
  DRAM (APSRC) themselves, and the program would put DRAM in self-refresh under them.

### What was ported (`patches/mt8163-sodi.c`, `drivers/cpuidle/mt8163-sodi.c`)

A cpuidle driver for CPU0 alone: WFI, plus "SODI" (exit latency 1.5 ms, residency 5 ms,
`TIMER_STOP`, so the GPT carries the tick). It registers only when enabled at run time
(`/sys/module/mt8163_sodi/parameters/enable`), and enters SODI only when `num_online_cpus() == 1` and
PWR_STATUS and PWR_STATUS_2ND show only CPU0's domain on. The sequence is Fire OS's `spm_go_to_sodi`.
Each entry runs `cpu_pm_enter` and `cpu_cluster_pm_enter` (mainline's GIC saves its distributor),
masks the GIC but SPM IRQ0, and clones CIRQ. It then resets and loads the PCM (`sodi_binary` copied
to a cleaned buffer), sets the pwrctrl, wake sources and backstop timer, kicks the PCM, and calls
`psci_cpu_suspend_enter(0x02010000)`. After the wait for SW_INT0 it reads the wake status, cleans up,
replays CIRQ into the GIC and restores. The differences from Fire OS all do less:

- DRAM never goes into self-refresh: the PCM holds its own APSRC request (`PCM_SRC_REQ` bit 0),
  DDRPHY_PDN_DIS is set and MEMPLL_RESET is not, so the DRAM vectors are never enabled. With that,
  the reason for Fire OS's clock mask is gone. Only the eMMC host's clock is still required off.
- No CPU voltage scaling (CPU_DVS_DIS, VCORE_DVS_DIS). Every PMIC wrapper DVFS slot is pointed at the
  MT6323's read-only chip ID (`0x0100`), so a stray DVFS command does nothing.
- CIRQ is cloned from the GIC enables as they were before masking. Fire OS cloned after, which left
  only IRQ0. So Wi-Fi's SDIO host, I2C and the display wake it too.
- The SPM IRQ0 is requested (SPI 163, level low, through the sysirq) so that its polarity is right.
- A PCM timer backstop (`timer_ms`, 2 s), and a 2 ms bounded wait for the PCM to finish. On a PCM
  assert, a PCM that does not finish, a failed reset or three PSCI errors, SODI is off until reboot.
- `cpu_pdn=0` runs the same PCM flow with CPU0 powered and in WFI (for tests). `deadman`/`keepalive`
  restart the unit if a test loses contact. `trace=N` logs the next N CPU_SUSPENDs. `status` holds the
  counters, the last wake status and a histogram of wake sources.
- The firmware array is in the 4.9 source under MediaTek's GPL-2.0 header (`mt_spm_sodi.c`), as data
  of that file. The note above takes it as GPL, like the rest of that file.

### Results

- **The secure monitor's CPU_SUSPEND 0x02010000 works.** It returns 0 through `cpu_resume` after the
  PCM has powered CPU0, the cluster and MCUSYS down and up again. In the test runs it did so on every
  entry (36 951 in the last boot), with no PSCI errors, PCM asserts or late PCMs.
- **But the arch timer's counter does not survive.** It lives in MCUSYS (the `cpuxgpt` behind
  MCUCFG 0x670/0x674), and after the first power-down it restarts from one fixed value every time. That
  value is the count about 2 ms into the first SODI, which the monitor apparently saved once. So
  CNTPCT runs backwards by however long the core ran in between (traced: `0x40fbddef` →
  `0x40fc4ff7`, then `0x40fcfa1d` → `0x40fc4fef`, `0x40fd0ac5` → `0x40fc4ffa`...). With the
  `arch_sys_counter` clocksource, timekeeping then stands still and the unit hangs within seconds. The
  hardware watchdog reset it back to the boot partition's kernel. sched_clock is already the GPT (the
  same 13 MHz, registered later), so only the clocksource matters: with `timer` (GPT2, always on)
  as the clocksource, SODI runs. The driver refuses to power down while the clocksource is the arch
  counter (`blocked_clock`). The cost is the vDSO: `clock_gettime` becomes a system call.
- **`cpu_pdn=0`** (PCM, GIC masked, CIRQ, CPU0 powered): 1 300 entries, every one ended by SW_INT0. The
  wake sources were CIRQ (all) and GPT.
- **Soak, the daemon stopped (so idle is idle), cpu1-3 offline, clocksource `timer`, 30 min:** about
  15 SODI entries a second and 94-95% of the time in SODI. That is 33 400 entries, every one powered
  down and back. The status showed no errors, and dmesg had nothing new. The memory test (`sodimem`:
  32 MiB rewritten and a 32 MiB static buffer, each checked after a 5-300 ms sleep, so the cluster
  goes down with dirty lines) found 0 bad words in 6 runs of 50 s (and in 1 run during the first test), and 0 in 4 runs
  under WFI. Measured against the Mac's NTP, the unit's clock runs about 700 ppm slow with or without
  SODI and with either clocksource, and busybox ntpd steps it.
- **A longer soak was not run.** It would have kept the daemon down for another hour or more in the
  night, and no decision depends on it: SODI cannot engage while the daemon runs (below), so
  default-on is out either way.
- **Wake sources:** GPT (the tick broadcast) and CIRQ wake it. Wi-Fi works through CIRQ: pings
  averaged 11.4 ms under SODI against 8.4 ms under WFI, with no loss. Touch and the buttons are EINT
  wake sources in the PCM's mask, but were not exercised: the user was asleep. The display is never
  starved, since DRAM stays up and the panel's video mode keeps scanning out.
- **Temperature, as a stand-in for power.** The battery-less unit has no current reading. With the
  daemon stopped and one core, the CPU zone settled at 41.3 °C on average over SODI's last 9
  quiet minutes, and at 41.7 °C over the 8 quiet minutes of WFI right after. It went from 41.1 to
  41.8 °C within the first minute of WFI. So SODI is worth about 0.4 °C here: a small saving, tens
  of milliwatts at most. The minutes with the memory test ran 43.6-44.6 °C either way.
- **Clock:** the drift against NTP was -790 ppm under SODI and -660/-690 ppm in the WFI blocks on
  either side. SODI loses about 100 ppm more, about 7 µs per entry: the GPT probably stops briefly
  inside the PCM's sequence. ntpd covers it.
- **With the daemon running, SODI never happens.** On CPU0 alone, echod's load (the microphone poll,
  about 300 SPI interrupts a second; the camera thread; the wake word) keeps the core busy enough
  that the microphone's FPGA overruns, about 2 a second, and the load average reaches 3.7. The
  longest idle is a few milliseconds: about 2 900 wakeups a second, including 1 900 timer interrupts
  and the sym827's I2C at 475-4 300 a second from cpufreq. In 30 s the governor never chose SODI. With
  two cores (cpuplug's `keep = 2`), SODI is not allowed at all.

### Verdict for stage B

SODI works on this hardware with this monitor, as long as timekeeping is off the arch counter. It
cannot save anything in the unit's normal operation. With one core, echod overloads it: the
microphone overruns, and cpuplug would bring a second core back within two seconds anyway. With two
cores, SODI never applies. So it belongs in the kernel off by default and dormant (no cpuidle driver
registers until it is enabled), and echod's cpuplug is unchanged. The image built from the tree
with it, #32 (`out/sodi/boot.img`, sha256 `1fa057ab…9b50`), booted three times from the test slot,
with the Wi-Fi and vcodec modules of #28 loaded and the daemon running. The boot partition was not
written: the session's permission system refused that step, so the units still run #28. It is worth turning on only if the
daemon's idle load comes down to well under one core with idle gaps of more than about 5 ms: no
always-on microphone polling at 300 Hz, no sym827 I2C storm from cpufreq. Then cpuplug could keep
one core while idle on a kernel that has `/sys/module/mt8163_sodi`, and switch the clocksource to
`timer` first.

Side findings:

- The sym827 (cpufreq's Vproc) takes 475 to 4 300 I2C interrupts a second (`11008000.i2c`), with
  `sugov:0` at about 4% of a core. schedutil changes the voltage about a thousand times a second.
  That costs power on its own, and a rate limit or a coarser OPP table would cut it. Section 10
  measures it again (sugov:0 was 13-15% of a core, not 4%) and finds the cause.
- The unit's 13 MHz reference is about 800 ppm slow against NTP, and busybox ntpd steps the clock
  every couple of minutes.

## 10. cpufreq churn on the sym827 (kernel #28, 2026-10-19)

### What happens

- **The OPP table** (mainline `mt8163.dtsi`, `cluster0_opp`, shared by all 4 cores): 600, 624, 741,
  871 and 1050 MHz at 1.15 V, 1133 MHz at 1.2 V, 1216 MHz at 1.25 V, 1300 MHz at 1.3 V. That is 4
  distinct voltages. The driver is `mtk-cpufreq` (mt7623 platform data: no SRAM rail, no CCI), and
  the governor is schedutil. `cpuinfo_transition_latency` is 0, so schedutil's `rate_limit_us`
  defaults to 1000. The kernel has no `CPU_FREQ_STAT` and no ftrace, so transitions were sampled
  from `scaling_cur_freq`.
- **Every frequency change below 1133 MHz raises and lowers the voltage.** `mtk_cpufreq_set_target`
  parks the cores on the intermediate clock (`arm_div_pll1_en`, MAINPLL, 1092 MHz) while ARMPLL
  relocks. The safe voltage for it is the ceiling OPP's, 1133 MHz, so 1.2 V. A change between two
  1.15 V OPPs therefore goes 1.15 → 1.2 → 1.15 V. A request for the voltage already set is a no-op in
  the regulator core, so equal voltages alone would cost nothing. Without the intermediate swing, only
  real voltage changes would reach the bus.
- **Each voltage change is about 4 I2C transfers.** fan53555 has no register cache. The core reads
  the voltage and the selector, then `regulator_set_voltage_sel_regmap` reads and writes VSEL. One
  read of `microvolts` costs 2.8 interrupts on `11008000.i2c`.
- With `scaling_max_freq` at 1050 MHz (one voltage), the I2C rate was still 3 200 a second: the
  intermediate swing alone. With the `performance` governor (no changes) it was 0.

### Measurements

At idle: echod running, screen as it was, cpu0-1 online (cpuplug), 180 s per setting after 30 s to
settle. "busy" is all non-idle time over both online cores. Wake word: echod's `wake frames` lines
(per 20 ms frame) in the same window. The temperatures are `cpu-thermal`'s average.

| rate_limit_us | I2C1 irq/s | sugov:0 | busy | CPU °C | wake mean_us | worst_ms |
| --- | --- | --- | --- | --- | --- | --- |
| 1000 (default) | 4 441 / 3 949 | 14.5% / 12.8% | 32.1% / 33.2% | 49.2 / 49.4 | 1 430-1 760 | 5-22 |
| 4000 | 1 346 | 4.8% | 28.1% | 48.9 | 1 314-1 329 | 16-19 |
| 10000 | 601 | 2.3% | 22.3% | 48.9 | 1 295-1 326 | 18-30 |
| 20000 | 310 | 1.2% | 21.2% | 48.9 | 1 218-1 278 | 16-20 |
| 50000 | 130 | 0.5% | 20.9% | 48.7 | 1 223-1 302 | 17-30 |

Alternatives, in a later and busier window (same idle, 150 s each):

| setting | I2C1 irq/s | sugov:0 | busy | CPU °C | wake mean_us |
| --- | --- | --- | --- | --- | --- |
| schedutil, max 1050 MHz, 1 ms | 3 186 | 8.1% | 42.2% | 50.4 | 1 513-1 584, only 37-47 frames/s |
| `performance` (1300 MHz) | 0 | 0 | 32.7% | 51.5 | 916-947 |
| schedutil, 10 ms | 395 | 1.4% | 36.4% | 51.5 | 1 125-1 153 |

Camera stream (`TestDeviceH264Rate`, `HW_MAXSHUT=1220`, 60 s, echod held, so all 4 cores online):

| rate_limit_us | pictures/s | I2C1 irq/s | sugov:0 | busy | CPU °C |
| --- | --- | --- | --- | --- | --- |
| 1000 | 24.9 | 2 492 | 7.3% | 30.9% | 53.8 |
| 4000 | 25.6 | 979 | 3.3% | 29.1% | 54.5 |
| 20000 | 25.2 | 300 | 1.3% | 31.9% | 53.6 |
| 50000 | 24.6 | 116 | 0.4% | 30.3% | 54.0 |

- At 1 ms, sugov:0 is 13-15% of a core at idle: the PLL relock and the I2C, most of a thousand
  times a second. A 10 ms limit cuts the I2C rate by 7-10x and sugov by 6x. The whole system's idle
  load fell by about 10 points of the two cores (0.2 of a core), more than sugov's own share: the
  changes also stall the cores on the intermediate clock and its voltage ramps.
- The wake word got faster, not slower: its per-frame time fell from about 1 600 to 1 300 µs.
  `worst_ms` stays in the same range (5-30 ms) at every setting. The stream holds the sensor's 25
  pictures a second at every setting tried (4 and 20 ms bracket the 10 ms that shipped).
- Capping at 1050 MHz is no use: the intermediate swing keeps the I2C, and the wake word falls
  behind (37-47 frames a second). `performance` has no churn and the fastest wake word, but holds
  1.3 V on all cores; it read 51.5 °C, the same as 10 ms in that window. Temperature, the only power proxy on this battery-less unit, moves
  by less than 1 °C between all of these, as with SODI.
- Collapsing OPPs that share a voltage would not help: the swing comes from the intermediate
  clock, not from the table's steps. Lowering the intermediate voltage (MAINPLL at 1.15 V,
  4% above the 1050 MHz OPP's rating) would remove the swing, but it runs the cores out of their
  OPP for the relock, so it was not tried.

### What shipped

`tools/linux/rootfs/etc/techo5/boot.sh` sets `policy0/schedutil/rate_limit_us` to 10 000, only on
`amazon,cronos` with `mtk-cpufreq` (the Fire OS kernel has neither). 10 ms keeps most of the gain
(the curve flattens after it) and still lets schedutil follow a burst within one 10 ms step. No
kernel change. Installed (rootfs v1.2.19-mainline.16, kernel #28), the slot committed good, and
the first measurement after boot (180 s, cpu0-1) read 403 I2C interrupts a second, sugov:0 at 1.4%,
busy 34.6% (the daemon's first minutes), 51.7 °C, and wake frames at 1 043-1 086 µs (worst 7-14 ms).

Not done: a register cache for the fan53555 driver (VSEL0, VSEL1 and CONTROL cached, the rest
volatile) would make each voltage change one I2C write instead of four transfers. It is a small
kernel patch, but the rate limit already takes the I2C from thousands to hundreds a second.

## Open questions

- The BL31's CPU_SUSPEND code. tee1 is encrypted, so it is known only by its behavior: it powers
  down at affinity level 2 and resumes through `cpu_resume`, but it restores the system counter
  from a stale value after every power-down but the first. Whether an SMC (Fire OS's `mcusys_smc_write`,
  0xC2000201) or an NS write of the CPUXGPT INIT count (MCUCFG 0x674/0x670) could set the counter
  right after a SODI is untested. Fire OS 3.18's `cpuxgpt_mt8163.c` wrote those registers from the
  kernel.
- The real saving of an offlined A53 versus WFI on this rail (sym827 Vproc shared by the cluster),
  and of SODI. Only temperature has been compared; there is no current measurement.
