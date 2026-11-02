package config

import "errors"

// WifiQuiet is a window of the day in which the device keeps off the Wi-Fi (feature/wifiquiet): for a
// household that switches its access point off at night, so the device neither hops onto another network
// it knows nor goes on searching the air until morning, and is back on the network when the window ends.
type WifiQuiet struct {
	// On is whether the device keeps off the Wi-Fi in its hours. Off is the default: a device leaves its
	// Wi-Fi alone unless somebody asks for this.
	On bool `json:"on,omitempty"`

	// Hours is the window, "01:00-07:00", read with ParseWindow; one that ends before it starts runs
	// past midnight. Empty is the default.
	Hours string `json:"hours,omitempty"`
}

// DefaultWifiQuietHours is the window offered before anyone has chosen one: an access point switched
// off for the night is the case this is for.
const DefaultWifiQuietHours = "01:00-07:00"

// Window is the window in minutes since midnight, the default when none is set or the one set cannot
// be read.
func (q WifiQuiet) Window() (from, to int) {
	if from, to, ok := ParseWindow(q.Hours); ok {
		return from, to
	}
	from, to, _ = ParseWindow(DefaultWifiQuietHours)
	return from, to
}

// In is whether the minute of the day m falls in the window, which may run past midnight.
func (q WifiQuiet) In(m int) bool {
	from, to := q.Window()
	if from < to {
		return m >= from && m < to
	}
	return m >= from || m < to
}

type WifiQuietWriter struct{ st *Store }

func (w WifiQuietWriter) On(v bool) error {
	return w.st.Update(func(c *Config) { c.WifiQuiet.On = v })
}

// Hours sets the window; one ParseWindow cannot read is refused rather than stored.
func (w WifiQuietWriter) Hours(v string) error {
	if _, _, ok := ParseWindow(v); !ok {
		return errWifiQuietHours
	}
	return w.st.Update(func(c *Config) { c.WifiQuiet.Hours = v })
}

var errWifiQuietHours = errors.New("config: Wi-Fi quiet hours are a window like 01:00-07:00 that does not start and end at the same time")
