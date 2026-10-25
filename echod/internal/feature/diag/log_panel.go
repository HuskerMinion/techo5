//go:build panel

package diag

import "github.com/HuskerMinion/techo5/echod/internal/android/logd"

// daemonLog is the last n lines of the daemon's log and the last wakeN wake lines in it. A panel logs
// to logcat alone and nothing writes its log file, so it is the last of the log the handler keeps in
// memory (logd.Recent): the wake lines are from that, not from the whole day.
func daemonLog(n, wakeN int) (last, wakes string) {
	s := newLogScan(n, wakeN)
	for _, line := range logd.Recent() {
		s.add(line)
	}
	return s.lines.String(), s.found.String()
}
