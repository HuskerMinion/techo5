//go:build !panel

package diag

import "github.com/HuskerMinion/techo5/echod/internal/layout"

// daemonLog is the last n lines of the daemon's log file and the last wakeN wake lines in all of it.
func daemonLog(n, wakeN int) (last, wakes string) {
	return readLog(layout.LogPath, n, wakeN)
}
