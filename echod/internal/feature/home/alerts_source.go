package home

import "time"

// Where the weather alerts come from. There is no setting for it: Home Assistant's country picks the
// source, the NWS in the U.S., MeteoAlarm in a country it covers. MeteoAlarm's regions for home are
// found from its location, and its feed is read every ten minutes; neither is a setting either.

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
		return meteoAlarmEvery
	}
	return alertsEvery
}

// alertSourceFor is the source home's country picks; with no country known, home's place: the U.S.,
// or one of MeteoAlarm's regions. A home in a country whose warnings come as outlines (the U.K.,
// Switzerland...) needs the country: MeteoAlarm has no regions there to find it by.
func alertSourceFor(lat, lon float64) alertSource {
	c := homeCountry()
	if _, ok := meteoAlarmCountries[c]; ok {
		return alertsFromMeteoAlarm
	}
	if inUS(lat, lon) {
		return alertsFromNWS
	}
	if c == "" && len(regionsNear(loadRegions(), lat, lon)) > 0 {
		return alertsFromMeteoAlarm
	}
	return alertsFromNone
}
