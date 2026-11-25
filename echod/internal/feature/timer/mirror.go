package timer

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"
	"github.com/ygelfand/go-esphome-device/api"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
)

// The device's own timers in Home Assistant's timer list.
//
// Home Assistant 2026.12 keeps a timer list for the device (timer_list.<device>_timers), and every timer in
// it is counted down and rung here. Those are Home Assistant's. A timer set on the device — from the
// screen, the setup page, the assistant or timer_start — runs from this clock so that it rings with Home
// Assistant away, and Home Assistant cannot see it. With "Timers set here in Home Assistant" on, each
// one also gets a timer in that list, created by the device through an action: dashboards, automations
// and voice then see it beside Home Assistant's, and can pause, change or cancel it there.
//
// The device's timer stays the one that counts and rings; the one in the list follows it, and changes
// made to it in Home Assistant come back as the timer events every timer in the list sends, which change
// the device's timer instead of adding one. A timer set while Home Assistant is away is added when it is
// back, and one whose stand-in went missing, with a restart of Home Assistant say, gets a new one.
//
// A silent timer (timer_start_silent) is not added: it shows a wait an automation in Home Assistant
// keeps, which Home Assistant knows about already.

// listSince is the first Home Assistant with timer lists. An older one is not asked: the action does not
// exist there.
const listSince = "2026.12"

// listLeeway is how far a timer event may be from a timer here and still be the same timer: Home
// Assistant counts in whole seconds, rounded up.
const listLeeway = 2 * time.Second

// listSyncDelay is how long after Home Assistant starts listening its timer list is brought in line.
var listSyncDelay = 5 * time.Second

// inListSwitch is the setting as Home Assistant's switch.
func (t *Timers) buildInListSwitch() {
	t.inList = &esphome.Switch{
		Base: esphome.Base{
			ObjectID: "timers_in_ha_list",
			Name:     "Timers set here in Home Assistant",
			Icon:     "mdi:timer-sync-outline",
			Category: esphome.CategoryConfig,
		},
		OnCommand: func(on bool) { t.SetInList(on) },
	}
}

// InList reports whether the device's own timers go in Home Assistant's timer list.
func InList() bool { return config.Get().Timers.InHAList }

// SetInList saves the setting, and adds the timers already running when it is turned on.
func (t *Timers) SetInList(on bool) {
	if err := config.Set().Timers().InHAList(on); err != nil {
		slog.Warn("saving the timer list setting failed", "err", err)
	}
	t.inList.Set(on)
	slog.Info("timers set here in home assistant", "on", on)
	if on {
		t.listSync()
	}
}

// listEntity is a template Home Assistant renders to this device's timer list: the timer_list entity on
// the device with this one's MAC address. The device knows nothing else Home Assistant would know it by.
func listEntity() string {
	raw, _ := layout.FactoryMAC()
	mac := haMAC(raw)
	return "{% set ns = namespace(e='') %}{% for s in states.timer_list %}" +
		"{% if ('mac', '" + mac + "') in (device_attr(device_id(s.entity_id), 'connections') or []) %}" +
		"{% set ns.e = s.entity_id %}{% endif %}{% endfor %}{{ ns.e }}"
}

// haMAC is a MAC address as Home Assistant writes one in a device's connections: lower case, in pairs
// with colons, which is how layout.MAC writes it.
func haMAC(raw string) string { return layout.MAC(raw) }

// seconds is a duration as the timer list's actions take one, in whole seconds rounded up.
func seconds(d time.Duration) string {
	return strconv.FormatInt(int64(math.Ceil(d.Seconds())), 10)
}

// listCall asks Home Assistant to run a timer list action on this device's list, with what it answers
// handed to reply.
func listCall(service string, data map[string]string, template string, reply func(json.RawMessage, error)) {
	component.CallService.Emit(component.Call{
		Service:          "timer_list." + service,
		Data:             data,
		Templates:        map[string]string{"entity_id": listEntity()},
		ResponseTemplate: template,
		MinVersion:       listSince,
		Reply:            reply,
	})
}

// listAdd gives one of the device's own timers a stand-in in Home Assistant's list, unless it has one or
// is getting one.
func (t *Timers) listAdd(id string) {
	if !InList() {
		return
	}
	t.mu.Lock()
	c, ok := t.held[id]
	if !ok || !c.local || c.silent || c.ha != "" || c.listing {
		t.mu.Unlock()
		return
	}
	c.listing = true
	name, left, active := c.name, c.remaining(time.Now()), c.active
	t.mu.Unlock()

	data := map[string]string{"duration": seconds(left)}
	if name != "" {
		data["name"] = name
	}
	listCall("create_timer", data, "{{ (response.values() | list | first).timer_id }}", func(resp json.RawMessage, err error) {
		var haID string
		if err == nil {
			err = json.Unmarshal(resp, &haID)
		}
		if err == nil && haID == "" {
			err = fmt.Errorf("timers: Home Assistant created no timer")
		}
		t.mu.Lock()
		c, still := t.held[id]
		if c != nil {
			c.listing = false
		}
		if err != nil || !still {
			t.mu.Unlock()
			if err != nil {
				slog.Warn("adding a timer to home assistant's list failed", "name", name, "err", err)
			} else {
				// Ended while it was being added: the stand-in goes too.
				listCall("cancel_timer", map[string]string{"timer_id": haID}, "", logReply("cancel_timer"))
			}
			return
		}
		if c.ha == "" {
			c.ha = haID
		}
		saved := t.saved(time.Now())
		t.mu.Unlock()
		saveLocal(saved)
		slog.Info("timer added to home assistant's list", "name", name, "id", id, "ha", haID)
		if !active {
			// Added running, as the list makes every timer; paused here, so paused there.
			listCall("pause_timer", map[string]string{"timer_id": haID}, "", logReply("pause_timer"))
		}
	})
}

