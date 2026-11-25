# Sourced by /etc/profile on the ThinkSmart View store root: the rescue environment's login shells
# (SSH, and the USB serial console where the kernel has the gadget). See tools/linux/rescue-profile.sh
# for why this is not /etc/motd.

cat <<'EOF'

  TECHO5 rescue environment (ThinkSmart View)
  -------------------------------------------
  No slot was started this boot, so you are on the root of the slot store,
  read-only. Nothing has been erased.

    STORE=/ slotctl status          what the slots hold and why none booted
    STORE=/ slotctl switch a        boot slot a next (a bad slot goes back on trial)
    STORE=/ slotctl install <rootfs.tar.gz>   a rootfs into the other slot, on trial
    cat /data/techo5-linux/init.log     what this boot's /sbin/init did
    cat /data/techo5-linux/rescue.log   the state it was left in
    vi /data/techo5-linux/wpa_supplicant.conf   add a Wi-Fi network
    reboot -f                       a plain `reboot` does nothing here: PID 1 is a script

EOF

PS1='techo5-rescue:\w # '
export PS1
