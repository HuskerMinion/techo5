## Echo Show 8 2nd gen (athena)

TECHO5 now runs on the 2nd gen Echo Show 8 with its own 64-bit kernel.

The first OS other than Fire OS on this Echo, and the first Echo Show on a mainline kernel that I can find.

What works:

- Screen and touch. The screen has a real display driver now.
- All four microphones and the speaker.
- Bluetooth audio with your phone.
- The light sensor.
- Wi-Fi. Set it up on the screen, or pass `--wifi` to the installer.

The speaker uses Amazon's own tuning for the amp. TECHO5 doesn't ship it. The installer reads it from
your unit's backup and checks it against the known versions.

### Which screens

Amazon used several screens in this model. TECHO5's kernel has settings for ten of them, and two are
tested on units: the ILI9881C with BOE glass and the JD9365 with KD/HSD glass. The installer checks
your unit and stops on any other screen: add `--force-panel` to try it, or run TECHO5 on Amazon's
kernel (`--kernel amazon`), which drives every screen.

### How to install

1. Update the Show to the latest Fire OS (7080).
2. Unlock it with saiyaku-athena ([XDA thread](https://xdaforums.com/t/unlock-root-twrp-unbrick-amazon-echo-show-8-2nd-gen-2021-athena.4804958/)).
   You end up in TWRP.
3. Run `python3 tools/install-athena.py --name Kitchen` with the unit in TWRP.
4. Add it in Home Assistant with the key the installer prints.

The installer backs up every partition first. Keep that backup. It's your way back to Fire OS, and
the speaker tuning comes from it.

Full steps: docs/install-athena.md.

### Known issues

- Light sensor values aren't calibrated yet.
- The microphone beamformer is off.
- Only Fire OS 7080's speaker tuning is known. On another version the installer stops and says so.

### Kernel source

TECHO5's kernel for this Show is Linux 6.18.56 with TECHO5's patches. The patches, device tree, config
and build script are in the repository at this release's tag:
[tools/linux/athena/kernel](https://github.com/HuskerMinion/techo5/tree/<version>/tools/linux/athena/kernel).
The matching full kernel source, as a tarball, is attached to this release.

### Other Shows

- The 1st gen Show 8 no longer reads its touchscreen (fts_ts) for buttons. It never had buttons there.

### Credits

The unlock is saiyaku-athena and kaeru, by R0rt1z2 with bengris32 and FieryFlames (jackmthws).
Thanks to nuno407 for the META mode idea.
[XDA thread](https://xdaforums.com/t/unlock-root-twrp-unbrick-amazon-echo-show-8-2nd-gen-2021-athena.4804958/).

The Linux port is by HuskerMinion.
