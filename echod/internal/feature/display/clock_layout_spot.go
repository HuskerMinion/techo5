//go:build spot

package display

// Where the Spot's clock sits: across the middle, or small at the foot of the circle, under a photo's
// subject. Only the styles that read well small go to the foot (footStyle); the rest stay whole.

// clockPositions are the Spot's choices.
var clockPositions = []clockPosition{
	{"Center", ""},
	{"Bottom", "bottom"},
}

// clockAtFoot is whether the clock is to sit at the foot.
func clockAtFoot() bool {
	return clockPositions[clockPositionIndex()].value == "bottom"
}

// footStyle is whether a style has a small version for the foot.
func footStyle(style string) bool {
	switch style {
	case styleClassic, styleFlip, styleLED, styleSun, styleGlow:
		return true
	}
	return false
}

// clockLayoutRows is Clock position on the screen, under Display.
func clockLayoutRows() []settingRow {
	return []settingRow{
		{id: "clockpos", label: tr("ui_clock_position"), sub: tr("ui_clock_position_styles"), kind: ctlChoice, value: clockPositions[clockPositionIndex()].label},
	}
}

// clockLayoutPicker is its list.
func clockLayoutPicker(id string) (pickerView, bool) {
	if id != "clockpos" {
		return pickerView{}, false
	}
	return pickerView{title: tr("ui_clock_position"), opts: clockPositionOptions(), cur: clockPositionIndex()}, true
}

// chooseClockLayout takes a choice from it, and says whether id was it.
func (d *Display) chooseClockLayout(id string, i int) bool {
	if id != "clockpos" {
		return false
	}
	setClockPosition(d.clockPos, i)
	d.wake()
	return true
}
