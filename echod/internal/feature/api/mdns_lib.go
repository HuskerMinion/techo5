//go:build !panel

package api

import (
	esphome "github.com/ygelfand/go-esphome-device"
	"github.com/ygelfand/go-esphome-device/mdns"
)

// advertiseMDNS is the library's own advertiser, which lists the interfaces itself.
func advertiseMDNS(cfg mdns.Config) (interface{ Close() error }, error) {
	return mdns.Advertise(cfg)
}

// firstStart leaves an Echo's key as the installer wrote it.
func (a *API) firstStart(psk *esphome.PSK) *esphome.PSK { return psk }
