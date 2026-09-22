package security

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// Writing the keys is only half of letting somebody in. The daemon writes KeysDir/authorized_keys,
// but dropbear never reads that path: it reads the home directory of the account logging in, which
// is /root/.ssh/authorized_keys. Boot scripts are what join the two, and they do it differently per
// device — the Dot makes /root/.ssh a symlink to KeysDir, the Show and the Spot keep it a real
// directory holding the key baked into the image and append KeysDir's file into it at boot.
//
// Nothing used to check that the join had worked, so a unit whose boot script had not run the way
// it should logged "authorized keys replaced" and then refused every one of those keys, and the
// only way back in was a USB serial console in the same room as the device. What follows reads the
// file dropbear will really use, says loudly when the keys are not in it, and mends the join where
// mending it cannot lose a key.

// ensureDropbearSees checks the keys are in the file dropbear reads and repairs the arrangement
// when they are not. It never returns an error and never fails a caller: the key write it follows
// has already succeeded, and the keys are safe on userdata whatever happens here. Its whole output
// is the log, which is what carries a broken device into the diagnostics bundle.
func ensureDropbearSees(keys []string) {
	if len(keys) == 0 {
		return // nothing to look for; an empty set removes nobody from an image's own key either
	}
	missing, found, err := missingFromHome(keys)
	if err == nil && len(missing) == 0 {
		return
	}

	attrs := []any{
		"daemon_writes", keysFile(), "dropbear_reads", homeKeysFile(), "root_ssh", describeHome(),
		"keys", len(keys), "missing", len(missing), "found_in_file", found, "missing_keys", labels(missing),
	}
	if err != nil {
		attrs = append(attrs, "err", err)
	}
	slog.Error("ssh: the keys were saved but dropbear cannot see them: it reads root's own authorized_keys, not the daemon's, and what joins the two is broken", attrs...)

	how, err := repairHome(keys)
	if err != nil {
		slog.Error("ssh: the keys are saved but dropbear still cannot see them, and this cannot be repaired from here: nobody can log in with them until it is put right on the device itself",
			"daemon_writes", keysFile(), "dropbear_reads", homeKeysFile(), "root_ssh", describeHome(), "repair", how, "err", err)
		return
	}
	missing, found, err = missingFromHome(keys)
	if err != nil || len(missing) > 0 {
		attrs := []any{
			"daemon_writes", keysFile(), "dropbear_reads", homeKeysFile(), "root_ssh", describeHome(),
			"repair", how, "keys", len(keys), "missing", len(missing), "found_in_file", found,
		}
		if err != nil {
			attrs = append(attrs, "err", err)
		}
		slog.Error("ssh: repairing root's authorized_keys did not help, and the keys are still not in the file dropbear reads: nobody can log in with them", attrs...)
		return
	}
	slog.Info("ssh: root's authorized_keys was not carrying the daemon's keys and has been repaired; dropbear can see them now",
		"dropbear_reads", homeKeysFile(), "root_ssh", describeHome(), "repair", how, "keys", len(keys), "found_in_file", found)
}

// missingFromHome reads the file dropbear will use and reports which of the keys are not in it,
// along with how many key lines it holds in all. A file that cannot be read counts as holding none.
func missingFromHome(keys []string) (missing []string, found int, err error) {
	b, err := os.ReadFile(homeKeysFile())
	if err != nil {
		return keys, 0, err
	}
	have := make(map[string]bool)
	for _, line := range strings.Split(string(b), "\n") {
		line = normalizeKey(line)
		if line == "" {
			continue
		}
		found++
		have[line] = true
	}
	for _, k := range keys {
		if !have[normalizeKey(k)] {
			missing = append(missing, k)
		}
	}
	return missing, found, nil
}

