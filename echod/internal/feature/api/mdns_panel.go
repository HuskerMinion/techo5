//go:build panel

package api

import (
	"fmt"
	"strings"

	"github.com/libp2p/zeroconf/v2"
	"github.com/ygelfand/go-esphome-device/mdns"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/metrics"
)

// advertiseMDNS publishes what the library's advertiser would, on the interfaces the app reported.
// The library leaves the interfaces to zeroconf, which lists them with the netlink request Android 11
// and later refuse an app, finds none and fails; this passes them in. The records are the library's:
// Home Assistant reads the TXT keys to decide how to connect.
func advertiseMDNS(cfg mdns.Config) (interface{ Close() error }, error) {
	ips := make([]string, 0, len(cfg.IPs))
	for _, ip := range cfg.IPs {
		ips = append(ips, ip.String())
	}
	srv, err := zeroconf.RegisterProxy(cfg.Name, mdns.Service, mdns.Domain, cfg.Port, cfg.Name, ips,
		txtRecords(cfg), metrics.MulticastInterfaces())
	if err != nil {
		return nil, fmt.Errorf("mdns: register: %w", err)
	}
	return server{srv}, nil
}

type server struct{ *zeroconf.Server }

func (s server) Close() error {
	s.Shutdown()
	return nil
}

// txtRecords matches the library's, key for key.
func txtRecords(cfg mdns.Config) []string {
	var txt []string
	add := func(k, v string) {
		if v != "" {
			txt = append(txt, k+"="+v)
		}
	}
	add("friendly_name", cfg.FriendlyName)
	add("version", cfg.Version)
	add("mac", strings.ToLower(strings.NewReplacer(":", "", "-", "").Replace(cfg.MACAddress)))
	add("platform", cfg.Platform)
	add("board", cfg.Board)
	add("network", cfg.Network)
	if cfg.Encrypted {
		add("api_encryption", mdns.NoiseEncryption)
	} else {
		add("api_encryption_supported", mdns.NoiseEncryption)
		if cfg.Provisionable {
			add("api_provisioning", "zero-psk")
		}
	}
	return txt
}
