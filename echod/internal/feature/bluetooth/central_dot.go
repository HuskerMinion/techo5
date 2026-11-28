//go:build dot && !cannoli

package bluetooth

// pairable is nothing: the Dot 2nd and 3rd gen make no connections.
const pairable = 0

// openCentral is nil on the Dot 2nd and 3rd gen: their proxy drives the HCI node itself and makes no
// connections. Lending them through bluetoothd as on the Echo Dot 5th gen (central_cannoli.go) is
// untested there.
var openCentral func(func(string, uint16, []byte), func(string)) (central, error)
