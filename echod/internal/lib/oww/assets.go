// Package oww runs openWakeWord models: a shared mel front end and speech embedding feeding a
// per-wake-word classifier.
package oww

import _ "embed"

// The mel and embedding models are the same for every openWakeWord wake word, and every
// classifier is trained against these exact two, so they ship with the engine rather than
// alongside the models a user installs.
//
// Embedded on purpose, against the rule that big data (dictionaries, fonts, word lists, models) is
// downloaded on demand into the state folder rather than built into the daemon, as the person
// detection model is (feature/person). Hearing the wake word is the device's core job: it must work
// out of the box, offline, with nothing downloaded first.
var (
	//go:embed assets/melspectrogram.tflite
	melModel []byte

	//go:embed assets/embedding_model.tflite
	embedModel []byte
)
