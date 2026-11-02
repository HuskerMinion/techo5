//go:build dot

package bluetooth

// pairable is nothing: the Dot makes no connections.
const pairable = 0

// openCentral is nil on the Dot: its proxy drives the HCI node itself, and makes no connections.
var openCentral func(func(string, uint16, []byte), func(string)) (central, error)
