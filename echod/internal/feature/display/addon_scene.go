//go:build !dot

package display

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/jpeg" // an add-on's JPEGs
	_ "image/png"  // and PNGs
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/image/font"

	"github.com/HuskerMinion/techo5/echod/internal/lib/mdi"
	"github.com/HuskerMinion/techo5/echod/internal/lib/picscale"
	"github.com/HuskerMinion/techo5/echod/pkg/addonapi"
)

// An add-on's scene (stage 2 of the add-on API, docs/addon-api.md), laid out and drawn by the display in
// its own theme, fonts and colors: the add-on sends blocks, never pixels but its images, and nothing in a
// block names a color, a place or a size in pixels. This is the part the Show and the Spot share, and the
// fake daemon's PNG of a scene too (RenderAddonScene): the layout of a scene into boxes, drawing them, and
// finding what a finger is on. Where a page or a card goes on each screen, and the name bar over a page,
// are each screen's (addon_screen.go, addon_screen_spot.go).
//
// A layout is made once for a scene as it is (its revision, its images' generation, its room and the
// fonts) and kept, so a frame that draws a page only draws it: nothing is measured, wrapped or decoded
// per frame. Images are decoded and scaled away from drawing (addonPics), and a frame draws the ones
// ready and leaves the others' places empty.

// sceneMetrics are a scene's sizes on the screen in hand: its four text sizes, the four spacings, and
// the parts' heights and paddings.
type sceneMetrics struct {
	text   [4]int // small, body, title, huge
	gap    [4]int // none, small, normal, large
	label  int    // a button's or a chip's words
	pad    int    // inside a card
	button int    // a button's height
	chip   int    // a chip's height
	padX   int    // either side of a button's or a chip's words
	icon   int    // an icon on a button or a chip
	bar    int    // a progress bar's thickness
	stripe int    // a card's accent stripe
}

// sceneMetrics are the Show 5's sizes scaled to the screen, or the Spot's own, a step smaller, for the
// round panel.
func (p *paint) sceneMetrics() sceneMetrics {
	if p.round {
		return sceneMetrics{text: [4]int{19, 24, 31, 54}, gap: [4]int{0, 6, 12, 22}, label: 22, pad: 14,
			button: 50, chip: 40, padX: 16, icon: 24, bar: 8, stripe: 5}
	}
	s := p.s
	return sceneMetrics{text: [4]int{s(22), s(28), s(38), s(72)}, gap: [4]int{0, s(8), s(16), s(28)}, label: s(25),
		pad: s(18), button: s(60), chip: s(46), padX: s(22), icon: s(28), bar: s(10), stripe: s(6)}
}

// sceneSize is a text size's place in sceneMetrics.text.
func sceneSize(size string) int {
	switch size {
	case addonapi.SizeSmall:
		return 0
	case addonapi.SizeTitle:
		return 2
	case addonapi.SizeHuge:
		return 3
	}
	return 1
}

// sceneGap is a spacing's room.
func (m sceneMetrics) sceneGap(spacing string) int {
	switch spacing {
	case "none":
		return m.gap[0]
	case "small":
		return m.gap[1]
	case "large":
		return m.gap[3]
	}
	return m.gap[2]
}

// sceneFaceKey is a face of a scene's: the fonts, the size and the weight.
type sceneFaceKey struct {
	k    fontKey
	px   int
	bold bool
}

// sceneFaces keeps the faces scenes are drawn in, made once each: a face is not cheap to make.
var sceneFaces struct {
	mu sync.Mutex
	m  map[sceneFaceKey]font.Face
}

// sceneFace is the screen's font at px pixels, regular or bold, at the text size in force.
func sceneFace(k fontKey, px int, bold bool) font.Face {
	key := sceneFaceKey{k, px, bold}
	sceneFaces.mu.Lock()
	defer sceneFaces.mu.Unlock()
	if f, ok := sceneFaces.m[key]; ok {
		return f
	}
	if len(sceneFaces.m) > 48 {
		sceneFaces.m = nil // another font or text size: the faces of the last go
	}
	if sceneFaces.m == nil {
		sceneFaces.m = map[sceneFaceKey]font.Face{}
	}
	f := k.face(bold, float64(px))
	sceneFaces.m[key] = f
	return f
}

// sceneBox is one block where the layout put it. A box in a list has its place in the list's content,
// from the top of the list's view, which is scrolled by the list's offset when it is drawn.
type sceneBox struct {
	b     *addonapi.Block
	r     image.Rectangle
	list  int // 1 and up: the list it is in; 0 for none
	item  bool
	lines []string // a text's lines, a button's or a chip's one
	face  font.Face
	tap   string          // what a tap on it sends: a button's or a chip's id, or a list item's
	step  int             // a stepper's − (-1) or + (1): a tap on it changes the stepper's number
	pic   scenePic        // an image's
	head  bool            // a list's section heading
	rule  bool            // a rule between a list's items
	label image.Rectangle // a progress tied to a timer: where its time is written; a stepper's number
	clip  image.Rectangle // the room it was laid out in, which nothing of it is drawn outside
	lead  bool            // a list row's icon at its start
}

// scenePic is an image's bytes and the key that names them, and the size it was laid out at.
type scenePic struct {
	data  []byte
	key   string
	cover bool
}

// sceneList is a list's view on the page, and how tall its content is: how far it can scroll.
type sceneList struct {
	view    image.Rectangle
	content int
}

// sceneLayout is a scene laid out.
type sceneLayout struct {
	boxes []sceneBox
	lists []sceneList
	// height is how tall the laid-out scene is: a card is as tall as what it holds. room is where it was
	// laid out, which nothing of it is drawn outside.
	height int
	room   image.Rectangle
	// rev is the scene's revision it was laid out from (serve.Scene.Rev), which a tap found in it is
	// sent with, so a tap on a layout older than a change to what it hit is not sent for what is there
	// now.
	rev uint64
}

// sceneImages answers an image block's bytes and their key (addonhost.Host.Image); false while there are
// none yet.
type sceneImages func(b *addonapi.Block) ([]byte, string, bool)

// sceneLayouter lays out one scene.
type sceneLayouter struct {
	p      *paint
	m      sceneMetrics
	k      fontKey
	card   bool
	imgs   sceneImages
	out    *sceneLayout
	list   int // the list being laid out, 1 and up
	imageH int // the tallest an image is laid out by itself
	// clip is the room the block being laid out has, its column's, its row's, the scene's: what does not
	// fit whole in it is left out (add), never drawn over its neighbors or cut through its words. Inside a
	// list, which scrolls, it is unbounded.
	clip image.Rectangle
}

// unbounded is a clip that leaves nothing out: a list's content, which scrolls.
var unbounded = image.Rect(-1<<20, -1<<20, 1<<20, 1<<20)

// layoutScene lays a scene out in room: a page's whole content, or a card's width (its height is what it
// holds, at most room's).
func (p *paint) layoutScene(root *addonapi.Block, room image.Rectangle, card bool, imgs sceneImages) *sceneLayout {
	l := &sceneLayouter{p: p, m: p.sceneMetrics(), k: p.faceKey(), card: card, imgs: imgs, out: &sceneLayout{}}
	l.imageH = room.Dy() * 6 / 10
	if card {
		l.imageH = room.Dy()
	}
	h := room.Dy()
	if card || (root.Type != addonapi.BlockColumn && root.Type != addonapi.BlockList) {
		h = min(l.size(root, room.Dx()).Y, room.Dy())
	}
	r := image.Rect(room.Min.X, room.Min.Y, room.Max.X, room.Min.Y+h)
	l.clip = r
	l.place(root, r)
	if card {
		// A card is as tall as what it kept: what did not fit whole was left out, and leaves no room.
		h = 0
		for _, b := range l.out.boxes {
			h = max(h, b.r.Max.Y-room.Min.Y)
		}
		h = min(h, room.Dy())
	}
	l.out.height, l.out.room = h, room
	return l.out
}

