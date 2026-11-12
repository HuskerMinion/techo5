package config

// BookPlaces is where each audiobook the library played was left, the latest first, as the device kept
// them before Music Assistant was asked for its own places (#524). It is no longer read or written:
// it stays only so that a state.json from before still loads, and it is carried as it is.
type BookPlaces struct {
	List []BookSpot `json:"list,omitempty"`
}

// BookSpot is where an audiobook was left: its address, the seconds into it, and when (Unix seconds).
type BookSpot struct {
	URI     string `json:"uri"`
	Seconds int    `json:"seconds"`
	At      int64  `json:"at"`
}
