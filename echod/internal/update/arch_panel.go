//go:build panel

package update

// archSuffix marks panel binaries apart from the Echos'. None are published: the app carries the
// daemon and is updated as a whole.
const archSuffix = "-panel"

// rootfsArch is the key a root filesystem would be published under. A panel has no slots and takes
// none (releases is empty), so it names its binary's architecture like the Dot and the Spot.
func rootfsArch() string { return arch }