// face is the face of a text block of size, regular or bold.
func (l *sceneLayouter) face(size string, bold bool) font.Face {
	return sceneFace(l.k, l.m.text[sceneSize(size)], bold)
}

func (l *sceneLayouter) labelFace() font.Face { return sceneFace(l.k, l.m.label, true) }

// lineH is a face's line height.
func lineH(f font.Face) int {
	if f == nil {
		return 0
	}
	return f.Metrics().Height.Ceil()
}

// textLines is a text block's words in lines of at most w, line breaks kept, held to its max_lines
// (on a card, to addonapi.MaxCardLines), the last cut with an ellipsis when words are left over.
func (l *sceneLayouter) textLines(b *addonapi.Block, f font.Face, w int) []string {
	most := b.MaxLines
	if l.card && (most == 0 || most > addonapi.MaxCardLines) {
		most = addonapi.MaxCardLines
	}
	var lines []string
	for _, para := range strings.Split(b.Text, "\n") {
		if strings.TrimSpace(para) == "" {
			lines = append(lines, "")
			continue
		}
		lines = append(lines, l.p.wrapLines(f, para, max(w, 1))...)
	}
	if most > 0 && len(lines) > most {
		rest := strings.Join(lines[most-1:], " ")
		lines = append(lines[:most-1], l.p.fit(f, rest+" …", w))
	}
	for i, ln := range lines {
		lines[i] = l.p.fit(f, ln, w)
	}
	return lines
}

// widest is the widest of lines in f.
func (l *sceneLayouter) widest(f font.Face, lines []string) int {
	w := 0
	for _, ln := range lines {
		w = max(w, l.p.width(f, ln))
	}
	return w
}

// pressWidth is a button's or a chip's natural width: its icon and its words, padded, at most w.
func (l *sceneLayouter) pressWidth(b *addonapi.Block, w int) int {
	n := 2 * l.m.padX
	if b.Icon != "" {
		n += l.m.icon
	}
	if b.Label != "" {
		if b.Icon != "" {
			n += l.m.gap[1]
		}
		n += l.p.width(l.labelFace(), b.Label)
	}
	return min(n, w)
}

