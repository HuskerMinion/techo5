package wifiwatch

import (
	"context"
	"time"
)

const (
	// noLease is how long the Wi-Fi may be joined with no IPv4 address before the DHCP client is
	// started again. A join asks for a lease and has one in seconds; a minute and a half is far past
	// that, and not so long that the device is out of Home Assistant's reach for long.
	noLease = 90 * time.Second

	// leaseWait is how long after a restart of the client before another: on a network whose DHCP
	// server is down, one every few minutes asks again without filling the log.
	leaseWait = 5 * time.Minute
)

// lease starts the DHCP client again when the Wi-Fi is joined but has had no IPv4 address for
// noLease. On the Echo Input, Fire OS 6.5.3's Wi-Fi firmware crashed about two minutes into a boot and
// the driver reset the chip, which takes wlan0 away and makes a new one: the supplicant joined again,
// but the DHCP client went on with the interface that was gone, so the device was joined with no
// address and nothing could reach it. Asking that client again does nothing; a new one does.
func (w *Watch) lease(ctx context.Context) {
	now := w.now()
	if _, joined := w.network(ctx); !joined || w.hasAddress() {
		w.noAddressAt = time.Time{}
		return
	}
	if w.noAddressAt.IsZero() {
		w.noAddressAt = now
		return
	}
	if now.Sub(w.noAddressAt) < noLease || now.Before(w.leaseNotTill) {
		return
	}
	w.say("wifi: joined with no address; starting the DHCP client again", "for", now.Sub(w.noAddressAt).Round(time.Second).String())
	w.restartDHCP()
	w.leaseNotTill = now.Add(leaseWait)
}
