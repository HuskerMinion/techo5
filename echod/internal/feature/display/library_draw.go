//go:build !dot && !spot

package display

import (
	"fmt"
	"hash/fnv"
	"image"
	"image/color"
	"slices"
	"strconv"

	"golang.org/x/image/font"

	"github.com/HuskerMinion/techo5/echod/internal/feature/dashboard"
	"github.com/HuskerMinion/techo5/echod/internal/lib/musicassistant"
)

// The music library as it is drawn: pill tabs fixed along the top, and under them a page a finger
// scrolls up and down, as the drawn dashboard itself scrolls - following the finger, held where it
// lifts. A tab is a grid of large covers with names under them (an artist's round); an album or a
// playlist opens on a header of its cover, its name and Play and Shuffle, with its tracks under it in
// rows; an artist on its picture and name, a row of its albums that scrolls along, and its top tracks.
// Only what is in view is drawn, and pictures are drawn as they come (library_pictures.go).

// libPartGoSearch is a part of an empty tab: its button to the search.
const libPartGoSearch = libPartRow + 1

// libParts are the library page's parts where they were last drawn, under zmu, for the touch
// goroutine: its zones and the page itself; how high what scrolls is, the view it scrolls in and how
// far it was scrolled; about how many covers a screen holds; and how far each row can scroll along.
type libParts struct {
	libZones    []mediaZone
	libCard     image.Rectangle
	libContentH int
	libViewH    int
	libScrolled int
	libVisible  int
	libRowMax   []int
}

func (r *renderer) setLibrary(zones []mediaZone, card image.Rectangle) {
	r.zmu.Lock()
	r.libZones, r.libCard = zones, card
	r.zmu.Unlock()
}

func (r *renderer) setLibraryScroll(contentH, viewH, scrolled, visible int, rowMax []int) {
	r.zmu.Lock()
	r.libContentH, r.libViewH, r.libScrolled, r.libRowMax = contentH, viewH, scrolled, rowMax
	if visible > 0 {
		r.libVisible = visible
	}
	r.zmu.Unlock()
}

// libraryAt is the part of the library page under x, y, and whether that is on the page at all.
func (r *renderer) libraryAt(x, y int) (mediaZone, float64, bool) {
	r.zmu.Lock()
	zones, card := slices.Clone(r.libZones), r.libCard
	r.zmu.Unlock()
	return mediaHit(zones, card, x, y)
}

// libRowAt is the section shown as a row under x, y, or -1.
func (r *renderer) libRowAt(x, y int) int {
	r.zmu.Lock()
	defer r.zmu.Unlock()
	for _, z := range r.libZones {
		if z.kind == libPartRow && image.Pt(x, y).In(z.r) {
			return z.index
		}
	}
	return -1
}

// libDragStart is a finger coming down on the library: where, and on which row.
func (d *Display) libDragStart(x, y int) {
	if d.r == nil {
		return
	}
	row := d.r.libRowAt(x, y)
	d.editLibrary(func(n *librarySheet) {
		n.drag = libDrag{at: image.Pt(x, y), row: row}
	})
}

// libDragMove is the finger moving: once it has gone far enough to tell, up or down scrolls the page
// and along a row scrolls the row, each following the finger from there. It says whether the library
// took it.
func (d *Display) libDragMove(x, y int) bool {
	d.mu.Lock()
	l := d.dashLibrary
	d.mu.Unlock()
	if l == nil || l.typing || d.r == nil {
		return false
	}
	d.r.zmu.Lock()
	most := max(d.r.libContentH-d.r.libViewH, 0)
	rowMax := slices.Clone(d.r.libRowMax)
	d.r.zmu.Unlock()
	dr := l.drag
	dx, dy := x-dr.at.X, y-dr.at.Y
	if dr.way == 0 && max(abs(dx), abs(dy)) < d.r.s(slideStart) {
		return true
	}
	d.editLibrary(func(n *librarySheet) {
		p := n.current()
		g := &n.drag
		if g.way == 0 {
			// Which way is decided once. It is measured from where the finger came down, as the
			// dashboard scrolls, so the page is where the finger says from the first move on.
			g.way = libDragDown
			if g.row >= 0 && abs(dx) > abs(dy) {
				g.way = libDragAlong
				g.base = libAlong(p, g.row)
			} else {
				g.base = min(p.scroll, most)
			}
		}
		g.scrolling = true
		if g.way == libDragAlong {
			end := 0
			if g.row < len(rowMax) {
				end = rowMax[g.row]
			}
			along := slices.Clone(p.along)
			for len(along) <= g.row {
				along = append(along, 0)
			}
			along[g.row] = min(max(g.base-(x-g.at.X), 0), end)
			p.along = along
			return
		}
		p.scroll = min(max(g.base-(y-g.at.Y), 0), most)
	})
	d.wake()
	return true
}