// repairHome puts the keys where dropbear will find them, in the way that suits what is already
// there. It says which way it took, for the log. The rules are the ones that cannot cost anybody a
// key: a missing entry or a symlink aimed at the wrong place is not data and can be replaced, and a
// real directory is somebody else's (the image's own key lives in one) and is only added to.
func repairHome(keys []string) (how string, err error) {
	// Whatever else happens, the daemon's own directory has to be there and be the owner's alone,
	// because a symlink repair makes it the very directory dropbear reads.
	if err := os.MkdirAll(KeysDir, 0o700); err != nil {
		return "none", err
	}
	if err := os.Chmod(KeysDir, 0o700); err != nil {
		return "none", err
	}

	fi, err := os.Lstat(HomeSSHDir)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return "linked", linkHome()
	case err != nil:
		return "none", err
	case fi.Mode()&os.ModeSymlink != 0:
		target, rerr := os.Readlink(HomeSSHDir)
		if rerr == nil && filepath.Clean(target) == filepath.Clean(KeysDir) {
			// Aimed at the right place already, so what is wrong is the permissions dropbear
			// insists on, or a file that went missing underneath it.
			return "permissions", fixPerms(KeysDir)
		}
		// A symlink is a pointer, not data; removing one loses nothing, including a dangling one.
		if err := os.Remove(HomeSSHDir); err != nil {
			return "relinked", err
		}
		return "relinked", linkHome()
	case fi.IsDir():
		return "merged", mergeHome(keys)
	default:
		// A plain file where the directory should be. Nothing here knows what it is, so it stays.
		return "none", fmt.Errorf("%s is a file, not a directory or a symlink", HomeSSHDir)
	}
}

// linkHome is the Dot's arrangement: root's .ssh is the daemon's directory under another name, so
// there is only ever one file and it cannot drift out of step with the other.
func linkHome() error {
	if err := os.MkdirAll(filepath.Dir(HomeSSHDir), 0o700); err != nil {
		return err
	}
	if err := os.Symlink(KeysDir, HomeSSHDir); err != nil {
		return err
	}
	return fixPerms(KeysDir)
}

// mergeHome is the Show and the Spot's arrangement: a real directory with a key built into the
// image in it, which somebody may be the only way into their own device with. So the file is added
// to and never replaced, and a key already in it is left where it is rather than written twice.
func mergeHome(keys []string) error {
	b, err := os.ReadFile(homeKeysFile())
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	var lines []string
	have := make(map[string]bool)
	for _, line := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		lines = append(lines, line)
		have[normalizeKey(line)] = true
	}
	for _, k := range keys {
		if have[normalizeKey(k)] {
			continue
		}
		lines = append(lines, k)
		have[normalizeKey(k)] = true
	}

	// Through a temporary file in the same directory, the way writeKeys does it: a half-written
	// authorized_keys would lock out the very people this is trying to keep in.
	tmp := filepath.Join(HomeSSHDir, ".authorized_keys.new")
	if err := os.WriteFile(tmp, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, homeKeysFile()); err != nil {
		os.Remove(tmp)
		return err
	}
	return fixPerms(HomeSSHDir)
}

// fixPerms is what dropbear insists on before it will read a key at all: the directory and the file
// the owner's alone. A key written through a group-writable directory is a key silently refused.
func fixPerms(dir string) error {
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	err := os.Chmod(filepath.Join(dir, "authorized_keys"), 0o600)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// describeHome says what root's .ssh is right now, for the log: the thing somebody reading the
// diagnostics bundle a week later needs first, and the one detail no other line carries.
func describeHome() string {
	fi, err := os.Lstat(HomeSSHDir)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return "missing"
	case err != nil:
		return "unreadable"
	case fi.Mode()&os.ModeSymlink != 0:
		target, rerr := os.Readlink(HomeSSHDir)
		if rerr != nil {
			return "symlink"
		}
		if _, serr := os.Stat(HomeSSHDir); serr != nil {
			return "dangling symlink to " + target
		}
		return "symlink to " + target
	case fi.IsDir():
		return "directory"
	default:
		return "file"
	}
}

// normalizeKey is how two spellings of the same key are told to be the same one: the fields with
// whatever spacing they came with squeezed out. Comments count, because a key line differing only
// in its comment is a line somebody meant to change.
func normalizeKey(line string) string { return strings.Join(strings.Fields(line), " ") }

// labels names keys in a log the way the screen names them — the comment, or failing that the type.
// Key material never goes in a log.
func labels(keys []string) []string {
	var out []string
	for _, k := range keys {
		out = append(out, keyLabel(k))
	}
	return out
}
