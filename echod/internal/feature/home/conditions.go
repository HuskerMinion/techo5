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
	case "partlycloudy":
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
