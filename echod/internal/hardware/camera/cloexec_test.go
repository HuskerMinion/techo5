package camera

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Every device the daemon opens by hand is close-on-exec. os.Open sets it; a raw syscall.Open does
// not, and a camera opened without it went with every program the daemon started: turned on while
// the camera was open, the SSH server and its shells held the camera, and the next open found it busy
// (techo5-spot#9).
func TestRawOpensAreCloseOnExec(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	open := regexp.MustCompile(`(syscall|unix)\.Open\([^\n]*`)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == "third_party" || d.Name() == "testdata") {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range open.FindAllString(string(b), -1) {
			if !strings.Contains(m, "O_CLOEXEC") {
				t.Errorf("%s: %s", path, m)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
