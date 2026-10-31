//go:build !dot

package display

import (
	"fmt"
	"log/slog"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
)

// The screen's text size: how large every word is drawn, in percent of the size the layout gives it.
// The layout's sizes are the Show 5's pixels scaled to the panel's width (drawnFor), which on a bigger
// panel, or one with larger pixels, can make words larger on the glass than they need to be, or a
// screen read from across the room may want them larger still. The boxes the words sit in (the
// rows, the buttons, the tiles) keep their size; only the faces change, and every face for the
// screen's words is made through fontKey.face, so they all follow it. Icons keep theirs.
//
// Chosen under Display beside the font, or as Screen text size in Home Assistant, and shown at once
// the same way: a new size is a new fontKey, and each renderer makes its faces again (useFont).

// textSteps are the sizes offered, in percent.
var textSteps = []int{70, 80, 90, 100, 110, 125}

// textSize is the text size in force: the one chosen, or the device's own (layout.DefaultTextSize).
func textSize() int {
	if v := config.Get().Screen.TextSize; v > 0 {
		return min(max(v, config.TextSizeLeast), config.TextSizeMost)
	}
	return layout.DefaultTextSize()
}

// textSizeIndex is the step nearest the size in force, which is the step itself unless the size came
// from elsewhere (a hand-edited file, a newer build).
func textSizeIndex() int {
	v, best := textSize(), 0
	for i, s := range textSteps {
		if abs(s-v) < abs(textSteps[best]-v) {
			best = i
		}
	}
	return best
}

// textSizeOptions are the steps as the picker and Home Assistant show them.
func textSizeOptions() []string {
	out := make([]string, len(textSteps))
	for i, s := range textSteps {
		out[i] = fmt.Sprintf("%d%%", s)
	}
	return out
}

// atTextSize is px drawn at the text size a paint's faces were made at.
func (p *paint) atTextSize(px int) int {
	if p.textSize <= 0 {
		return px
	}
	return px * p.textSize / 100
}

// grownForText is px, made taller for text drawn larger than 100% and never shorter: a settings row or
// header grows with its words, so they keep their room, and keeps its finger-sized height when they shrink.
func (p *paint) grownForText(px int) int { return max(px, p.atTextSize(px)) }

// textSizeRow is the setting on the screen, under Display, beside the font.
func textSizeRow() settingRow {
	return settingRow{id: "textsize", label: tr("text_size_label"), sub: tr("text_size_description"), kind: ctlChoice,
		value: textSizeOptions()[textSizeIndex()]}
}

func textSizePicker() (pickerView, bool) {
	return pickerView{title: tr("text_size_label"), opts: textSizeOptions(), cur: textSizeIndex()}, true
}

// setTextSize saves the text size at step i and shows it at once, so it can be chosen while looking.
func (d *Display) setTextSize(i int) {
	if i < 0 || i >= len(textSteps) {
		return
	}
	if err := config.Set().Screen().TextSize(textSteps[i]); err != nil {
		slog.Error("saving the text size failed", "err", err)
		return
	}
	slog.Info("screen: text size", "percent", textSteps[i])
	if d.textSizeSel != nil {
		d.textSizeSel.Set(textSizeOptions()[i])
	}
	d.wake()
}

// textSizeSelect is Screen text size in Home Assistant.
func textSizeSelect(d *Display) *esphome.Select {
	s := &esphome.Select{
		Base: esphome.Base{
			ObjectID: "screen_text_size",
			Name:     "Screen text size",
			Icon:     "mdi:format-font-size-increase",
			Category: esphome.CategoryConfig,
		},
		Options: textSizeOptions(),
	}
	s.OnCommand = func(v string) {
		for i, o := range textSizeOptions() {
			if o == v {
				d.setTextSize(i)
				return
			}
		}
	}
	return s
}