// libraryDragEnd is the finger lifting: it says whether it scrolled the library, which is then all it
// did.
func (d *Display) libraryDragEnd() bool {
	scrolled := false
	d.editLibrary(func(n *librarySheet) {
		scrolled = n.drag.scrolling
		n.drag = libDrag{row: -1}
	})
	if scrolled {
		d.wake()
	}
	return scrolled
}

// libAlong is how far along row sec of p is scrolled.
func libAlong(p *libPage, sec int) int {
	if sec < len(p.along) {
		return p.along[sec]
	}
	return 0
}

// libGeom is where the library's parts go on this panel: the page, the bar along its top, the view
// under it that scrolls, and the size of a cover in a grid, cols abreast, with the names under it.
type libGeom struct {
	card, bar, view              image.Rectangle
	pad, gap, inner, cols        int
	tile, tileH, hero, x0, x1    int
	albumRow, trackRow, headingH int
	noteH, fieldH, bottomSpacing int
}

// libTileAbout is about how wide a cover is, in the panel's own pixels: four abreast on the Show 5 and
// five on the Show 8, as a music app shows them.
const libTileAbout = 235

func (r *renderer) libGeometry() libGeom {
	var g libGeom
	g.card = r.dst.Rect.Inset(r.s(8))
	g.pad, g.gap = r.s(16), r.s(20)
	g.bar = image.Rect(g.card.Min.X+g.pad, g.card.Min.Y+g.pad, g.card.Max.X-g.pad, g.card.Min.Y+g.pad+r.s(46))
	g.view = image.Rect(g.card.Min.X, g.bar.Max.Y+r.s(10), g.card.Max.X, g.card.Max.Y-r.s(6))
	g.x0, g.x1 = g.card.Min.X+g.pad, g.card.Max.X-g.pad
	g.inner = g.x1 - g.x0
	g.cols = max(2, (g.inner+libTileAbout/2)/libTileAbout)
	g.tile = (g.inner - g.gap*(g.cols-1)) / g.cols
	g.tileH = g.tile + r.s(62)
	g.hero = min(r.h*2/5, g.inner/3)
	g.albumRow, g.trackRow, g.headingH = r.s(54), r.s(64), r.s(50)
	g.noteH, g.fieldH, g.bottomSpacing = r.s(34), r.s(66), r.s(24)
	return g
}

// libBlock is a strip of the page that scrolls: its height, and how it is drawn with its top at y.
type libBlock struct {
	h    int
	draw func(y int)
}

// libHues are the placeholders' colors, one for each item by its name, so a grid still loading looks
// like covers rather than a wall of gray.
var libHues = []color.RGBA{{0xE5, 0x73, 0x73, 255}, {0xF0, 0xA0, 0x50, 255}, {0xE8, 0xC5, 0x4A, 255},
	{0x7C, 0xC5, 0x76, 255}, {0x4D, 0xB6, 0xAC, 255}, {0x5C, 0x9D, 0xE0, 255}, {0x86, 0x79, 0xD9, 255},
	{0xC9, 0x6F, 0xC9, 255}}

func libHue(name string) color.RGBA {
	h := fnv.New32a()
	h.Write([]byte(name))
	return libHues[h.Sum32()%uint32(len(libHues))]
}

// libImageURL is the address of e's picture: its own, or for a track without one its album's.
func libImageURL(base string, e musicassistant.Entry) string {
	c := musicassistant.Client{URL: base}
	if u := c.ImageURL(e, 256); u != "" {
		return u
	}
	if e.Album != nil {
		return c.ImageURL(*e.Album, 256)
	}
	return ""
}

// libIcon is the icon drawn for an item without a picture of its own, or until its own has come.
func libIcon(kind string) string {
	switch kind {
	case "artist":
		return "account-music"
	case "album":
		return "album"
	case "playlist":
		return "playlist-music"
	case "radio":
		return "radio"
	}
	return "music-note"
}

