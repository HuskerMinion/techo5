package timezone

import (
	"errors"
)

// Common are the zones most people setting a device up will want, in the order they are offered.
// Each has its POSIX rule here too, so it can be put in force on a unit whose image has no zone
// database: an early install that never had one, which updates only by swapping the daemon and so
// never got one later. Those rules are the footers of the zones' own zoneinfo files, which is what
// the database applies after its last listed change, so the two agree from now on (common_test.go).
var Common = []string{
	"America/New_York", "America/Chicago", "America/Denver", "America/Phoenix", "America/Los_Angeles",
	"America/Anchorage", "Pacific/Honolulu", "America/Toronto", "America/Vancouver", "America/Mexico_City",
	"America/Sao_Paulo", "Europe/London", "Europe/Dublin", "Europe/Paris", "Europe/Berlin",
	"Europe/Madrid", "Europe/Rome", "Europe/Amsterdam", "Europe/Stockholm", "Europe/Warsaw",
	"Europe/Moscow", "Africa/Johannesburg", "Asia/Dubai", "Asia/Kolkata", "Asia/Singapore",
	"Asia/Shanghai", "Asia/Tokyo", "Asia/Seoul", "Australia/Perth", "Australia/Sydney",
	"Pacific/Auckland", "UTC",
}

const (
	usEastern = "EST5EDT,M3.2.0,M11.1.0"
	usPacific = "PST8PDT,M3.2.0,M11.1.0"
	euCentral = "CET-1CEST,M3.5.0,M10.5.0/3"
	euWestern = "GMT0BST,M3.5.0/1,M10.5.0"
)

// commonRules is each of Common as a POSIX rule.
var commonRules = map[string]string{
	"America/New_York":    usEastern,
	"America/Chicago":     "CST6CDT,M3.2.0,M11.1.0",
	"America/Denver":      "MST7MDT,M3.2.0,M11.1.0",
	"America/Phoenix":     "MST7",
	"America/Los_Angeles": usPacific,
	"America/Anchorage":   "AKST9AKDT,M3.2.0,M11.1.0",
	"Pacific/Honolulu":    "HST10",
	"America/Toronto":     usEastern,
	"America/Vancouver":   "MST7", // daylight time all year from November 2026
	"America/Mexico_City": "CST6",
	"America/Sao_Paulo":   "<-03>3",
	"Europe/London":       euWestern,
	"Europe/Dublin":       "GMT0IST,M3.5.0/1,M10.5.0",
	"Europe/Paris":        euCentral,
	"Europe/Berlin":       euCentral,
	"Europe/Madrid":       euCentral,
	"Europe/Rome":         euCentral,
	"Europe/Amsterdam":    euCentral,
	"Europe/Stockholm":    euCentral,
	"Europe/Warsaw":       euCentral,
	"Europe/Moscow":       "MSK-3",
	"Africa/Johannesburg": "SAST-2",
	"Asia/Dubai":          "<+04>-4",
	"Asia/Kolkata":        "IST-5:30",
	"Asia/Singapore":      "<+08>-8",
	"Asia/Shanghai":       "CST-8",
	"Asia/Tokyo":          "JST-9",
	"Asia/Seoul":          "KST-9",
	"Australia/Perth":     "AWST-8",
	"Australia/Sydney":    "AEST-10AEDT,M10.1.0,M4.1.0/3",
	"Pacific/Auckland":    "NZST-12NZDT,M9.5.0,M4.1.0/3",
	"UTC":                 "UTC0",
}

// errNoDatabase is a zone name asked for on a unit with no zone database, and not one of Common.
var errNoDatabase = errors.New("this device has no time zone database, so only the common zones and Home Assistant's zone work here; choose one of those")
