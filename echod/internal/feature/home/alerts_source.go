package home

import (
	"log/slog"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// Where the weather alerts come from, and how MeteoAlarm's are matched to home: automatic (the NWS in
// the U.S., MeteoAlarm in a country it covers) or one of them always; the MeteoAlarm regions, found
// from home's location or set by hand; how far around home a region still counts; and how often
// MeteoAlarm's feed is read. Offered on the device's settings (the source), the setup page and as
// Home Assistant's "Alerts source", "Alert regions", "Alerts nearby" and "MeteoAlarm refresh".

type alertSource int

const (
	alertsFromNone alertSource = iota
	alertsFromNWS
	alertsFromMeteoAlarm
)

func (s alertSource) String() string {
	switch s {
	case alertsFromNWS:
		return "NWS"
	case alertsFromMeteoAlarm:
		return "MeteoAlarm"
	}
	return "none"
}

// every is how long one fetch from the source is good for.
func (s alertSource) every() time.Duration {
	if s == alertsFromMeteoAlarm {
		return MeteoAlarmEvery()
	}
	return alertsEvery
}

// alertSourceFor is the source the setting and home's place pick. Automatic goes by Home Assistant's
// country; with none, by the place: the U.S., or one of MeteoAlarm's regions.
func alertSourceFor(lat, lon float64) alertSource {
	switch config.Get().Home.AlertsSource {
	case config.AlertsNWS:
		return alertsFromNWS
	case config.AlertsMeteoAlarm:
		return alertsFromMeteoAlarm
	}
	c := homeCountry()
	if _, ok := meteoAlarmCountries[c]; ok {
		return alertsFromMeteoAlarm
	}
	if inUS(lat, lon) {
		return alertsFromNWS
	}
	if c == "" && len(regionsNear(loadRegions(), lat, lon, 0)) > 0 {
		return alertsFromMeteoAlarm
	}
	return alertsFromNone
}

// alertSourceChoices are the source's options, in the order every place offers them; value is what
// config keeps.
var alertSourceChoices = []struct{ label, value string }{
	{"Automatic", ""},
	{"NWS (U.S.)", config.AlertsNWS},
	{"MeteoAlarm (Europe)", config.AlertsMeteoAlarm},
}

// AlertsSourceOptions are the source choices' labels.
func AlertsSourceOptions() []string {
	out := make([]string, len(alertSourceChoices))
	for i, c := range alertSourceChoices {
		out[i] = c.label
	}
	return out
}

// AlertsSourceIndex is the saved source's place among them; a value none has reads as automatic.
func AlertsSourceIndex() int {
	v := config.Get().Home.AlertsSource
	for i, c := range alertSourceChoices {
		if c.value == v {
			return i
		}
	}
	return 0
}

// AlertsNearChoices are how far around home a MeteoAlarm region still counts, in kilometers; 0 is
// only the regions home lies in.
var AlertsNearChoices = []int{0, 10, 25, 50}

// AlertsEveryChoices are how often MeteoAlarm's feed may be read, in minutes; the default is 10, the
// longest MeteoAlarm's terms allow what is shown to lag behind it.
var AlertsEveryChoices = []int{5, 10, 15, 30, 60}

func nearLabel(km int) string {
	if km == 0 {
		return "Off"
	}
	return strconv.Itoa(km) + " km"
}

func everyLabel(m int) string { return strconv.Itoa(m) + " min" }

// MeteoAlarmEvery is how often MeteoAlarm's feed is read.
func MeteoAlarmEvery() time.Duration {
	if m := config.Get().Home.AlertsEveryMinutes; slices.Contains(AlertsEveryChoices, m) {
		return time.Duration(m) * time.Minute
	}
	return meteoAlarmEvery
}

// AlertsNearKm is the saved reach around home, one of AlertsNearChoices.
func AlertsNearKm() int {
	if km := config.Get().Home.AlertsNearKm; slices.Contains(AlertsNearChoices, km) {
		return km
	}
	return 0
}

// regionCode is what an EMMA_ID looks like: two letters and three digits.
var regionCode = regexp.MustCompile(`^[A-Z]{2}[0-9]{3}$`)

// ParseAlertRegions reads a list of EMMA_IDs, separated by commas or spaces, as the setup page and
// Home Assistant take them; the first one that is not one is said.
func ParseAlertRegions(s string) (codes []string, bad string) {
	for _, c := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ';' || r == ' ' }) {
		c = strings.ToUpper(strings.TrimSpace(c))
		if !regionCode.MatchString(c) {
			return nil, c
		}
		if !slices.Contains(codes, c) {
			codes = append(codes, c)
		}
	}
	return codes, ""
}

// AlertRegionsNow are the MeteoAlarm regions home's alerts are matched against now, code and name: the
// ones set, or the ones home's location finds. Empty when home's location is not known.
func AlertRegionsNow() []string {
	lat, lon, err := homeLocation()
	if err != nil {
		return nil
	}
	h := config.Get().Home
	return areaFor(loadRegions(), lat, lon, h.AlertRegions, AlertsNearKm(), homeCountry()).names
}