// logReply is a reply to an action whose answer only matters when it failed.
func logReply(service string) func(json.RawMessage, error) {
	return func(_ json.RawMessage, err error) {
		if err != nil {
			slog.Warn("a timer list action failed", "service", service, "err", err)
		}
	}
}

// listFollow tells Home Assistant's list that one of the device's own timers was changed here: paused,
// resumed, or given more or less time (by).
func (t *Timers) listFollow(id, what string, by time.Duration) {
	t.mu.Lock()
	c, ok := t.held[id]
	haID := ""
	if ok {
		haID = c.ha
	}
	t.mu.Unlock()
	if haID == "" {
		return
	}
	data := map[string]string{"timer_id": haID}
	service := map[string]string{"paused": "pause_timer", "resumed": "unpause_timer"}[what]
	if what == "changed" {
		service = "add_time"
		if by < 0 {
			service, by = "subtract_time", -by
		}
		data["duration"] = seconds(by)
	}
	if service != "" {
		listCall(service, data, "", logReply(service))
	}
}

// listDrop takes the stand-in of a timer canceled here out of Home Assistant's list. One that cannot be
// reached now is dropped when Home Assistant is next there (listSync).
func (t *Timers) listDrop(haID string) {
	if haID == "" {
		return
	}
	listCall("cancel_timer", map[string]string{"timer_id": haID}, "", func(_ json.RawMessage, err error) {
		if err == nil {
			return
		}
		slog.Info("a timer canceled here stays in home assistant's list until it is back", "ha", haID, "err", err)
		t.mu.Lock()
		t.listGone = append(t.listGone, haID)
		t.mu.Unlock()
	})
}

// listEvent applies a timer event for a stand-in to the device's timer it stands for, and reports whether
// the event was for one. An event for a timer that is being added and has no id yet is taken for its
// stand-in when its name and time match: Home Assistant says the timer started before it answers with
// its id.
func (t *Timers) listEvent(e esphome.TimerEvent) bool {
	now := time.Now()
	t.mu.Lock()
	id, c := t.standingFor(e)
	if c == nil {
		_, endedHere := t.ended[e.TimerID]
		endedHere = endedHere || slices.Contains(t.listGone, e.TimerID)
		t.mu.Unlock()
		// The stand-in of a timer that already ended here: it rang, or was canceled, here.
		return endedHere && e.Type != api.VoiceAssistantTimerEvent_VOICE_ASSISTANT_TIMER_STARTED
	}
	if c.ha == "" {
		c.ha = e.TimerID
	}

	switch e.Type {
	case api.VoiceAssistantTimerEvent_VOICE_ASSISTANT_TIMER_UPDATED:
		if now.Before(t.listQuiet) {
			// Home Assistant just came back and says what it has, which is what it had before it went:
			// a change made here meanwhile is newer. The device's timer stays as it is, and listSync
			// brings the stand-in in line with it in a moment.
			t.mu.Unlock()
			slog.Info("timer list's word passed over while it is brought in line", "id", id)
			return true
		}
		fallthrough
	case api.VoiceAssistantTimerEvent_VOICE_ASSISTANT_TIMER_STARTED:
		left := time.Duration(e.SecondsLeft) * time.Second
		changed := e.IsActive != c.active || absDuration(left-c.remaining(now)) > listLeeway
		if changed {
			c.left, c.at, c.active = left, now, e.IsActive
			c.total = max(c.total, time.Duration(e.TotalSeconds)*time.Second, left)
		}
		saved := t.saved(now)
		t.mu.Unlock()
		if changed {
			saveLocal(saved)
			slog.Info("timer changed in home assistant's list", "id", id, "left", left, "running", e.IsActive)
			select {
			case t.woke <- struct{}{}:
			default:
			}
		}
	case api.VoiceAssistantTimerEvent_VOICE_ASSISTANT_TIMER_CANCELLED:
		delete(t.held, id)
		saved := t.saved(now)
		t.mu.Unlock()
		saveLocal(saved)
		slog.Info("timer canceled in home assistant's list", "id", id, "name", c.name)
	case api.VoiceAssistantTimerEvent_VOICE_ASSISTANT_TIMER_FINISHED:
		// Finished early there, or at the same moment as here: it rings here, once.
		c.left, c.at = 0, now
		t.mu.Unlock()
		t.end([]string{id}, now)
	default:
		t.mu.Unlock()
	}
	return true
}

