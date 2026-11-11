package config

import (
	"slices"
	"time"
)

// BookPlaces is where each audiobook the library played was left, the latest first, for it to offer to
// go on from. Music Assistant keeps no place for a book played through Home Assistant, so the device
// keeps its own. Apart from MusicAssistant so that one stays a value that can be compared.
type BookPlaces struct {
	List []BookSpot `json:"list,omitempty"`
}

// BookSpot is where an audiobook was left: its address, the seconds into it, and when (Unix seconds).
type BookSpot struct {
	URI     string `json:"uri"`
	Seconds int    `json:"seconds"`
	At      int64  `json:"at"`
}

// MaxBookSpots is how many books' places are kept; the one left longest ago goes first.
const MaxBookSpots = 50

// Spot is where the book uri was left, if it was.
func (b BookPlaces) Spot(uri string) (int, bool) {
	for _, s := range b.List {
		if s.URI == uri {
			return s.Seconds, true
		}
	}
	return 0, false
}

type BookPlacesWriter struct{ st *Store }

func (w Writer) BookPlaces() BookPlacesWriter { return BookPlacesWriter(w) }

// Save keeps where the book uri was left, as the latest; past MaxBookSpots the oldest is let go.
func (w BookPlacesWriter) Save(uri string, seconds int, at time.Time) error {
	return w.st.Update(func(c *Config) {
		rest := slices.DeleteFunc(slices.Clone(c.BookPlaces.List), func(s BookSpot) bool { return s.URI == uri })
		c.BookPlaces.List = append([]BookSpot{{URI: uri, Seconds: seconds, At: at.Unix()}}, rest...)
		if len(c.BookPlaces.List) > MaxBookSpots {
			c.BookPlaces.List = c.BookPlaces.List[:MaxBookSpots]
		}
	})
}

// Forget lets go of where the book uri was left: it was finished, or started over.
func (w BookPlacesWriter) Forget(uri string) error {
	if _, ok := w.st.Get().BookPlaces.Spot(uri); !ok {
		return nil // nothing to write
	}
	return w.st.Update(func(c *Config) {
		c.BookPlaces.List = slices.DeleteFunc(slices.Clone(c.BookPlaces.List), func(s BookSpot) bool { return s.URI == uri })
	})
}
