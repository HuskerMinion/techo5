//go:build dot && !donut

package update

// archSuffix marks the Dot's builds, so a release carrying only the Show's arm binary offers this
// device nothing rather than the wrong thing.
const archSuffix = "-dot"

// rootfsArch is the key this device's root filesystem is published under: its architecture, as
// for its binary (arch_cronos.go has why the Show 8 2nd gen differs).
func rootfsArch() string { return arch }
