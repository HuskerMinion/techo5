package security

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// Writing the keys is only half of letting somebody in. The daemon writes KeysDir/authorized_keys,
// but dropbear never reads that path: it reads the home directory of the account logging in, which
// is /root/.ssh/authorized_keys. What joins the two is not the same on every boot, and it is the
// root filesystem rather than the model that decides which:
//
//   - A slot boot runs the rootfs tools/linux/mkrootfs.sh builds, where /root/.ssh is a symlink to
//     KeysDir. There is only ever one file, so the two cannot drift apart. This is the normal case
//     on every unit, Dot, Show and Spot alike.
//   - A boot that finds no usable slot stays in the initramfs as the rescue environment, where
//     /root/.ssh is a real directory. The image may carry a key of its own in it, the one
//     tools/linux/build-image.sh copies in (the image published with releases has none), and
//     network_up in tools/linux/init appends KeysDir's file into that directory on each boot rather
//     than linking the two. A unit whose symlink has been replaced by hand ends up the same shape.
//
// Nothing used to check that the join had worked, so a unit whose boot script had not run the way
// it should logged "authorized keys replaced" and then refused every one of those keys, and the
// only way back in was a USB serial console in the same room as the device. What follows reads the
// file dropbear will really use, says loudly when the keys are not in it, and mends the join where
// mending it cannot lose a key.
//
// The real directory can be added to but not replaced, because the key in it may be somebody's only
// way into their own unit. Adding was all this ever did, though, which made a removal in Home
// Assistant no removal at all: the key stayed in the file dropbear reads and went on working.
// Nothing on disk says which lines the daemon put there, so the daemon writes down the ones it did
// (managedFile) and rewrites exactly those — a line in the record and no longer in the key set goes,
// a line in neither is somebody else's and stays where it is. A unit upgrading into this has no
// record yet, and a daemon with no record removes nothing.

// ensureDropbearSees checks the keys are in the file dropbear reads and repairs the arrangement
// when they are not. It never returns an error and never fails a caller: the key write it follows
// has already succeeded, and the keys are safe on userdata whatever happens here. Its whole output
// is the log, which is what carries a broken device into the diagnostics bundle.
func ensureDropbearSees(keys []string) {
	missing, found, revoked, err := compareHome(keys)
	if len(missing) == 0 && len(revoked) == 0 && err == nil {
		return
	}

	// Which of the two complaints it is. A file that has everything it should and something it
	// should not is a removal that did not take, and saying the keys cannot be seen would send
	// whoever reads the log looking for the wrong breakage.
	what := "ssh: the keys were saved but dropbear cannot see them: it reads root's own authorized_keys, not the daemon's, and what joins the two is broken"
	if len(missing) == 0 && err == nil {
		what = "ssh: keys that were taken away are still in root's own authorized_keys, which is the file dropbear reads: they can still log in until it is rewritten"
	}

	attrs := []any{
		"daemon_writes", keysFile(), "dropbear_reads", homeKeysFile(), "root_ssh", describeHome(),
		"keys", len(keys), "missing", len(missing), "found_in_file", found, "missing_keys", labels(missing),
		"revoked", len(revoked), "revoked_keys", labels(revoked),
	}
	if err != nil {
		attrs = append(attrs, "err", err)
	}
	slog.Error(what, attrs...)

	how, err := repairHome(keys)
	if err != nil {
		slog.Error("ssh: the keys are saved but dropbear still cannot see them, and this cannot be repaired from here: nobody can log in with them until it is put right on the device itself",
			"daemon_writes", keysFile(), "dropbear_reads", homeKeysFile(), "root_ssh", describeHome(), "repair", how, "err", err)
		return
	}
	missing, found, revoked, err = compareHome(keys)
	if err != nil || len(missing) > 0 || len(revoked) > 0 {
		attrs := []any{
			"daemon_writes", keysFile(), "dropbear_reads", homeKeysFile(), "root_ssh", describeHome(),
			"repair", how, "keys", len(keys), "missing", len(missing), "found_in_file", found, "revoked", len(revoked),
		}
		if err != nil {
			attrs = append(attrs, "err", err)
		}
		slog.Error("ssh: rewriting root's authorized_keys did not help, and it still does not hold the keys the device was given and no others", attrs...)
		return
	}
	slog.Info("ssh: root's authorized_keys was out of step with the keys the device was given and has been put right; dropbear reads the right set now",
		"dropbear_reads", homeKeysFile(), "root_ssh", describeHome(), "repair", how, "keys", len(keys), "found_in_file", found)
}

