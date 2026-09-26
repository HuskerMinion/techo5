//go:build !dot && !spot

package display

import (
	"log/slog"
	"strings"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// The event pop-ups' settings in Home Assistant, the same as on the screen (Settings, General).

type popupEntities struct {
	on, chime    *esphome.Switch
	lead, allDay *esphome.Select
}

func (d *Display) buildPopupEntities() {
	d.pop.on = &esphome.Switch{Base: esphome.Base{ObjectID: "calendar_popups", Name: "Event pop-ups",
		Icon: "mdi:calendar-alert", Category: esphome.CategoryConfig}}
	d.pop.on.OnCommand = func(on bool) {
		if err := config.Set().Calendar().Popups(on); err != nil {
			slog.Error("saving the pop-ups failed", "err", err)
		}
		d.popupSettingsChanged()
	}
	d.pop.chime = &esphome.Switch{Base: esphome.Base{ObjectID: "calendar_popup_chime", Name: "Pop-up chime",
		Icon: "mdi:bell-ring-outline", Category: esphome.CategoryConfig}}
	d.pop.chime.OnCommand = func(on bool) {
		if err := config.Set().Calendar().PopupSilent(!on); err != nil {
			slog.Error("saving the pop-up chime failed", "err", err)
		}
		d.popupSettingsChanged()
	}
	d.pop.lead = &esphome.Select{Base: esphome.Base{ObjectID: "calendar_popup_before", Name: "Pop up",
		Icon: "mdi:timer-outline", Category: esphome.CategoryConfig}, Options: popupLeadLabels}
	d.pop.lead.OnCommand = func(v string) {
		for i, l := range popupLeadLabels {
			if l == v {
				if err := config.Set().Calendar().PopupLead(popupLeads[i]); err != nil {
					slog.Error("saving the pop-up time failed", "err", err)
				}
			}
		}
		d.popupSettingsChanged()
	}
	d.pop.allDay = &esphome.Select{Base: esphome.Base{ObjectID: "calendar_popup_all_day", Name: "All-day events",
		Icon: "mdi:calendar-today", Category: esphome.CategoryConfig}, Options: popupAllDayOpts}
	d.pop.allDay.OnCommand = func(v string) {
		if err := config.Set().Calendar().PopupAllDayNever(v == popupAllDayOpts[1]); err != nil {
			slog.Error("saving the all-day pop-ups failed", "err", err)
		}
		d.popupSettingsChanged()
	}
}

// popupSettingsChanged shows the pop-up settings, however they were changed, in Home Assistant.
func (d *Display) popupSettingsChanged() {
	if d.pop.on == nil {
		return
	}
	c := config.Get().Calendar
	d.pop.on.Set(c.Popups)
	d.pop.chime.Set(!c.PopupSilent)
	d.pop.lead.Set(popupLeadLabels[popupLeadIndex()])
	d.pop.allDay.Set(popupAllDayOpts[popupAllDayIndex()])
	d.wake()
}

// setPopupCalendars is the calendar_popup_sources action: the calendars whose events pop up, as entity
// ids separated by commas; empty is every one this device shows.
func setPopupCalendars(list string) error {
	var pick []string
	for _, id := range strings.Split(list, ",") {
		if id = strings.TrimSpace(id); id != "" {
			pick = append(pick, id)
		}
	}
	return config.Set().Calendar().PopupCalendars(pick)
}
