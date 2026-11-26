package rootonly

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOnlyWhatRootWouldHaveMade(t *testing.T) {
	old := Owner
	t.Cleanup(func() { Owner = old })
	Owner = os.Getuid()
	dir := t.TempDir()
	_ = os.Chmod(dir, 0o755) // a test's folder is group-writable
	file := filepath.Join(dir, "script")
	_ = os.WriteFile(file, []byte("x"), 0o755)
	if !File(file) || !Dir(dir) {
		st, _ := os.Stat(dir)
		fs, _ := os.Stat(file)
		t.Fatalf("a file (%v) and a folder (%v) of the owner's were refused", fs.Mode(), st.Mode())
	}
	_ = os.Chmod(file, 0o777)
	if File(file) {
		t.Error("a file others can write to passed")
	}
	link := filepath.Join(dir, "link")
	_ = os.Symlink(file, link)
	_ = os.Chmod(file, 0o755)
	if File(link) || Dir(link) {
		t.Error("a link passed")
	}
	if File(dir) || Dir(file) {
		t.Error("a folder passed as a file, or a file as a folder")
	}
	open := filepath.Join(dir, "open")
	_ = os.Mkdir(open, 0o777)
	_ = os.Chmod(open, 0o777)
	if Dir(open) || MkdirAll(open, 0o755) {
		t.Error("a folder others can write to passed")
	}
	if !MkdirAll(filepath.Join(dir, "new", "deeper"), 0o700) {
		t.Error("a new folder was refused")
	}
	Owner = os.Getuid() + 1
	if File(file) || Dir(dir) {
		t.Error("another user's passed")
	}
}
