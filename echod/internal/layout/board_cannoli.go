//go:build dot && cannoli

package layout

const boardCannoli = "cannoli"

// buildBoard is the Echo Dot 5th gen whatever the unit says: its build runs on nothing else, and its
// factory product id is not one this package knows yet (no unit's /proc/idme/device_type_id has been
// read), so Fire OS's ro.product.device ("cannoli") is the only name it has, and the Linux image has no
// Fire OS to ask. The other Dots' names are not looked for here, nor this one on their build.
func buildBoard() (board, model, name string) {
	return boardCannoli, "Echo Dot 5 (cannoli)", "Echo Dot"
}

// Cannoli is whether this is the Echo Dot 5th gen: always, on its own build (-tags dot,cannoli).
func Cannoli() bool { return true }
