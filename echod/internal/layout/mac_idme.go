//go:build !panel

package layout

import "os"

// readMAC is the address idme recorded at the factory: /proc/idme on Amazon's kernels, idme's area on
// the eMMC on TECHO5's mainline ones (readIDME).
func readMAC() ([]byte, error) { return readIDME("mac_addr", os.ReadFile) }
