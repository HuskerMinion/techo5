//go:build !dot && !spot

package display

import "golang.org/x/image/font"

// calendarMost is the text size the month and the week are drawn at, at most, in percent. At 150% a
// month's day holds a line of "+N more" and no event, and a week's boxes keep three letters of their
// titles: those two pages are drawn at 125% then, and the day, the agenda and the clock keep the size
// chosen, where their words have the room to be larger.
const calendarMost = 125

// faceSet is the renderer's faces and the text size they were made for, to be put back as they were.
type faceSet struct {
	clock, big, ampm, title, body, small, tiny, micro font.Face
	fc                                                *sheetFaces
	textSize                                          int
	styleFaces                                        map[styleFaceKey]font.Face
}

func (r *renderer) faceSet() faceSet {
	return faceSet{r.clock, r.big, r.ampm, r.title, r.body, r.small, r.tiny, r.micro, r.fc, r.textSize, r.styleFaces}
}

func (r *renderer) setFaceSet(f faceSet) {
	r.clock, r.big, r.ampm, r.title, r.body, r.small, r.tiny, r.micro = f.clock, f.big, f.ampm, f.title, f.body, f.small, f.tiny, f.micro
	r.fc, r.textSize, r.styleFaces = f.fc, f.textSize, f.styleFaces
}

// calendarFaces puts the faces of calendarMost in place while the text size is larger, and gives the
// func that puts the chosen ones back. They are made once per font and kept (calCapped): a face is
// not cheap to make, and this is every frame the page is up.
func (r *renderer) calendarFaces() (restore func()) {
	if r.textSize <= calendarMost {
		return func() {}
	}
	was := r.faceSet()
	k := fontKey{r.font, r.fontGen, calendarMost}
	if r.calCapped == nil || r.calCappedFor != k {
		r.makeFaces(k)
		capped := r.faceSet()
		r.calCapped, r.calCappedFor = &capped, k
	}
	r.setFaceSet(*r.calCapped)
	return func() { r.setFaceSet(was) }
}
