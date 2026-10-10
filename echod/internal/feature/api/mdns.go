package api

import (
	"context"
	"log/slog"
	"net"
	"time"

	"github.com/ygelfand/go-esphome-device/mdns"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/metrics"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
)

// mdnsRetry is how long to wait between attempts. There is no attempt limit: wifi can come back long
// after boot, and an advert that never reappears is a device that has to be found by address.
const mdnsRetry = 3 * time.Second

// advertise publishes the addresses the device actually has, and how its key stands, and republishes
// when either changes.
//
// The key decides what Home Assistant does with a device it finds (keyTXT). A device waiting to be
// added that announced itself as needing a key had Home Assistant asking for one nobody had, and the
// window shut on a key nobody would ever have.
//
// echod starts from init, well before wifi has associated. The addresses are passed rather than left
// to the library, which falls back to loopback when nothing routable exists — a record Home Assistant
// discovers and then cannot connect to, which is worse than no record at all. Discovery is a
// convenience and a device reachable by address works without it, so this only logs.
func (a *API) advertise(ctx context.Context, port int) {
	var movedAt time.Time // the last IPv4 move, which the announcements after it follow up
	for attempt := 1; ; attempt++ {
		ips := metrics.Addresses()
		if len(ips) == 0 {
			if attempt == 1 {
				slog.Info("waiting for an address before advertising over mdns")
			}
			if !pause(ctx, mdnsRetry) {
				return
			}
			continue
		}

		keyed := a.keyed.Load()
		encrypted, provisionable := keyTXT(keyed)
		info := a.server().Info
		adv, err := mdns.Advertise(mdns.Config{
			Name:          a.name,
			FriendlyName:  info.FriendlyName,
			Port:          port,
			MACAddress:    info.MACAddress,
			Version:       info.Version,
			Platform:      layout.Platform,
			Board:         layout.Board,
			Encrypted:     encrypted,
			Provisionable: provisionable,
			IPs:           ips,
		})
		if err != nil {
			// Only the first failure is worth a warning: after that it is the expected state of a
			// device waiting for its network, and saying so every few seconds buries everything else.
			if attempt == 1 {
				slog.Warn("mdns advertise failed, retrying", "err", err)
			} else {
				slog.Debug("mdns advertise failed", "attempt", attempt, "err", err)
			}
			if !pause(ctx, mdnsRetry) {
				return
			}
			continue
		}

		slog.Info("advertising over mdns", "name", a.name, "port", port, "addrs", metrics.AddressKey(ips), "key", keyName(keyed))

		// The registration stands until the addresses it was made with are no longer the ones the
		// device has (a lease that changed, or a network that arrived late), or the key changed kind.
		// For a while after a move it is also made again every so often (followMove).
		advertisedAt, again := time.Now(), false
		for metrics.AddressKey(metrics.Addresses()) == metrics.AddressKey(ips) && a.keyed.Load() == keyed {
			if !movedAt.IsZero() && time.Since(movedAt) < followMove && time.Since(advertisedAt) >= followEvery {
				again = true
				break
			}
			if !pause(ctx, mdnsRetry) {
				adv.Close()
				return
			}
		}
		adv.Close()
		if again {
			// Gone and back: a browser that saw the record already sees it anew, and Home Assistant,
			// which ignores a new address while it still counts the old connection as alive, gets
			// another chance once it has given that up. How long that takes is its keepalive, twenty
			// seconds a ping and four and a half of them missed, and more: it was seen at two minutes
			// and more after the move, past the gap.
			if !pause(ctx, goodbyeGap) {
				return
			}
			continue
		}
		moved := movedIPv4(ips, metrics.Addresses())
		slog.Info("addresses or key changed, re-advertising over mdns", "was", metrics.AddressKey(ips), "key was", keyName(keyed))
		if !moved {
			continue
		}
		// The IPv4 address Home Assistant connected to has gone for another, as when a fixed one is set
		// (lib/wifi/address.go): an IPv6 address arriving or rotating, or a first lease, is no reason to
		// drop working connections.
		//
		// Home Assistant does not take a new address for a device it still counts as connected, so a
		// device on Wi-Fi that roams is not torn from a working connection by a stray record. A
		// connection to an address the device no longer has looks connected until its keepalive gives
		// up, ninety seconds on. The device drops its side (which cannot reach Home Assistant from an
		// address it has left), and stays unannounced until Home Assistant has given up too: announced
		// sooner, the new address was ignored, and Home Assistant kept trying the old one for good.
		//
		// The gap runs from the last change: the address can move more than once in a row (a fixed
		// address tried and dropped, then another kept), and a gap counted from the first ended while
		// Home Assistant still held a connection made in between, so the new address was ignored again.
		a.server().Reconnect()
		movedAt = time.Now()
		last, until := v4Key(metrics.Addresses()), movedAt.Add(movedGap)
		for time.Now().Before(until) {
			if !pause(ctx, mdnsRetry) {
				return
			}
			if k := v4Key(metrics.Addresses()); k != last {
				movedAt = time.Now()
				last, until = k, movedAt.Add(movedGap)
				a.server().Reconnect()
			}
		}
	}
}

// movedIPv4 is whether the device's IPv4 addresses changed from one set to another: the address Home
// Assistant connected to is gone. A first address, an address lost with nothing in its place, and any
// change to the IPv6 addresses alone are not a move.
func movedIPv4(was, now []net.IP) bool {
	before, after := v4Key(was), v4Key(now)
	return before != "" && after != "" && before != after
}

func v4Key(ips []net.IP) string {
	var v4 []net.IP
	for _, ip := range ips {
		if ip.To4() != nil {
			v4 = append(v4, ip)
		}
	}
	return metrics.AddressKey(v4)
}

// After a move, the device is announced again every followEvery until followMove has passed since it,
// with goodbyeGap unannounced in between: a goodbye and a new record a moment apart, which a browser takes
// as the device gone and back rather than as a refresh of what it has.
const (
	followMove  = 5 * time.Minute
	followEvery = 30 * time.Second
	goodbyeGap  = 2 * time.Second
)

// movedGap is how long a device that changed address stays unannounced: longer than Home Assistant's
// keepalive (aioesphomeapi: 20 s pings, given up after 4.5 of them) takes to drop the old connection.
const movedGap = 2 * time.Minute

// keyTXT is what the record says about a kind of key. A real key is api_encryption: Home Assistant asks
// for it. The zero key of a device waiting to be added (adopt.go) is api_encryption_supported with
// api_provisioning=zero-psk: Home Assistant adds it with nothing typed and sets a key of its own, over
// zero-key Noise, or over plaintext as Home Assistant 2026.9 does (the library takes either while the
// zero key is served). No key is plaintext, which supports encryption without asking for it.
func keyTXT(kind int32) (encrypted, provisionable bool) {
	return kind == keyReal, kind == keyZero
}

func keyName(k int32) string {
	switch k {
	case keyZero:
		return "waiting for a Home Assistant to set one"
	case keyReal:
		return "set"
	}
	return "none"
}

// pause sleeps unless ctx ends first, reporting whether there is any point carrying on.
func pause(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}
