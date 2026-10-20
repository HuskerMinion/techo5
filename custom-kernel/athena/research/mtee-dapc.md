# MTEE DAPC trusted app — can it reopen the MM DEVAPC for the SCP?

Offline reverse-engineering of the decrypted MTEE image (MediaTek TEE, MT8183).
Companion to `tfa-devapc-psci.md`: BL31 sets DEVAPC so the SCP master (domain 3 /
MM domain 3) has FORBID on every multimedia slave, and no BL31 SMC re-domains or
re-permits. The one remaining avenue was "the DAPC trusted app inside MTEE".

## Verdict

**Not possible.** The DAPC trusted app in MTEE is a debug/test stub. Its
`invoke_command` entry rejects *every* command unconditionally, it has no
set-master-domain / set-slave-permission / dump-violation command, and the only
code that programs DEVAPC runs once at MTEE boot from a static built-in config and
is not reachable from the normal world. The in-binary permission-setter helpers
exist but have **zero callers** (dead code). There is therefore no SMC/KREE
sequence — from Fire OS's TEE client or from raw SMCs on a mainline kernel — that
can give MM domain 3 (the SCP) access to VENC/VDEC/IMGSYS/FDVT/DIP or move the SCP
master to domain 0. The AP-serves-SCP workaround (`scp-mmproxy`) remains the only
mechanism.

## Evidence

### Image / load base
Decrypted TEE blob: `scratchpad/tfa/dec/tee.bin` (2.56 MB), decrypted by
`scratchpad/tfa/dec_tee.py` from `backups/<unit>/partitions/p3-tee1.img`.
Link/load base recovered as **0x10000000** (string-pointer fit: e.g. the word at
file offset 0x1a9e60 equals 0x101a9e90 which points at `"DAPC TA"`).

### The DAPC trusted app
TA descriptor at VA **0x101a9e60**, struct `{name, uuid, create, open, close,
invoke}` (ordering confirmed against the Crypto TA at 0x101aa350):

```
0x101a9e60  -> 0x101a9e90  "DAPC TA"
0x101a9e68  -> 0x101a9e98  "2603779b-b2a6-4f7e-8783-62ab11b383fd"   (UUID)
0x101a9e70  -> 0x1000adc4  create        (MTEE_DapcInit)
0x101a9e78  -> 0x1000ade4  open_session  (MTEE_DapcSession create)
0x101a9e80  -> 0x1000ae04  close_session (MTEE_DapcSession close)
0x101a9e88  -> 0x1000ae10  invoke_command
```
Registered via the TA-pointer table (pointer to 0x101a9e60 at 0x1025e538,
alongside Crypto/EMI/Keymaster/DRM TAs).

`create`, `open`, `close` are pure debug prints:
```
1000adc4: adrp/add x0, "KSY Debug: [DAPC] [TEE] MTEE_DapcInit start..."
          bl printf ; mov w0, wzr ; ret            ; returns 0
1000ade4: ... "MTEE_DapcSession create..." ; mov w0, wzr ; ret
1000ae04: ... "MTEE_DapcSession close!!!" ; b printf (tail)
```

`invoke_command` — the only runtime entry a normal-world caller can drive — is a
hard stub:
```
1000ae10: mov  w0, #-0xfff6      ; 0xFFFF000A  (TEEC_ERROR_NOT_SUPPORTED)
1000ae14: ret
```
There is **no command-id switch, no shared-memory parsing, no session routing**.
Every `TeeServiceCall`/`invoke` to this TA returns 0xFFFF000A. (For comparison the
Crypto TA invoke at 0x1000bdd4 is likewise a stub, `mov w0,#-0x10000; ret` — these
"TestSession" TAs ship as stubs on production MTEE.)

### Where DEVAPC is actually programmed: boot only
`start_devapc` at **0x1000ae18** prints `"[DAPC] [TEE] DAPC start_devapc()
starts."` then writes the INFRA DEVAPC controller (MTEE-internal VA ≈ 0xF000E000 /
0xF0010000 / 0xF0200000 range), setting the global enable (value 0x80000001 to
`+0xED4`, `+0x3000`, `+0x0`; 0x80000000 to `+0xF00`) and reads back the
`INFRA VIO_MASK`/`INFRA VIO_STA` registers for logging. It touches **INFRA DEVAPC
only** — not the multimedia DEVAPC that gates VENC 0x17020000 / VDEC 0x16000000 /
IMGSYS-FDVT 0x1502xxxx / DIP 0x15022000.

`start_devapc` is referenced solely by a **boot init-function table** (pointer at
0x1025e4c8, among 0x1016f430, 0x10007440, 0x1000c0a8, …). It has **no `BL`
caller** and is **not** one of the DAPC TA's four entry points. It runs once at
MTEE init and cannot be triggered from the normal world.

