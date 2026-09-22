package security

import (
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/HuskerMinion/techo5/echod/internal/layout"
)

// Where SSH keeps what it needs. The keys live on userdata, survive slot changes, and are never
// part of an image; the host keys are on userdata for the same reason (/etc/dropbear links there).
//
// KeysDir and HomeSSHDir are variables rather than constants so a test can point the pair at a
// temporary directory and exercise the arrangement between them, which is the whole of rootssh.go.
var (
	KeysDir = layout.StateDir + "/ssh"

	// HomeSSHDir is root's own .ssh, and the only place dropbear ever looks for an authorized key.
	// On a slot boot it is a symlink to KeysDir, put there by the rootfs itself; in the rescue
	// environment it is a real directory that may hold the boot image's own key, with KeysDir's file
	// appended into it at boot. rootssh.go is the whole of what the daemon does about the difference.
	HomeSSHDir = "/root/.ssh"
)

const (
	hostKeys = "/data/techo5-linux/dropbear"
	pidFile  = "/run/dropbear.pid"

	// slotMarker is left by the initramfs on a normal slot boot. The rescue environment runs the
	// daemon too, and has its own SSH server that this must not take away.
	slotMarker = "/run/techo5/slot"
)

// keysFile is what the daemon writes; homeKeysFile is what dropbear reads. On a device where the
// arrangement is intact they are the same file, and the point of checking is that nothing says so.
func keysFile() string     { return filepath.Join(KeysDir, "authorized_keys") }
func homeKeysFile() string { return filepath.Join(HomeSSHDir, "authorized_keys") }

// sshAvailable reports whether the daemon manages SSH here: a slot boot of the Linux image.
func sshAvailable() bool {
	if layout.OnAndroid() {
		return false
	}
	if _, err := os.Stat(slotMarker); err != nil {
		return false
	}
	_, err := exec.LookPath("dropbear")
	return err == nil
}

// sshPID is the listening server's process, or 0. Sessions are children with the same name; the
// pid file names only the listener.
func sshPID() int {
	b, err := os.ReadFile(pidFile)
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 0 {
		return 0
	}
	comm, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/comm")
	if err != nil || strings.TrimSpace(string(comm)) != "dropbear" {
		return 0
	}
	return pid
}

func sshRunning() bool { return sshPID() != 0 }

// startSSH starts dropbear, which puts itself in the background. Keys only: -s turns password
// logins off for every account.
func startSSH() error {
	// A device that booted with the chain from KeysDir to root's .ssh already broken would open the
	// port and then refuse every key. Check and mend it here too, so it is reported and repaired
	// without waiting for somebody to push a key at a device they can no longer reach.
	adoptManaged()
	ensureDropbearSees(readKeys())
	if err := os.MkdirAll(hostKeys, 0o700); err != nil {
		return err
	}
	out, err := exec.Command("dropbear", "-R", "-s", "-p", "22", "-P", pidFile).CombinedOutput()
	if err != nil {
		return errors.New(strings.TrimSpace(err.Error() + ": " + string(out)))
	}
	return nil
}

// stopSSH stops the listener. Sessions already open are their own processes and carry on, so
// switching SSH off from inside an SSH session does not cut it.
func stopSSH() error {
	pid := sshPID()
	if pid == 0 {
		return nil
	}
	return syscall.Kill(pid, syscall.SIGTERM)
}

// readKeys is what is already on the device, and it forgives what setting a key does not.
//
// parseKeys refuses a whole set on one bad line, which is right at the door: somebody is watching,
// and the old keys stay untouched. It is wrong here. A file written before the checks got stricter
// can hold a line nothing could log in with - a key pasted on top of its own type did exactly that
// here - and failing the read would turn that into zero keys, which settleSSH reads as "nobody can
// log in" and answers by not starting the server at all. One bad line would take SSH away from a
// device that still has a good key in the same file, on an update, with nothing said about it.
//
// So every line is taken on its own: the good ones are kept and the rest are counted out loud.
func readKeys() []string {
	b, err := os.ReadFile(keysFile())
	if err != nil {
		return nil
	}
	var keys []string
	var dropped int
	for _, line := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		one, err := parseKeys(line)
		if err != nil || len(one) != 1 {
			dropped++
			continue
		}
		keys = append(keys, one[0])
	}
	if dropped > 0 {
		slog.Warn("ssh: lines in the authorized keys file are not keys anything could log in with; the rest still work",
			"kept", len(keys), "dropped", dropped, "file", keysFile())
	}
	return keys
}

// writeKeys replaces the file through a temporary one; dropbear refuses keys whose file or
// directory others can write, so both are the owner's alone.
func writeKeys(keys []string) error {
	if err := os.MkdirAll(KeysDir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(KeysDir, 0o700); err != nil {
		return err
	}
	if len(keys) == 0 {
		err := os.Remove(keysFile())
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	tmp := filepath.Join(KeysDir, ".authorized_keys.new")
	if err := os.WriteFile(tmp, []byte(strings.Join(keys, "\n")+"\n"), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, keysFile())
}