// picOf is an image block's bytes and their source size, when there are any yet.
func (l *sceneLayouter) picOf(b *addonapi.Block) (scenePic, image.Point, bool) {
	if l.imgs == nil {
		return scenePic{}, image.Point{}, false
	}
	data, key, ok := l.imgs(b)
	if !ok {
		return scenePic{}, image.Point{}, false
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || addonapi.ImageSize(cfg.Width, cfg.Height) != nil {
		return scenePic{}, image.Point{}, false
	}
	return scenePic{data: data, key: key, cover: b.Fit == "cover"}, image.Pt(cfg.Width, cfg.Height), true
}

// imageSize is an image's natural size in w: its own pixels at the screen's scale, at most w wide and
// imageH tall, its shape kept; one not there yet is a 16:9 place.
func (l *sceneLayouter) imageSize(b *addonapi.Block, w int) image.Point {
	_, src, ok := l.picOf(b)
	if !ok {
		pw := min(w, l.p.s(320))
		return image.Pt(pw, min(pw*9/16, l.imageH))
	}
	iw, ih := l.p.s(src.X), l.p.s(src.Y)
	if l.p.round {
		iw, ih = src.X, src.Y
	}
	if b.Fit == "cover" && iw < w {
		ih, iw = ih*w/max(iw, 1), w
	}
	if iw > w {
		ih, iw = ih*w/max(iw, 1), w
	}
	if ih > l.imageH {
		iw, ih = iw*l.imageH/max(ih, 1), l.imageH
	}
	return image.Pt(max(iw, 1), max(ih, 1))
}

// size is a block's natural size in w: as wide as it needs, at most w, and as tall as that makes it.
func (l *sceneLayouter) size(b *addonapi.Block, w int) image.Point {
	m := l.m
	switch b.Type {
	case addonapi.BlockText:
		f := l.face(b.Size, b.Bold)
		lines := l.textLines(b, f, w)
		return image.Pt(l.widest(f, lines), len(lines)*lineH(f))
	case addonapi.BlockButton:
		return image.Pt(l.pressWidth(b, w), m.button)
	case addonapi.BlockChip:
		return image.Pt(l.pressWidth(b, w), m.chip)
	case addonapi.BlockToggle:
		return image.Pt(min(w, l.p.width(l.face(addonapi.SizeBody, false), b.Label)+m.gap[2]+l.toggleW()), m.button)
	case addonapi.BlockStepper:
		lw := 0
		if b.Label != "" {
			lw = l.p.width(l.face(addonapi.SizeBody, false), b.Label) + m.gap[2]
		}
		return image.Pt(min(w, lw+l.stepperControls(b)), m.button)
	case addonapi.BlockProgress:
		h := m.bar
		if b.Timer != "" {
			h = max(h, lineH(l.face(addonapi.SizeBody, false)))
		}
		return image.Pt(w, h)
	case addonapi.BlockDivider:
		return image.Pt(w, 2*m.gap[1]+1)
	case addonapi.BlockSpacer:
		return image.Pt(m.gap[2], m.gap[2])
	case addonapi.BlockImage:
		return l.imageSize(b, w)
	case addonapi.BlockList:
		h := 0
		for si := range b.Sections {
			h += l.sectionHeight(&b.Sections[si], w)
		}
		return image.Pt(w, h)
	case addonapi.BlockCard:
		inner := l.columnSize(b.Children, b.Spacing, w-2*m.pad-m.stripe)
		return image.Pt(w, inner.Y+2*m.pad)
	case addonapi.BlockRow:
		lead := l.leadW(b)
		widths, h := l.rowWidths(b, w-lead)
		total := lead
		for _, x := range widths {
			total += x
		}
		if lead > 0 {
			h = max(h, l.m.button)
		}
		return image.Pt(min(w, total+m.sceneGap(b.Spacing)*max(len(widths)-1, 0)), h)
	case addonapi.BlockColumn:
		return l.columnSize(b.Children, b.Spacing, w)
	}
	return image.Point{}
}

// toggleW is how wide a toggle's switch is.
func (l *sceneLayouter) toggleW() int { return l.m.chip * 7 / 4 }

// leadW is the room a list row's picture or icon takes at its start, gap and all; 0 for a row with
// neither.
func (l *sceneLayouter) leadW(b *addonapi.Block) int {
	if b.Type != addonapi.BlockRow || (b.Icon == "" && b.Name == "" && b.URL == "") {
		return 0
	}
	return l.m.button + l.m.sceneGap(b.Spacing)
}

// stepperControls is how wide a stepper's − number unit + is: two round buttons and, between them, room
// for the widest its number can be with its unit, so the buttons do not move as it changes.
func (l *sceneLayouter) stepperControls(b *addonapi.Block) int {
	f := l.face(addonapi.SizeBody, true)
	w := 0
	for _, v := range []*float64{b.Min, b.Max, b.Value} {
		if v != nil {
			c := *b
			c.Value = v
			w = max(w, l.p.width(f, stepperValue(&c)))
		}
	}
	return 2*l.m.button + 2*l.m.gap[2] + w
}

// stepperValue is a stepper's number as it is drawn, with its unit: as many decimal places as it has.
func stepperValue(b *addonapi.Block) string {
	v := 0.0
	if b.Value != nil {
		v = *b.Value
	}
	out := strconv.FormatFloat(v, 'f', -1, 64)
	if b.Unit != "" {
		out += " " + b.Unit
	}
	return out
}

// columnSize is children one under another in w.
func (l *sceneLayouter) columnSize(children []addonapi.Block, spacing string, w int) image.Point {
	gap := l.m.sceneGap(spacing)
	var out image.Point
	for i := range children {
		s := l.size(&children[i], w)
		out.X = max(out.X, s.X)
		out.Y += s.Y
		if i > 0 {
			out.Y += gap
		}
	}
	return out
}

// sectionHeight is a list's section: its heading, and its items with a rule between each two.
func (l *sceneLayouter) sectionHeight(s *addonapi.Section, w int) int {
	h := 0
	if s.Heading != "" {
		h += lineH(l.face(addonapi.SizeSmall, true)) + l.m.gap[2]
	}
	for i := range s.Items {
		h += l.size(&s.Items[i], w).Y + 2*l.m.gap[2] + 1
	}
	return h
}

// flexible is a row's child that gives up room when the row has too little: words, which wrap.
func flexible(b *addonapi.Block) bool {
	switch b.Type {
	case addonapi.BlockText, addonapi.BlockColumn, addonapi.BlockCard, addonapi.BlockList, addonapi.BlockImage:
		return true
	}
	return false
}

// rowWidths is how wide each of a row's children is in w, and how tall the row is: the children with no
// weight at their own width, giving room up from the words when there is too little; those with one
// sharing what is left by their weights.
func (l *sceneLayouter) rowWidths(b *addonapi.Block, w int) ([]int, int) {
	n := len(b.Children)
	widths := make([]int, n)
	if n == 0 {
		return widths, 0
	}
	room := w - l.m.sceneGap(b.Spacing)*(n-1)
	fixed, flex, weights := 0, 0, 0.0
	for i := range b.Children {
		c := &b.Children[i]
		if c.Weight > 0 {
			weights += c.Weight
			continue
		}
		widths[i] = l.size(c, max(room, 1)).X
		if flexible(c) {
			flex += widths[i]
		} else {
			fixed += widths[i]
		}
	}
	if over := fixed + flex - room; over > 0 && flex > 0 {
		// The words give up room: the ones that need no more than an even share keep theirs, and the
		// rest share what is left evenly, so a short word beside a long paragraph stays whole.
		left := max(room-fixed, 0)
		var wide []int
		for i := range b.Children {
			if c := &b.Children[i]; c.Weight == 0 && flexible(c) {
				wide = append(wide, i)
			}
		}
		for len(wide) > 0 {
			share := left / len(wide)
			kept := wide[:0]
			for _, i := range wide {
				if widths[i] <= share {
					left -= widths[i]
				} else {
					kept = append(kept, i)
				}
			}
			if len(kept) == len(wide) {
				for _, i := range kept {
					widths[i] = share
				}
				break
			}
			wide = kept
		}
		fixed, flex = room, 0
	} else if over > 0 {
		// Nothing to wrap: the buttons and chips give up room alike, their words cut to fit.
		n := 0
		for i := range b.Children {
			if c := &b.Children[i]; c.Weight == 0 && addonapi.Pressable(c.Type) {
				n++
			}
		}
		if n > 0 {
			cut := (over + n - 1) / n
			for i := range b.Children {
				if c := &b.Children[i]; c.Weight == 0 && addonapi.Pressable(c.Type) {
					widths[i] = max(widths[i]-cut, l.m.button)
				}
			}
		}
	}
	if weights > 0 {
		left := max(room-fixed-flex, 0)
		for i := range b.Children {
			if c := &b.Children[i]; c.Weight > 0 {
				widths[i] = int(float64(left) * c.Weight / weights)
			}
		}
	}
	h := 0
	for i := range b.Children {
		c := &b.Children[i]
		if c.Type == addonapi.BlockSpacer && c.Weight == 0 {
			continue
		}
		h = max(h, l.size(c, max(widths[i], 1)).Y)
	}
	return widths, h
}

// add is a box laid out.
//
// A block that does not fit whole in the room it was given (l.clip) is left out, words, buttons, chips,
// steppers and toggles alike, so nothing is drawn over the block below it or cut through its middle, and
// nothing half seen takes a finger. A picture, and a card's ground, are cut cleanly at the room's edge
// instead, as they can be.
func (l *sceneLayouter) add(box sceneBox) {
	box.list = l.list
	box.clip = l.clip
	if !box.r.In(l.clip) && !box.r.Empty() {
		surface := box.b != nil && (box.b.Type == addonapi.BlockImage || box.b.Type == addonapi.BlockCard) && box.tap == ""
		if !surface || !box.r.Overlaps(l.clip) {
			return
		}
	}
	l.out.boxes = append(l.out.boxes, box)
}

// within lays out what f lays out in r, inside the room it has so far: a column's, a row's or a card's
// blocks are held to it.
func (l *sceneLayouter) within(r image.Rectangle, f func()) {
	was := l.clip
	l.clip = l.clip.Intersect(r)
	f()
	l.clip = was
}

// place lays a block out in r.
func (l *sceneLayouter) place(b *addonapi.Block, r image.Rectangle) {
	m := l.m
	switch b.Type {
	case addonapi.BlockText:
		f := l.face(b.Size, b.Bold)
		l.add(sceneBox{b: b, r: r, lines: l.textLines(b, f, r.Dx()), face: f})
	case addonapi.BlockButton, addonapi.BlockChip:
		f := l.labelFace()
		label := ""
		if b.Label != "" {
			room := r.Dx() - 2*m.padX
			if b.Icon != "" {
				room -= m.icon + m.gap[1]
			}
			label = l.p.fit(f, b.Label, max(room, 1))
		}
		l.add(sceneBox{b: b, r: r, lines: []string{label}, face: f, tap: b.ID})
	case addonapi.BlockToggle:
		// Its label from the start and its switch at the end; a tap anywhere on it flips it.
		f := l.face(addonapi.SizeBody, false)
		label := l.p.fit(f, b.Label, max(r.Dx()-l.toggleW()-m.gap[2], 1))
		sw := min(l.toggleW(), r.Dx())
		h := min(m.chip, r.Dy())
		top := r.Min.Y + (r.Dy()-h)/2
		l.add(sceneBox{b: b, r: r, lines: []string{label}, face: f, tap: b.ID,
			label: image.Rect(r.Max.X-sw, top, r.Max.X, top+h)})
	case addonapi.BlockStepper:
		// Its label from the start, and its − number unit + at the end, − and + round buttons as tall
		// as a button and as wide, so a finger finds them.
		f := l.face(addonapi.SizeBody, false)
		ctl := min(l.stepperControls(b), r.Dx())
		label := ""
		if room := r.Dx() - ctl - m.gap[2]; b.Label != "" && room >= l.p.width(f, "W…") {
			label = l.p.fit(f, b.Label, room) // no room for a letter of it: none, rather than a lone "…"
		}
		side := min(m.button, r.Dy())
		if full := l.stepperControls(b); full > r.Dx() {
			// Narrower than − number +: the buttons shrink (to two thirds at the least) before they would
			// overlap the number or each other; past that the number gives up its room (cut with "…").
			side = max(min(side, side-(full-r.Dx()+1)/2), side*2/3)
			side = min(side, r.Dx()/2)
		}
		top := r.Min.Y + (r.Dy()-side)/2
		plus := image.Rect(r.Max.X-side, top, r.Max.X, top+side)
		left := max(r.Max.X-ctl, r.Min.X)
		minus := image.Rect(left, top, min(left+side, plus.Min.X), top+side)
		l.add(sceneBox{b: b, r: r, lines: []string{label, stepperValue(b)}, face: f,
			label: image.Rect(minus.Max.X, r.Min.Y, plus.Min.X, r.Max.Y)})
		l.add(sceneBox{b: b, r: minus, tap: b.ID, step: -1})
		l.add(sceneBox{b: b, r: plus, tap: b.ID, step: 1})
	case addonapi.BlockProgress:
		box := sceneBox{b: b, r: r, face: l.face(addonapi.SizeBody, false)}
		if b.Timer != "" {
			tw := l.p.width(box.face, "00:00")
			box.label = image.Rect(r.Max.X-tw, r.Min.Y, r.Max.X, r.Max.Y)
		}
		l.add(box)
	case addonapi.BlockDivider, addonapi.BlockSpacer:
		l.add(sceneBox{b: b, r: r})
	case addonapi.BlockImage:
		pic, src, ok := l.picOf(b)
		box := sceneBox{b: b, r: r, pic: pic}
		if ok && !pic.cover {
			box.r = containPart(image.Rect(0, 0, src.X, src.Y), r)
		}
		l.add(box)
	case addonapi.BlockCard:
		l.add(sceneBox{b: b, r: r})
		inner := image.Rect(r.Min.X+m.pad+m.stripe, r.Min.Y+m.pad, r.Max.X-m.pad, r.Max.Y-m.pad)
		l.within(inner, func() { l.placeColumn(b.Children, b.Spacing, inner) })
	case addonapi.BlockColumn:
		l.within(r, func() { l.placeColumn(b.Children, b.Spacing, r) })
	case addonapi.BlockRow:
		if lead := l.leadW(b); lead > 0 {
			// A list row's picture or icon, a small square at its start, and the rest after it.
			side := min(m.button, r.Dy())
			sq := image.Rect(r.Min.X, r.Min.Y+(r.Dy()-side)/2, r.Min.X+side, r.Min.Y+(r.Dy()-side)/2+side)
			if thumb, ok := b.Thumb(); ok {
				t := &thumb
				pic, _, _ := l.picOf(t)
				l.add(sceneBox{b: t, r: sq, pic: pic})
			} else {
				l.add(sceneBox{b: b, r: sq, lead: true})
			}
			r.Min.X += lead
		}
		l.within(r, func() { l.placeRow(b, r) })
	case addonapi.BlockList:
		l.placeList(b, r)
	}
}

// placeColumn lays children out one under another in r: each at its own height, but the ones with a
// weight (and a list, which has one of 1 unless it says otherwise) share what height is left by their
// weights. A button, a chip or an image keeps its own width; the rest take r's.
func (l *sceneLayouter) placeColumn(children []addonapi.Block, spacing string, r image.Rectangle) {
	gap := l.m.sceneGap(spacing)
	heights := make([]int, len(children))
	fixed, weights := 0, 0.0
	weightOf := func(c *addonapi.Block) float64 {
		if c.Weight > 0 {
			return c.Weight
		}
		if c.Type == addonapi.BlockList {
			return 1
		}
		return 0
	}
	for i := range children {
		c := &children[i]
		if wt := weightOf(c); wt > 0 {
			weights += wt
			continue
		}
		heights[i] = l.size(c, r.Dx()).Y
		fixed += heights[i]
	}
	fixed += gap * max(len(children)-1, 0)
	if over := fixed - r.Dy(); over > 0 && len(children) > 1 {
		// Too little room: the gaps narrow first, and then the pictures shrink (to a third at the
		// least), before a page's column scrolls or a card leaves anything out (add).
		n := len(children) - 1
		cut := min(gap, (over+n-1)/n)
		gap -= cut
		fixed -= cut * n
	}
	if over := fixed - r.Dy(); over > 0 {
		pics := 0
		for i := range children {
			if c := &children[i]; c.Type == addonapi.BlockImage && weightOf(c) == 0 {
				pics += heights[i]
			}
		}
		if pics > 0 {
			keep := max(pics-over, pics/3)
			for i := range children {
				if c := &children[i]; c.Type == addonapi.BlockImage && weightOf(c) == 0 {
					was := heights[i]
					heights[i] = was * keep / pics
					fixed -= was - heights[i]
				}
			}
		}
	}
	if fixed > r.Dy() && !l.card && l.list == 0 && len(l.out.lists) < maxSceneLists && !holdsList(children) {
		// On a page, a column with more than its room even so scrolls, as a list does: every control on
		// a page must stay where a finger can reach it. (A card, a glance, leaves out what does not fit.)
		l.scrollColumn(children, heights, gap, r)
		return
	}
	if weights > 0 {
		left := max(r.Dy()-fixed, 0)
		for i := range children {
			if wt := weightOf(&children[i]); wt > 0 {
				heights[i] = int(float64(left) * wt / weights)
			}
		}
	}
	l.stackColumn(children, heights, gap, r, r.Min.Y)
}

// stackColumn places children one under another in r's width from y, at heights, gap apart: a button,
// a chip or an image at its own width, the rest at r's.
func (l *sceneLayouter) stackColumn(children []addonapi.Block, heights []int, gap int, r image.Rectangle, y int) int {
	for i := range children {
		c := &children[i]
		box := image.Rect(r.Min.X, y, r.Max.X, y+heights[i])
		switch c.Type {
		case addonapi.BlockButton, addonapi.BlockChip:
			box.Max.X = r.Min.X + l.size(c, r.Dx()).X
		case addonapi.BlockImage:
			if c.Weight == 0 {
				w := l.size(c, r.Dx()).X
				box.Min.X = r.Min.X + (r.Dx()-w)/2
				box.Max.X = box.Min.X + w
			}
		}
		l.place(c, box)
		y += heights[i] + gap
	}
	return y
}

// scrollColumn lays a page's column with more than its room r out as a list is: its blocks at their own
// heights in a content of their own, seen through r and scrolled by the daemon (sceneLayout.lists), cut
// cleanly at r's edges, with a tap only on what is seen.
func (l *sceneLayouter) scrollColumn(children []addonapi.Block, heights []int, gap int, r image.Rectangle) {
	for i := range children {
		if heights[i] == 0 {
			heights[i] = l.size(&children[i], r.Dx()).Y // one with a weight, at its own height here
		}
	}
	l.out.lists = append(l.out.lists, sceneList{view: r.Intersect(l.clip)})
	// Its blocks keep clear of the thumb at its right edge (scrollHints), so the thumb is never drawn
	// over one, nor a finger meant for the thumb's place taken by one.
	inner := r
	inner.Max.X -= scrollGutter(l.p)
	for i := range children {
		if children[i].Type != addonapi.BlockImage {
			heights[i] = l.size(&children[i], inner.Dx()).Y
		}
	}
	was, wasClip := l.list, l.clip
	l.list, l.clip = len(l.out.lists), unbounded
	end := l.stackColumn(children, heights, gap, inner, 0)
	l.out.lists[l.list-1].content = max(end-gap, 0)
	l.list, l.clip = was, wasClip
}

// scrollGutter is the room a scrolling column leaves at its right edge for its thumb.
func scrollGutter(p *paint) int { return p.s(22) }

// holdsList is whether a list is among blocks, or in them: a column that holds one gives it the room
// left, and does not scroll itself.
func holdsList(blocks []addonapi.Block) bool {
	for i := range blocks {
		if blocks[i].Type == addonapi.BlockList || holdsList(blocks[i].Children) {
			return true
		}
	}
	return false
}

// placeRow lays a row's children out start to end in r, each at its width (rowWidths), centered top to
// bottom, but cards and columns, which take the row's height.
func (l *sceneLayouter) placeRow(b *addonapi.Block, r image.Rectangle) {
	widths, _ := l.rowWidths(b, r.Dx())
	gap := l.m.sceneGap(b.Spacing)
	x := r.Min.X
	for i := range b.Children {
		c := &b.Children[i]
		box := image.Rect(x, r.Min.Y, x+widths[i], r.Max.Y)
		switch c.Type {
		case addonapi.BlockCard, addonapi.BlockColumn, addonapi.BlockList:
		default:
			h := min(l.size(c, max(widths[i], 1)).Y, r.Dy())
			box.Min.Y = r.Min.Y + (r.Dy()-h)/2
			box.Max.Y = box.Min.Y + h
		}
		l.place(c, box)
		x += widths[i] + gap
	}
}

// placeList lays a list out: its view is r, and its content, each section's heading and its items with a
// rule between, is laid out from the view's top, to be scrolled by the daemon.
func (l *sceneLayouter) placeList(b *addonapi.Block, r image.Rectangle) {
	// A list in a column with too little room for it is seen only in the room it has, and scrolls.
	l.out.lists = append(l.out.lists, sceneList{view: r.Intersect(l.clip)})
	was, wasClip := l.list, l.clip
	l.list, l.clip = len(l.out.lists), unbounded
	defer func() { l.list, l.clip = was, wasClip }()
	m := l.m
	y := 0
	head := l.face(addonapi.SizeSmall, true)
	for si := range b.Sections {
		s := &b.Sections[si]
		if s.Heading != "" {
			hb := image.Rect(r.Min.X, y, r.Max.X, y+lineH(head))
			l.add(sceneBox{b: b, r: hb, lines: []string{l.p.fit(head, upper(s.Heading), r.Dx())}, face: head, head: true})
			y = hb.Max.Y + m.gap[2]
		}
		for i := range s.Items {
			it := &s.Items[i]
			h := l.size(it, r.Dx()).Y
			ib := image.Rect(r.Min.X, y, r.Max.X, y+h+2*m.gap[2])
			if it.ID != "" {
				l.add(sceneBox{b: it, r: ib, tap: it.ID, item: true})
			}
			n := len(l.out.boxes)
			l.place(it, image.Rect(r.Min.X, y+m.gap[2], r.Max.X, y+m.gap[2]+h))
			// What the item holds is in the list's content as it is.
			for j := n; j < len(l.out.boxes); j++ {
				l.out.boxes[j].list = l.list
			}
			y = ib.Max.Y
			l.add(sceneBox{b: b, r: image.Rect(r.Min.X, y, r.Max.X, y+1), rule: true})
			y++
		}
	}
	l.out.lists[l.list-1].content = y
}

// maxScroll is how far list i (from 0) can scroll.
func (s *sceneLayout) maxScroll(i int) int {
	if i < 0 || i >= len(s.lists) {
		return 0
	}
	return max(s.lists[i].content-s.lists[i].view.Dy(), 0)
}

// at is box i's place on the screen, the whole scene moved by off and its lists scrolled as scrolls says,
// and the part of it that can be seen: a box in a list is cut to the list's view.
func (s *sceneLayout) at(i int, scrolls []int, off image.Point) (image.Rectangle, image.Rectangle) {
	b := s.boxes[i]
	if b.list == 0 {
		r := b.r.Add(off)
		return r, r
	}
	lv := s.lists[b.list-1].view.Add(off)
	by := 0
	if b.list-1 < len(scrolls) {
		by = min(max(scrolls[b.list-1], 0), s.maxScroll(b.list-1))
	}
	r := b.r.Add(image.Pt(off.X, lv.Min.Y-by))
	return r, r.Intersect(lv)
}

// shown is at, but nothing seen of a block in a list or a scrolling column that is only partly in its
// view: words, a button, a chip, a stepper or a toggle is drawn, and takes a finger, only whole, so a
// scroll leaves no sliver of a line at the view's edge; a picture, the rule between items and an item's
// whole are cut at the edge, a picture only while a third of it is seen.
func (s *sceneLayout) shown(i int, scrolls []int, off image.Point) (image.Rectangle, image.Rectangle) {
	r, seen := s.at(i, scrolls, off)
	b := s.boxes[i]
	if b.list == 0 || seen == r || b.item || b.rule {
		return r, seen
	}
	if b.b != nil && (b.b.Type == addonapi.BlockImage || b.lead) && seen.Dy()*3 >= r.Dy() {
		return r, seen
	}
	return r, image.Rectangle{}
}

// tapAt is the id a tap at pt sends, if it is on something that can be tapped: the innermost, so a button
// in a list's item is the button's, and anywhere else on the item the item's.
func (s *sceneLayout) tapAt(pt image.Point, slop int, scrolls []int, off image.Point) string {
	id, _, _ := s.tapStepAt(pt, slop, scrolls, off)
	return id
}

// tapStepAt is tapAt, and for a stepper's − or + which it is (-1, 1, 0 for anything else) and the
// stepper's block.
func (s *sceneLayout) tapStepAt(pt image.Point, slop int, scrolls []int, off image.Point) (string, int, *addonapi.Block) {
	for i := len(s.boxes) - 1; i >= 0; i-- {
		if s.boxes[i].tap == "" {
			continue
		}
		_, seen := s.shown(i, scrolls, off)
		if seen.Empty() {
			continue
		}
		reach := seen.Inset(-slop)
		if l := s.boxes[i].list; l > 0 {
			reach = reach.Intersect(s.lists[l-1].view.Add(off))
		}
		if pt.In(reach) {
			return s.boxes[i].tap, s.boxes[i].step, s.boxes[i].b
		}
	}
	return "", 0, nil
}

// listAt is the list a finger at pt is on, or the first when it is on none; -1 with no list.
func (s *sceneLayout) listAt(pt image.Point, off image.Point) int {
	for i, l := range s.lists {
		if pt.In(l.view.Add(off)) {
			return i
		}
	}
	if len(s.lists) > 0 {
		return 0
	}
	return -1
}

// scenePalette is what a scene is drawn in: the theme's, or the night clock's dim one.
type scenePalette struct {
	ground, text, dim, accent, rules, onAccent color.RGBA
	surface                                    func(int) color.RGBA
}

// themePalette is the theme in force.
func themePalette() scenePalette {
	return scenePalette{ground: walnut, text: cream, dim: dim, accent: amber, rules: ember, onAccent: onAccent(), surface: surface}
}

// sceneTimers answers one of the add-on's timers by its id: how much is left of how much.
type sceneTimers func(id string) (left, total time.Duration, ok bool)

// scenePics answers an image's pixels at the size it is drawn, or nil while they are not ready.
type scenePics func(p scenePic, w, h int) *image.RGBA

// drawScene draws a laid-out scene moved by off: each box in turn, a list's cut to its view and scrolled
// by scrolls.
func (p *paint) drawScene(s *sceneLayout, scrolls []int, off image.Point, pal scenePalette, timers sceneTimers, pics scenePics) {
	m := p.sceneMetrics()
	screen := p.dst
	defer func() { p.dst = screen }()
	// Nothing of a scene is drawn outside its room: what does not fit is cut at its edge, never drawn
	// over the name bar, the way out or the device's own parts of the screen.
	room := s.room.Add(off).Intersect(screen.Rect)
	var clip image.RGBA
	for i := range s.boxes {
		r, seen := s.shown(i, scrolls, off)
		if seen.Empty() {
			continue
		}
		view := room
		if c := s.boxes[i].clip; s.boxes[i].list == 0 && !c.Empty() {
			view = view.Intersect(c.Add(off))
		}
		if l := s.boxes[i].list; l > 0 {
			// Drawn on the screen as seen through the list's view: nothing of it past the view's edges.
			view = s.lists[l-1].view.Add(off).Intersect(room)
		}
		if view.Empty() || !seen.Overlaps(view) {
			continue
		}
		clip = *screen
		clip.Rect = view
		clip.Pix = screen.Pix[screen.PixOffset(view.Min.X, view.Min.Y):]
		p.dst = &clip
		p.drawBox(&s.boxes[i], r, m, pal, timers, pics)
	}
	p.dst = screen
	// A list with more than its view holds says so: its edges fade, and a thumb says where the view is.
	for i, l := range s.lists {
		most := s.maxScroll(i)
		if most == 0 {
			continue
		}
		by := 0
		if i < len(scrolls) {
			by = min(max(scrolls[i], 0), most)
		}
		p.scrollHints(l.view.Add(off), by, most, pal.ground)
	}
}

// drawBox draws one box at r.
func (p *paint) drawBox(box *sceneBox, r image.Rectangle, m sceneMetrics, pal scenePalette, timers sceneTimers, pics scenePics) {
	b := box.b
	// Its parts' places (a timer's time, a stepper's number, a toggle's switch) moved as the box was, as
	// a list or a scrolling column moves it.
	lbl := box.label.Add(r.Min.Sub(box.r.Min))
	switch {
	case box.rule:
		for x := r.Min.X; x < r.Max.X; x++ {
			p.blendAt(x, r.Min.Y, pal.rules, 0.8)
		}
		return
	case box.head:
		p.text(box.face, box.lines[0], r.Min.X, r.Min.Y+box.face.Metrics().Ascent.Ceil(), pal.accent)
		return
	case box.item:
		return // the item's own blocks are drawn as they come
	case box.lead:
		// A list row's icon, in the theme's accent; one this build has no glyph for is drawn as nothing.
		if addonHasIcon(b.Icon) {
			side := min(r.Dx(), r.Dy()) * 3 / 4
			c := image.Pt(r.Min.X+r.Dx()/2, r.Min.Y+r.Dy()/2)
			p.mdiIconIn(b.Icon, image.Rect(c.X-side/2, c.Y-side/2, c.X+side/2, c.Y+side/2), p.unscaled(side), pal.accent)
		}
		return
	}
	switch b.Type {
	case addonapi.BlockText:
		col := pal.text
		if b.Size == addonapi.SizeSmall {
			col = pal.dim
		}
		lh, asc := lineH(box.face), box.face.Metrics().Ascent.Ceil()
		for i, ln := range box.lines {
			w := p.width(box.face, ln)
			x := r.Min.X
			switch b.Align {
			case "center":
				x += (r.Dx() - w) / 2
			case "end":
				x = r.Max.X - w
			}
			p.text(box.face, ln, x, r.Min.Y+i*lh+asc, col)
		}
	case addonapi.BlockToggle:
		f := box.face
		base := r.Min.Y + r.Dy()/2 + (f.Metrics().Ascent.Ceil()-f.Metrics().Descent.Ceil())/2
		p.text(f, box.lines[0], r.Min.X, base, pal.text)
		// The switch: a track, filled with the accent when on, and its knob at the end it is at.
		sw := lbl
		rad := float64(sw.Dy()) / 2
		knob := sw.Dy() - 2*max(sw.Dy()/10, 2)
		pad := (sw.Dy() - knob) / 2
		kx := sw.Min.X + pad
		if b.On {
			p.roundFill(sw, rad, shift(pal.accent, 10), shift(pal.accent, -10))
			kx = sw.Max.X - pad - knob
		} else {
			p.roundFill(sw, rad, pal.surface(4), pal.surface(4))
			p.roundStroke(sw, rad, 1, pal.rules)
		}
		ink := pal.text
		if b.On {
			ink = pal.onAccent
		}
		p.aaDisc(float64(kx)+float64(knob)/2, float64(sw.Min.Y)+float64(sw.Dy())/2, float64(knob)/2, ink)
	case addonapi.BlockStepper:
		if box.step == 0 {
			// Its label at the start, its number between − and +.
			f := box.face
			base := r.Min.Y + r.Dy()/2 + (f.Metrics().Ascent.Ceil()-f.Metrics().Descent.Ceil())/2
			if box.lines[0] != "" {
				p.text(f, box.lines[0], r.Min.X, base, pal.text)
			}
			bold := sceneFace(p.faceKey(), m.text[1], true)
			v := p.fit(bold, box.lines[1], max(lbl.Dx(), 1))
			p.text(bold, v, lbl.Min.X+(lbl.Dx()-p.width(bold, v))/2, base, pal.text)
			return
		}
		// − or +, a round button, drawn faint where it would go past the range.
		end := (box.step < 0 && b.Value != nil && b.Min != nil && *b.Value <= *b.Min) ||
			(box.step > 0 && b.Value != nil && b.Max != nil && *b.Value >= *b.Max)
		rad := float64(r.Dy()) / 2
		ink := pal.text
		if end {
			p.roundFill(r, rad, pal.surface(2), pal.surface(2))
			p.roundStroke(r, rad, 1, pal.rules)
			ink = pal.dim
		} else {
			p.roundFill(r, rad, shift(pal.surface(5), 4), pal.surface(3))
			p.roundStroke(r, rad, 1, lerp(pal.rules, pal.surface(5), 0.3))
		}
		cx, cy := float64(r.Min.X)+float64(r.Dx())/2, float64(r.Min.Y)+float64(r.Dy())/2
		arm, pen := float64(r.Dy())*0.2, max(float64(r.Dy())/16, 2)
		p.aaLine(cx-arm, cy, cx+arm, cy, pen, ink)
		if box.step > 0 {
			p.aaLine(cx, cy-arm, cx, cy+arm, pen, ink)
		}
	case addonapi.BlockButton, addonapi.BlockChip:
		rad := float64(r.Dy()) / 2
		ink := pal.text
		if b.Type == addonapi.BlockButton {
			p.roundFill(r, rad, shift(pal.surface(5), 4), pal.surface(3))
			p.roundStroke(r, rad, 1, lerp(pal.rules, pal.surface(5), 0.3))
		} else if b.Selected {
			p.roundFill(r, rad, shift(pal.accent, 10), shift(pal.accent, -10))
			ink = pal.onAccent
		} else {
			p.roundFill(r, rad, pal.surface(2), pal.surface(2))
			p.roundStroke(r, rad, 1, pal.rules)
		}
		x := r.Min.X + m.padX
		if b.Label == "" {
			x = r.Min.X + (r.Dx()-m.icon)/2
		}
		if b.Icon != "" {
			p.mdiIconIn(b.Icon, image.Rect(x, r.Min.Y, x+m.icon, r.Max.Y), p.unscaled(m.icon), ink)
			x += m.icon + m.gap[1]
		}
		if len(box.lines) > 0 && box.lines[0] != "" {
			mid := r.Min.Y + r.Dy()/2
			base := mid + (box.face.Metrics().Ascent.Ceil()-box.face.Metrics().Descent.Ceil())/2
			p.text(box.face, box.lines[0], x, base, ink)
		}
	case addonapi.BlockProgress:
		v := 0.0
		bar := r
		if b.Value != nil {
			v = *b.Value
		}
		if b.Timer != "" {
			bar.Max.X = lbl.Min.X - m.gap[2]
			if left, total, ok := timers.get(b.Timer); ok {
				if total > 0 {
					v = float64(left) / float64(total)
				}
				// Its time at the end; past an hour it is longer than the room kept for it, and the bar
				// gives up what it needs.
				t := sceneTime(left)
				x := lbl.Max.X - p.width(box.face, t)
				bar.Max.X = min(bar.Max.X, x-m.gap[2])
				p.text(box.face, t, x, r.Min.Y+r.Dy()/2+box.face.Metrics().Ascent.Ceil()/2-p.s(2), pal.text)
			}
		}
		mid := (bar.Min.Y + bar.Max.Y) / 2
		track := image.Rect(bar.Min.X, mid-m.bar/2, bar.Max.X, mid-m.bar/2+m.bar)
		rad := float64(m.bar) / 2
		p.roundFill(track, rad, pal.surface(4), pal.surface(4))
		if fill := int(float64(track.Dx()) * max(0, min(1, v))); fill > m.bar {
			f := track
			f.Max.X = f.Min.X + fill
			p.roundFill(f, rad, pal.accent, pal.accent)
		}
	case addonapi.BlockDivider:
		mid := (r.Min.Y + r.Max.Y) / 2
		for x := r.Min.X; x < r.Max.X; x++ {
			p.blendAt(x, mid, pal.rules, 1)
		}
	case addonapi.BlockImage:
		rad := float64(p.s(12))
		if box.pic.key == "" || pics == nil {
			p.roundFill(r, rad, pal.surface(2), pal.surface(2))
			return
		}
		img := pics(box.pic, r.Dx(), r.Dy())
		if img == nil {
			p.roundFill(r, rad, pal.surface(2), pal.surface(2))
			return
		}
		draw.Draw(p.dst, r, img, img.Rect.Min, draw.Over)
	case addonapi.BlockCard:
		rad := float64(p.s(18))
		p.roundFill(r, rad, pal.surface(2), pal.surface(1))
		col := pal.rules
		switch b.Accent {
		case "accent":
			col = pal.accent
		case "dim":
			col = pal.dim
		case "":
			return
		}
		stripe := image.Rect(r.Min.X+m.pad/2, r.Min.Y+m.pad, r.Min.X+m.pad/2+m.stripe, r.Max.Y-m.pad)
		p.roundFill(stripe, float64(m.stripe)/2, col, col)
	}
}

// get is the timer's time left and its length, with no timers known as none.
func (t sceneTimers) get(id string) (time.Duration, time.Duration, bool) {
	if t == nil {
		return 0, 0, false
	}
	return t(id)
}

// sceneTime is a countdown's time: m:ss, or h:mm:ss past an hour.
func sceneTime(d time.Duration) string {
	s := int(max(d, 0).Round(time.Second) / time.Second)
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60)
	}
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

