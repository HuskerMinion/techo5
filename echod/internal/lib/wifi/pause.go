package wifi

import (
	"context"
	"os"
)

// QuietFlag, while present, tells the boot scripts' network keeper that the device is off the network
// on purpose (Pause), so it neither starts the supplicant again nor reboots the device for being
// offline. It lives in /run, so a reboot clears it.
const QuietFlag = "/run/techo5/wifi-quiet"

// Pause leaves the network and keeps off it: the supplicant drops the connection and, unlike when the
// network merely goes away, does not look for it or any other it knows, so the radio sends nothing
// until Resume. The saved networks stay as they are.
func Pause(ctx context.Context) error {
	// Marked first: the keeper must not take the disconnect for a fault in between.
	_ = os.WriteFile(QuietFlag, []byte("1"), 0o644)
	if _, err := cli(ctx, "disconnect"); err != nil {
		_ = os.Remove(QuietFlag)
		return err
	}
	return nil
}

// Resume lets the supplicant look for its networks again and join the first it finds, as it does
// after a start. It changes nothing on a device that was not paused.
func Resume(ctx context.Context) error {
	_, err := cli(ctx, "reconnect")
	if err == nil {
		_ = os.Remove(QuietFlag)
	}
	return err
}
