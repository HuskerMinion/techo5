//go:build !dot && !spot

package display

import (
	"log/slog"
	"net"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/bluetooth"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
	"github.com/HuskerMinion/techo5/echod/internal/feature/mute"
	"github.com/HuskerMinion/techo5/echod/internal/feature/sendspin"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
	"github.com/HuskerMinion/techo5/echod/internal/lib/wake"
	"github.com/HuskerMinion/techo5/echod/internal/lib/wifi"
)

// gather collects what the settings sheet shows. Cheap enough per frame: a few reads and one
// interface listing.
func (d *Display) gather(s scene, restartArm time.Time) settings {
	d.mu.Lock()
	st := settings{cat: d.cat, picker: d.picker, cardScroll: d.cardScroll, pickScroll: d.pickScroll, checking: d.checking, colors: d.colors, folder: d.folder, brightness: d.ceiling, auto: d.autoOn, now: s.now, restartArm: restartArm}
	d.mu.Unlock()
	if st.brightness == 0 {
		st.brightness = config.DefaultScreenBrightness
	}
	st.muted, _ = mute.Get().Muted()
	st.volume = media.Get().Volume()

	c := config.Get()
	st.name = c.Device.Name
	if st.name == "" {
		st.name = "TECHO5"
	}
	st.wakeWord = strings.ReplaceAll(c.Wake.Slot(0).ID, "_", " ")
	if m, ok := wake.Find(wake.Lib().Ours(), c.Wake.Slot(0).ID); ok && m.Phrase != "" {
		st.wakeWord = m.Phrase
	}
	if st.wakeWord == "" {
		st.wakeWord = "off"
	}
	st.weather = home.Get().WeatherSource()
	st.version = layout.Version
	st.night = config.Get().Screen.Night
	d.mu.Lock()
	st.wifi = wifiSummary(d.wifi.status)
	st.wifiName = cmpOr(d.wifi.status.SSID, "Not connected")
	if ws := d.wifi.status; !ws.Connected && ws.SSID != "" && ws.State != "" {
		st.wifiName = ws.SSID + " · " + ws.State
	}
	d.mu.Unlock()
	st.wifiOK = wifi.Available()
	st.btProxy = bluetooth.Get().Enabled()
	if !wifi.Available() {
		st.wifi = st.address
	}
	st.sendspin = sendspin.Get().Enabled()
	st.insecureTLS = c.Diag.InsecureTLS
	st.slot = slotName()
	st.address = address()

	return st
}

// slotName is which rootfs slot booted, from the file the initramfs leaves; empty on Android.
func slotName() string {
	b, err := os.ReadFile("/run/techo5/slot")
	if err != nil {
		return "-"
	}
	return strings.TrimSpace(string(b))
}

// address is the device's IPv4 address, which is how people find it on the network.
func address() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return "-"
	}
	for _, i := range ifaces {
		if i.Flags&net.FlagLoopback != 0 || i.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, _ := i.Addrs()
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil {
				return ipn.IP.String()
			}
		}
	}
	return "-"
}

// restart reboots the device the plain way; the slot store and the daemon's state are on disk
// already, so nothing needs saying goodbye to.
func restart() {
	syscall.Sync()
	if err := syscall.Reboot(syscall.LINUX_REBOOT_CMD_RESTART); err != nil {
		slog.Error("restart failed", "err", err)
	}
}
