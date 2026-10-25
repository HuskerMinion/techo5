//go:build !panel

package logd

// keepRecent is false off a panel: the start script puts the daemon's output in its log file.
const keepRecent = false
