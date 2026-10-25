package config

// Tessera is whether the device offers itself to the Tessera screen add-on as a screen
// (feature/tessera). Off unless somebody turns it on: on, the device carries a sub-device of its
// own with the entities and the action the add-on looks for, and the add-on may send it layouts.
type Tessera struct {
	On bool `json:"on,omitempty"`
	// Grid is the screen's grid as columns by rows ("6x3"), chosen in Home Assistant; empty is the
	// screen's own (feature/tessera gridFor, gridForSize).
	Grid string `json:"grid,omitempty"`
}

type TesseraWriter struct{ st *Store }

// On is taken back when it could not be saved, since a change to it restarts the daemon: one that did
// not hold would come back the other way after the restart.
func (w TesseraWriter) On(v bool) error {
	return w.st.UpdateOrUndo(func(c *Config) { c.Tessera.On = v })
}

func (w TesseraWriter) Grid(v string) error {
	return w.st.Update(func(c *Config) { c.Tessera.Grid = v })
}