// standingFor is the device's own timer an event is for, by its stand-in's id, or, for one being added
// that has no id yet, by name and time. Call it with the lock held.
func (t *Timers) standingFor(e esphome.TimerEvent) (string, *timer) {
	for id, c := range t.held {
		if c.local && c.ha == e.TimerID {
			return id, c
		}
	}
	if e.Type != api.VoiceAssistantTimerEvent_VOICE_ASSISTANT_TIMER_STARTED {
		return "", nil
	}
	now := time.Now()
	for id, c := range t.held {
		if c.local && c.listing && c.ha == "" && c.name == e.Name &&
			absDuration(time.Duration(e.SecondsLeft)*time.Second-c.remaining(now)) <= listLeeway {
			return id, c
		}
	}
	return "", nil
}

func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

// listed is a timer in Home Assistant's list as listSync reads it.
type listed struct {
	ID     string  `json:"id"`
	Status string  `json:"status"`
	Left   float64 `json:"left"`
}

// listSync brings Home Assistant's list in line with the device's own timers, for when Home Assistant
// is back or the setting was turned on: timers without a stand-in get one, stand-ins Home Assistant no
// longer has are replaced, ones that were paused, resumed or changed here meanwhile are changed there,
// and stand-ins of timers canceled here meanwhile are canceled.
func (t *Timers) listSync() {
	if !InList() {
		return
	}
	running := "(response.values() | list | first).timers | selectattr('status', 'in', ['active', 'paused'])"
	listCall("get_timers", map[string]string{},
		"{{ "+running+" | map(attribute='timer_id') | list | tojson }}"+
			"|{{ "+running+" | map(attribute='status') | list | tojson }}"+
			"|{{ "+running+" | map(attribute='remaining') | list | tojson }}",
		func(resp json.RawMessage, err error) {
			if err != nil {
				slog.Warn("reading home assistant's timer list failed", "err", err)
				return
			}
			there, err := parseListed(resp)
			if err != nil {
				slog.Warn("reading home assistant's timer list failed", "err", err)
				return
			}
			t.listReconcile(there, time.Now())
		})
}

// parseListed reads listSync's answer: three JSON lists, of ids, statuses and seconds left, joined by
// bars. One template answer, since the device gets one string back.
func parseListed(resp json.RawMessage) (map[string]listed, error) {
	var joined string
	if err := json.Unmarshal(resp, &joined); err != nil {
		return nil, err
	}
	parts := strings.Split(joined, "|")
	if len(parts) != 3 {
		return nil, fmt.Errorf("timers: unexpected answer %q", joined)
	}
	var ids, statuses []string
	var lefts []float64
	for i, into := range []any{&ids, &statuses, &lefts} {
		if err := json.Unmarshal([]byte(parts[i]), into); err != nil {
			return nil, err
		}
	}
	if len(ids) != len(statuses) || len(ids) != len(lefts) {
		return nil, fmt.Errorf("timers: unexpected answer %q", joined)
	}
	out := make(map[string]listed, len(ids))
	for i, id := range ids {
		out[id] = listed{ID: id, Status: statuses[i], Left: lefts[i]}
	}
	return out, nil
}

// listReconcile is listSync's work once it knows what Home Assistant's list holds.
func (t *Timers) listReconcile(there map[string]listed, now time.Time) {
	type follow struct {
		id, what string
		by       time.Duration
	}
	var add []string
	var follows []follow

	t.mu.Lock()
	gone := t.listGone
	t.listGone = nil
	for id, c := range t.held {
		if !c.local || c.silent || c.listing {
			continue
		}
		s, ok := there[c.ha]
		if c.ha == "" || !ok {
			c.ha = ""
			add = append(add, id)
			continue
		}
		if running := s.Status == "active"; running != c.active {
			follows = append(follows, follow{id: id, what: map[bool]string{true: "resumed", false: "paused"}[c.active]})
		}
		if diff := c.remaining(now) - time.Duration(s.Left*float64(time.Second)); absDuration(diff) > listLeeway {
			follows = append(follows, follow{id: id, what: "changed", by: diff})
		}
	}
	t.mu.Unlock()

	for _, haID := range gone {
		if _, ok := there[haID]; ok {
			listCall("cancel_timer", map[string]string{"timer_id": haID}, "", logReply("cancel_timer"))
		}
	}
	for _, f := range follows {
		t.listFollow(f.id, f.what, f.by)
	}
	for _, id := range add {
		t.listAdd(id)
	}
	if len(gone)+len(follows)+len(add) > 0 {
		slog.Info("home assistant's timer list brought in line", "added", len(add), "changed", len(follows), "canceled", len(gone))
	}
}
