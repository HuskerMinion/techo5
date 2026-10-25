//go:build panel

package layout

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sync"
)

// macPath holds the panel's address. Android hides the real Wi-Fi address from apps, so a panel makes
// one up the first time it starts and keeps it: a random, locally administered unicast address, which
// can never be a real vendor's. Home Assistant knows the device by it, so it must not change; clearing
// the app's data is what makes a new device.
var macPath = StateDir + "/mac"

var macOnce sync.Mutex

func readMAC() ([]byte, error) {
	macOnce.Lock()
	defer macOnce.Unlock()

	b, err := os.ReadFile(macPath)
	if err == nil {
		return b, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}

	var a [6]byte
	if _, err := rand.Read(a[:]); err != nil {
		return nil, err
	}
	a[0] = a[0]&^0x01 | 0x02 // unicast, locally administered
	mac := fmt.Sprintf("%02x%02x%02x%02x%02x%02x\n", a[0], a[1], a[2], a[3], a[4], a[5])
	if err := os.MkdirAll(StateDir, 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(macPath, []byte(mac), 0o600); err != nil {
		return nil, err
	}
	return []byte(mac), nil
}
