//go:build !dot && !spot

package display

import (
	"fmt"
	"image"
	"image/color"
	"strings"

	"golang.org/x/image/font"

	"github.com/HuskerMinion/techo5/echod/internal/feature/dashboard"
	"github.com/HuskerMinion/techo5/echod/internal/lib/mealie"
)

// The recipe sheet's guide as drawn: the recipe's name and a Back button along the top, a page under them, and a
// Previous and Next button along the bottom. The first page lists the ingredients, as the
// To-do tab lists its items; every other page is a step. A page too long for its room scrolls, and shows
// it with scrollHints.

// mealieLine is a piece of a guide's page: a line of text, or a rule, at its place on the page, which
// is counted from the page's top whatever the page is scrolled to.
type mealieLine struct {
	text string
	face font.Face
	col  color.RGBA
	y    int // the baseline of a text, the place of a rule
	rule bool
}

// mealieGuide draws the guide of the selected recipe, or what stands in for its page, and returns the
// zones of what can be tapped, how high the page is and how high the room for it is.
func (r *renderer) mealieGuide(s *mealieSheet, g mealieGeom, pal dashPal, fc sheetFaces) ([]mediaZone, int, int) {
	// The bar: the recipe's name at the left, and Back at the right.
	word := tr("back")
	back := image.Rect(g.bar.Max.X-r.mealiePillW(word, fc), g.bar.Min.Y, g.bar.Max.X, g.bar.Max.Y)
	r.mealiePill(back, word, pal.accent, pal.bg, fc)
	name := r.fit(fc.labelBold, dashboard.CleanName(s.selected.Name), back.Min.X-r.s(16)-g.bar.Min.X)
	r.text(fc.labelBold, name, g.bar.Min.X, g.bar.Min.Y+r.s(33), pal.text)
	zones := []mediaZone{{r: back, kind: mealiePartBack}}

	if icon, msg, hint := mealieNotice(s); msg != "" {
		r.mealieMessage(g, pal, fc, icon, msg, hint)
		return zones, 0, g.view.Dy()
	}

	pages := mealiePages(s.detail)
	step := min(s.step, pages-1)
	zones = append(zones, r.mealieFoot(s, step, pages, g, pal, fc)...)

	var lines []mealieLine
	var contentH int
	if first := min(len(s.detail.Ingredients), 1); step < first {
		lines, contentH = r.mealieIngredients(s, g, pal, fc)
	} else {
		lines, contentH = r.mealieStep(s, step-first, g, pal, fc)
	}

	// What is drawn here keeps to the room for the page: every drawing keeps to r.dst.
	full := r.dst
	r.dst = full.SubImage(g.guide).(*image.RGBA)
	defer func() { r.dst = full }()
	most := max(contentH-g.guide.Dy(), 0)
	scroll := min(s.scroll, most)
	rule := lerp(pal.card, pal.sub, 0.25)
	for _, l := range lines {
		y := g.guide.Min.Y + l.y - scroll
		if y < g.guide.Min.Y-r.s(60) || y > g.guide.Max.Y+r.s(60) {
			continue // too far out to reach into the room
		}
		if l.rule {
			r.roundFill(image.Rect(g.x0, y, g.x1, y+max(r.s(1), 1)), 0, rule, rule)
			continue
		}
		r.text(l.face, l.text, g.x0, y, l.col)
	}
	if most > 0 {
		r.scrollHints(g.guide, scroll, most, pal.card) // there is more to see: fade into its edges, and a bar
	}
	return zones, contentH, g.guide.Dy()
}

