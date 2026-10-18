//go:build dot

package bluetooth

// openCentral is nil on the Dot: its proxy drives the HCI node itself, and makes no connections.
var openCentral func(func(string, uint16, []byte), func(string)) (central, error)