// unscaled is a size already scaled to the screen as mdiIconIn takes it, which scales it again.
func (p *paint) unscaled(px int) int {
	if p.sDen == 0 || p.sNum == p.sDen {
		return px
	}
	return px * p.sDen / p.sNum
}

// addonInitials is an add-on's mark on its tile and its name bar when its catalog entry gives it no icon:
// up to two capitals of its name. Never an icon the add-on chooses: a picture it picked could pass for one
// of the device's own. The icon there is, is the catalog reviewer's (catalog.Version.Icon).
func addonInitials(name string) string {
	out := ""
	for _, w := range strings.FieldsFunc(name, func(r rune) bool { return r == ' ' || r == '-' || r == '_' }) {
		if r, _ := utf8.DecodeRuneInString(w); r != utf8.RuneError && utf8.RuneLen(r) > 0 && utf8.RuneCountInString(out) < 2 {
			out += upper(string(r))
		}
	}
	if out == "" {
		out = "?"
	}
	return out
}

// addonMark draws an add-on's mark in a disc of the accent, centered on cx, cy: the icon its catalog entry
// gives it, or without one (or one this build has no glyph for) its initials.
func (p *paint) addonMark(name, icon string, cx, cy, rad int, pal scenePalette, f font.Face) {
	p.aaDisc(float64(cx), float64(cy), float64(rad), pal.accent)
	if addonHasIcon(icon) {
		side := rad * 13 / 10
		p.mdiIconIn(icon, image.Rect(cx-side/2, cy-side/2, cx+side/2, cy+side/2), p.unscaled(side), pal.onAccent)
		return
	}
	in := addonInitials(name)
	w := p.width(f, in)
	asc := f.Metrics().Ascent.Ceil() - f.Metrics().Descent.Ceil()
	p.text(f, in, cx-w/2, cy+asc/2, pal.onAccent)
}

