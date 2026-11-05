package babymon

import (
	"log/slog"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// The Baby monitor light choices, as Home Assistant and the setup page name them.
var lightNames = []struct{ value, name string }{
	{config.BabyLightNormal, "Normal"},
	{config.BabyLightDim, "Dimmest"},
	{config.BabyLightOff, "Off"},
}

// LightName is the name of a BabyLight value; an unknown one reads as Normal.
func LightName(v string) string {
	for _, l := range lightNames {
		if l.value == v {
			return l.name
		}
	}
	return lightNames[0].name
}

// LightValue is the BabyLight value of a name or a value, and whether it is one.
func LightValue(s string) (string, bool) {
	for _, l := range lightNames {
		if s == l.name || (s == l.value && s != "") {
			return l.value, true
		}
	}
	return "", false
}

// LightNames is every choice's name, Normal first.
func LightNames() []string {
	out := make([]string, len(lightNames))
	for i, l := range lightNames {
		out[i] = l.name
	}
	return out
}

func newLightSelect(set func(string)) *esphome.Select {
	return &esphome.Select{
		Base:      esphome.Base{ObjectID: "baby_monitor_light", Name: "Baby monitor light", Icon: "mdi:lightbulb-night-outline", Category: esphome.CategoryConfig},
		Options:   LightNames(),
		OnCommand: set,
	}
}

// light is the device's choice. It is read only while somebody listens, since config.Get copies the
// settings and the screen asks on every frame.
func light() string { return config.Get().Security.BabyLight }

// Shown is whether the screen or the ring should say that somebody is listening now: somebody is,
// and the light is not set to Off.
func Shown() bool { return Listening() && light() != config.BabyLightOff }

// Dim is whether the mark that says so is to be at its least light.
func Dim() bool { return Listening() && light() == config.BabyLightDim }

// SetLight saves how the device shows a listener, by name or value, and shows it that way at once.
func (f *Feature) SetLight(s string) error {
	v, ok := LightValue(s)
	if !ok {
		return errUnknownLight
	}
	if err := config.Set().Security().BabyLight(v); err != nil {
		f.light.Set(LightName(light()))
		return err
	}
	slog.Info("security", "setting", "baby_monitor_light", "value", LightName(v))
	f.light.Set(LightName(v))
	f.Changed.Emit(struct{}{}) // the screen and the ring look again
	return nil
}
