//go:build dot && cannoli

package config

// DefaultMixing on the Echo Dot 5th gen: ch2, the microphone that hears the speaker least (6 dB under
// the other two). The same 60 "Hey Jarvis" said over the unit's own music and in a quiet room
// (wakebench -r, 2026-11-23, two runs), replayed through each mix and the cascade canceller: 27 caught
// with ch2 alone against 24 with the three averaged, and ch2 was ahead or level at every volume. With
// the canceller as loud as the echo it has to remove, the 6 dB less echo is worth more than averaging
// three microphones' self-noise. A weighting of 0.2/0.2/0.6 caught 23.
const DefaultMixing = MixCenter
