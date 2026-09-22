package security

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The keys the tests push, and the one the Show's image brings with it.
const (
	testKey  = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExample someone@desk"
	testKey2 = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAnother second@desk"
	imageKey = "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQExample techo5-image"
)

// offDevice points KeysDir and HomeSSHDir at temporary directories, so the arrangement between them
// can be broken and mended on a machine that has neither /data nor a root account. It returns the
// two paths and captures the log, which is most of what the repair produces.
func offDevice(t *testing.T) (keys, home string, log *bytes.Buffer) {
	t.Helper()
	root := t.TempDir()
	keys, home = filepath.Join(root, "state", "ssh"), filepath.Join(root, "root", ".ssh")
	wasKeys, wasHome := KeysDir, HomeSSHDir
	KeysDir, HomeSSHDir = keys, home
	t.Cleanup(func() { KeysDir, HomeSSHDir = wasKeys, wasHome })

	log = &bytes.Buffer{}
	was := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(log, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(was) })
	return keys, home, log
}

// homeLines is what dropbear would read, one line per key.
func homeLines(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile(homeKeysFile())
	if err != nil {
		t.Fatalf("reading what dropbear reads: %v", err)
	}
	var out []string
	for _, line := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(line) != "" {
			out = append(out, strings.TrimSpace(line))
		}
	}
	return out
}

// The Dot's arrangement, already right: the keys go straight into the file dropbear reads, and
// nothing is repaired or said about it.
func TestSymlinkAlreadyRightIsLeftAlone(t *testing.T) {
	keys, home, log := offDevice(t)
	if err := os.MkdirAll(keys, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(home), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(keys, home); err != nil {
		t.Fatal(err)
	}

	if err := writeKeys([]string{testKey}); err != nil {
		t.Fatal(err)
	}
	ensureDropbearSees([]string{testKey})

	if log.Len() != 0 {
		t.Errorf("a working device logged something: %s", log)
	}
	if got := homeLines(t); len(got) != 1 || got[0] != testKey {
		t.Errorf("dropbear reads %q", got)
	}
	if fi, err := os.Lstat(home); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("the symlink did not survive: %v %v", fi, err)
	}
}

// Root's .ssh not there at all, which is what a boot script that did not run leaves behind. It
// becomes a symlink to KeysDir, and the keys are visible without another write.
func TestMissingHomeDirIsLinked(t *testing.T) {
	keys, home, log := offDevice(t)
	if err := writeKeys([]string{testKey}); err != nil {
		t.Fatal(err)
	}
	ensureDropbearSees([]string{testKey})

	target, err := os.Readlink(home)
	if err != nil {
		t.Fatalf("root's .ssh is not a symlink: %v", err)
	}
	if target != keys {
		t.Errorf("symlink points at %q, not %q", target, keys)
	}
	if got := homeLines(t); len(got) != 1 || got[0] != testKey {
		t.Errorf("dropbear reads %q", got)
	}
	if !strings.Contains(log.String(), "level=ERROR") {
		t.Errorf("the breakage was not reported: %s", log)
	}
	if !strings.Contains(log.String(), "repair=linked") {
		t.Errorf("the repair was not reported: %s", log)
	}
	if strings.Contains(log.String(), "AAAA") {
		t.Errorf("the log has key material in it: %s", log)
	}
}

// A symlink aimed somewhere else, which is the failure seen in the field. The pointer is not data,
// so it is replaced.
func TestSymlinkToTheWrongPlaceIsRepaired(t *testing.T) {
	keys, home, log := offDevice(t)
	elsewhere := filepath.Join(t.TempDir(), "old-ssh")
	if err := os.MkdirAll(elsewhere, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(home), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, home); err != nil {
		t.Fatal(err)
	}

	if err := writeKeys([]string{testKey}); err != nil {
		t.Fatal(err)
	}
	ensureDropbearSees([]string{testKey})

	if target, err := os.Readlink(home); err != nil || target != keys {
		t.Fatalf("symlink points at %q (%v), not %q", target, err, keys)
	}
	if got := homeLines(t); len(got) != 1 || got[0] != testKey {
		t.Errorf("dropbear reads %q", got)
	}
	if !strings.Contains(log.String(), "repair=relinked") {
		t.Errorf("the repair was not reported: %s", log)
	}
}

// A dangling symlink is the same case: it points at a directory that is no longer there, and
// removing it loses nothing.
func TestDanglingSymlinkIsRepaired(t *testing.T) {
	keys, home, log := offDevice(t)
	if err := os.MkdirAll(filepath.Dir(home), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(t.TempDir(), "gone"), home); err != nil {
		t.Fatal(err)
	}

	if err := writeKeys([]string{testKey}); err != nil {
		t.Fatal(err)
	}
	ensureDropbearSees([]string{testKey})

	if target, err := os.Readlink(home); err != nil || target != keys {
		t.Fatalf("symlink points at %q (%v), not %q", target, err, keys)
	}
	if got := homeLines(t); len(got) != 1 || got[0] != testKey {
		t.Errorf("dropbear reads %q", got)
	}
	if !strings.Contains(log.String(), "dangling symlink") {
		t.Errorf("the log does not say what was there: %s", log)
	}
}

