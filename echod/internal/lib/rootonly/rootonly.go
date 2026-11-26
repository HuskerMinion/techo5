// Package rootonly says whether a path the daemon is about to trust as root's is root's: a file it runs
// or reads a process id from, a folder it puts sockets or pipes in.
//
// Users other than root run on the device (the receivers, the video decoder, and add-ons, each as a
// user of its own: feature/addons), and the paths the daemon has always used are fixed names in /run.
// The images mount /run 0755 now, so nobody else can make anything there; but an image whose initramfs
// mounted it 1777, as they all did before, leaves a window until boot.sh puts that right, and a name
// another user got in first would be run, or followed, as root. So such a path is used only when it is
// what root would have made: not a link, owned by root, and not writable by anyone else.
package rootonly

import (
	"os"
	"syscall"
)

// Owner is the user a path has to belong to: root. A variable, so a test running as somebody else can
// make paths that pass.
var Owner = 0

// File reports whether path is a regular file of Owner's, not a link, that nobody else can write.
func File(path string) bool {
	st, err := os.Lstat(path)
	return err == nil && st.Mode().IsRegular() && owned(st)
}

// Dir reports whether path is a folder of Owner's, not a link, that nobody else can write.
func Dir(path string) bool {
	st, err := os.Lstat(path)
	return err == nil && st.IsDir() && owned(st)
}

func owned(st os.FileInfo) bool {
	sys, ok := st.Sys().(*syscall.Stat_t)
	return ok && int(sys.Uid) == Owner && st.Mode().Perm()&0o022 == 0
}

// MkdirAll makes path, a folder of Owner's with mode perm, and reports whether it is one: made now, or
// there already and as it should be. A link, a folder of another user's, or one others can write to is
// refused, not repaired: whoever made it may already have put something in it.
func MkdirAll(path string, perm os.FileMode) bool {
	if err := os.MkdirAll(path, perm); err != nil {
		return false
	}
	return Dir(path)
}