// libDuration is a track's length as a music app writes it: 3:07, or 1:02:45.
func libDuration(seconds int) string {
	if seconds <= 0 {
		return ""
	}
	h, m, s := seconds/3600, seconds/60%60, seconds%60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}

// unscale is a size in this panel's pixels as the Show 5's, which mdiIcon and its kin take.
func (p *paint) unscale(px int) int {
	if p.sDen == 0 || p.sNum == p.sDen {
		return px
	}
	return px * p.sDen / p.sNum
}

// libCover draws e's picture in the square b, round for an artist: the picture once it has come, and
// until then a soft gradient in the item's own color with its icon. lit rings it in the accent color.
func (r *renderer) libCover(base string, e musicassistant.Entry, kind string, b image.Rectangle, lit bool, pal dashPal) {
	round := kind == "artist"
	rad := float64(max(b.Dx()/14, 2))
	if round {
		rad = float64(b.Dx()) / 2
	}
	if lit {
		ring := b.Inset(-r.s(5))
		rr := rad + float64(r.s(5))
		r.roundFill(ring, rr, pal.accent, pal.accent)
		inner := b.Inset(-r.s(2))
		r.roundFill(inner, rad+float64(r.s(2)), pal.card, pal.card)
	}
	if img := libPics.picture(libImageURL(base, e), b.Dx(), round); img != nil {
		r.drawLibPicture(img, b)
		return
	}
	hue := libHue(e.Name)
	top, bottom := lerp(pal.card, hue, 0.55), lerp(pal.card, hue, 0.22)
	r.roundFill(b, rad, top, bottom)
	r.mdiIconIn(libIcon(kind), b, max(r.unscale(b.Dx()*3/10), 14), lerp(bottom, pal.text, 0.6))
}