// mealieFoot draws Previous and Next along the bottom with where in the recipe the page is between them,
// and returns the zones of the two that can be tapped: Previous is not on the first page, nor Next on
// the last.
func (r *renderer) mealieFoot(s *mealieSheet, step, pages int, g mealieGeom, pal dashPal, fc sheetFaces) []mediaZone {
	prevWord, nextWord := tr("ui_mealie_previous"), tr("next")
	w := max(r.width(fc.navBold, prevWord), r.width(fc.navBold, nextWord)) + 2*r.s(20)
	w = max(w, r.s(120))
	prev := image.Rect(g.foot.Min.X, g.foot.Min.Y, g.foot.Min.X+w, g.foot.Max.Y)
	next := image.Rect(g.foot.Max.X-w, g.foot.Min.Y, g.foot.Max.X, g.foot.Max.Y)
	idle, idleText := lerp(pal.card, pal.text, 0.06), lerp(pal.card, pal.sub, 0.6)
	var zones []mediaZone
	if step > 0 {
		r.mealiePill(prev, prevWord, lerp(pal.card, pal.text, 0.14), pal.text, fc)
		zones = append(zones, mediaZone{r: prev, kind: mealiePartPrev})
	} else {
		r.mealiePill(prev, prevWord, idle, idleText, fc)
	}
	if step < pages-1 {
		r.mealiePill(next, nextWord, pal.accent, pal.bg, fc)
		zones = append(zones, mediaZone{r: next, kind: mealiePartNext})
	} else {
		r.mealiePill(next, nextWord, idle, idleText, fc)
	}
	where := mealieWhere(s.detail, step)
	r.text(fc.sub, where, (g.foot.Min.X+g.foot.Max.X-r.width(fc.sub, where))/2, g.foot.Min.Y+g.foot.Dy()/2+r.s(7), pal.sub)
	return zones
}

// mealieWhere returns where in the recipe the page at step is: the ingredients, or which step of how
// many.
func mealieWhere(recipe *mealie.DetailedRecipe, step int) string {
	first := min(len(recipe.Ingredients), 1)
	if step < first {
		return tr("ui_mealie_ingredients")
	}
	return fmt.Sprintf(tr("ui_mealie_step_of"), step-first+1, len(recipe.Steps))
}

// mealieIngredients lays out the ingredients page: each ingredient on a row with a rule under it, and
// the heading of a section as a bold row. The page has no heading of its own, since the footer says it
// is the ingredients. It returns the lines and how high the page is.
func (r *renderer) mealieIngredients(s *mealieSheet, g mealieGeom, pal dashPal, fc sheetFaces) ([]mealieLine, int) {
	y := r.s(6)
	var lines []mealieLine
	for _, in := range s.detail.Ingredients {
		if in.Section != "" {
			lines = append(lines, mealieLine{text: r.fit(fc.labelBold, in.Section, g.inner), face: fc.labelBold, col: pal.accent, y: y + r.s(36)})
			y += r.s(52)
		}
		wrapped := r.wrapLines(fc.label, in.Text, g.inner)
		for i, text := range wrapped {
			lines = append(lines, mealieLine{text: text, face: fc.label, col: pal.text, y: y + r.s(34) + i*r.s(34)})
		}
		y += r.s(20) + len(wrapped)*r.s(34)
		lines = append(lines, mealieLine{rule: true, y: y - 1})
	}
	return lines, y + r.s(6)
}

// mealieStep lays out the page of the step at index: its title, if it has one, then its text, a
// paragraph at a time. It returns the lines and how high the page is.
func (r *renderer) mealieStep(s *mealieSheet, index int, g mealieGeom, pal dashPal, fc sheetFaces) ([]mealieLine, int) {
	step := s.detail.Steps[index]
	y := r.s(6)
	var lines []mealieLine
	if step.Title != "" {
		wrapped := r.wrapLines(fc.labelBold, step.Title, g.inner)
		for i, text := range wrapped {
			lines = append(lines, mealieLine{text: text, face: fc.labelBold, col: pal.accent, y: y + r.s(32) + i*r.s(40)})
		}
		y += len(wrapped)*r.s(40) + r.s(10)
	}
	for _, paragraph := range strings.Split(step.Text, "\n") {
		wrapped := r.wrapLines(fc.label, paragraph, g.inner)
		for i, text := range wrapped {
			lines = append(lines, mealieLine{text: text, face: fc.label, col: pal.text, y: y + r.s(32) + i*r.s(42)})
		}
		if len(wrapped) > 0 {
			y += len(wrapped)*r.s(42) + r.s(14)
		}
	}
	return lines, y + r.s(6)
}
