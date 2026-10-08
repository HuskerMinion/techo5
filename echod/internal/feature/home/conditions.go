package home

import "strings"

// ConditionWords turns Home Assistant's weather condition into words for a screen: "partlycloudy"
// is "Partly cloudy". The clock, the Spot's face and a dashboard's weather tile all say it the same.
func ConditionWords(c string) string {
	switch c {
	case "", "unknown", "unavailable":
		return ""
	case "clear-night":
		return "Clear"
	case "partlycloudy", PartlyCloudyNight:
		return "Partly cloudy"
	case "lightning-rainy":
		return "Thunderstorms"
	case "snowy-rainy":
		return "Sleet"
	case "exceptional":
		return "Severe"
	case "windy-variant":
		return "Windy"
	}
	// "sunny", "cloudy", "rainy", "pouring", "fog", "hail", "snowy", "windy", "lightning"…
	return strings.ToUpper(c[:1]) + c[1:]
}

// PartlyCloudyNight is partly cloudy with the sun down, drawn with the moon behind the cloud. Home
// Assistant's conditions have a night form only for clear, so at one in the morning a partly cloudy
// night was drawn with a sun. It is ours, not Home Assistant's, and only the reading for now has it:
// a forecast's day is drawn as the day.
const PartlyCloudyNight = "partlycloudy-night"

// atNight is a condition as the sky looks with the sun down. Each one drawn with a sun by day has a
// night form drawn with the moon: sunny is clear-night (some integrations report sunny all night), and
// partly cloudy is PartlyCloudyNight. The rest have no sun in them and look the same either way.
func atNight(c string) string {
	switch c {
	case "sunny":
		return "clear-night"
	case "partlycloudy":
		return PartlyCloudyNight
	}
	return c
}