// librarySheet draws the library over the drawn dashboard and keeps where its parts are for
// libraryAt, and how far it scrolls for the finger.
func (r *renderer) librarySheet(v *libraryView, th dashboard.Theme) {
	if v == nil {
		r.setLibrary(nil, image.Rectangle{})
		r.setLibraryScroll(0, 0, 0, 0, nil)
		return
	}
	l := v.sheet
	whole := r.dst.Rect
	if l.typing {
		// The keyboard is the whole screen's, as the Wi-Fi passphrase's is; every tap on it is its.
		r.useFont()
		fillCanvas(r.dst, walnut)
		r.keyboard(libWord(libWordSearchTitle), l.text, false, l.shifted, l.symbols, libWord(libSearch))
		r.setLibrary(nil, whole)
		r.setLibraryScroll(0, 0, 0, 0, nil)
		return
	}
	pal := r.palette(th)
	fc := r.faces()
	g := r.libGeometry()
	washOver(r.dst, whole, color.RGBA{0, 0, 0, 150})
	r.roundFill(g.card, pal.rad, pal.card, pal.card)
	var zones []mediaZone
	// clip is what a pill's part is kept to: the page for the bar's, the view for those that scroll.
	clip := whole

	// pill draws a button as a pill, its icon before its word, and keeps it to be tapped unless off.
	pill := func(b image.Rectangle, icon, word string, fill, fg color.RGBA, off bool, kind, index int) {
		if off {
			fill, fg = lerp(pal.card, pal.text, 0.05), lerp(pal.card, pal.sub, 0.6)
		}
		r.roundFill(b, float64(b.Dy())/2, fill, fill)
		face := fc.navBold
		tw := r.width(face, word)
		iw := 0
		if icon != "" {
			iw = r.s(24)
			if word != "" {
				iw += r.s(8)
			}
		}
		start := b.Min.X + max((b.Dx()-iw-tw)/2, r.s(12))
		if icon != "" {
			r.mdiIcon(icon, start, b.Min.Y+(b.Dy()-r.s(24))/2, 24, fg)
		}
		r.text(face, r.fit(face, word, b.Max.X-start-iw-r.s(10)), start+iw, b.Min.Y+b.Dy()/2+r.s(8), fg)
		if z := b.Intersect(clip); !off && !z.Empty() {
			zones = append(zones, mediaZone{r: z, kind: kind, index: index})
		}
	}
	pillW := func(word string, icon bool) int {
		w := r.width(fc.navBold, word) + 2*r.s(20)
		if icon {
			w += r.s(32)
		}
		return max(w, r.s(84))
	}
	plain, lit := lerp(pal.card, pal.text, 0.1), pal.accent

	// The bar, which stays where it is: Done at the right, and the tabs, or Back on a page opened from
	// one.
	bar := g.bar
	doneW := pillW(libWord(libWordDone), false)
	pill(image.Rect(bar.Max.X-doneW, bar.Min.Y, bar.Max.X, bar.Max.Y), "", libWord(libWordDone), lit, pal.bg, false, libPartDone, 0)
	right := bar.Max.X - doneW - r.s(10)
	switch {
	case !l.set:
		r.text(fc.labelBold, r.fit(fc.labelBold, libWord(libWordLibrary), right-bar.Min.X), bar.Min.X, bar.Min.Y+r.s(33), pal.text)
	case len(l.stack) > 0:
		bw := pillW(libWord(libWordBack), true)
		pill(image.Rect(bar.Min.X, bar.Min.Y, bar.Min.X+bw, bar.Max.Y), "chevron-left", libWord(libWordBack), plain, pal.text, false, libPartBack, 0)
	default:
		// As wide as their words, or shared out evenly when they will not all fit so.
		tabGap := r.s(10)
		widths := make([]int, libTabs)
		sum := 0
		for i := range widths {
			widths[i] = pillW(libWord(i), false)
			if i == libSearch {
				widths[i] = r.s(64) // the magnifier alone, as music apps have it
			}
			sum += widths[i]
		}
		if room := right - bar.Min.X - tabGap*(libTabs-1); sum > room {
			for i := range widths[:libSearch] {
				widths[i] = (room - widths[libSearch]) / libSearch
			}
		}
		x := bar.Min.X
		for i, w := range widths {
			icon, word := "", libWord(i)
			if i == libSearch {
				icon, word = "magnify", ""
			}
			fill, fg := plain, pal.text
			if i == l.tab {
				fill, fg = lit, pal.bg
			}
			pill(image.Rect(x, bar.Min.Y, x+w, bar.Max.Y), icon, word, fill, fg, false, libPartTab, i)
			x += w + tabGap
		}
	}

	p := l.current()
	clip = g.view
	blocks, rowMax := r.libBlocks(&l, p, g, pal, fc, pill, &zones)
	contentH := 0
	for _, b := range blocks {
		contentH += b.h
	}
	viewH := g.view.Dy()
	scroll := min(max(p.scroll, 0), max(contentH-viewH, 0))
	// What scrolls is drawn clipped to the view under the bar: every drawing here keeps to r.dst.
	full := r.dst
	r.dst = full.SubImage(g.view).(*image.RGBA)
	y := g.view.Min.Y - scroll
	for _, b := range blocks {
		if y+b.h > g.view.Min.Y && y < g.view.Max.Y {
			b.draw(y)
		}
		y += b.h
	}
	r.dst = full
	// What scrolls, after its parts, so a tap on one finds the part.
	zones = append(zones, mediaZone{r: g.view, kind: libPartContent})
	visible := g.cols * (viewH/max(g.tileH, 1) + 1)
	r.setLibrary(zones, whole)
	r.setLibraryScroll(contentH, viewH, scroll, visible, rowMax)
}

