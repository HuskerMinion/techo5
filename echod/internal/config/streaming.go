package config

// Streaming is the device as a speaker other apps play to (feature/streaming): AirPlay from an
// iPhone, iPad or Mac, and Spotify Connect from the Spotify app. Both are off on a new device: each
// runs a program that listens on the network while it is on.
type Streaming struct {
	AirPlay bool `json:"airplay,omitempty"`
	Spotify bool `json:"spotify,omitempty"`

	// AirPlayClassic keeps AirPlay to the classic receiver where the image also carries AirPlay 2
	// (feature/streaming): for somebody whose group it will not play in step with. Off, so AirPlay 2, on a
	// new device and on one that had AirPlay before there was a choice.
	AirPlayClassic bool `json:"airplay_classic,omitempty"`

	// AirPlaySetsVolume makes an iPhone's AirPlay volume slider the device's volume (feature/streaming,
	// AirPlay sets the volume, #312). Off, as it comes: the slider only scales the stream inside
	// shairport-sync, the device's own volume staying where it is, so nothing changes for a device
	// that has AirPlay already.
	AirPlaySetsVolume bool `json:"airplay_sets_volume,omitempty"`

	// DLNA makes the device a DLNA renderer (feature/dlna): apps and music servers on the network send
	// it songs. Off on a new device, as the others are.
	DLNA bool `json:"dlna,omitempty"`

	// DLNAID is the renderer's UPnP id, made at random the first time it is needed and kept, so a
	// controller knows the device again and nothing about the device can be read from it.
	DLNAID string `json:"dlna_id,omitempty"`

	// SpotifyBitrate is the quality Spotify Connect asks Spotify for, in kbit/s: 96, 160 or 320
	// (SpotifyKbps). 0, as on a new device and one from before there was a choice, is 160.
	SpotifyBitrate int `json:"spotify_bitrate,omitempty"`

	// SpotifyNormalize evens out the loudness from song to song, as the Spotify app's own "Normalize
	// volume" does (librespot's volume normalization). Off, as it comes.
	SpotifyNormalize bool `json:"spotify_normalize,omitempty"`
}

// SpotifyBitrates are the qualities Spotify offers, in kbit/s, the first the one a new device uses.
var SpotifyBitrates = []int{160, 96, 320}

// SpotifyKbps is the quality Spotify Connect asks for: the one chosen, or 160 for none or one Spotify
// does not offer.
func (s Streaming) SpotifyKbps() int {
	for _, b := range SpotifyBitrates {
		if s.SpotifyBitrate == b {
			return b
		}
	}
	return SpotifyBitrates[0]
}

type StreamingWriter struct{ st *Store }

func (w StreamingWriter) AirPlay(v bool) error {
	return w.st.Update(func(c *Config) { c.Streaming.AirPlay = v })
}

func (w StreamingWriter) AirPlayClassic(v bool) error {
	return w.st.Update(func(c *Config) { c.Streaming.AirPlayClassic = v })
}

func (w StreamingWriter) AirPlaySetsVolume(v bool) error {
	return w.st.Update(func(c *Config) { c.Streaming.AirPlaySetsVolume = v })
}

func (w StreamingWriter) DLNAID(v string) error {
	return w.st.Update(func(c *Config) { c.Streaming.DLNAID = v })
}

func (w StreamingWriter) DLNA(v bool) error {
	return w.st.Update(func(c *Config) { c.Streaming.DLNA = v })
}

func (w StreamingWriter) Spotify(v bool) error {
	return w.st.Update(func(c *Config) { c.Streaming.Spotify = v })
}

func (w StreamingWriter) SpotifyBitrate(v int) error {
	return w.st.Update(func(c *Config) { c.Streaming.SpotifyBitrate = v })
}

func (w StreamingWriter) SpotifyNormalize(v bool) error {
	return w.st.Update(func(c *Config) { c.Streaming.SpotifyNormalize = v })
}
