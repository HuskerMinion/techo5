//go:build !dot && !spot

package display

import (
	"image"
	"testing"
	"time"

	"github.com/makiuchi-d/gozxing"
	"github.com/makiuchi-d/gozxing/qrcode"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/dashboard"
)

// A drawn dashboard with no access to Home Assistant shows a QR code to the setup page's Connect to
// Home Assistant with what to do, at every panel size; with no address to give, it says what to do
// alone. With SHOW_PREVIEW set, each is written there to look at.
func TestDashboardOffersToConnect(t *testing.T) {
	previewFont(t)
	restore := connectAddress
	t.Cleanup(func() { connectAddress = restore })
	at := time.Date(2026, 10, 4, 14, 7, 0, 0, time.Local)
	v := dashboard.Drawn{Problem: "A drawn dashboard needs this device connected to Home Assistant. On its setup page, " +
		"choose Connect to Home Assistant on the Network tab.", Connect: true}
	for _, panel := range []struct {
		name       string
		wide, high int
	}{{"", showWide, showHigh}, {"-show8", show8Wide, show8High}, {"-skylight", skylightWide, skylightHigh}} {
		for _, addr := range []string{"http://192.168.1.50:8181", ""} {
			connectAddress = func() string { return addr }
			img := image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high))
			newRenderer(img).draw(scene{now: at, phase: "idle", showDash: true, dashMode: config.DashboardDrawn, drawn: v})
			white := 0
			for i := 0; i < len(img.Pix); i += 4 {
				if img.Pix[i] == 255 && img.Pix[i+1] == 255 && img.Pix[i+2] == 255 {
					white++
				}
			}
			// The code's quiet zone and light modules are pure white; nothing else on the page is.
			if hasCode := white > panel.wide*panel.high/40; hasCode != (addr != "") {
				t.Errorf("%s with address %q: a code drawn %v (%d white pixels)", panel.name, addr, hasCode, white)
			}
			if addr != "" {
				// And it reads, as a phone's camera would: to the setup page's Connect.
				bmp, _ := gozxing.NewBinaryBitmap(gozxing.NewHybridBinarizer(gozxing.NewLuminanceSourceFromImage(img)))
				res, err := qrcode.NewQRCodeReader().Decode(bmp, nil)
				if err != nil || res.GetText() != addr+connectPath {
					t.Errorf("%s: the code reads %v %v", panel.name, res, err)
				}
				writePreview(t, img, "dash-connect"+panel.name+".png")
			}
		}
	}
}
