//go:build dot && linux && !panel

package improv

import (
	"embed"
	"encoding/binary"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
)

// The spoken prompts a device with no screen says while it is set up (tools/make-dot-prompts.py, which
// has what each says): raw 16-bit mono PCM at 16 kHz, raised here to the speaker's rate.
//
//go:embed say/*.pcm
var sayFiles embed.FS

const sayRate = 16000

// said is the prompt named name as notes to follow a tone with; none when it is not there.
func said(name string) []speaker.Note {
	b, err := sayFiles.ReadFile("say/" + name + ".pcm")
	if err != nil || len(b) < 4 {
		return nil
	}
	in := make([]int16, len(b)/2)
	for i := range in {
		in[i] = int16(binary.LittleEndian.Uint16(b[2*i:]))
	}
	// Up to the speaker's rate by straight lines between samples: speech at 16 kHz has nothing above
	// 8 kHz to spoil.
	k := speaker.Rate / sayRate
	out := make([]int16, 0, len(in)*k)
	for i, s := range in {
		next := s
		if i+1 < len(in) {
			next = in[i+1]
		}
		for j := 0; j < k; j++ {
			out = append(out, int16(int(s)+(int(next)-int(s))*j/k))
		}
	}
	return []speaker.Note{speaker.Synth(out)}
}
