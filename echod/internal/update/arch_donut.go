//go:build dot && donut

package update

// archSuffix marks the Echo Dot 3's builds, published beside the Dot 2's on the same channel
// (releases_dot.go): a release carrying only the Dot 2's "-dot" assets offers this device nothing rather
// than the wrong thing (Manifest.Serves). The Echo Input runs the same build and root filesystem, so it
// takes the same assets (layout.Cupcake says what differs at run time).
const archSuffix = "-donut"

// rootfsArch is the key this device's root filesystem is published under: arm-donut.
func rootfsArch() string { return arch }