func (f *Feature) buildAlertSettings() {
	f.alertsSrcSel = &esphome.Select{
		Base:      esphome.Base{ObjectID: "alerts_source", Name: "Alerts source", Icon: "mdi:alert-circle-outline", Category: esphome.CategoryConfig},
		Options:   AlertsSourceOptions(),
		OnCommand: func(v string) { f.SetAlertsSource(slices.Index(AlertsSourceOptions(), v)) },
	}
	f.alertRegionsTxt = &esphome.Text{
		Base:      esphome.Base{ObjectID: "alert_regions", Name: "Alert regions", Icon: "mdi:map-marker-radius", Category: esphome.CategoryConfig},
		MaxLength: 255,
		Pattern:   `^[A-Za-z0-9 ,;]*$`,
		OnCommand: func(v string) {
			if err := f.SetAlertRegions(v); err != "" {
				slog.Warn("home: alert regions not kept", "not a region", err)
				f.alertRegionsTxt.Set(strings.Join(config.Get().Home.AlertRegions, ", "))
			}
		},
	}
	var nears, everys []string
	for _, km := range AlertsNearChoices {
		nears = append(nears, nearLabel(km))
	}
	for _, m := range AlertsEveryChoices {
		everys = append(everys, everyLabel(m))
	}
	f.alertsNearSel = &esphome.Select{
		Base:    esphome.Base{ObjectID: "alerts_nearby", Name: "Alerts nearby", Icon: "mdi:map-marker-distance", Category: esphome.CategoryConfig},
		Options: nears,
		OnCommand: func(v string) {
			if i := slices.Index(nears, v); i >= 0 {
				f.SetAlertsNearKm(AlertsNearChoices[i])
			}
		},
	}
	f.alertsEverySel = &esphome.Select{
		Base:    esphome.Base{ObjectID: "alerts_refresh", Name: "MeteoAlarm refresh", Icon: "mdi:update", Category: esphome.CategoryConfig},
		Options: everys,
		OnCommand: func(v string) {
			if i := slices.Index(everys, v); i >= 0 {
				f.SetAlertsEvery(AlertsEveryChoices[i])
			}
		},
	}
}

// restoreAlertSettings shows the saved settings in Home Assistant.
func (f *Feature) restoreAlertSettings() {
	f.alertsSrcSel.Set(alertSourceChoices[AlertsSourceIndex()].label)
	f.alertRegionsTxt.Set(strings.Join(config.Get().Home.AlertRegions, ", "))
	f.alertsNearSel.Set(nearLabel(AlertsNearKm()))
	f.alertsEverySel.Set(everyLabel(int(MeteoAlarmEvery() / time.Minute)))
}

// refetchAlerts drops what was fetched and has the alerts fetched again the next time the screen draws.
func (f *Feature) refetchAlerts() {
	a := &f.alerts
	a.mu.Lock()
	a.view, a.fetched = AlertView{}, time.Time{}
	a.mu.Unlock()
	f.Changed.Emit(struct{}{})
}

// SetAlertsSource saves the source at i and has the alerts fetched again from it.
func (f *Feature) SetAlertsSource(i int) {
	if i < 0 || i >= len(alertSourceChoices) {
		return
	}
	if err := config.Set().Home().AlertsSource(alertSourceChoices[i].value); err != nil {
		slog.Error("saving the alerts source failed", "err", err)
		return
	}
	f.alertsSrcSel.Set(alertSourceChoices[i].label)
	f.refetchAlerts()
}

// SetAlertRegions saves the MeteoAlarm regions set by hand (empty: found from home's location); what
// is not an EMMA_ID is returned and nothing is saved.
func (f *Feature) SetAlertRegions(s string) (bad string) {
	codes, bad := ParseAlertRegions(s)
	if bad != "" {
		return bad
	}
	if err := config.Set().Home().AlertRegions(codes); err != nil {
		slog.Error("saving the alert regions failed", "err", err)
		return ""
	}
	f.alertRegionsTxt.Set(strings.Join(codes, ", "))
	f.refetchAlerts()
	return ""
}

// SetAlertsNearKm saves how far around home a region still counts.
func (f *Feature) SetAlertsNearKm(km int) {
	if !slices.Contains(AlertsNearChoices, km) {
		return
	}
	if err := config.Set().Home().AlertsNearKm(km); err != nil {
		slog.Error("saving the alerts reach failed", "err", err)
		return
	}
	f.alertsNearSel.Set(nearLabel(km))
	f.refetchAlerts()
}

// SetAlertsEvery saves how often MeteoAlarm's feed is read, in minutes. What was fetched stays: the
// next fetch is only due sooner or later (alertState.wait).
func (f *Feature) SetAlertsEvery(m int) {
	if !slices.Contains(AlertsEveryChoices, m) {
		return
	}
	if err := config.Set().Home().AlertsEveryMinutes(m); err != nil {
		slog.Error("saving the MeteoAlarm refresh failed", "err", err)
		return
	}
	f.alertsEverySel.Set(everyLabel(m))
}
