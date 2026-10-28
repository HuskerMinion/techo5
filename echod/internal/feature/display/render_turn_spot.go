//go:build spot

package display

import "image"

// The Spot's turn screen in the Wave, Bars, VU, Orb, HAL 9000 or Face style (turn_draw.go, turn_visual.go): the picture across the upper
// middle of the face, clear of the rim, which still says listening, thinking or muted around it, and
// the words beneath, narrowing with the circle.

var (
	// turnWaveBand and turnBarsBand are where the picture goes. Their corners, and the wave's glow
	// turnWavePad beyond it, stay inside the rim's inner edge.
	turnWaveBand = image.Rect(72, 132, 408, 268)
	turnBarsBand = image.Rect(80, 120, 400, 250)
)

const (
	turnWavePad  = 30
	turnWordsTop = 318 // the first line of words' baseline
)

// turnFace draws a turn with its picture.
func (r *roundRenderer) turnFace(s roundScene) {
	v := s.eq
	switch {
	case v.wave:
		if r.wb == nil {
			r.wb = &waveBuf{}
		}
		drawWave(r.dst, r.wb, turnWaveBand, turnWavePad, v, colBackground, s.now, 2, 0.22)
	case v.style == "vu":
		drawVU(r.dst, turnBarsBand, v, colBackground)
	case v.style == "orb":
		drawOrb(r.dst, turnWaveBand, v, v.phase, s.now)
	case v.style == "hal":
		drawHAL(r.dst, turnWaveBand, v, v.phase, s.now)
	case v.style == "face":
		drawFace(r.dst, turnWaveBand, v, s.now, colBackground)
	default:
		drawBars(r.dst, turnBarsBand, v, colBackground, 1, 3)
	}

	y := turnWordsTop
	switch s.phase {
	case "listening":
		r.centered(r.body, tr("ui_listening_turn"), y, colListening)
		return
	case "thinking":
		r.centered(r.body, tr("ui_thinking_turn"), y, colThinking)
		y += 36
	}
	answer := s.reply != "" && (s.phase == "replying" || s.phase == "lingering")
	if s.heard != "" {
		lines := 2
		if answer {
			lines = 1
		}
		y = r.paragraph(r.small, "“"+s.heard+"”", y, colDim, lines)
		y += 6
	}
	if answer {
		r.paragraph(r.body, s.reply, y, colText, 3)
	}
}
