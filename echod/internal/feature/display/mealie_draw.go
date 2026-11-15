//go:build !dot && !spot

package display

import (
	"image"
	"image/color"
	"strings"
	"unicode/utf8"

	"github.com/HuskerMinion/techo5/echod/internal/feature/dashboard"
)

// The recipe sheet as it is drawn: "Recipe" as a title and a Done button  along its top, and under them either a grid of the recipes,
// or a message saying why there is none.

// What a part of the recipe sheet is, for a tap. Zero is none, so that a zone that was not found is no part.
const (
	_ = iota
	mealiePartDone
	mealiePartItem // index: which recipe, in the order of the list
	mealiePartBack // the guide's way back to the list
	mealiePartPrev
	mealiePartNext
)

// mealieTargetTileWidth is the width the layout aims for a recipe's tile, in the panel's own pixels: it
// makes four abreast on the Show 5 and five on the Show 8, as the library's covers are. The tiles
// themselves are as wide as the room left by that many columns allows.
const mealieTargetTileWidth = 235

// mealieParts are the recipe sheet's parts where they were last drawn, under zmu, for the touch
// goroutine: its zones, and the card they are on, and how high the recipes are and how high the view
// they scroll in is, for how far they can be scrolled.
type mealieParts struct {
	mealieZones    []mediaZone
	mealieCard     image.Rectangle
	mealieContentH int
	mealieViewH    int
}

func (r *renderer) setMealie(zones []mediaZone, card image.Rectangle, contentH, viewH int) {
	r.zmu.Lock()
	r.mealieZones, r.mealieCard, r.mealieContentH, r.mealieViewH = zones, card, contentH, viewH
	r.zmu.Unlock()
}

// mealieMaxScroll returns how far the recipes can be scrolled, in pixels, as they were last drawn.
func (r *renderer) mealieMaxScroll() int {
	r.zmu.Lock()
	defer r.zmu.Unlock()
	return max(r.mealieContentH-r.mealieViewH, 0)
}

// mealieAt returns the part of the recipe sheet under x, y, and whether that is on the sheet at all.
func (r *renderer) mealieAt(x, y int) (mediaZone, bool) {
	r.zmu.Lock()
	zones, card := r.mealieZones, r.mealieCard
	r.zmu.Unlock()
	z, _, on := mediaHit(zones, card, x, y)
	return z, on
}

// mealieGeom is where the recipe sheet's parts go, for this panel.
type mealieGeom struct {
	card, bar, view   image.Rectangle
	foot, guide       image.Rectangle // the guide's Previous and Next along the bottom, and the page between
	pad, gap, inner   int
	cols, tile, tileH int
	x0, x1            int
}

// mealieGeometry returns where the recipe sheet's parts go: the card, the bar along its top, the view
// under it that the recipes fill, and the size of a tile. The guide has the footer along the bottom, and
// its page in the view between the bar and the footer.
func (r *renderer) mealieGeometry() mealieGeom {
	var g mealieGeom
	g.card = r.dst.Rect.Inset(r.s(8))
	g.pad, g.gap = r.s(16), r.s(20)
	g.bar = image.Rect(g.card.Min.X+g.pad, g.card.Min.Y+g.pad, g.card.Max.X-g.pad, g.card.Min.Y+g.pad+r.s(46))
	g.view = image.Rect(g.card.Min.X, g.bar.Max.Y+r.s(10), g.card.Max.X, g.card.Max.Y-r.s(6))
	g.x0, g.x1 = g.card.Min.X+g.pad, g.card.Max.X-g.pad
	g.inner = g.x1 - g.x0
	g.foot = image.Rect(g.x0, g.card.Max.Y-g.pad-r.s(46), g.x1, g.card.Max.Y-g.pad)
	g.guide = image.Rect(g.card.Min.X, g.view.Min.Y, g.card.Max.X, g.foot.Min.Y-r.s(10))
	g.cols = max(2, (g.inner+mealieTargetTileWidth/2)/mealieTargetTileWidth)
	g.tile = (g.inner - g.gap*(g.cols-1)) / g.cols
	g.tileH = g.tile + r.s(46)
	return g
}

// mealieSheet draws the recipe sheet over the drawn dashboard and keeps where its parts are, for
// mealieAt. With no sheet it draws nothing and keeps no parts.
func (r *renderer) mealieSheet(s *mealieSheet, th dashboard.Theme) {
	if s == nil {
		r.setMealie(nil, image.Rectangle{}, 0, 0)
		return
	}
	whole := r.dst.Rect
	pal := r.palette(th)
	fc := r.faces()
	g := r.mealieGeometry()
	washOver(r.dst, whole, color.RGBA{0, 0, 0, 150})
	r.roundFill(g.card, pal.rad, pal.card, pal.card)

	var zones []mediaZone
	var contentH, viewH int
	if s.selected != nil {
		zones, contentH, viewH = r.mealieGuide(s, g, pal, fc)
	} else {
		// The bar: the sheet's name at the left, and Done at the right.
		word := libWord(libWordDone)
		doneW := max(r.width(fc.navBold, word)+2*r.s(20), r.s(84))
		done := image.Rect(g.bar.Max.X-doneW, g.bar.Min.Y, g.bar.Max.X, g.bar.Max.Y)
		r.mealiePill(done, word, pal.accent, pal.bg, fc)
		r.text(fc.labelBold, tr("ui_mealie_recipes"), g.bar.Min.X, g.bar.Min.Y+r.s(33), pal.text)

		var content []mediaZone
		content, contentH = r.mealieContent(s, g, pal, fc)
		zones, viewH = append([]mediaZone{{r: done, kind: mealiePartDone}}, content...), g.view.Dy()
	}
	// The whole screen is the sheet's: a tap beside the card is not a tap on the dashboard under it.
	r.setMealie(zones, whole, contentH, viewH)
}

