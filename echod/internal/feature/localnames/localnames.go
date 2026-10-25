// Package localnames keeps the .local names this device was given findable: Home Assistant's address
// as the home_assistant action gave it (http://homeassistant.local:8123, the address Home Assistant
// itself suggests) and Music Assistant's. Nothing on a TECHO5 root filesystem can look one up, so
// every request to it failed with "no such host" - the forecast, the calendars, the dashboards, text
// to speech. Each is looked up over multicast DNS (lib/localname) and written into the hosts file.
//
// Only where /etc/hosts is the link to /run/hosts that the root filesystem makes (tools/linux
// mkrootfs.sh): elsewhere the hosts file is not this daemon's to write, and it does nothing.
package localnames

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"net"
	neturl "net/url"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
	"github.com/HuskerMinion/techo5/echod/internal/lib/localname"
)

func init() {
	component.Register(component.Network, &Feature{}, component.Order(15))
}

var (
	// hostsLink is the hosts file as programs open it, and hostsFile where it really is.
	hostsLink = "/etc/hosts"
	hostsFile = "/run/hosts"

	// every is how often a name found is looked up again: an address from DHCP can change. soon is how
	// often the addresses given are looked at, and a name not found yet is asked for.
	every = 5 * time.Minute
	soon  = 15 * time.Second

	// resolve and onOwnNetwork are lib/localname's, or a test's.
	resolve      = localname.ResolveAll
	onOwnNetwork = localname.OnOwnNetwork
)

// pick is the address to keep for a name from what it answered (got, the device's own networks
// first): the one known while it is still among them, unless the known one is on none of the device's
// networks and another is. A second responder answering for the name, or one with a stale address,
// does not move Home Assistant away from where it is.
func pick(known net.IP, got []net.IP) net.IP {
	if known != nil && slices.ContainsFunc(got, known.Equal) && (onOwnNetwork(known) || !onOwnNetwork(got[0])) {
		return known
	}
	return got[0]
}

type Feature struct{}

func (*Feature) Name() string { return "local names" }

// Run looks a new name up at once (within soon of it being given), and each again every few
// minutes. An address found stays written while its name goes unanswered: a responder that misses a
// question now and then must not take Home Assistant away with it. Nor does another address for it,
// while the one written is still among those answered (pick); a change is logged as a warning.
func (*Feature) Run(ctx context.Context) error {
	if target, err := os.Readlink(hostsLink); err != nil || target != hostsFile {
		slog.Debug("local names: the hosts file is not this device's to write", "file", hostsLink)
		<-ctx.Done()
		return nil
	}
	known := map[string]net.IP{} // by the name in lowercase, as it is asked for
	asked := map[string]time.Time{}
	warned := map[string]bool{} // said once per name, not every turn it goes unanswered
	written := map[string]net.IP{}
	for {
		given := names()
		want := map[string]bool{}
		for name := range given {
			want[strings.ToLower(name)] = true
		}
		for name := range known {
			if !want[name] {
				delete(known, name)
				delete(asked, name)
				delete(warned, name)
			}
		}
		for name := range want {
			if known[name] != nil && time.Since(asked[name]) < every {
				continue
			}
			lctx, cancel := context.WithTimeout(ctx, 3*time.Second)
			ips, err := resolve(lctx, name)
			cancel()
			if err != nil {
				if known[name] == nil && !warned[name] {
					slog.Warn("local names: no answer", "name", name)
					warned[name] = true
				}
				continue // asked again at the next turn, soon
			}
			ip := pick(known[name], ips)
			switch {
			case known[name] == nil:
				slog.Info("local names: found", "name", name, "address", ip.String())
			case !ip.Equal(known[name]):
				// Said louder: everything this device asks of Home Assistant goes there now.
				slog.Warn("local names: the address changed", "name", name, "was", known[name].String(),
					"now", ip.String(), "answered", fmt.Sprint(ips))
			}
			known[name], asked[name], warned[name] = ip, time.Now(), false
		}
		// Each name as it was given, and in lowercase (localname.WriteHosts).
		hosts := map[string]net.IP{}
		for name := range given {
			if ip := known[strings.ToLower(name)]; ip != nil {
				hosts[name] = ip
			}
		}
		if !maps.EqualFunc(written, hosts, func(a, b net.IP) bool { return a.Equal(b) }) {
			if err := localname.WriteHosts(hostsFile, hosts); err != nil {
				slog.Error("local names: writing the hosts file", "err", err)
				clear(written) // written again at the next turn
			} else {
				written = hosts
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(soon):
		}
	}
}

// names are the .local hosts in the addresses this device was given, as they were given.
func names() map[string]bool {
	out := map[string]bool{}
	for _, raw := range []string{hass.Get().Address(), config.Get().MusicAssistant.URL} {
		u, err := neturl.Parse(raw)
		if err != nil {
			continue
		}
		if h := strings.TrimSuffix(u.Hostname(), "."); localname.IsLocal(h) {
			out[h] = true
		}
	}
	return out
}
