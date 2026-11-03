//go:build !dot && !spot

package display

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// The event pop-ups' settings in Home Assistant, the same as on the screen (Settings, General).

type popupEntities struct {
	on, chime    *esphome.Switch
	lead, allDay *esphome.Select
	// shown and popping are the calendars chosen, as text (calendar_sources, calendar_popup_sources).
	shown, popping *esphome.TextSensor
}

func (d *Display) buildPopupEntities() {
	d.pop.shown = &esphome.TextSensor{Base: esphome.Base{ObjectID: "calendar_sources_chosen", Name: "Calendars shown",
		Icon: "mdi:calendar-multiple", Category: esphome.CategoryDiagnostic}}
	d.pop.popping = &esphome.TextSensor{Base: esphome.Base{ObjectID: "calendar_popup_sources_chosen", Name: "Calendars that pop up",
		Icon: "mdi:calendar-alert", Category: esphome.CategoryDiagnostic}}
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
	d.calendarListsChanged()
	d.wake()
}

// setPopupCalendars is the calendar_popup_sources action: the calendars whose events pop up, as entity
// ids separated by commas; empty is every one this device shows.
func setPopupCalendars(list string) error {
	shown := config.Get().Calendar.Sources
	var pick []string
	for _, id := range strings.Split(list, ",") {
		if id = strings.TrimSpace(id); id == "" || slices.Contains(pick, id) {
			continue
		}
		if !slices.Contains(shown, id) {
			return fmt.Errorf("calendar: %s is not one of this device's calendars; choose it with calendar_sources first", id)
		}
		pick = append(pick, id)
	}
	return config.Set().Calendar().PopupCalendars(pick)
}

// calendarListText is a list of calendar entities as Home Assistant reads it back: the ids separated by
// commas, the same way the actions take them, cut at a whole id and ended with the number left out when
// the list would pass the 255 characters a state can hold. "none" for an empty list.
func calendarListText(ids []string) string {
	if len(ids) == 0 {
		return "none"
	}
	const limit = 255
	out := strings.Join(ids, ", ")
	if len(out) <= limit {
		return out
	}
	for n := len(ids) - 1; n > 0; n-- {
		cut := strings.Join(ids[:n], ", ") + fmt.Sprintf(", +%d more", len(ids)-n)
		if len(cut) <= limit {
			return cut
		}
	}
	return fmt.Sprintf("%d calendars", len(ids))
}

// calendarListsChanged shows the calendars chosen, however they were chosen: here, on the screen, on the
// setup page or by the actions. The popup list says every one this device shows when none is picked.
func (d *Display) calendarListsChanged() {
	if d.pop.shown == nil {
		return
	}
	c := config.Get().Calendar
	popping := c.PopupCalendars
	if len(popping) == 0 {
		popping = c.Sources
	}
	// Only a change is sent: the watch below asks every ten seconds, and each Set is a message to Home
	// Assistant.
	if v := calendarListText(c.Sources); v != d.pop.shown.Get() {
		d.pop.shown.Set(v)
	}
	if v := calendarListText(popping); v != d.pop.popping.Get() {
		d.pop.popping.Set(v)
	}
}

// watchCalendarLists keeps the two text sensors current when the lists are changed from somewhere that
// does not tell the display (the actions and the setup page write the settings directly).
func (d *Display) watchCalendarLists(ctx context.Context) {
	t := time.NewTicker(10 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			d.calendarListsChanged()
		}
	}
}
