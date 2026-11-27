//go:build dot && !cannoli

package config

// DefaultMixing on the Dot: all seven microphones averaged. Measured against the middle microphone
// alone (techo5-dot docs/microphones.md, 2026-09-15), it heard speech 1-2.5 dB better in a quiet room,
// 2-4 dB better between 1.5 and 4.7 kHz, at 1 and 3 m and from either side; steering added nothing on
// top.
const DefaultMixing = MixAll
