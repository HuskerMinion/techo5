package safename

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestPlainNamesAreAllowed(t *testing.T) {
	for _, name := range []string{
		"okay_nabu",
		"hey-jarvis.tflite",
		"4f7a1c9e2b8d0a65.wav",
		".hidden",
		"a name with spaces",
	} {
		if !OK(name) {
			t.Errorf("%q was refused, and it names a file in the directory", name)
		}
	}
}

// The three shapes that walk out of a directory, and the pieces they are built from.
func TestNamesThatLeaveTheDirectoryAreRefused(t *testing.T) {
	for _, name := range []string{
		"",
		".",
		"..",
		"../escaped",
		"../../etc/passwd",
		"/etc/passwd",
		`C:\Windows\System32\drivers\etc\hosts`,
		"sub/model",
		`sub\model`,
		"has\na newline",
		"has\x00a nul",
	} {
		if OK(name) {
			t.Errorf("%q was allowed, and it does not name a file in the directory", name)
		}
	}
}

func TestJoinRefusesAndReports(t *testing.T) {
	path, err := Join("/data/models", "okay_nabu.tflite")
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	if want := filepath.Join("/data/models", "okay_nabu.tflite"); path != want {
		t.Errorf("Join = %q, want %q", path, want)
	}

	if _, err := Join("/data/models", "../../etc/passwd"); !errors.Is(err, ErrName) {
		t.Errorf("Join of an escaping name gave %v, want ErrName", err)
	}
}
