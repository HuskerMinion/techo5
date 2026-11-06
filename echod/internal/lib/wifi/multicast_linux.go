//go:build linux

package wifi

import (
	"context"
	"time"

	"golang.org/x/sys/unix"
)

// allMultiFor is how long RefreshMulticast leaves the interface taking in all multicast.
const allMultiFor = 5 * time.Second

// RefreshMulticast has the Wi-Fi driver program its multicast filter again, by taking in all
// multicast for a few seconds and then going back. On the Echo Dot's MediaTek radio the filter can be
// lost after days up: broadcasts and the device's own traffic still flow, but nothing sent to a group
// arrives, so mDNS (AirPlay, Sendspin, ESPHome discovery) and router adverts stop (TECHO5 #458). The
// link stays up throughout, unlike a reassociation. An interface already taking in all multicast is
// left as it is.
func RefreshMulticast(ctx context.Context) error {
	fd, err := unix.Socket(unix.AF_INET, unix.SOCK_DGRAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	flags := func() (uint16, error) {
		ifr, err := unix.NewIfreq(iface())
		if err != nil {
			return 0, err
		}
		if err := unix.IoctlIfreq(fd, unix.SIOCGIFFLAGS, ifr); err != nil {
			return 0, err
		}
		return ifr.Uint16(), nil
	}
	set := func(f uint16) error {
		ifr, err := unix.NewIfreq(iface())
		if err != nil {
			return err
		}
		ifr.SetUint16(f)
		return unix.IoctlIfreq(fd, unix.SIOCSIFFLAGS, ifr)
	}
	f, err := flags()
	if err != nil {
		return err
	}
	if f&unix.IFF_ALLMULTI != 0 {
		return nil
	}
	if err := set(f | unix.IFF_ALLMULTI); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
	case <-time.After(allMultiFor):
	}
	// The flags are read again: something else may have changed them in those seconds.
	if f, err = flags(); err != nil {
		return err
	}
	return set(f &^ unix.IFF_ALLMULTI)
}
