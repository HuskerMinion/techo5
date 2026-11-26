//go:build dot && cannoli

package update

// archSuffix marks the Echo Dot 5th gen's builds (-tags dot,cannoli), published beside the Dot 2's and
// the Dot 3's on the same channel: a release carrying only their assets offers this device nothing
// rather than the wrong thing (Manifest.Serves). Another SoC (MT8519), kernel (5.4) and sound card, so
// neither of theirs would run here.
const archSuffix = "-cannoli"

// rootfsArch is the key this device's root filesystem is published under: arm-cannoli.
func rootfsArch() string { return arch }
