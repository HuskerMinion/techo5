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

// RefreshRxFilter has the Wi-Fi driver send its receive filter and multicast list to the firmware again,
// by switching promiscuous mode on and straight off (what `ip link set wlan0 promisc on`, then `off`
// does): each change calls the driver's ndo_set_rx_mode. After a reassociation the Echo Dot's MediaTek
// driver leaves most IPv4 multicast (mDNS) out until it is sent again (techo5-dot#45). Unlike
// RefreshMulticast it does not wait. An interface already promiscuous is left as it is.
func RefreshRxFilter(context.Context) error {
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
	if f&unix.IFF_PROMISC != 0 {
		return nil
	}
	if err := set(f | unix.IFF_PROMISC); err != nil {
		return err
	}
	// Read again: something else may have changed the flags in between. Should that read fail, the
	// flags as they were are put back rather than the interface left promiscuous.
	if g, err := flags(); err == nil {
		f = g
	}
	if err := set(f &^ unix.IFF_PROMISC); err != nil {
		_ = set(f &^ unix.IFF_PROMISC) // once more: a toggle left on would pass every frame up
		return err
	}
	return nil
}
