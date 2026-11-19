package fonts

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"slices"
	"sort"
	"testing"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"
)

// withTables is the font file ttf with tables added (or put in place of its own of the same tag).
func withTables(t *testing.T, ttf []byte, add map[string][]byte) []byte {
	t.Helper()
	be := binary.BigEndian
	n := int(be.Uint16(ttf[4:]))
	tables := map[string][]byte{}
	for i := 0; i < n; i++ {
		rec := ttf[12+16*i:]
		off, length := be.Uint32(rec[8:]), be.Uint32(rec[12:])
		tables[string(rec[:4])] = ttf[off : off+length]
	}
	for tag, b := range add {
		tables[tag] = b
	}
	tags := make([]string, 0, len(tables))
	for tag := range tables {
		tags = append(tags, tag)
	}
	sort.Strings(tags)
	var out bytes.Buffer
	out.Write(ttf[:4])
	binary.Write(&out, be, uint16(len(tags)))
	out.Write(make([]byte, 6)) // the search hints, which nothing here reads
	at := 12 + 16*len(tags)
	var body bytes.Buffer
	for _, tag := range tags {
		b := tables[tag]
		out.WriteString(tag)
		binary.Write(&out, be, uint32(0))
		binary.Write(&out, be, uint32(at+body.Len()))
		binary.Write(&out, be, uint32(len(b)))
		body.Write(b)
		for body.Len()%4 != 0 {
			body.WriteByte(0)
		}
	}
	out.Write(body.Bytes())
	return out.Bytes()
}

// colrTable is a COLR version 0 table of bases, each a glyph and its layers (a glyph and a palette
// index each), in the order given.
func colrTable(bases []colorBase, layers []colorLayer) []byte {
	be := binary.BigEndian
	var b bytes.Buffer
	binary.Write(&b, be, uint16(0))
	binary.Write(&b, be, uint16(len(bases)))
	binary.Write(&b, be, uint32(14))
	binary.Write(&b, be, uint32(14+6*len(bases)))
	binary.Write(&b, be, uint16(len(layers)))
	for _, x := range bases {
		binary.Write(&b, be, []uint16{uint16(x.glyph), uint16(x.first), uint16(x.n)})
	}
	for _, l := range layers {
		binary.Write(&b, be, []uint16{uint16(l.glyph), l.color})
	}
	return b.Bytes()
}

// cpalTable is a CPAL table of one palette.
func cpalTable(colors ...color.NRGBA) []byte {
	be := binary.BigEndian
	var b bytes.Buffer
	binary.Write(&b, be, []uint16{0, uint16(len(colors)), 1, uint16(len(colors))})
	binary.Write(&b, be, uint32(14))
	binary.Write(&b, be, uint16(0))
	for _, c := range colors {
		b.Write([]byte{c.B, c.G, c.R, c.A})
	}
	return b.Bytes()
}

var red = color.NRGBA{255, 0, 0, 255}

// COLR's glyphs and CPAL's colors are read as the format lays them out: the bases put in glyph order,
// a base whose layers the table does not have left out, the colors stored blue first.
func TestColorTablesAreRead(t *testing.T) {
	colr := colrTable(
		[]colorBase{{glyph: 9, first: 1, n: 2}, {glyph: 4, first: 0, n: 1}, {glyph: 7, first: 2, n: 5}},
		[]colorLayer{{20, 0}, {21, 1}, {22, textColor}})
	c, err := parseColor(colr, cpalTable(red, color.NRGBA{1, 2, 3, 128}))
	if err != nil {
		t.Fatal(err)
	}
	if got := c.glyph(4); !slices.Equal(got, []colorLayer{{20, 0}}) {
		t.Errorf("glyph 4's layers are %v", got)
	}
	if got := c.glyph(9); !slices.Equal(got, []colorLayer{{21, 1}, {22, textColor}}) {
		t.Errorf("glyph 9's layers are %v", got)
	}
	if c.glyph(7) != nil || c.glyph(5) != nil {
		t.Error("a glyph with layers past the table's, or none, is a color glyph")
	}
	if !slices.Equal(c.palette, []color.NRGBA{red, {1, 2, 3, 128}}) {
		t.Errorf("the palette is %v", c.palette)
	}

	text := image.NewUniform(color.RGBA{0, 0, 200, 255})
	if c.ink(textColor, text) != text || c.ink(7, text) != text {
		t.Error("the text's color, or a color the palette does not have, is not the text's")
	}
	half := image.NewUniform(color.NRGBA{0, 0, 200, 128})
	if got := c.ink(0, half).(*image.Uniform).C.(color.NRGBA); got.R != 255 || got.A < 127 || got.A > 128 {
		t.Errorf("red for half-faded text is %v", got)
	}
}

