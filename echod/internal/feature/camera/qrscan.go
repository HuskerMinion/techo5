//go:build !dot && !spot

package camera

import (
	"context"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/feature/display"
)

// qrScanLongest is the longest a scan may be asked for. Home Assistant gives up on an action's answer
// after 30 seconds (aioesphomeapi's DEFAULT_EXECUTE_SERVICE_TIMEOUT), and a scan answered after that
// is one nobody hears.
const qrScanLongest = 25 * time.Second

// qrScanDefault is a scan asked for without a length.
const qrScanDefault = 20 * time.Second

// Actions is qr_scan: the screen shows the camera's picture until a QR code held up to it is read, and
// the action answers with its text, for an automation or an assistant to go on with (a Matter device's
// setup code, say). Only the text leaves the device, never a picture.
func (f *Feature) Actions() []*esphome.Action {
	return []*esphome.Action{{
		Name:    "qr_scan",
		Args:    []esphome.Arg{{Name: "seconds", Type: esphome.ArgInt}},
		Answers: true,
		Run: func(c esphome.Call) (any, error) {
			code, err := display.Get().ScanQR(context.Background(), qrScanFor(c.Int("seconds")))
			if err != nil {
				return nil, err
			}
			return map[string]string{"code": code}, nil
		},
	}}
}

// qrScanFor is how long a scan asked for seconds runs: qrScanDefault for none, at most qrScanLongest.
func qrScanFor(seconds int) time.Duration {
	if seconds <= 0 {
		return qrScanDefault
	}
	return min(time.Duration(seconds)*time.Second, qrScanLongest)
}