// compareHome is the whole of what can be wrong with the file dropbear reads: keys the device was
// given that are not in it, and keys the daemon put there that have been taken away since and are.
//
// An empty key set asks for nobody to be let in, not for a file full of keys, so nothing counts as
// missing and a file that cannot be read is not a complaint either — a unit whose keys have all been
// removed has nothing to say unless the daemon's own lines are still sitting in root's file.
func compareHome(keys []string) (missing []string, found int, revoked []string, err error) {
	revoked = revokedInHome(keys)
	missing, found, err = missingFromHome(keys)
	if len(keys) == 0 {
		missing, err = nil, nil
	}
	return missing, found, revoked, err
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

// mergeHome is the real-directory arrangement: a file the daemon shares with whoever else writes to
// it, which on a rescue boot is the image's own key and somebody's only way into their own unit. So
// every line is kept unless the daemon can show it put that line there itself.
//
// Three things happen to a line, and which one depends only on the record (managedFile):
//
//   - a key the device has now is kept where it already is, or added at the end if it is not there;
//     a second copy of it goes, because init appends the daemon's file into this one on every boot
//     and the copies would otherwise pile up a boot at a time;
//   - a key in the record that the device no longer has goes, which is what makes a removal in Home
//     Assistant an actual removal rather than a line that quietly keeps working;
//   - anything else — the image's key, a comment, a line somebody added by hand — is not the
//     daemon's and is written back exactly as it was found.
//
// With no record nothing is in the second case, so a unit that has never been through here loses
// nothing on the first pass and has a record afterwards.
func mergeHome(keys []string) error {
	b, err := os.ReadFile(homeKeysFile())
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	want := make(map[string]bool, len(keys))
	for _, k := range keys {
		want[normalizeKey(k)] = true
	}
	managed := make(map[string]bool)
	if record, ok := managedKeys(); ok {
		for _, k := range record {
			managed[normalizeKey(k)] = true
		}
	}

	var lines []string
	kept := make(map[string]bool)
	for _, line := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		n := normalizeKey(line)
		switch {
		case want[n]:
			if kept[n] {
				continue // the same key again, from a boot that appended it a second time
			}
			kept[n] = true
		case managed[n]:
			continue // the daemon wrote this line and the device has since been told to drop it
		}
		lines = append(lines, line)
	}
	for _, k := range keys {
		n := normalizeKey(k)
		if kept[n] {
			continue
		}
		lines = append(lines, k)
		kept[n] = true
	}

	// Through a temporary file in the same directory, the way writeKeys does it: a half-written
	// authorized_keys would lock out the very people this is trying to keep in.
	var body []byte
	if len(lines) > 0 {
		body = []byte(strings.Join(lines, "\n") + "\n")
	}
	tmp := filepath.Join(HomeSSHDir, ".authorized_keys.new")
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, homeKeysFile()); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := fixPerms(HomeSSHDir); err != nil {
		return err
	}

	// Last, because the record is a claim about what is in the file and must not name lines that
	// never got there. Failing to write it costs nothing today and a removal tomorrow, so it is said
	// out loud rather than failed: the keys themselves are right either way.
	if err := writeManaged(keys); err != nil {
		slog.Warn("ssh: root's authorized_keys was rewritten but the daemon could not write down which lines are its own; a key taken away later may stay in the file",
			"record", managedFile(), "err", err)
	}
	return nil
}

// managedFile is where the daemon writes down which lines of root's own authorized_keys are its
// own. It sits beside the keys on userdata, because it has to outlive the image: the whole point is
// to still know, after an update or a slot change, which lines may be taken away again.
func managedFile() string { return filepath.Join(KeysDir, "root_authorized_keys") }

// managedKeys is that record, and ok says whether there is one at all. No record is not an empty
// record: it means nothing in root's file is known to be the daemon's, and nothing may be removed.
func managedKeys() (keys []string, ok bool) {
	b, err := os.ReadFile(managedFile())
	if err != nil {
		return nil, false
	}
	for _, line := range strings.Split(string(b), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			keys = append(keys, line)
		}
	}
	return keys, true
}

