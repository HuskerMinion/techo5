//go:build panel

package update

// releases is empty on a panel: the daemon comes inside the app, so there is nothing to fetch and a
// check finds no manifest.
const releases = ""
