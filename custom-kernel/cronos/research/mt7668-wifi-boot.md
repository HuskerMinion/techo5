# MT7668 Wi-Fi that does not come up at boot (Show 5, mainline)

Status (2026-10-20): root cause not reached; mitigated in the boot scripts (t5_wifi_reset), and every
failure now leaves its kernel log on the unit. 150 boots with the change: Wi-Fi up on all of them, and
none needed the reset, so the recovery is proven only on a simulated failure.

## What happens

About 1 boot in 50 the unit has no network after boot, and the network keeper reboots it 15
minutes later, which has always fixed it. Counted from the daemon log on the unit
(`techo5.log`, `techo5.log.1`): 5 of about 260 daemon starts, 2026-10-17 to -19, on kernels and
rootfs from mainline.5 to .22. Each of those 5 starts is stamped 1970: the clock was never set,
because there was no network.

The bad boots were not first boots after a slot install, and they happened both in other
sessions' reboot loops and in normal use. Today's two came at 13:48 and 15:28.

What the timing says (no kernel log survived from any of them; see "Evidence lost" below):

- echod started at 130 s of uptime, against about 27 s on a good boot. `techo5-run` waits up to
  90 s for an address after the boot script ends, so boot.sh ended at about 38 s, against 26 s.
  That rules out the 60 s association wait (WIFI_WAIT): t5_wifi_up gave up early, at the driver.
- The reboot came at 938 s. 15 keeper rounds of about 60 s each, starting at about 35 s, fit
  that. A round in which wlan0 exists waits out WIFI_WAIT, which would have made rounds of about
  2 min and a 30 min reboot. So wlan0 never appeared: insmod failed, or the driver loaded and its
  probe made no wlan0.
- echod reported `bluetooth up` on the 13:48 boot. So the SDIO card enumerated and btmtksdio
  (function 2) bound. What failed is the Wi-Fi half (function 1, mt76x8_wlan's `mtk_sdio_probe`).

On good boots (38 kept dmesgs from today's reboot loops, all good):

- the card enumerates at about 2.6 s (once at 0.66 s);
- btmtksdio's setup finishes at about 3.45 s (once, 2.4 s long, at 3.10 s);
- mt76x8_wlan probes from 3.8 s to 5.5 s, `done(0)`.

So the two drivers never overlapped on a kept boot. The Wi-Fi driver waits up to 5 s for the ROM
patch semaphore that Bluetooth's firmware download holds (`wlanPatchIsDownloaded`). A race there is
possible but not shown.

## Recovery, tested on the unit

Each test took the driver down on a good boot and brought it back with t5_wifi_up:

| how | wlan0 with an address after |
| --- | --- |
| unbind and bind SDIO function 1 from `wlan` | 10 s |
| `rmmod mt76x8_wlan`, insmod | 16 s |
| rmmod, unbind and bind the SDIO host `11250000.mmc` (mtk-msdc) | 18 s |

Unbinding the host does power the chip off: `wifi_3v3`, the host's vmmc (GPIO 32), goes to
`disabled`, the card is removed, and on bind it is back 1 s later. Bluetooth probes again
(`hci0: Device setup in 838 ms`), so a host reset belongs before t5_bt_up, where the boot does it.

Simulated failure (the host unbound, so the driver loads to no card, then the new t5_wifi_up):
`driver loaded but no wlan0`, a reset, `card back after 1s`, `driver loaded, wlan0 present`, and an
address 28 s after the start.

## The change (tools/linux/techo5-lib.sh, rootfs/etc/techo5/boot.sh)

- `t5_wifi_up`: when the driver makes no wlan0 (or insmod fails), `t5_wifi_reset` keeps `dmesg` in
  `/data/techo5-linux/wifi-fail/` (the newest 5), unloads the driver, powers the chip off and on
  through the SDIO host, and the driver is loaded again. It does this twice a boot at most, counted in
  `/run/techo5/wifi-resets`. The keeper's 15-minute reboot stays as the last resort.
  `WIFI_SDIO_HOST` (boot.sh's Show 5 defaults) names the host. Where that path does not exist
  (Amazon's kernels, the Show 8), nothing changes.
- The kernel's crash records (pstore) are copied with this boot's id in the name. The cronos kernel
  keeps its console in ramoops (128 KB), so every warm restart leaves the boot before's kernel log,
  from about 3 s on, which covers the Wi-Fi probe. They were named by the clock, which reads 1970
  when they are copied, so each boot's copy replaced the last, and the failed boots' logs were lost.
  The newest 20 are kept.

## 150 boots with the change (2026-10-20)

Show 5, kernel #40, rootfs mainline.29 (batches 1-4, 120 boots) and mainline.35 (batch 5, 30 boots;
the same Wi-Fi code). The loop rebooted over ssh about every 48 s, read each boot's `/run/boot.log`,
`/run/techo5/wifi-resets` and `/data/techo5-linux/wifi-fail`, and collected the per-boot ramoops
records every 10 minutes.

- **Wi-Fi: 150 of 150 boots got an address**, the earliest at 8.9 s, the median at 9.0 s and the
  latest at 18.1 s (the slower ones are a second supplicant scan). No boot needed the in-place reset:
  `wifi-resets` was 0 every time and `wifi-fail/` was never made. So the recovery path still has only
  the simulated failure above behind it.
- Zero failures in 150, against 5 in about 260 before (17-19 Oct), is not proof that the fault is gone:
  at 2% a run of 150 clean boots happens about 1 time in 20. If the cause changed, it changed between
  the kernels (#28-#36 then, #40 now) or the boot timing. Nothing here shows which.
- **Bootloader display stalls: 10** (fps=6000), about 6% of the 160 boots the bootloader made. Each
  was a boot that took 90-100 s to reach "boot script done", against 30-40 s for the rest. The 4 of
  them with a collected record say so, and their Wi-Fi came up too (addresses at 69-78 s).
- **boot.sh's display restart is late.** busybox init does not act on `reboot` until the sysinit step
  (boot.sh) has ended. So a stalled boot logs "restarting (1 of 2)", sleeps 60 s, logs "unstarted
  again; carrying on" (wrongly: it is the first), runs the rest of its boot (Wi-Fi, Bluetooth, about
  15 s), and only then reboots, at about 80-90 s instead of at once. The next boot was good each
  time. `reboot -f` after the `sync`, or ending boot.sh there, would restart at once. Not changed here.
- The ramoops console is a 128 KB ring. A boot that runs for long fills it with the microphone
  driver's `amazon-spi-pcm ... FPGA overrun` lines: one record, from a 44-minute boot outside the loop,
  starts at 1074 s, and its Wi-Fi probe is gone. So only short failed boots (the keeper reboots at
  15 min) keep their probe there; `wifi-fail/` is taken at the failure itself and does not depend on it.
- One boot answered ssh only after about 5 minutes, with Wi-Fi up at 9 s. Not explained; its record was
  not collected.

## Evidence lost, and how to get it next time

None of the 5 failed boots left a kernel log: the keeper reboots without one, and the pstore copy was
overwritten at the next boot. With this change, a failure leaves two:
`/data/techo5-linux/wifi-fail/*.txt` and the next boot's `crash/*-console-ramoops-0`. The first one
says why the probe failed (firmware, patch semaphore, SDIO errors) and decides whether a driver fix
is worth it.
