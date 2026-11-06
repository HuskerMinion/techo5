package config

import "time"

// Realtime is speech-to-speech voice (feature/voice, lib/realtime): a wake word slot chosen for it is
// answered by a model at OpenAI, xAI or Google, the voice going to them as it is heard and the answer
// coming back as speech. Nothing uses it until a slot is chosen for it (WakeWord.Realtime), on the setup
// page; Home Assistant has no switch for it.
type Realtime struct {
	Provider string `json:"provider,omitempty"` // openai, xai or gemini
	Key      string `json:"key,omitempty"`
	// Endpoint is a relay on the home network to use in place of the provider's own address, which can
	// hold the key itself.
	Endpoint string `json:"endpoint,omitempty"`
	Model    string `json:"model,omitempty"`
	Voice    string `json:"voice,omitempty"`

	// MCPToken is a Home Assistant token, of a user of its own with no administrator rights, for Home
	// Assistant's MCP server: the house's tools, as Assist has them. Empty offers the model the device's
	// own tools only.
	MCPToken string `json:"mcp_token,omitempty"`

	// DailyMinutes is the most minutes a day realtime sessions may be open, which the provider bills by;
	// zero is DefaultDailyMinutes. UsedDay and UsedSeconds are this day's, by the device's clock.
	DailyMinutes int    `json:"daily_minutes,omitempty"`
	UsedDay      string `json:"used_day,omitempty"`
	UsedSeconds  int    `json:"used_seconds,omitempty"`
}

const (
	DefaultDailyMinutes = 30
	MaxDailyMinutes     = 600
)

// Providers that realtime voice speaks.
var RealtimeProviders = []string{"openai", "xai", "gemini"}

// Ready is whether realtime voice can be used: a provider, and a key or an endpoint of its own.
func (r Realtime) Ready() bool {
	known := false
	for _, p := range RealtimeProviders {
		known = known || r.Provider == p
	}
	return known && (r.Key != "" || r.Endpoint != "")
}

// Daily is the day's cap in minutes.
func (r Realtime) Daily() int {
	if r.DailyMinutes <= 0 {
		return DefaultDailyMinutes
	}
	return min(r.DailyMinutes, MaxDailyMinutes)
}

// SecondsLeft is how many seconds of the day's cap are left on the day of now.
func (r Realtime) SecondsLeft(now time.Time) int {
	used := 0
	if r.UsedDay == now.Format(time.DateOnly) {
		used = r.UsedSeconds
	}
	return max(r.Daily()*60-used, 0)
}

type RealtimeWriter struct{ st *Store }

// Set sets everything but the key and the token, which only SetKey and SetMCPToken change: a form that
// shows them empty does not clear them by being saved.
func (w RealtimeWriter) Set(provider, endpoint, model, voice string, dailyMinutes int) error {
	return w.st.Update(func(c *Config) {
		r := &c.Realtime
		if r.Provider != provider || r.Endpoint != endpoint {
			// Another provider or another relay: the key was for the one before, and is not sent on.
			r.Key = ""
		}
		r.Provider, r.Endpoint, r.Model, r.Voice = provider, endpoint, model, voice
		r.DailyMinutes = min(max(dailyMinutes, 0), MaxDailyMinutes)
	})
}

func (w RealtimeWriter) SetKey(key string) error {
	return w.st.Update(func(c *Config) { c.Realtime.Key = key })
}

func (w RealtimeWriter) SetMCPToken(token string) error {
	return w.st.Update(func(c *Config) { c.Realtime.MCPToken = token })
}

// Used adds seconds to the day's count, starting it over on a new day.
func (w RealtimeWriter) Used(seconds int, now time.Time) error {
	return w.st.Update(func(c *Config) {
		day := now.Format(time.DateOnly)
		if c.Realtime.UsedDay != day {
			c.Realtime.UsedDay, c.Realtime.UsedSeconds = day, 0
		}
		c.Realtime.UsedSeconds += max(seconds, 0)
	})
}

// Realtime chooses realtime voice for this slot, or its usual assistant.
func (w WakeWriter) Realtime(on bool) error {
	// A slot with no wake word is not made for it: it would be one with no word to wake it. The first
	// is there whether or not it has been written.
	if w.slot < 0 {
		return errSlot(w.slot)
	}
	none := false
	conflict := false
	err := w.st.Update(func(c *Config) {
		switch {
		case on && w.slot < len(c.Wake.Words) && c.Wake.Words[w.slot].CallContact != "":
			conflict = true
		case w.slot > 0 && w.slot < len(c.Wake.Words) && on && c.Wake.Words[w.slot].ID == "":
			none = true // a slot with no word in it: nothing would wake it
		case w.slot < len(c.Wake.Words):
			c.Wake.Words[w.slot].Realtime = on
		case w.slot == 0:
			c.Wake.Words = append(c.Wake.Words, DefaultWakeWord())
			c.Wake.Words[0].Realtime = on
		default:
			none = true
		}
	})
	if err == nil && conflict {
		return errWakeMode
	}
	if err == nil && none {
		err = errSlot(w.slot)
	}
	return err
}