### The permission-setter helpers are dead code
Two helpers that do encode slave permissions live right after `start_devapc`:
```
1000af18  set_perm:  args (w0=slave idx <=0x150, w1=domain <=7/<=3, w2=perm 0..3)
                     computes base 0xF000E000 + (dom<<8) + (idx>>4)<<2,
                     RMW: clears 2 bits at (idx&0xf)*2, ORs in perm   -> writes perm
1000afd4  clr_perm:  same addressing, forces the 2-bit field to 0
```
These are exactly "set slave permission for (domain, slave)" primitives — but a
whole-image scan for `BL` targets and for code/data pointers to 0x1000af18 and
0x1000afd4 finds **none**. They are compiled-in but unreferenced; nothing in the
TA dispatch, in any SMC handler, or in `start_devapc` calls them. There is no
"set master domain" / "domain remap" primitive at all, and no violation-dump
command exposed.

### Normal-world → MTEE path (for completeness)
Fire OS reaches MTEE through MediaTek's KREE/MTEE client (`drivers/misc/mediatek/
mtee/tz_mod.c`, syms `KREE_CreateSession`, `KREE_TeeServiceCall`,
`KREE_RegisterSharedmem`, …), which builds a message in shared memory and SMCs
into BL31's MTEE SPD (`services/spd/mtee/mtee_main.c`, handlers `mtee_smc_handler`,
`mtee_std`, `mtee_fast`; also prints `INFO: [DAPC] Walk TEE`). A mainline kernel
*could* reimplement this KREE transport with raw SMCs and open a session to UUID
`2603779b-b2a6-4f7e-8783-62ab11b383fd`. But because the DAPC TA's `invoke_command`
is the 0xFFFF000A stub above, any such call returns NOT_SUPPORTED and changes
nothing. Reachability is therefore moot — there is no command to reach.

Adjacent TEE services were checked and do **not** help:
- EMI TA (`f80dab1a-…`): programs the **EMI MPU** (DRAM region protection) and
  reports violation master/domain IDs — not the register-bus DEVAPC permissions.
- `TZ_M4U`: configures the **M4U/IOMMU** secure ports — address translation, not
  DEVAPC domains.
- Secure CMDQ (`[CMDQ_TZ] … to disable DAPC`, `enginesNeedDAPC[0x%llx]`): toggles
  DAPC only for a fixed built-in allow-list of secure-playback engines during
  secure command-queue execution; it is not a general "grant domain 3 access to
  slave X" interface and is gated to those engines.

## Full avenue survey

The complete survey of every way to give the SCP direct MM access (SiP SMCs, AP
rewriting the SCP master domain / MM remap, bus-reroute / GCE-CMDQ proxy, EL2, the
saiyaku/kaeru unlock's privilege and boot order, other TEE TAs, debug/efuse) is in
`devapc-scp-access.md`. Bottom line there: Amazon hardened the DEVAPC self-protection
to `S_RW_ONLY` for the AP domain (ChromeOS leaves it `NO_SEC`), so no non-secure path
— kernel, EL2, or kaeru (which runs NS-EL1 in LK, after BL31 locks DEVAPC) — can
write DEVAPC_AO. The AP-side proxy stays; the only runtime improvement is offloading
its bulk MM writes to GCE/CMDQ on the AP side.

## Why not, in one line
The DAPC trusted app ships as a debug stub (invoke = `return 0xFFFF000A`), DEVAPC
is programmed only at boot from static config via a non-exported init function,
the multimedia DEVAPC that guards VENC/VDEC/IMG/DIP isn't managed by MTEE at
runtime at all, and the in-binary permission primitives have no callers — so no
SMC/KREE command can re-domain or re-permit the SCP.

## Minimal non-persistent experiment (confirmation only)
DEVAPC resets at reboot, so nothing below persists. This only *confirms* the
negative finding on real hardware; it does not open any access.

1. On the device (mainline or Fire OS), open a KREE session to the DAPC TA:
   UUID `2603779b-b2a6-4f7e-8783-62ab11b383fd` via `KREE_CreateSession`
   (or the equivalent raw KREE-over-SMC message through BL31's MTEE SPD).
2. Issue `KREE_TeeServiceCall` with any command id and any param types.
3. Expected result: return code **0xFFFF000A** (TEEC_ERROR_NOT_SUPPORTED) for
   every command id — proving the TA has no functional command surface.

No register is written, no partition is touched, no unit is modified. If (and only
if) a future/different TEE build returned success for some command id, the
`set_perm` encoding documented above (base `0xF000E000 + (domain<<8) +
((slave_idx>>4)<<2)`, 2-bit field at `(slave_idx&0xf)*2`, perm 0..3) would be the
primitive to target — but on this image it is unreachable.

## Source artifacts
- `scratchpad/tfa/dec/tee.bin` (decrypted MTEE), load base 0x10000000
- `scratchpad/dapc/dis.py` (annotating aarch64 disassembler), `dapc/dapc_svc.S`
- DAPC TA UUID 2603779b-b2a6-4f7e-8783-62ab11b383fd; descriptor 0x101a9e60;
  invoke stub 0x1000ae10; boot `start_devapc` 0x1000ae18; dead setters 0x1000af18 /
  0x1000afd4