// libBlocks lays page p out as strips that scroll: the notes over it, the search's field, an opened
// page's header, its sections, or what stands in for them. rowMax is how far each section shown as a
// row can scroll along. Parts are added to zones as they are drawn, kept to the view.
func (r *renderer) libBlocks(l *librarySheet, p *libPage, g libGeom, pal dashPal, fc sheetFaces,
	pill func(image.Rectangle, string, string, color.RGBA, color.RGBA, bool, int, int), zones *[]mediaZone) ([]libBlock, []int) {
	var blocks []libBlock
	add := func(h int, draw func(y int)) { blocks = append(blocks, libBlock{h: h, draw: draw}) }
	zone := func(b image.Rectangle, kind, index int) {
		if b = b.Intersect(g.view); !b.Empty() {
			*zones = append(*zones, mediaZone{r: b, kind: kind, index: index})
		}
	}
	note := func(text string) {
		add(g.noteH, func(y int) { r.text(fc.sub, r.fit(fc.sub, text, g.inner), g.x0, y+r.s(22), pal.sub) })
	}
	rowMax := make([]int, len(p.sections))
	if !l.set {
		add(g.view.Dy(), func(y int) {
			ty := y + r.s(40)
			for _, line := range r.wrapLines(fc.label, libWord(libWordSetUp), g.inner) {
				r.text(fc.label, line, g.x0, ty, pal.sub)
				ty += r.s(38)
			}
		})
		return blocks, rowMax
	}
	if !l.finding && l.target == "" {
		note(libWord(libWordNoPlayer))
	}
	if len(p.sections) > 0 && p.sections[0].derived {
		note(libWord(libWordFromTracks))
	}
	if len(l.stack) == 0 && l.tab == libSearch {
		add(g.fieldH, func(y int) {
			field := image.Rect(g.x0, y, g.x1, y+r.s(52))
			fill := lerp(pal.card, pal.text, 0.08)
			r.roundFill(field, float64(field.Dy())/2, fill, fill)
			r.mdiIcon("magnify", field.Min.X+r.s(18), field.Min.Y+(field.Dy()-r.s(26))/2, 26, pal.sub)
			shown, c := p.query, pal.text
			if shown == "" {
				shown, c = libWord(libWordTapToSearch), pal.sub
			}
			r.text(fc.value, r.fit(fc.value, shown, field.Dx()-r.s(70)), field.Min.X+r.s(56), field.Min.Y+field.Dy()/2+r.s(9), c)
			zone(field, libPartField, 0)
		})
	}
	if len(l.stack) > 0 {
		r.libHero(l, p, g, pal, fc, pill, add, zone)
	}

	has := false
	for _, s := range p.sections {
		has = has || len(s.entries) > 0
	}
	if !has {
		used := 0
		for _, b := range blocks {
			used += b.h
		}
		r.libEmpty(l, p, g, pal, fc, pill, add, max(g.view.Dy()-used, r.s(240)))
		return blocks, rowMax
	}

	for si, s := range p.sections {
		if len(s.entries) == 0 {
			continue
		}
		if s.word >= 0 {
			word := libWord(s.word)
			add(g.headingH, func(y int) { r.text(fc.labelBold, word, g.x0, y+r.s(36), pal.text) })
		}
		switch s.show {
		case libShowGrid:
			for first := 0; first < len(s.entries); first += g.cols {
				add(g.tileH+r.s(10), func(y int) {
					for k := 0; k < g.cols && first+k < len(s.entries); k++ {
						r.libTile(l, s, si, first+k, g.x0+k*(g.tile+g.gap), y, g, pal, fc, zone)
					}
				})
			}
		case libShowRow:
			width := len(s.entries)*(g.tile+g.gap) - g.gap
			rowMax[si] = max(width-g.inner, 0)
			along := min(libAlong(p, si), rowMax[si])
			add(g.tileH+r.s(10), func(y int) {
				first := max((along-g.gap)/(g.tile+g.gap), 0)
				for i := first; i < len(s.entries); i++ {
					x := g.x0 + i*(g.tile+g.gap) - along
					if x > g.view.Max.X {
						break
					}
					r.libTile(l, s, si, i, x, y, g, pal, fc, zone)
				}
				zone(image.Rect(g.view.Min.X, y, g.view.Max.X, y+g.tileH), libPartRow, si)
			})
		default:
			numbered := p.what == libAlbum || p.what == libPlaylist
			h := g.trackRow
			if p.what == libAlbum {
				h = g.albumRow
			}
			for i := range s.entries {
				add(h, func(y int) { r.libTrack(l, p, s, si, i, y, h, numbered, g, pal, fc, zone) })
			}
		}
	}
	if p.problem != "" {
		note(p.problem) // the next part of a tab, or half of an artist, that would not come
	}
	add(g.bottomSpacing, func(int) {})
	return blocks, rowMax
}

// libTile draws item i of section s as a cover with its name, and a dimmer line under it, at x, y.
func (r *renderer) libTile(l *librarySheet, s libSection, si, i, x, y int, g libGeom, pal dashPal, fc sheetFaces,
	zone func(image.Rectangle, int, int)) {
	e := s.entries[i]
	kind := libEntryType(e, s.kind)
	index := si*libSecStride + i
	lit := l.isLit(index)
	pic := image.Rect(x, y+r.s(6), x+g.tile, y+r.s(6)+g.tile)
	r.libCover(l.base, e, kind, pic, lit, pal)
	name := dashboard.CleanName(e.Name)
	sub := ""
	switch kind {
	case "album", "track":
		sub = dashboard.CleanName(e.By())
	case "playlist":
		sub = dashboard.CleanName(e.Owner)
	}
	nameC := pal.text
	if lit {
		nameC = pal.accent
	}
	tw := g.tile - r.s(4)
	place := func(face font.Face, text string) int {
		if kind == "artist" {
			return x + (g.tile-r.width(face, text))/2 // under a round picture, in the middle
		}
		return x + r.s(2)
	}
	name = r.fit(fc.navBold, name, tw)
	r.text(fc.navBold, name, place(fc.navBold, name), pic.Max.Y+r.s(30), nameC)
	if sub != "" {
		sub = r.fit(fc.sub, sub, tw)
		r.text(fc.sub, sub, place(fc.sub, sub), pic.Max.Y+r.s(54), pal.sub)
	}
	zone(image.Rect(x, y, x+g.tile, y+g.tileH), libPartItem, index)
}