// The Show and the Spot: a real directory holding the key built into the image. Replacing it with a
// symlink, or writing over the file, would take away the only way somebody has into their own
// device. Ours is added, theirs stays, and the directory stays a directory.
func TestRealDirectoryKeepsTheKeyThatWasThere(t *testing.T) {
	_, home, log := offDevice(t)
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "authorized_keys"), []byte(imageKey+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := writeKeys([]string{testKey, testKey2}); err != nil {
		t.Fatal(err)
	}
	ensureDropbearSees([]string{testKey, testKey2})

	got := homeLines(t)
	want := []string{imageKey, testKey, testKey2}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("dropbear reads %q, want %q", got, want)
	}
	if fi, err := os.Lstat(home); err != nil || !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
		t.Errorf("the directory was replaced: %v %v", fi, err)
	}
	if !strings.Contains(log.String(), "repair=merged") {
		t.Errorf("the repair was not reported: %s", log)
	}
	if fi, err := os.Stat(filepath.Join(home, "authorized_keys")); err != nil {
		t.Fatal(err)
	} else if fi.Mode().Perm() != 0o600 {
		t.Errorf("file mode %v, dropbear wants 0600", fi.Mode().Perm())
	}
	if fi, err := os.Stat(home); err != nil {
		t.Fatal(err)
	} else if fi.Mode().Perm() != 0o700 {
		t.Errorf("directory mode %v, dropbear wants 0700", fi.Mode().Perm())
	}
}

// Home Assistant pushes the same keys again, as it does on any change. A device whose root .ssh is
// a real directory must not collect a second copy of every key each time.
func TestWritingTheSameKeysTwiceDoesNotDuplicate(t *testing.T) {
	_, home, log := offDevice(t)
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "authorized_keys"), []byte(imageKey+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 3; i++ {
		if err := writeKeys([]string{testKey, testKey2}); err != nil {
			t.Fatal(err)
		}
		ensureDropbearSees([]string{testKey, testKey2})
	}

	got := homeLines(t)
	want := []string{imageKey, testKey, testKey2}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("dropbear reads %q, want %q", got, want)
	}
	if n := strings.Count(log.String(), "repair=merged"); n != 1 {
		t.Errorf("repaired %d times; only the first write should have had anything to do: %s", n, log)
	}
}

// Nothing that can be done: a plain file where root's .ssh should be. The keys are saved, the write
// still counts as having worked, and the log says what is in the way.
func TestUnrepairableIsLoggedNotFailed(t *testing.T) {
	_, home, log := offDevice(t)
	if err := os.MkdirAll(filepath.Dir(home), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(home, []byte("not a directory\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := writeKeys([]string{testKey}); err != nil {
		t.Fatalf("the key write itself failed: %v", err)
	}
	ensureDropbearSees([]string{testKey}) // must not panic, and has nothing to return

	if got := readKeys(); len(got) != 1 || got[0] != testKey {
		t.Errorf("the keys were not saved: %q", got)
	}
	if !strings.Contains(log.String(), "cannot be repaired from here") {
		t.Errorf("the log does not say it gave up: %s", log)
	}
	if !strings.Contains(log.String(), "is a file, not a directory") {
		t.Errorf("the log does not say what is in the way: %s", log)
	}
}

// A read-only root filesystem, which is the case this is most likely to meet: root's .ssh is
// missing and its parent cannot be written to. Nothing fails; the log carries it.
func TestUnwritableParentIsLoggedNotFailed(t *testing.T) {
	_, home, log := offDevice(t)
	parent := filepath.Dir(home)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(parent, 0o700) })
	if os.Geteuid() == 0 {
		t.Skip("running as root, which is allowed to write to it anyway")
	}

	if err := writeKeys([]string{testKey}); err != nil {
		t.Fatalf("the key write itself failed: %v", err)
	}
	ensureDropbearSees([]string{testKey})

	if got := readKeys(); len(got) != 1 || got[0] != testKey {
		t.Errorf("the keys were not saved: %q", got)
	}
	if !strings.Contains(log.String(), "cannot be repaired from here") {
		t.Errorf("the log does not say it gave up: %s", log)
	}
}

// An empty key set is a removal, and has nothing to look for. It must not go near a real directory
// holding an image's key.
func TestNoKeysChecksNothing(t *testing.T) {
	_, home, log := offDevice(t)
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "authorized_keys"), []byte(imageKey+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := writeKeys(nil); err != nil {
		t.Fatal(err)
	}
	ensureDropbearSees(nil)

	if got := homeLines(t); len(got) != 1 || got[0] != imageKey {
		t.Errorf("the image's key was touched: %q", got)
	}
	if log.Len() != 0 {
		t.Errorf("logged something about nothing: %s", log)
	}
}
