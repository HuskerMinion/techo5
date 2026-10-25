package speaker

// playback is the stream the write loop feeds: the ALSA playback device on an Echo, a local socket to
// the app on a panel. Writes take interleaved Rate, Channels, S16_LE frames and block at the pace the
// output plays them, which is the clock everything queued here runs on.
type playback interface {
	Write(p []byte) (int, error)
	Close() error
	// Delay is how many frames written are still to be played, as near as the output can say; an
	// error when it cannot, and Latency falls back to DefaultLatency.
	Delay() (int, error)
}