// A broken table is refused rather than read past its end, and a font with no COLR has no color.
func TestBrokenColorTablesAreRefused(t *testing.T) {
	good := colrTable([]colorBase{{glyph: 1, first: 0, n: 1}}, []colorLayer{{2, 0}})
	for name, colr := range map[string][]byte{
		"short":      good[:10],
		"bases cut":  good[:16],
		"layers cut": good[:len(good)-1],
	} {
		if _, err := parseColor(colr, nil); err == nil {
			t.Errorf("%s: read", name)
		}
	}
	if _, err := parseColor(good, cpalTable(red)[:10]); err == nil {
		t.Error("a short palette was read")
	}
	// An offset past 2 GB is refused by the check itself, also where an int is 32 bits and the offset
	// would be negative as one (GOARCH=arm).
	far := slices.Clone(good)
	binary.BigEndian.PutUint32(far[4:], 0xFFFFFFF0)
	if _, err := parseColor(far, nil); err == nil {
		t.Error("a base offset past 2 GB was read")
	}
	far = slices.Clone(good)
	binary.BigEndian.PutUint32(far[8:], 0x80000000)
	if _, err := parseColor(far, nil); err == nil {
		t.Error("a layer offset past 2 GB was read")
	}
	pal := cpalTable(red)
	binary.BigEndian.PutUint32(pal[8:], 0xFFFFFFFC)
	if _, err := parsePalette(pal); err == nil {
		t.Error("a palette offset past 2 GB was read")
	}
	if c, err := readColor(bytes.NewReader(bundled(t, "opensans"))); c != nil || err != nil {
		t.Errorf("a font with no color tables: %v, %v", c, err)
	}
	if _, err := readColor(bytes.NewReader([]byte("ttcf"))); err == nil {
		t.Error("a file cut short was read")
	}
}

// colorFace is a screen face whose font of one's own is Open Sans with its dotless j made a color glyph:
// a red O, and a full stop in the text's color over it; and Open Sans's own face, for its O.
func colorFace(t *testing.T) (face, plain font.Face) {
	t.Helper()
	ttf := bundled(t, "opensans")
	f, err := sfnt.Parse(ttf)
	if err != nil {
		t.Fatal(err)
	}
	var buf sfnt.Buffer
	gid := func(r rune) sfnt.GlyphIndex {
		g, err := f.GlyphIndex(&buf, r)
		if err != nil || g == 0 {
			t.Fatalf("no glyph for %q", r)
		}
		return g
	}
	colr := colrTable([]colorBase{{glyph: gid(dotlessJ), first: 0, n: 2}},
		[]colorLayer{{gid('O'), 0}, {gid('.'), textColor}})
	write(t, ownDir(t), "color.ttf", withTables(t, ttf, map[string][]byte{"COLR": colr, "CPAL": cpalTable(red)}))
	Rescan()
	face, err = Face(Go, false, 40)
	if err != nil {
		t.Fatal(err)
	}
	if s, ok := face.(*Stack); !ok || !s.colored {
		t.Fatalf("the face is %T, not a stack with color", face)
	}
	plain, err = opentype.NewFace(f, &opentype.FaceOptions{Size: 40, DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		t.Fatal(err)
	}
	return face, plain
}

// count is how many of img's pixels are near c.
func count(img *image.RGBA, c color.RGBA) int {
	n := 0
	for i := 0; i < len(img.Pix); i += 4 {
		p := img.Pix[i : i+4]
		if absDiff(p[0], c.R) < 40 && absDiff(p[1], c.G) < 40 && absDiff(p[2], c.B) < 40 {
			n++
		}
	}
	return n
}

func absDiff(a, b uint8) uint8 {
	if a > b {
		return a - b
	}
	return b - a
}

func canvas() *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, 160, 80))
	draw.Draw(img, img.Bounds(), image.White, image.Point{}, draw.Src)
	return img
}

