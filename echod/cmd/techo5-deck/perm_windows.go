//go:build windows

package main

import "os"

// checkPrivate is the Unix check; on Windows the file lives in the user's own profile folder, which
// other users can't write to.
func checkPrivate(os.FileInfo, string) error { return nil }