// addonHasIcon is whether icon is one to draw as an add-on's mark: one the catalog gave it (a check made
// again here, as the catalog was), with a glyph in this build's font.
func addonHasIcon(icon string) bool {
	if !strings.HasPrefix(icon, "mdi:") {
		return false
	}
	_, ok := mdi.Rune(icon)
	return ok
}

// addonPicKey is an image as it is kept: its bytes' key, which begins with its add-on's id
// (serve.imageKey), at a size, filled or fitted.
type addonPicKey struct {
	key   string
	w, h  int
	cover bool
}

// addon is the add-on whose image it is.
func (k addonPicKey) addon() string {
	id, _, _ := strings.Cut(k.key, "/")
	return id
}

// addonPictures keeps add-ons' images decoded and scaled to the size they are drawn at, made one at a
// time on a goroutine of their own: a frame draws what is kept, and never waits for one. What is kept is
// held to addonPicsBytes all together, the least recently drawn going first; an add-on's own 8 MB is
// the server's to hold (its bytes), this is the screen's copy of what it shows. Each add-on's pictures
// are its own (their keys begin with its id), go when it does (drop), and are made at most
// addonPicBurst at once and addonPicsPerSecond a second after that, so an add-on that keeps changing
// its pictures cannot keep a core decoding.
type addonPictures struct {
	mu      sync.Mutex
	kept    map[addonPicKey]*addonPic
	bytes   int
	asked   map[addonPicKey]bool
	failed  map[addonPicKey]time.Time
	budgets map[string]*addonPicBudget // by add-on
	later   *time.Timer                // a frame drawn when a picture held back may be made
	work    chan addonPicJob
	changed func() // a picture is ready: draw again
}

