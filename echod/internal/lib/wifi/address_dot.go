//go:build dot

package wifi

// addressBuild is false on the Echo Dot: its image has a techo5-net of its own, which joins Wi-Fi
// whenever it is run and knows nothing of a fixed address, so driving it would start another DHCP
// client at each Save and never apply anything (address_build.go).
const addressBuild = false

// keptByDate is whether techo5-net keeps the supplicant's file only while it is newer than the
// network set on the unit (keptDate): the Dot's does.
const keptByDate = true
