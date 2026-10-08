//go:build !windows

package main

import (
	"fmt"
	"os"
)

// checkPrivate refuses a script list others can write to: whoever can change it can change what the
// deck's Run script buttons run.
func checkPrivate(st os.FileInfo, fix string) error {
	if st.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("others can write to it (%s)", fix)
	}
	return nil
}
