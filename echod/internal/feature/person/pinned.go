//go:build !dot

package person

import "github.com/HuskerMinion/techo5/echod/internal/update"

// modelFile is the model, as the release that first carried it published it: TensorFlow's
// person_detect.tflite, unchanged, which the tests have in testdata and check against this. Built into
// the daemon, so what a device runs is what its release said, wherever the file comes from.
var modelFile = update.Binary{
	URL:    "https://github.com/HuskerMinion/techo5/releases/download/v1.6.40/techo5-person-detect.tflite",
	SHA256: "808cfdfc0cf3a6fa6f6fa26bfa379ea97c16d5db7334637766e39c3408502e9d",
	Size:   300568,
}
