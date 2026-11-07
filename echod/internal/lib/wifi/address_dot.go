//go:build dot

package wifi

// addressBuild is false on the Echo Dot: its image has a techo5-net of its own, which joins Wi-Fi
// whenever it is run and knows nothing of a fixed address, so driving it would start another DHCP
// client at each Save and never apply anything (address_build.go).
const addressBuild = false

// joinStartsDHCP is whether a join starts the DHCP client when none runs: on the Dot, whose
// techo5-net starts it only after a network was joined at boot (renewLease).
const joinStartsDHCP = true