// A color glyph is drawn in its colors, its layers in order, and the text's color where a layer asks
// for it; letters beside it are drawn exactly as before.
func TestAColorGlyphIsDrawnInItsColors(t *testing.T) {
	face, plain := colorFace(t)
	blue := image.NewUniform(color.RGBA{0, 0, 255, 255})

	img := canvas()
	DrawString(img, blue, face, fixed.P(10, 60), string(dotlessJ))
	if n := count(img, color.RGBA{255, 0, 0, 255}); n < 100 {
		t.Errorf("%d red pixels: the O was not drawn in its color", n)
	}
	if n := count(img, color.RGBA{0, 0, 255, 255}); n < 10 {
		t.Errorf("%d blue pixels: the stop was not drawn in the text's color", n)
	}

	// The same advance and bounds as measured: the layers', not the glyph's own.
	b, adv := BoundString(face, string(dotlessJ))
	if adv != MeasureString(face, string(dotlessJ)) || adv <= 0 {
		t.Errorf("advance %v, measured %v", adv, MeasureString(face, string(dotlessJ)))
	}
	ob, _ := font.BoundString(plain, "O")
	if w, ow := (b.Max.X - b.Min.X).Ceil(), (ob.Max.X - ob.Min.X).Ceil(); w < ow-2 || w > ow+2 {
		t.Errorf("the color glyph is %d wide, its O %d", w, ow)
	}

	// Plain text in the same face is font.Drawer's, pixel for pixel.
	text := "Ok, 12:30 " + string(dotlessJ) + " a"
	got, want := canvas(), canvas()
	DrawString(got, blue, face, fixed.P(4, 50), "Ok, 12:30")
	(&font.Drawer{Dst: want, Src: blue, Face: face, Dot: fixed.P(4, 50)}).DrawString("Ok, 12:30")
	if !bytes.Equal(got.Pix, want.Pix) {
		t.Error("plain text was drawn otherwise than before")
	}
	end := DrawString(canvas(), blue, face, fixed.P(4, 50), text)
	if end.X != fixed.I(4)+MeasureString(face, text) {
		t.Errorf("the dot ends at %v, the text measures %v", end.X, MeasureString(face, text))
	}
}

// Drawn as one tint, a color glyph is in shades of the text's color only: through DrawStringTinted
// and through font.Drawer alike.
func TestAColorGlyphTintedIsOneColor(t *testing.T) {
	face, _ := colorFace(t)
	tint := image.NewUniform(color.RGBA{150, 14, 0, 255})
	for name, draw := range map[string]func(*image.RGBA){
		"DrawStringTinted": func(img *image.RGBA) { DrawStringTinted(img, tint, face, fixed.P(10, 60), string(dotlessJ)) },
		"font.Drawer": func(img *image.RGBA) {
			(&font.Drawer{Dst: img, Src: tint, Face: face, Dot: fixed.P(10, 60)}).DrawString(string(dotlessJ))
		},
	} {
		img := image.NewRGBA(image.Rect(0, 0, 160, 80)) // black
		draw(img)
		lit := 0
		for i := 0; i < len(img.Pix); i += 4 {
			r, g, b := int(img.Pix[i]), int(img.Pix[i+1]), int(img.Pix[i+2])
			if r == 0 && g == 0 && b == 0 {
				continue
			}
			lit++
			// The night clock's red, at some strength: green a tenth of red, no blue.
			if b != 0 || g*150 > r*14+150 {
				t.Fatalf("%s: a pixel of another color, %d,%d,%d", name, r, g, b)
			}
		}
		if lit < 100 {
			t.Errorf("%s: %d pixels lit", name, lit)
		}
	}
}

// Drawn into a mask (a halo's, say), a color glyph is all of its layers' shape: the O and the stop.
func TestAColorGlyphInAMaskIsItsShape(t *testing.T) {
	face, plain := colorFace(t)
	m := image.NewAlpha(image.Rect(0, 0, 160, 80))
	DrawString(m, image.Opaque, face, fixed.P(10, 60), string(dotlessJ))
	o := image.NewAlpha(m.Rect)
	DrawString(o, image.Opaque, plain, fixed.P(10, 60), "O")
	DrawString(o, image.Opaque, plain, fixed.P(10, 60), ".") // over it, where the layer puts it
	diff := 0
	for i := range m.Pix {
		if absDiff(m.Pix[i], o.Pix[i]) > 8 {
			diff++
		}
	}
	if diff > 20 {
		t.Errorf("the mask differs from its layers' in %d pixels", diff)
	}
}

