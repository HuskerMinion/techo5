//go:build !dot

package setup

import (
	"strings"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/netbird"
	"github.com/HuskerMinion/techo5/echod/internal/feature/tailscale"
	"github.com/HuskerMinion/techo5/echod/internal/lib/wifi"
)

// The lines of the sections a device with a screen draws.
func TestScreenSummaryLines(t *testing.T) {
	join := func(s []string) string { return strings.Join(s, " · ") }
	for _, c := range []struct{ got, want string }{
		{join(videoSummary(config.Video{On: true, DLNA: true}, true)), "On · DLNA video on"},
		{join(videoSummary(config.Video{}, true)), "Off"},
		{join(addressSummary(wifi.Address{}, "")), "Automatic (DHCP)"},
		{join(addressSummary(wifi.Address{}, "the gateway did not answer")), "Automatic (DHCP) · last change not kept"},
		{join(dashboardPanelSummary(config.DashboardOff, true)), "Off"},
		{join(dashboardPanelSummary(config.DashboardDrawn, true)), "Drawn on the device · in place of the clock"},
		{join(dashboardServerSummary(config.Dashboard{Server: "192.168.1.20:9555"}, nil)), "Set up · no key"},
		{join(tailscaleSummary(tailscale.Status{})), "Off"},
		{join(tailscaleSummary(tailscale.Status{On: true, State: "Running", Name: "kitchen-show"})), "On as kitchen-show"},
		{join(netbirdSummary(netbird.Status{})), "Off"},
		{join(netbirdSummary(netbird.Status{Joining: true, Downloading: true, Got: 0.4})), "Downloading · 40%"},
	} {
		if c.got != c.want {
			t.Errorf("got %q, want %q", c.got, c.want)
		}
	}
}
