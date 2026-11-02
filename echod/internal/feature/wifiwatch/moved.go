package wifiwatch

import "context"

// moved gets a new lease when the supplicant has gone from one saved network to another by itself. udhcpc
// only asks again when its lease runs out, so until then the device kept the old network's address on the
// new one, where nothing could reach it (#384). A join from the screen renews already; this covers the
// supplicant's own choice, at a restart or when the first network fades.
func (w *Watch) moved(ctx context.Context) {
	ssid, joined := w.network(ctx)
	if !joined || ssid == "" {
		return
	}
	if w.ssid != "" && ssid != w.ssid {
		w.say("wifi: joined another network; renewing the lease")
		w.renew()
	}
	w.ssid = ssid
}
