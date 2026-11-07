//go:build !dot

package wifi

// addressBuild is whether this build offers a fixed address at all. The Echo Show and the Echo Spot
// images carry the techo5-net that tries one; the Echo Dot's image has a techo5-net of its own that
// joins Wi-Fi and knows nothing of a fixed address (address_dot.go).
const addressBuild = true

// joinStartsDHCP is whether a join starts the DHCP client when none runs: only on the Dot
// (address_dot.go); elsewhere techo5-net keeps one running.
const joinStartsDHCP = false
