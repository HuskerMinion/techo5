//go:build !dot && !spot

package display

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/mute"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/camera"
	"github.com/HuskerMinion/techo5/echod/internal/lib/wifi"
)

// Joining a network from its Wi-Fi QR code: the one a phone shows to share its network, or a router
// prints on its sticker. The Wi-Fi page and first run's Wi-Fi step have a button for it on a device
// with a camera; the camera's picture is up until a Wi-Fi code is read, and the network it names is
// joined as one chosen from the list would be, with nothing typed.

// wifiQRFor is how long the scan waits for a code. Nobody is waiting on an answer here, as Home
// Assistant is on its own scan, so it leaves time to find the code on a phone.
var wifiQRFor = 60 * time.Second

// wifiQRCamera is whether the device has a camera to scan with. A variable so that a test can say yes
// on a machine with none.
var wifiQRCamera = camera.Available

// wifiQRLook is what the Wi-Fi scan reads and says: only a Wi-Fi code ends it, and the page names the
// network it read, or why it cannot be joined, but never shows the passphrase.
func wifiQRLook() qrLook {
	return qrLook{title: tr("wifi_qr_title"), accept: isWifiQR, say: wifiQRSays, wait: true}
}

// wifiQRSays is what the scan page says of a Wi-Fi code.
func wifiQRSays(code string) string {
	q, err := wifi.ParseQR(code)
	if err != nil {
		return wifiQRRefusal(err)
	}
	return fmt.Sprintf(tr("wifi_qr_read"), q.SSID)
}

// isWifiQR is whether a code is a Wi-Fi code at all, as ParseQR tells one from anything else.
func isWifiQR(code string) bool {
	_, err := wifi.ParseQR(code)
	return !errors.Is(err, wifi.ErrQRNotWifi)
}

// wifiQRRefusal says in the screen's language why a Wi-Fi code is not joined.
func wifiQRRefusal(err error) string {
	switch {
	case errors.Is(err, wifi.ErrQRWPA3):
		return tr("wifi_qr_wpa3")
	case errors.Is(err, wifi.ErrQREnterprise):
		return tr("wifi_qr_enterprise")
	case errors.Is(err, wifi.ErrQRWEP):
		return tr("wifi_qr_wep")
	case errors.Is(err, wifi.ErrQRInvalid), errors.Is(err, wifi.ErrQRNotWifi):
		return tr("wifi_qr_invalid")
	}
	return err.Error() // Join's own reason: a name or passphrase no network could have
}

// wifiScanQR scans for a Wi-Fi code over the Wi-Fi page and joins the network it names. What went
// wrong is said on the page, where a join's failure is.
func (d *Display) wifiScanQR() {
	code, err := d.scanQR(context.Background(), wifiQRFor, wifiQRLook())
	if err != nil {
		if errors.Is(err, ErrNothingScanned) {
			return
		}
		msg := err.Error()
		if muted, _ := mute.Get().Muted(); muted {
			msg = tr("qr_scan_muted")
		}
		d.wifiSay(strings.TrimPrefix(msg, "qr scan: "))
		return
	}
	q, err := wifi.ParseQR(code)
	if err != nil {
		slog.Info("wifi: a QR code not joined", "err", err)
		d.wifiSay(wifiQRRefusal(err))
		return
	}
	slog.Info("wifi: joining from a QR code", "ssid", q.SSID, "hidden", q.Hidden, "open", q.Passphrase == "")
	d.wifiJoining(wifi.Network{SSID: q.SSID, Secured: q.Passphrase != ""}, func() error {
		return wifiJoinQR(context.Background(), q)
	})
}

// wifiJoinQR joins what a code names. A variable so that a test joins nothing.
var wifiJoinQR = wifi.JoinQR

// wifiSay puts a line on the Wi-Fi page where a failed join's reason goes.
func (d *Display) wifiSay(msg string) {
	d.mu.Lock()
	if d.wifiOpen {
		d.wifi.err = msg
	}
	d.mu.Unlock()
	d.wake()
}