// libTrack draws track i of section s as a row: its number on an album or a playlist, else its
// cover; its name, and under it who it is by (and its album, off an album's page); its length at the
// end.
func (r *renderer) libTrack(l *librarySheet, p *libPage, s libSection, si, i, y, h int, numbered bool, g libGeom,
	pal dashPal, fc sheetFaces, zone func(image.Rectangle, int, int)) {
	e := s.entries[i]
	index := si*libSecStride + i
	row := image.Rect(g.x0, y, g.x1, y+h)
	lit := l.isLit(index)
	if lit {
		fill := lerp(pal.card, pal.accent, 0.3)
		r.roundFill(row, float64(r.s(12)), fill, fill)
	}
	tx := g.x0 + r.s(12)
	if numbered {
		n := e.TrackNumber
		if n <= 0 || p.what == libPlaylist {
			n = i + 1
		}
		num := strconv.Itoa(n)
		r.text(fc.value, num, tx+r.s(34)-r.width(fc.value, num), y+h/2+r.s(9), pal.sub)
		tx += r.s(56)
	} else {
		side := h - r.s(16)
		pic := image.Rect(tx, y+(h-side)/2, tx+side, y+(h-side)/2+side)
		r.libCover(l.base, e, "track", pic, false, pal)
		tx = pic.Max.X + r.s(16)
	}
	end := g.x1 - r.s(12)
	if d := libDuration(e.Duration); d != "" {
		r.text(fc.value, d, end-r.width(fc.value, d), y+h/2+r.s(9), pal.sub)
		end -= r.width(fc.value, d) + r.s(20)
	}
	sub := ""
	if p.what != libAlbum {
		sub = dashboard.CleanName(e.By())
		if e.Album != nil && e.Album.Name != "" && p.what != libPlaylist {
			if sub != "" {
				sub += " · "
			}
			sub += dashboard.CleanName(e.Album.Name)
		}
	}
	nameC := pal.text
	if lit {
		nameC = pal.accent
	}
	name := r.fit(fc.value, dashboard.CleanName(e.Name), end-tx)
	if sub == "" {
		r.text(fc.value, name, tx, y+h/2+r.s(9), nameC)
	} else {
		r.text(fc.value, name, tx, y+h/2-r.s(4), nameC)
		r.text(fc.sub, r.fit(fc.sub, sub, end-tx), tx, y+h/2+r.s(21), pal.sub)
	}
	if !lit && i < len(s.entries)-1 {
		line := lerp(pal.card, pal.text, 0.07)
		r.fillRect(image.Rect(tx, row.Max.Y-1, g.x1, row.Max.Y), line)
	}
	zone(row, libPartItem, index)
}