type addonPic struct {
	img  *image.RGBA
	used time.Time
}

type addonPicJob struct {
	k    addonPicKey
	data []byte
}

// addonPicBudget is how many pictures an add-on may have made now: up to addonPicBurst, one more every
// second / addonPicsPerSecond.
type addonPicBudget struct {
	left float64
	at   time.Time
}

const (
	// addonPicsBytes is the most the screen keeps of add-ons' images, decoded and scaled: two pages'
	// worth of photos on a Show 8.
	addonPicsBytes = 12 << 20
	// addonPicRetry is how long an image that would not decode is left before it is tried again, and
	// addonPicsFailed how many of those are remembered, the oldest forgotten first.
	addonPicRetry   = time.Minute
	addonPicsFailed = 256
	// addonPicBurst and addonPicsPerSecond bound an add-on's decoding: that many pictures at once (a
	// page of thumbnails), then that many a second. One that waits is asked for again by the frame drawn
	// when it may be made.
	addonPicBurst      = 8
	addonPicsPerSecond = 2
)

// addonPics are the screen's add-on images.
var addonPics = &addonPictures{}

// picture is the image at w by h, if it is ready; otherwise nil, and it is made.
func (a *addonPictures) picture(p scenePic, w, h int) *image.RGBA {
	if p.key == "" || w <= 0 || h <= 0 {
		return nil
	}
	k := addonPicKey{p.key, w, h, p.cover}
	a.mu.Lock()
	defer a.mu.Unlock()
	if pic, ok := a.kept[k]; ok {
		pic.used = time.Now()
		return pic.img
	}
	if a.asked[k] {
		return nil
	}
	if at, ok := a.failed[k]; ok {
		if time.Since(at) < addonPicRetry {
			return nil
		}
		delete(a.failed, k)
	}
	if a.work == nil {
		a.kept, a.asked, a.failed = map[addonPicKey]*addonPic{}, map[addonPicKey]bool{}, map[addonPicKey]time.Time{}
		a.budgets = map[string]*addonPicBudget{}
		a.work = make(chan addonPicJob, 32)
		go a.worker()
	}
	if !a.takeLocked(k.addon()) {
		return nil
	}
	select {
	case a.work <- addonPicJob{k, p.data}:
		a.asked[k] = true
	default: // asked for again by the next frame, and not counted
		a.budgets[k.addon()].left++
	}
	return nil
}

