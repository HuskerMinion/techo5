# Giving the SCP direct MM access on athena — full avenue survey

Goal: let the SCP (Cortex-M4 media co-processor, kukui SCP firmware) write the
multimedia blocks (VENC 0x17020000, VDEC 0x16000000, IMGSYS/FDVT 0x1502xxxx, DIP
0x15022000) directly, instead of trapping each access with an MPU and serving it
from the AP (`patches/scp-mmproxy.py`, `patches/scp-vdec-proxy/proxy.c`).

Everything below is **static analysis of this unit's own eMMC dumps**. No device
was touched. Companion to `mtee-dapc.md` (MTEE DAPC TA is a stub) and the stashed
`tfa-devapc-psci.md` (BL31 SiP/DEVAPC/PSCI map). Firmware artifacts and tooling in
the scratchpad (`tfa/dec/atf.bin`, `tfa/dec/atf_dram.bin`, `tfa/dec/tee.bin`,
`tfa/dec/devapc.txt`). Backups: `backups/*/partitions/`.

## TL;DR

**Direct SCP → MM access is not achievable at runtime on this locked BL31, and the
unlock does not help.** The one mechanism that governs which domain a bus master
presents (DEVAPC_AO) is secure-only on this unit — Amazon hardened the DEVAPC
self-protection to `S_RW_ONLY` for the AP's domain, so the non-secure world (Linux
at EL1 *or* EL2) cannot rewrite the SCP's master domain, the MM domain remap, or
any slave permission. The saiyaku/kaeru unlock runs in **LK = non-secure EL1
(AArch32), after BL31 already locked DEVAPC**; it gives persistent code execution
on every boot but at no higher privilege than the kernel we already control, so it
cannot write DEVAPC either. The SCP is also firewalled out of the GCE/CMDQ engine
(`INFRASYS_GCE` D4 = FORBID for the SCP's domain), so it cannot even borrow a
friendly on-SoC DMA master. **The AP-side proxy remains the only mechanism.** The
only realistic improvement is to offload the proxy's bulk register programming to
GCE/CMDQ on the AP side (D0 = NO_SEC), cutting AP round-trips; that is an
optimization of the proxy, not SCP-direct access.

A true fix would require secure-world (EL3 BL31, or S-EL1 MTEE) code execution —
which means either an exploitable memory-corruption bug in a runtime-reachable SMC
/ TA (none found; not audited exhaustively), or writing tee1/lk/preloader (FORBIDDEN
— bricks the unit).

## The mechanism, in one paragraph (why it's all about DEVAPC_AO)

A bus transaction's permission is decided by: (master → MAS_DOM) gives a raw domain,
(DOM_RMP) remaps it to a per-bus domain, and the slave's per-domain 2-bit APC entry
(`NO_SEC`=0 open, `S_RW_ONLY`=1 secure-only, `S_RW_NS_R`=2, `FORBID`=3) decides
allow/deny. All three register groups live in the **DEVAPC_AO** blocks:
INFRA 0x1000E000 (MAS_DOM 0x1000EA00, DOM_RMP 0x1000ED00/04, D*_APC 0x1000E000+),
MM 0x1001C000 (MM_DOM_RMP 0x1001CD00, D*_APC 0x1001C000+), MD 0x10019000. Register
map confirmed against ChromeOS TF-A `plat/mediatek/mt8183/drivers/devapc/devapc.h`.
On this unit: preloader puts **SCP (master 0) in MAS_DOM domain 3**, and MM_DOM_RMP
maps every domain except 0 to **MM domain 3**; BL31 sets **MM domain 3 = FORBID**
on all 141 MM slaves while leaving **MM domain 0 = NO_SEC** (open). The AP CPU is
domain 0, so it has full MM access (that is exactly why the proxy works); the SCP
is domain 3 and is denied. To "fix" this you must change one DEVAPC_AO register
(SCP's MAS_DOM to 0, or MM_DOM_RMP so the SCP's domain → MM domain 0, or the MM D3
entries). Who may write DEVAPC_AO is therefore the whole question.

## Avenue 1 — A SiP SMC in BL31 that writes DEVAPC. **Verdict: NO (conclusive).**

Full SiP dispatcher and handler table are in `tfa-devapc-psci.md` §2. Re-checked
against `atf_dram.bin`:

- `set_module_apc()` (slave APC writer, 0x54604744) has **ten call sites, all inside
  `devapc_init` (0x546047fc)**, which runs once at BL31 bring-up. No SMC path reaches it.
- No handler writes `MAS_DOM` (0x1000EA00), `DOM_RMP` (0x1000ED00), `MM_DOM_RMP`
  (0x1001CD00), any `D*_APC`, or `MM_SEC_EN`. No generic secure register read/write SMC.
- The only DEVAPC-tagged SMCs are LK-stage-only and harmless: `0x82000101`
  (`dump_devapc`, read-only), `0x82000102` (fixed modem-register lock writes).
  `0x82000207`/`0xC2000207` — the traditional MTK "DAPC" SMC slot — is a **stub
  returning −4** (0x54600814).
- The EMI-MPU SMCs `0xC2000209/20A` program DRAM region protection (EMI MPU), a
  different controller; they cannot touch register-bus DEVAPC.

Evidence settles it: there is no BL31 SiP entry point that re-domains the SCP or
re-permits any MM slave.

## Avenue 2 — AP rewrites the SCP's master domain / MM remap directly. **Verdict: NO (hard evidence).**

The registers exist and are simple one-word writes (set SCP MAS_DOM domain to 0, or
set MM_DOM_RMP so the SCP's domain → MM domain 0). The question is purely whether
the **non-secure AP (domain 0)** may write the DEVAPC_AO blocks. The DEVAPC
controllers protect themselves via dedicated slave entries. Read from Amazon's BL31
INFRA permission table in `atf_dram.bin` (table at VA 0x5460B6F8; 3 bytes/slave in
column order D0,D4,D1; alignment validated by matching 163/196 entries' D0,D1 against
ChromeOS `devapc.h`):

| INFRA slave (ChromeOS name) | idx | Amazon D0 (AP/NS) | ChromeOS D0 |
|---|---|---|---|
| `INFRA_AO_DEVICE_APC_AO_INFRA_PERI` (gates 0x1000E000) | 14 | **1 = S_RW_ONLY** | 0 = NO_SEC |
| `INFRA_AO_DEVICE_APC_AO_MD` (gates 0x10019000) | 25 | **1 = S_RW_ONLY** | 0 = NO_SEC |
| `INFRA_AO_DEVICE_APC_AO_MM` (gates 0x1001C000) | 28 | **1 = S_RW_ONLY** | 0 = NO_SEC |

Amazon **hardened the DEVAPC self-protection**: domain 0 (the AP, including Linux
NS-EL1/EL2) gets `S_RW_ONLY`, i.e. only the secure world may read/write the DEVAPC
registers. ChromeOS left these `NO_SEC` (open) — which is why Google's open TF-A
can reconfigure freely and why the ChromeOS SCP sits in its own accessible domain.
On this unit a non-secure write to 0x1000EA00 / 0x1000ED00 / 0x1001CD00 /
0x1001Cxxxx is blocked by DEVAPC and raises a violation; it cannot change the SCP's
domain, the remap, or any MM slave permission.

(For completeness: MM slave D0 = NO_SEC for all 141 MM slaves — confirmed from the
MM table at 0x5460B128 — so the AP's own MM access is unrestricted. The asymmetry is
entirely the SCP's domain, which only DEVAPC_AO can change.)

## Avenue 3 — Re-route the SCP's bus transactions through a friendlier master/domain. **Verdict: NO.**

Two sub-ideas, both dead:

- **Change the domain the SCP's own AXI carries without DEVAPC_AO.** The domain is
  assigned exclusively by DEVAPC_AO `MAS_DOM` (secure, see Avenue 2). The SCP's own
  "remap" registers (SCP SRAM/`scp remap` windows in the 0x105xxxxx config space)
  remap *addresses*, not master IDs or domains, so they cannot make the SCP's bus
  transactions present domain 0.
- **Have the SCP drive GCE/CMDQ (an on-SoC DMA-like command engine) to do the MM
  writes under GCE's own domain.** This is how MTK's secure CMDQ works and would
  need no DEVAPC change if GCE's domain has MM access. But the SCP is **FORBIDDEN
  from the GCE registers**: `INFRASYS_GCE` (INFRA idx 74) reads Amazon D4 = 3 =
  FORBID, and the SCP is INFRA domain 4 (preloader DOM_RMP maps MAS_DOM 3 → INFRA 4).
  So the SCP cannot program GCE. (GCE's D0 = NO_SEC — the AP can drive it. See
  "Most promising improvement" below.)

The SCP is boxed in on every side a non-secure change could reach.

## Avenue 4 — EL2 / a hypervisor. **Verdict: NO.**

DEVAPC distinguishes **secure vs non-secure** (SCR.NS / the NS attribute on the
transaction), not EL1 vs EL2. `S_RW_ONLY` on the DEVAPC_AO slaves (Avenue 2) blocks
all non-secure access regardless of exception level, so running the kernel at NS-EL2
or adding a non-secure hypervisor gains nothing. DEVAPC is controllable only from the
secure world (EL3 BL31, or S-EL1 MTEE). Whether Linux is entered at EL1 or EL2 is
irrelevant to DEVAPC.

## Avenue 5 — Runtime secure code via the unlock, on every boot. **Verdict: NO.**

The unlock is **saiyaku-athena / kaeru** (R0rt1z2, bengris32, FieryFlames). On
athena it lives in the **`lk` partition** (p18): LK strings include
`=> HACKED FASTBOOT mode`, `Copyright (C) 2023-2026 KAERU Labs`,
`Found unlock check at 0x%08X`, `Jumping to kaeru stage 2`,
`Failed to patch mt_init_boot() pointer`, plus `[DM-VERITY] … erase it!` and
`AMZN_LK_VERIFY`. So kaeru: patches LK's unlock check, hooks `mt_init_boot()` to run
its stage-2, and disables boot/vendor signature + dm-verity enforcement so unsigned
`boot`/`system` run. (This matches the amonet/cronos model of a boot-image/LK-stage
payload — on cronos it chains from `tee1`→`expdb` kaeru; on athena it's the `lk`
partition itself, which is why writing tee1/lk/preloader bricks the unit.)

Crucially, kaeru runs where **LK** runs:

- **LK is non-secure EL1 (AArch32).** Evidence (`tfa-devapc-psci.md` §2): the SiP
  dispatcher routes LK through the *non-secure* 0x82/0xC2 tables (SCR.NS=1), gated by
  a stage flag `*0x111088` that is 1 in LK and cleared to 0 by
  `bl31_prepare_kernel_entry` at EL3. Secure callers are restricted to two EINT SMCs.
- **Boot order: preloader (BL2, secure) → BL31 (EL3) → LK (BL33, NS).** BL31's
  `devapc_init` runs during BL31 bring-up and prints `[DAPC] … Init Done` *before*
  handing to BL33. LK (and the kernel) see DEVAPC already locked. The task's
  hypothetical "LK patches DEVAPC before BL31 locks it" does not hold on MT8183 —
  BL31 runs first.
- LK itself does not even manage the media DEVAPC: a whole-image scan of p18 finds no
  reference to 0x1000E000 / 0x1001C000 / 0x10019000; its `[DEVAPC] sec_post_init` is
  effectively a no-op for our purposes.

So kaeru gives us reliable NS-EL1 code on every boot, but **no secure privilege and
no DEVAPC write capability** — nothing the Linux kernel doesn't already have.

## Avenue 6 — An MTEE TA (or other secure service) with an arbitrary write. **Verdict: NO candidate (not exhaustively audited).**

From `mtee-dapc.md`: the DAPC TA's `invoke_command` is a hard stub (returns
0xFFFF000A for every command id); its boot-time `start_devapc` only touches INFRA
DEVAPC and is unreachable from the normal world; the in-binary `set_perm`/`clr_perm`
primitives have zero callers. Adjacent services don't help: EMI TA = EMI MPU (DRAM),
`TZ_M4U` = IOMMU ports, secure CMDQ = fixed secure-playback allow-list. No TA exposes
a general "set DEVAPC permission / re-domain master" command. A memory-corruption bug
in some MTEE TA or SiP handler giving S-EL1/EL3 arbitrary write *would* be a path
(then write SCP MAS_DOM=0 or MM_DOM_RMP), but none is known; a full MTEE TA fuzz/audit
is out of scope and risky, and the DAPC-relevant surfaces are already shown inert.

## Avenue 7 — Debug mode / efuse / secure-boot downgrade. **Verdict: NO.**

LK strings confirm a **fused production secure SoC**: `secure SoC should run signed
%s`, `non secure SoC run with unsigned %s`, `Only try verify %s with prod key on
production device`. There is no software efuse downgrade, and no NS-reachable
"disable DEVAPC" debug: `APC_CON` (0x…F00) is set to 0x80000001 (enabled) by both
preloader and BL31 and lives behind the same `S_RW_ONLY` DEVAPC_AO protection, so NS
cannot clear it. DEVAPC violations are logged, not bypass-able.

## Most promising path forward (ranked)

1. **Keep the AP-side proxy — but offload bulk MM register programming to GCE/CMDQ on
   the AP.** GCE's registers are `NO_SEC` for the AP (D0=0) and GCE as a master has MM
   access (the display stack already uses it), so the AP can build a CMDQ command
   buffer that performs the sequences the SCP needs and fire it in one go, instead of
   trapping and servicing each SCP register access individually. This removes per-access
   AP round-trips (the proxy's main cost) without any DEVAPC change. **This is the only
   viable runtime improvement and does not touch forbidden partitions.** It is *not*
   SCP-direct, but it is the closest reachable outcome.
   - Read-only first experiments (safe): (a) confirm from the live kernel that the AP
     can write an MM slave via CMDQ/GCE (it already does for DISP); (b) measure proxy
     cost vs a CMDQ-batched path for a representative VDEC/VENC register burst.

2. **Secure-world exploitation (high effort, uncertain, out of current scope).** Find a
   runtime-reachable memory-corruption bug in a BL31 SiP handler or an MTEE TA that
   yields EL3/S-EL1 arbitrary write; then a single secure write to SCP `MAS_DOM_0`
   (domain 0) or `MM_DOM_RMP_0` (SCP's domain → MM domain 0) opens full SCP MM access
   for that boot. No such bug is identified; the obvious surfaces (DAPC TA, SiP 207,
   EMI/M4U/CMDQ TAs) are inert. Would need a dedicated vulnerability-research effort and
   would still be per-boot (DEVAPC resets on reboot) unless re-applied each boot from
   kaeru/kernel via the found primitive.

3. **Do nothing new; the proxy is the architectural answer.** Given avenues 1–7, the
   SoC's non-secure software is intended to reach MM only as domain 0 (the AP). The SCP
   being domain 3/FORBID is a deliberate Amazon hardening (self-protection raised from
   ChromeOS's NO_SEC to S_RW_ONLY). Absent a secure exploit, the AP is the SCP's only
   route to MM.

## What would settle the one remaining "unknown" (Avenue 6), safely

DEVAPC resets at reboot, so none of this persists or risks the unit:

- Enumerate every MTEE TA's `invoke_command` command-id surface (open a KREE session to
  each UUID via raw SMC over BL31's MTEE SPD, sweep command ids, record return codes) to
  confirm no TA has a functional register-write/`set-permission` command. Expected: all
  DAPC-relevant TAs return NOT_SUPPORTED (0xFFFF000A), matching `mtee-dapc.md`.
- Static-audit the handful of SiP handlers that take caller-supplied addresses
  (0xC2000209/20A EMI-MPU, 0xC2000201 MCUSYS, 0xC2000211 MCSI) for a range-check bug
  that could be coerced into an arbitrary secure write. (All three are range-checked in
  the disassembly; included only to document the audit.)

## Source artifacts
- `scratchpad/tfa/dec/atf_dram.bin` (BL31 DRAM part, base 0x54600000): INFRA perm
  table 0x5460B6F8 (D0,D4,D1 / slave; D7 FORBID set separately), MM table 0x5460B128
  (D0, then D3 FORBID forced), `devapc_init` 0x546047FC, `set_module_apc` 0x54604744.
- `backups/*/partitions/p18-lk.img`: kaeru/LK (AArch32), 32-bit ARM, load base in the
  0x4Bxx–0x4Dxx range; unlock/hook strings as quoted above.
- ChromeOS reference: TF-A `plat/mediatek/mt8183/drivers/devapc/{devapc.c,devapc.h}`
  — register map, `MASTER_SCP=0`, `set_master_domain(MASTER_SCP, DOMAIN_1)`, and the
  permission tables used to validate alignment and names.
