package wifi

import (
	"os"
	"path/filepath"
)

// KeepBandFlag, while present, tells the boot scripts' network keeper to leave the band the link is on:
// it neither looks for a stronger 5 GHz radio nor moves the link to one (t5_wifi_prefer5), and takes
// off a channel it had pinned the link to. It lives in /run, so a reboot clears it and the daemon puts
// it back as it starts (feature/wifiband). A variable for the tests.
var KeepBandFlag = "/run/techo5/wifi-keep-band"

// SetKeepBand puts the flag up or takes it down.
func SetKeepBand(on bool) error {
	if !on {
		if err := os.Remove(KeepBandFlag); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(KeepBandFlag), 0o755); err != nil {
		return err
	}
	return os.WriteFile(KeepBandFlag, []byte("1"), 0o644)
}