// takeLocked spends one of the add-on's pictures to make, and says whether there was one; with none
// left, a frame is drawn when there is. a.mu is held.
func (a *addonPictures) takeLocked(id string) bool {
	now := time.Now()
	b := a.budgets[id]
	if b == nil {
		b = &addonPicBudget{left: addonPicBurst, at: now}
		a.budgets[id] = b
	}
	b.left = min(addonPicBurst, b.left+now.Sub(b.at).Seconds()*addonPicsPerSecond)
	b.at = now
	if b.left >= 1 {
		b.left--
		return true
	}
	if a.later == nil && a.changed != nil {
		changed := a.changed
		a.later = time.AfterFunc(time.Duration((1-b.left)/addonPicsPerSecond*float64(time.Second)), func() {
			a.mu.Lock()
			a.later = nil
			a.mu.Unlock()
			changed()
		})
	}
	return false
}

// drop lets go of everything of the add-on's: its pictures, those on their way (which are not kept when
// they are made), and those that would not decode.
func (a *addonPictures) drop(id string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for k, p := range a.kept {
		if k.addon() == id {
			a.bytes -= len(p.img.Pix)
			delete(a.kept, k)
		}
	}
	for k := range a.asked {
		if k.addon() == id {
			delete(a.asked, k)
		}
	}
	for k := range a.failed {
		if k.addon() == id {
			delete(a.failed, k)
		}
	}
	delete(a.budgets, id)
}

