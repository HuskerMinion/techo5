# Sourced by /etc/profile in the rescue environment, which is where a login shell on the USB serial
# console lands. It is the only thing that greets anybody there.
#
# Not /etc/motd: the console runs `sh -l`, not login or getty, so nothing ever prints motd. Alpine's
# own motd is still in the image and still says "Welcome to Alpine! ... setup-alpine", which is worse
# than nothing - it describes a system this is not. /etc/issue is never shown either, for the same
# reason. A file under /etc/profile.d is the one thing on this path that actually runs.

cat <<'EOF'

  TECHO5 rescue environment
  -------------------------
  The system this device normally boots did not start, so you are in the
  rescue environment instead. Nothing has been erased and your data is intact.

  Where you are:   an initramfs in the boot partition, not a slot.
  What is here:    slotctl, the store under /store, logs in /data/techo5-linux.

    STORE=/store slotctl status     what the slots hold and why none booted
    cat /data/techo5-linux/rescue.log   what this boot did, step by step
    reboot -f                       a plain `reboot` does nothing here: PID 1 is a script

  Installing? This is the expected place to be partway through.
  Not installing? docs/install.md in the TECHO5 repository has the way out.

EOF

# So every prompt says where you are. The kernel leaves the hostname unset, and the default prompt
# reads "(none):/#", which tells somebody landing here by cable precisely nothing.
PS1='techo5-rescue:\w # '
export PS1
