//go:build !dot && !spot && !panel

package update

// releases is where the Echo Show 5's releases are published. A variable so that a build of the
// mainline kernel option can follow the fork that publishes it (-ldflags -X, with releaseKey).
var releases = "https://github.com/HuskerMinion/techo5/releases"