// The color emoji font as tools/fonts/make_color_emoji_font.py builds it, from TECHO5_COLOR_EMOJI_FONT
// (its FluentEmojiFlat.ttf); skipped without one, as the font is fetched rather than kept here. With
// TECHO5_COLOR_EMOJI_PNG set, the line drawn is written there to be looked at.
func TestTheColorEmojiFont(t *testing.T) {
	path := os.Getenv("TECHO5_COLOR_EMOJI_FONT")
	if path == "" {
		t.Skip("TECHO5_COLOR_EMOJI_FONT is not set")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	write(t, ownDir(t), "zz-fluent-emoji.ttf", b)
	Rescan()
	if o := Own(); len(o) != 1 || o[0].Problem != "" {
		t.Fatalf("the font is listed as %+v", o)
	}
	face, err := Face(Go, false, 32)
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, 900, 120))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.RGBA{24, 24, 24, 255}), image.Point{}, draw.Src)
	white := image.NewUniform(color.White)
	DrawString(img, white, face, fixed.P(8, 44), "Party 🎉 😀 ❤ 👍🏽 🖤 🎱 ✅ 🇺🇸")
	DrawStringTinted(img, image.NewUniform(color.RGBA{150, 14, 0, 255}), face, fixed.P(8, 100), "Night 🎉 😀 ❤ 🖤 🎱")
	if name := os.Getenv("TECHO5_COLOR_EMOJI_PNG"); name != "" {
		f, err := os.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		png.Encode(f, img)
		f.Close()
	}
	for _, r := range "😀🎉❤🖤🎱✅" {
		if _, ok := face.(*Stack).colorOf(r); !ok {
			t.Errorf("%q is not a color glyph", r)
		}
	}
	// The black heart has its light edge.
	heart := image.NewRGBA(image.Rect(0, 0, 60, 60))
	DrawString(heart, white, face, fixed.P(8, 44), "🖤")
	if n := count(heart, color.RGBA{0xB0, 0xB0, 0xB0, 255}); n < 20 {
		t.Errorf("the black heart's edge is %d pixels", n)
	}
}

// A color glyph whose layers cover far more than a few squares of its size (a broken font's, its
// layers far apart) is drawn as its plain glyph, as text and as one tint, rather than having tone
// ask for a buffer of the layers' whole span. Here Open Sans is told its em is 128 units rather than
// 2048, so its O, a layer of the dotless j, is sixteen times too big.
func TestAColorGlyphTooBigIsItsPlainGlyph(t *testing.T) {
	ttf := bundled(t, "opensans")
	f, err := sfnt.Parse(ttf)
	if err != nil {
		t.Fatal(err)
	}
	var buf sfnt.Buffer
	gid := func(r rune) sfnt.GlyphIndex {
		g, err := f.GlyphIndex(&buf, r)
		if err != nil || g == 0 {
			t.Fatalf("no glyph for %q", r)
		}
		return g
	}
	head := findTable(t, ttf, "head")
	binary.BigEndian.PutUint16(head[18:], 128) // unitsPerEm
	colr := colrTable([]colorBase{{glyph: gid(dotlessJ), first: 0, n: 2}},
		[]colorLayer{{gid('O'), 0}, {gid('.'), textColor}})
	write(t, ownDir(t), "color.ttf", withTables(t, ttf, map[string][]byte{"head": head, "COLR": colr, "CPAL": cpalTable(red)}))
	Rescan()
	face, err := Face(Go, false, 40)
	if err != nil {
		t.Fatal(err)
	}
	s, ok := face.(*Stack)
	if !ok || !s.colored {
		t.Fatalf("the face is %T, not a stack with color", face)
	}
	if cg, ok := s.colorOf(dotlessJ); ok {
		t.Fatalf("a glyph covering %v at 40 pixels is a color glyph", cg.bounds())
	}
	// Still drawn, as its plain glyph: no red, and something in the text's color.
	img := image.NewRGBA(image.Rect(0, 0, 600, 600))
	blue := image.NewUniform(color.RGBA{0, 0, 255, 255})
	DrawString(img, blue, face, fixed.P(10, 500), string(dotlessJ))
	if n := count(img, color.RGBA{255, 0, 0, 255}); n != 0 {
		t.Errorf("%d red pixels: drawn in color", n)
	}
	if n := count(img, color.RGBA{0, 0, 255, 255}); n == 0 {
		t.Error("nothing drawn")
	}
	(&font.Drawer{Dst: img, Src: blue, Face: face, Dot: fixed.P(10, 500)}).DrawString(string(dotlessJ))
}

// findTable is a copy of the table tag of the font file ttf.
func findTable(t *testing.T, ttf []byte, tag string) []byte {
	t.Helper()
	be := binary.BigEndian
	for i := 0; i < int(be.Uint16(ttf[4:])); i++ {
		rec := ttf[12+16*i:]
		if string(rec[:4]) == tag {
			off, length := be.Uint32(rec[8:]), be.Uint32(rec[12:])
			return slices.Clone(ttf[off : off+length])
		}
	}
	t.Fatalf("no %s table", tag)
	return nil
}