// failLocked notes a picture that would not decode, forgetting those whose time is up and, past
// addonPicsFailed, the oldest. a.mu is held.
func (a *addonPictures) failLocked(k addonPicKey) {
	if len(a.failed) >= addonPicsFailed {
		for x, at := range a.failed {
			if time.Since(at) >= addonPicRetry {
				delete(a.failed, x)
			}
		}
		for len(a.failed) >= addonPicsFailed {
			var oldest addonPicKey
			var first time.Time
			for x, at := range a.failed {
				if first.IsZero() || at.Before(first) {
					oldest, first = x, at
				}
			}
			delete(a.failed, oldest)
		}
	}
	a.failed[k] = time.Now()
}

func (a *addonPictures) worker() {
	for j := range a.work {
		img, err := scaledPicture(j.data, j.k.w, j.k.h, j.k.cover)
		a.mu.Lock()
		if !a.asked[j.k] {
			a.mu.Unlock()
			continue // its add-on went meanwhile (drop)
		}
		delete(a.asked, j.k)
		if err != nil {
			a.failLocked(j.k)
			a.mu.Unlock()
			continue
		}
		a.kept[j.k] = &addonPic{img: img, used: time.Now()}
		a.bytes += len(img.Pix)
		for a.bytes > addonPicsBytes && len(a.kept) > 1 {
			var oldest addonPicKey
			var at time.Time
			for k, p := range a.kept {
				if k != j.k && (at.IsZero() || p.used.Before(at)) {
					oldest, at = k, p.used
				}
			}
			a.bytes -= len(a.kept[oldest].img.Pix)
			delete(a.kept, oldest)
		}
		changed := a.changed
		a.mu.Unlock()
		if changed != nil {
			changed()
		}
	}
}

// errPictureSize is an image whose pixels are not what its header said, past the API's limits.
var errPictureSize = errors.New("an add-on's image is larger than its header said")

// scaledPicture decodes an add-on's image and scales it to w by h: all of it, its shape kept, or filling
// it and cut at the edges (cover). Its header is held to the API's limits before anything is decoded,
// and its pixels to what the header said.
func scaledPicture(data []byte, w, h int, cover bool) (*image.RGBA, error) {
	if err := addonapi.ImageBytes(data); err != nil {
		return nil, err
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if err := addonapi.ImageSize(src.Bounds().Dx(), src.Bounds().Dy()); err != nil {
		return nil, fmt.Errorf("%w: %w", errPictureSize, err)
	}
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	sr := src.Bounds()
	if cover {
		sr = coverPart(sr, out.Rect)
	}
	picscale.CatmullRom.Scale(out, out.Rect, src, sr, draw.Src)
	return out, nil
}

// containPart is the part of dst that src fills scaled to fit inside it, its shape kept, in the middle.
func containPart(src, dst image.Rectangle) image.Rectangle {
	w, h := dst.Dx(), src.Dy()*dst.Dx()/max(src.Dx(), 1)
	if h > dst.Dy() {
		w, h = src.Dx()*dst.Dy()/max(src.Dy(), 1), dst.Dy()
	}
	x, y := dst.Min.X+(dst.Dx()-w)/2, dst.Min.Y+(dst.Dy()-h)/2
	return image.Rect(x, y, x+max(w, 1), y+max(h, 1))
}

// coverPart is the middle of src with dst's shape: what fills dst, cut at the edges.
func coverPart(src, dst image.Rectangle) image.Rectangle {
	sw, sh, dw, dh := src.Dx(), src.Dy(), dst.Dx(), dst.Dy()
	if sw <= 0 || sh <= 0 || dw <= 0 || dh <= 0 {
		return src
	}
	if sw*dh > sh*dw {
		w := max(1, sh*dw/dh)
		x := src.Min.X + (sw-w)/2
		return image.Rect(x, src.Min.Y, x+w, src.Max.Y)
	}
	hh := max(1, sw*dh/dw)
	y := src.Min.Y + (sh-hh)/2
	return image.Rect(src.Min.X, y, src.Max.X, y+hh)
}

// picturesNow decodes an image at once, for the fake daemon's PNG and the previews, where nothing waits
// on a frame.
func picturesNow(p scenePic, w, h int) *image.RGBA {
	img, err := scaledPicture(p.data, w, h, p.cover)
	if err != nil {
		return nil
	}
	return img
}
