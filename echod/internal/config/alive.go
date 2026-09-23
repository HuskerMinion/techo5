package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// alivePath is a file beside the state file holding the last moment the device is known to have been
// running. It is its own file rather than a key in the state, because it is written every few
// minutes and the state is marshalled and fsynced whole on every change.
func alivePath() string { return filepath.Join(filepath.Dir(store().path), "alive") }

// Alive is the last moment the device recorded itself running, and false if it never has.
//
// It is how a ring that fell due while the device was off gets noticed at all: the scheduler starts
// from now, so without this nothing ever looks at the time the device was away.
func Alive() (time.Time, bool) {
	b, err := os.ReadFile(alivePath())
	if err != nil {
		return time.Time{}, false
	}
	n, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	if err != nil || n <= 0 {
		return time.Time{}, false
	}
	return time.Unix(n, 0), true
}

// KeepAlive records t as a moment the device was running. Written through a temporary file and
// synced, since a power cut is exactly when it is read back: a record that lost its last write would
// have the device away for longer than it was, and call a ring that did sound a missed one.
func KeepAlive(t time.Time) error {
	path := alivePath()
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(strconv.FormatInt(t.Unix(), 10) + "\n"); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
