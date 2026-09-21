//go:build !dot

package display

import (
	"log/slog"
	"strings"

	"github.com/HuskerMinion/techo5/echod/internal/feature/timezone"
)

// The Time zone row. A device takes its zone from Home Assistant, which is right for almost every
// device and no use at all to one that has never met a Home Assistant — it sits on UTC with nothing
// able to change it. Choosing here holds until Follow Home Assistant is chosen again.

// followHA is the first choice in the region list: give the zone back to Home Assistant. common is
// the second, a short list that saves swiping through the 174 zones under America to reach Denver.
const (
	followHA = "Follow Home Assistant"
	common   = "Common"
)

// commonZones are the ones most people setting this up will want, in the order they are offered.
// Everything else is still there, a region at a time.
var commonZones = []string{
	"America/New_York", "America/Chicago", "America/Denver", "America/Phoenix", "America/Los_Angeles",
	"America/Anchorage", "Pacific/Honolulu", "America/Toronto", "America/Vancouver", "America/Mexico_City",
	"America/Sao_Paulo", "Europe/London", "Europe/Dublin", "Europe/Paris", "Europe/Berlin",
	"Europe/Madrid", "Europe/Rome", "Europe/Amsterdam", "Europe/Stockholm", "Europe/Warsaw",
	"Europe/Moscow", "Africa/Johannesburg", "Asia/Dubai", "Asia/Kolkata", "Asia/Singapore",
	"Asia/Shanghai", "Asia/Tokyo", "Asia/Seoul", "Australia/Perth", "Australia/Sydney",
	"Pacific/Auckland", "UTC",
}

// zoneValue is what the row shows: the zone in force, short enough to read.
func zoneValue() string {
	zone := timezone.Get().Current()
	switch {
	case zone == "":
		return "Not set"
	case !timezone.Get().SetHere():
		return zoneShort(zone)
	}
	return zoneShort(zone)
}

// zoneSub says where the zone came from, since that is what decides whether it will change again.
func zoneSub() string {
	if timezone.Get().SetHere() {
		return "Chosen here"
	}
	if timezone.Get().Current() == "" {
		return "Waiting for Home Assistant"
	}
	return "From Home Assistant"
}

// zoneShort is a zone as the row says it: the city, with the region behind it dropped, since the row
// has no space for "America/Indiana/Indianapolis". A POSIX rule is shown as it is.
func zoneShort(zone string) string {
	if i := strings.LastIndex(zone, "/"); i >= 0 {
		zone = zone[i+1:]
	}
	return strings.ReplaceAll(zone, "_", " ")
}

// zonePicker lists one region's zones, or the common ones, which are written as they are.
func zonePicker(region string) pickerView {
	if region == common {
		p := pickerView{title: common, cur: -1}
		current := timezone.Get().Current()
		for i, z := range commonZones {
			p.opts = append(p.opts, strings.ReplaceAll(z, "_", " "))
			if z == current {
				p.cur = i
			}
		}
		return p
	}
	zones := timezone.Zones(region)
	opts := make([]string, len(zones))
	cur := -1
	current := timezone.Get().Current()
	for i, z := range zones {
		opts[i] = strings.ReplaceAll(z, "_", " ")
		if region+"/"+z == current {
			cur = i
		}
	}
	return pickerView{title: region, opts: opts, cur: cur}
}

// chooseZone puts the i'th zone of a region in force.
func chooseZone(region string, i int) {
	if region == common {
		if i >= 0 && i < len(commonZones) {
			set(commonZones[i])
		}
		return
	}
	zones := timezone.Zones(region)
	if i < 0 || i >= len(zones) {
		return
	}
	set(region + "/" + zones[i])
}

func set(zone string) {
	if err := timezone.Get().Choose(zone); err != nil {
		slog.Warn("setting the time zone failed", "zone", zone, "err", err)
	}
}