// writeManaged replaces the record, through a temporary file the way the keys themselves are
// written. An empty set leaves no record, which is right: there is nothing of the daemon's left in
// root's file to take away.
func writeManaged(keys []string) error {
	if len(keys) == 0 {
		err := os.Remove(managedFile())
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if err := os.MkdirAll(KeysDir, 0o700); err != nil {
		return err
	}
	tmp := filepath.Join(KeysDir, ".root_authorized_keys.new")
	if err := os.WriteFile(tmp, []byte(strings.Join(keys, "\n")+"\n"), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, managedFile()); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// revokedInHome is the lines the daemon put into root's own authorized_keys that the device no
// longer has a key for and that are still in the file. Nothing else in there is the daemon's to
// touch, so this is the whole of what a removal has left to do.
func revokedInHome(keys []string) []string {
	record, ok := managedKeys()
	if !ok {
		return nil
	}
	b, err := os.ReadFile(homeKeysFile())
	if err != nil {
		return nil
	}
	want := make(map[string]bool, len(keys))
	for _, k := range keys {
		want[normalizeKey(k)] = true
	}
	inHome := make(map[string]bool)
	for _, line := range strings.Split(string(b), "\n") {
		if n := normalizeKey(line); n != "" {
			inHome[n] = true
		}
	}
	var out []string
	seen := make(map[string]bool)
	for _, k := range record {
		n := normalizeKey(k)
		if want[n] || !inHome[n] || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, k)
	}
	return out
}

// adoptManaged writes the first record on a unit that was set up before there was one, so that the
// first key taken away after an update is really taken away rather than the one after it.
//
// It claims only keys the daemon holds on userdata that are also in root's own file: either the
// daemon put them there or boot appended them from the daemon's file, and either way they are the
// daemon's to rewrite. A key the image brought with it is not on userdata and is never claimed. The
// one line it can read wrong is an image key that somebody also pushed through Home Assistant, which
// is the same key twice and goes when they ask for it to go.
//
// It has to run before the keys are replaced, because afterwards nothing remembers the old set.
func adoptManaged() {
	if _, err := os.Stat(managedFile()); err == nil {
		return
	}
	fi, err := os.Lstat(HomeSSHDir)
	if err != nil || fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir() {
		return // the symlink arrangement: one file, shared with nobody, nothing to write down
	}
	b, err := os.ReadFile(homeKeysFile())
	if err != nil {
		return
	}
	inHome := make(map[string]bool)
	for _, line := range strings.Split(string(b), "\n") {
		if n := normalizeKey(line); n != "" {
			inHome[n] = true
		}
	}
	var owned []string
	for _, k := range readKeys() {
		if inHome[normalizeKey(k)] {
			owned = append(owned, k)
		}
	}
	if len(owned) == 0 {
		return
	}
	if err := writeManaged(owned); err != nil {
		slog.Warn("ssh: the daemon could not write down which lines of root's authorized_keys are its own; a key taken away may stay in the file",
			"record", managedFile(), "err", err)
		return
	}
	slog.Info("ssh: wrote down which lines of root's authorized_keys are the daemon's, so a key taken away can be taken out of it",
		"record", managedFile(), "keys", len(owned))
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

// labels names keys in a log without naming anybody: the key's type and the same SHA256 fingerprint
// ssh-keygen -l prints, so whoever reads it can tell which key is missing and hold it against their
// own. The screen names a key by its comment instead, which is nearly always user@hostname — a person
// and the name of their machine — and this log is what the diagnostics bundle carries out of the
// house, where the first line promises names and addresses have been replaced. Key material never
// goes in a log either; a fingerprint is a hash of the public half and identifies nobody.
func labels(keys []string) []string {
	var out []string
	for _, k := range keys {
		out = append(out, fingerprint(k))
	}
	return out
}

// fingerprint is "<type> SHA256:<hash>" for a key line. A line whose body does not decode gets its
// type alone: there is nothing to hash, and the rest of the line is the part that must not go.
func fingerprint(k string) string {
	fields := strings.Fields(k)
	if len(fields) == 0 {
		return "(empty)"
	}
	if len(fields) < 2 {
		return fields[0]
	}
	raw, err := base64.StdEncoding.DecodeString(fields[1])
	if err != nil {
		return fields[0]
	}
	sum := sha256.Sum256(raw)
	return fields[0] + " SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:])
}
