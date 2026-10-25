//go:build panel

package buttons

import (
	"context"
	"testing"
)

// A panel reads no input devices: the tablet's keys are Android's. Before, a panel listed
// /dev/input like an Echo, and a tablet whose key codes are a Show's (the volume keys, 114 and 115)
// would have been read as a Show's buttons wherever the nodes opened; where they did not, the buttons
// failed to start and were restarted for as long as the app ran.
func TestAPanelReadsNoInputDevices(t *testing.T) {
	if Here {
		t.Fatal("a panel claims buttons of its own")
	}
	c := &Controller{}
	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("Start on a panel: %v", err)
	}
	if len(c.devices) != 0 {
		t.Fatalf("a panel opened %d input devices", len(c.devices))
	}
	if len(codes) != 0 {
		t.Fatalf("a panel maps key codes to buttons: %v", codes)
	}

	// Run waits for the end rather than failing for want of a node, so the supervisor leaves it be.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.Run(ctx); err != nil {
		t.Fatalf("Run on a panel: %v", err)
	}
}
