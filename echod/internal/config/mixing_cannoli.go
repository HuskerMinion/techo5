//go:build dot && cannoli

package config

// DefaultMixing on the Echo Dot 5th gen: the superdirective beam on the measured array, which holds
// still and nulls the device's own speaker while it plays (hardware/mic/steered_mix.go).
//
// Replayed over the wake bench recordings (2026-11-23, 114 prompts of "Alexa" and "Hey Jarvis" over
// the unit's own music from 0 to 65%), it caught 79 against 60 for ch2 alone, the previous default (the
// microphone that hears the speaker least), and 51 for the three averaged. Live the same evening,
// talker in front: 19 caught over 0 to 65% with no false accept, where ch2 would have caught 16 of
// those 19 and nothing the beam missed. The gain is all over music: without it every mix caught the same.
const DefaultMixing = MixSuperdirective