// mealieNotice returns what stands in for the recipes, or for the guide's page, when the sheet has none
// to show: an icon, a message and a hint under it, any of which may be empty. With no message there is
// none to say, and the recipes or the page are shown.
func mealieNotice(s *mealieSheet) (icon, msg, hint string) {
	switch {
	case s.selected != nil && s.detailProblem != "":
		return "alert-circle-outline", s.detailProblem, ""
	case s.selected != nil && s.detail == nil:
		return "", libWord(libWordLoading), ""
	case s.selected != nil && mealiePages(s.detail) == 0:
		return "chef-hat", libWord(libWordEmpty), tr("ui_mealie_no_steps")
	case s.selected != nil:
		return "", "", ""
	case !s.set:
		return "cog-outline", tr("ui_mealie_set_up_hint"), ""
	case s.listProblem != "":
		return "alert-circle-outline", s.listProblem, ""
	case !s.listLoaded:
		return "", libWord(libWordLoading), ""
	case len(s.recipes) == 0:
		return "chef-hat", libWord(libWordEmpty), tr("ui_mealie_empty_hint")
	}
	return "", "", ""
}

// mealieContent draws what is under the bar, the recipes or the message that stands in for them, and
// returns the zones of the recipes it drew and how high all the recipes are, drawn or not. The recipes
// are drawn scrolled up by the sheet's scroll, kept to what there is to scroll.
func (r *renderer) mealieContent(s *mealieSheet, g mealieGeom, pal dashPal, fc sheetFaces) ([]mediaZone, int) {
	if icon, msg, hint := mealieNotice(s); msg != "" {
		r.mealieMessage(g, pal, fc, icon, msg, hint)
		return nil, 0
	}

	// What is drawn here keeps to the view: every drawing keeps to r.dst.
	full := r.dst
	r.dst = full.SubImage(g.view).(*image.RGBA)
	defer func() { r.dst = full }()
	top := r.s(6)
	rows := (len(s.recipes) + g.cols - 1) / g.cols
	contentH := top + rows*g.tileH
	scroll := min(s.scroll, max(contentH-g.view.Dy(), 0))
	var zones []mediaZone
	for i, recipe := range s.recipes {
		x := g.x0 + (i%g.cols)*(g.tile+g.gap)
		y := g.view.Min.Y + top + (i/g.cols)*g.tileH - scroll
		if y >= g.view.Max.Y {
			break
		}
		if y+g.tileH <= g.view.Min.Y {
			continue // scrolled out above
		}
		r.mealieTile(recipe.Name, x, y, g, pal, fc)
		if z := image.Rect(x, y, x+g.tile, y+g.tileH).Intersect(g.view); !z.Empty() {
			zones = append(zones, mediaZone{r: z, kind: mealiePartItem, index: i})
		}
	}
	return zones, contentH
}

// mealieTile draws one recipe: a square in a color made from its name with its first letter, and its
// name under it. The square is tinted.
func (r *renderer) mealieTile(name string, x, y int, g mealieGeom, pal dashPal, fc sheetFaces) {
	pic := image.Rect(x, y, x+g.tile, y+g.tile)
	hue := libHue(name)
	top, bottom := lerp(pal.card, hue, 0.55), lerp(pal.card, hue, 0.22)
	r.roundFill(pic, float64(max(pic.Dx()/14, 2)), top, bottom)
	if first, _ := utf8.DecodeRuneInString(name); first != utf8.RuneError {
		letter := strings.ToUpper(string(first))
		m := fc.header.Metrics()
		base := pic.Min.Y + g.tile/2 + (m.Ascent.Ceil()-m.Descent.Ceil())/2
		r.text(fc.header, letter, pic.Min.X+(g.tile-r.width(fc.header, letter))/2, base, lerp(bottom, pal.text, 0.6))
	}
	label := r.fit(fc.navBold, dashboard.CleanName(name), g.tile-r.s(4))
	r.text(fc.navBold, label, x+r.s(2), pic.Max.Y+r.s(30), pal.text)
}

// mealiePill draws a button as a pill with word in the middle of it.
func (r *renderer) mealiePill(b image.Rectangle, word string, fill, fg color.RGBA, fc sheetFaces) {
	r.roundFill(b, float64(b.Dy())/2, fill, fill)
	start := b.Min.X + max((b.Dx()-r.width(fc.navBold, word))/2, r.s(12))
	r.text(fc.navBold, r.fit(fc.navBold, word, b.Max.X-start-r.s(10)), start, b.Min.Y+b.Dy()/2+r.s(8), fg)
}

// mealieMessage draws msg, with icon over it if there is one and hint under it if there is one, in the
// middle of the view, in place of the recipes.
func (r *renderer) mealieMessage(g mealieGeom, pal dashPal, fc sheetFaces, icon, msg, hint string) {
	mid := g.x0 + g.inner/2
	y := g.view.Min.Y + r.s(60)
	if icon != "" {
		side := r.s(64)
		r.mdiIconIn(icon, image.Rect(mid-side/2, y, mid+side/2, y+side), 56, lerp(pal.card, pal.sub, 0.7))
		y += side + r.s(16)
	}
	for _, line := range r.wrapLines(fc.label, msg, g.inner*4/5) {
		y += r.s(32)
		r.text(fc.label, line, mid-r.width(fc.label, line)/2, y, pal.text)
	}
	for _, line := range r.wrapLines(fc.sub, hint, g.inner*4/5) {
		y += r.s(28)
		r.text(fc.sub, line, mid-r.width(fc.sub, line)/2, y, pal.sub)
	}
}
