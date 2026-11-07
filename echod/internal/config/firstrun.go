package config

// FirstRun is how far the device has got with first run (feature/firstrun), the same on every device:
// the step it is on, and whether it is done. Seen says this build has already looked, once, whether the
// device was set up before first run existed (firstrun.Settle); until it has, first run is not pending,
// so a device never sees it before that look, and a test's fresh config never does.
type FirstRun struct {
	Seen bool   `json:"seen,omitempty"`
	Done bool   `json:"done,omitempty"`
	Step string `json:"step,omitempty"`
	// Marked says the marker a gift install or a reset leaves (feature/firstrun) has been acted on: first
	// run went back to its start, once. A restart partway through then keeps its step.
	Marked bool `json:"marked,omitempty"`
}

// FirstRunPending is whether first run still has the device: looked at, and not done. A Show whose
// welcome card was put away before first run existed is done as well.
func (c Config) FirstRunPending() bool {
	return c.FirstRun.Seen && !c.FirstRun.Done && !c.Screen.Welcomed
}

type FirstRunWriter struct{ st *Store }

func (w Writer) FirstRun() FirstRunWriter { return FirstRunWriter(w) }

// Settle records the one look at whether the device was set up already; done says it was.
func (w FirstRunWriter) Settle(done bool) error {
	return w.st.Update(func(c *Config) {
		c.FirstRun.Seen = true
		c.FirstRun.Done = c.FirstRun.Done || done
	})
}

// Step saves the step first run is on.
func (w FirstRunWriter) Step(s string) error {
	return w.st.Update(func(c *Config) { c.FirstRun.Step = s })
}

// Finish puts first run away for good.
func (w FirstRunWriter) Finish() error {
	return w.st.Update(func(c *Config) {
		c.FirstRun = FirstRun{Seen: true, Done: true}
		c.Screen.Welcomed = true
	})
}

// Marked brings first run back to its start for the marker a gift install leaves, whatever the
// settings it left in place say: not done, not welcomed, at the first step, no room.
func (w FirstRunWriter) Marked() error {
	return w.st.Update(func(c *Config) {
		c.FirstRun = FirstRun{Seen: true, Marked: true}
		c.Screen.Welcomed = false
		c.Home.Area = ""
	})
}

// ForSomeoneElse puts every setting back to the defaults, for a device being passed on, with first
// run to do (feature/firstrun.Reset). Only what the daemon was started with stays.
func (w FirstRunWriter) ForSomeoneElse() error {
	return w.st.Update(func(c *Config) {
		device := c.Device
		*c = Defaults()
		c.Device = device
		c.FirstRun = FirstRun{Seen: true}
	})
}