// libHero is an opened page's header: its cover (round for an artist) at the left, and beside it its
// name, who it is by and when, and Play and Shuffle as large pills.
func (r *renderer) libHero(l *librarySheet, p *libPage, g libGeom, pal dashPal, fc sheetFaces,
	pill func(image.Rectangle, string, string, color.RGBA, color.RGBA, bool, int, int),
	add func(int, func(int)), zone func(image.Rectangle, int, int)) {
	kind := libPageType(p.what)
	add(g.hero+r.s(30), func(y int) {
		cover := image.Rect(g.x0, y+r.s(8), g.x0+g.hero, y+r.s(8)+g.hero)
		r.libCover(l.base, p.entry, kind, cover, false, pal)
		tx := cover.Max.X + r.s(30)
		tw := g.x1 - tx
		ty := cover.Min.Y + r.s(40)
		lines := r.wrapLines(fc.header, dashboard.CleanName(p.entry.Name), tw)
		if len(lines) > 2 {
			lines = append(lines[:1], r.fit(fc.header, lines[1]+"…", tw))
		}
		for _, line := range lines {
			r.text(fc.header, line, tx, ty, pal.text)
			ty += r.s(44)
		}
		var about string
		switch kind {
		case "album":
			about = dashboard.CleanName(p.entry.By())
			if p.entry.Year > 0 {
				if about != "" {
					about += " · "
				}
				about += strconv.Itoa(p.entry.Year)
			}
		case "playlist":
			about = dashboard.CleanName(p.entry.Owner)
		}
		if about != "" {
			r.text(fc.value, r.fit(fc.value, about, tw), tx, ty-r.s(6), pal.sub)
		}
		// The pills along the cover's foot; with nothing to play on they are there, and dim.
		off := l.target == ""
		ph := r.s(54)
		py := cover.Max.Y - ph
		pw := max(r.width(fc.navBold, libWord(libWordPlay))+r.s(80), r.s(150))
		sw := max(r.width(fc.navBold, libWord(libWordShuffle))+r.s(80), r.s(170))
		playFill, playFg := pal.accent, pal.bg
		if l.isLit(libLitPlay) {
			playFill = lerp(pal.accent, pal.text, 0.4) // pressed
		}
		shufFill, shufFg := lerp(pal.card, pal.text, 0.12), pal.text
		if l.isLit(libLitShuffle) {
			shufFill, shufFg = pal.accent, pal.bg
		}
		pb := image.Rect(tx, py, tx+pw, py+ph)
		sb := image.Rect(pb.Max.X+r.s(14), py, pb.Max.X+r.s(14)+sw, py+ph)
		pill(pb, "play", libWord(libWordPlay), playFill, playFg, off, libPartPlay, 0)
		pill(sb, "shuffle-variant", libWord(libWordShuffle), shufFill, shufFg, off, libPartShuffle, 0)
	})
}

// libEmpty is what a page with nothing to list shows instead, in the middle of what is left of the
// view: that it is loading, what went wrong, or that there is nothing yet and where to find some, with
// a way to the search.
func (r *renderer) libEmpty(l *librarySheet, p *libPage, g libGeom, pal dashPal, fc sheetFaces,
	pill func(image.Rectangle, string, string, color.RGBA, color.RGBA, bool, int, int), add func(int, func(int)), h int) {
	icon, msg, hint, button := "", "", "", ""
	switch {
	case p.loading || (!p.loaded && p.what != libSearch):
		msg = libWord(libWordLoading)
	case p.problem != "":
		icon, msg = "alert-circle-outline", p.problem
	case p.what == libSearch && p.query == "":
		icon, msg = "magnify", libWord(libWordSearchTitle)
	case p.what == libSearch:
		icon, msg = "magnify", libWord(libWordNoResults)
	case p.what == libRadio:
		icon, msg, button = "radio", libWord(libWordNoStations), libWord(libWordSearchStations)
	case p.what < libTabs:
		icon, msg, hint, button = libIcon(libTypes[p.what]), libWord(libWordEmpty), libWord(libWordEmptyHint), libWord(libSearch)
	default:
		icon, msg = libIcon(libPageType(p.what)), libWord(libWordEmpty)
	}
	add(h, func(y int) {
		mid := g.x0 + g.inner/2
		cy := y + r.s(28)
		if icon != "" {
			side := r.s(64)
			r.mdiIconIn(icon, image.Rect(mid-side/2, cy, mid+side/2, cy+side), 56, lerp(pal.card, pal.sub, 0.7))
			cy += side + r.s(16)
		}
		for _, line := range r.wrapLines(fc.label, msg, g.inner*4/5) {
			cy += r.s(32)
			r.text(fc.label, line, mid-r.width(fc.label, line)/2, cy, pal.text)
		}
		for _, line := range r.wrapLines(fc.sub, hint, g.inner*4/5) {
			cy += r.s(28)
			r.text(fc.sub, line, mid-r.width(fc.sub, line)/2, cy, pal.sub)
		}
		if button != "" {
			cy += r.s(24)
			w := r.width(fc.navBold, button) + r.s(32) + 2*r.s(24)
			pill(image.Rect(mid-w/2, cy, mid+w/2, cy+r.s(50)), "magnify", button, pal.accent, pal.bg, false, libPartGoSearch, 0)
		}
	})
}
