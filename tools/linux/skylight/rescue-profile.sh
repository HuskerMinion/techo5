# Sourced by /etc/profile on the Skylight store root: the rescue environment's login shells (USB
# serial console and SSH). See tools/linux/rescue-profile.sh for why this is not /etc/motd.

cat <<'EOF'

  TECHO5 rescue environment (Skylight Calendar 15)
  ------------------------------------------------
  No slot was started this boot, so you are on the root of the system
  partition (the slot store), read-only. Nothing has been erased.

    STORE=/ slotctl status          what the slots hold and why none booted
    STORE=/ slotctl switch a        boot slot a next (a bad slot goes back on trial)
    STORE=/ slotctl install <rootfs.tar.gz>   a rootfs into the other slot, on trial
    STORE=/ slotctl grow /dev/mmcblk0p14      fill techo5-store (done at every boot)
    cat /data/techo5-linux/rescue.log   what this boot did
    vi /data/techo5-linux/wpa_supplicant.conf   add a Wi-Fi network
    reboot -f                       a plain `reboot` does nothing here: PID 1 is a script

  USB: this serial port, and RNDIS at 172.16.42.1 (ssh root@172.16.42.1).

EOF

PS1='techo5-rescue:\w # '
export PS1
