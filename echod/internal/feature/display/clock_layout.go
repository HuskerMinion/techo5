//go:build !dot && !spot

package display

import (
	"image/color"
	"log/slog"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// Where the home screen's clock and weather sit, and the date's color: for a photo behind the clock,
// whose subject is so often in the middle. The center, the weather top left and the date's usual
// color stay the default.

// clockPositions are the choices, with what the config keeps for each; the center is kept as nothing.
var clockPositions = []clockPosition{
	{"Center", ""},
	{"Bottom left", "bottom-left"},
	{"Bottom center", "bottom-center"},
	{"Bottom right", "bottom-right"},
}

// weatherPositions are Weather position's choices, with what the config keeps for each; the top left,
// where the weather has always been, is kept as nothing.
var weatherPositions = []clockPosition{
	{"Top left", ""},
	{"Top right", "top-right"},
}

// dateColors are the date's colors; Default is the theme's own.
var dateColors = []struct {
	label, value string
	c            color.RGBA
}{
	{"Default", "", color.RGBA{}},
	{"White", "white", color.RGBA{240, 240, 240, 255}},
	{"Gold", "gold", color.RGBA{255, 200, 90, 255}},
	{"Sky", "sky", color.RGBA{120, 190, 255, 255}},
	{"Mint", "mint", color.RGBA{120, 230, 170, 255}},
	{"Rose", "rose", color.RGBA{255, 140, 170, 255}},
}

func dateColorIndex() int {
	v := config.Get().Screen.DateColor
	for i, c := range dateColors {
		if c.value == v {
			return i
		}
	}
	return 0
}

func dateColorOptions() []string {
	out := make([]string, len(dateColors))
	for i, c := range dateColors {
		out[i] = c.label
	}
	return out
}

func weatherPositionIndex() int {
	v := config.Get().Screen.WeatherPosition
	for i, p := range weatherPositions {
		if p.value == v {
			return i
		}
	}
	return 0
}

func weatherPositionOptions() []string {
	out := make([]string, len(weatherPositions))
	for i, p := range weatherPositions {
		out[i] = p.label
	}
	return out
}

// weatherOnRight is whether the home screen's weather sits at the top right.
func weatherOnRight() bool {
	return weatherPositions[weatherPositionIndex()].value == "top-right"
}

func setWeatherPosition(s *esphome.Select, i int) {
	if i < 0 || i >= len(weatherPositions) {
		return
	}
	if err := config.Set().Screen().WeatherPosition(weatherPositions[i].value); err != nil {
		slog.Error("saving the weather position failed", "err", err)
		return
	}
	if s != nil {
		s.Set(weatherPositions[i].label)
	}
}

// clockAlign is where the clock sits across: -1 on the left, 0 centered, 1 on the right; and whether
// it sits at the foot rather than across the middle.
func clockAlign() (align int, foot bool) {
	switch clockPositions[clockPositionIndex()].value {
	case "bottom-left":
		return -1, true
	case "bottom-center":
		return 0, true
	case "bottom-right":
		return 1, true
	}
	return 0, false
}

// dateColor is the date's color: the one chosen, or fallback, the theme's. A chosen one is kept over a
// photo too (chosenColor), where the theme's would turn gold.
func dateColor(fallback color.RGBA) color.Color {
	if i := dateColorIndex(); i > 0 {
		return chosenColor(dateColors[i].c)
	}
	return fallback
}

func setDateColor(s *esphome.Select, i int) {
	if i < 0 || i >= len(dateColors) {
		return
	}
	if err := config.Set().Screen().DateColor(dateColors[i].value); err != nil {
		slog.Error("saving the date color failed", "err", err)
		return
	}
	if s != nil {
		s.Set(dateColors[i].label)
	}
}

// clockLayoutRows are the three settings on the screen, under Display.
func clockLayoutRows() []settingRow {
	return []settingRow{
		{id: "clockpos", label: "Clock position", sub: "Not for Dashboard, Agenda or World", kind: ctlChoice, value: clockPositions[clockPositionIndex()].label},
		{id: "weatherpos", label: "Weather position", sub: "The weather's corner over the clock", kind: ctlChoice, value: weatherPositions[weatherPositionIndex()].label},
		{id: "datecolor", label: "Date color", sub: "The date and AM/PM", kind: ctlChoice, value: dateColors[dateColorIndex()].label},
	}
}

// clockLayoutPicker is the list for any of them.
func clockLayoutPicker(id string) (pickerView, bool) {
	switch id {
	case "clockpos":
		return pickerView{title: "Clock position", opts: clockPositionOptions(), cur: clockPositionIndex()}, true
	case "weatherpos":
		return pickerView{title: "Weather position", opts: weatherPositionOptions(), cur: weatherPositionIndex()}, true
	case "datecolor":
		return pickerView{title: "Date color", opts: dateColorOptions(), cur: dateColorIndex()}, true
	}
	return pickerView{}, false
}

// chooseClockLayout takes a choice from any of the lists, and says whether id was one of them.
func (d *Display) chooseClockLayout(id string, i int) bool {
	switch id {
	case "clockpos":
		setClockPosition(d.clockPos, i)
	case "weatherpos":
		setWeatherPosition(d.weatherPos, i)
	case "datecolor":
		setDateColor(d.dateCol, i)
	default:
		return false
	}
	d.wake()
	return true
}

// clockLayoutSelects are Clock position, Date color and Weather position in Home Assistant.
func clockLayoutSelects(wake func()) (position, date, weather *esphome.Select) {
	position = clockPositionSelect(wake)
	weather = &esphome.Select{
		Base: esphome.Base{
			ObjectID: "screen_weather_position",
			Name:     "Weather position",
			Icon:     "mdi:weather-partly-cloudy",
			Category: esphome.CategoryConfig,
		},
		Options: weatherPositionOptions(),
	}
	weather.OnCommand = func(v string) {
		for i, p := range weatherPositions {
			if p.label == v {
				setWeatherPosition(weather, i)
				wake()
				return
			}
		}
	}
	date = &esphome.Select{
		Base: esphome.Base{
			ObjectID: "screen_date_color",
			Name:     "Date color",
			Icon:     "mdi:palette",
			Category: esphome.CategoryConfig,
		},
		Options: dateColorOptions(),
	}
	date.OnCommand = func(v string) {
		for i, c := range dateColors {
			if c.label == v {
				setDateColor(date, i)
				wake()
				return
			}
		}
	}
	return position, date, weather
}
