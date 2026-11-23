//go:build !dot && !spot

package display

import (
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/cast"
)

// The Cast rows, at the end of Connections: turn receiving on, and show the code a phone scans to pair.
// Turning it on is also a switch in Home Assistant; the key is made when Cast is first turned on, so
// none of this needs Home Assistant at all.

// The Connections card's line mentions Cast on the Show; the Spot has no Cast and keeps the shared one.
func init() {
	categoryBlurbs[catConnections] = "Wi-Fi, Bluetooth, the Bluetooth proxy and Cast"
	categoryBlurbKeys[catConnections] = "cast_connections_blurb"
}

func castRows() []settingRow {
	c := config.Get().Cast
	sub := tr("ui_off")
	if c.Enabled {
		sub = tr("cast_phones_can_cast_here")
	}
	rows := []settingRow{{id: "cast", label: tr("cast_from_a_phone"), sub: sub, kind: ctlToggle, on: c.Enabled}}
	if !c.Enabled {
		return rows
	}
	pair := tr("cast_scan_it_in_the_app")
	return append(rows,
		settingRow{id: "castpair", label: tr("cast_pairing_code"), sub: pair, kind: ctlButton, button: tr("option_show")},
	)
}

// castRowTap is a tap on one of those rows.
func (d *Display) castRowTap(id string) {
	f := cast.Get()
	switch id {
	case "cast":
		f.SetEnabled(!config.Get().Cast.Enabled)
	case "castpair":
		d.closeSheet()
		f.ShowPairing(true)
	}
}
