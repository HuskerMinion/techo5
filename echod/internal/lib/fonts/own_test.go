package fonts

import (
	"bytes"
	"image"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

// ownDir points the fonts folder at a fresh one for a test, and back after.
func ownDir(t *testing.T) string {
	t.Helper()
	old := Dir
	Dir = filepath.Join(t.TempDir(), "fonts")
	Rescan()
	t.Cleanup(func() { Dir = old; Rescan() })
	return Dir
}

func bundled(t *testing.T, name string) []byte {
	t.Helper()
	b, err := files.ReadFile("files/" + name + "/Regular.ttf")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func write(t *testing.T, dir, name string, b []byte) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// dotlessJ is a character Go has no glyph for and Open Sans and Comic Neue both do: it can only come
// from a font of one's own while Go is the screen's font.
const dotlessJ = 'ȷ'

// A missing folder is no fonts, and Go stays Go's face alone.
func TestNoFolderNoFonts(t *testing.T) {
	ownDir(t)
	if len(Own()) != 0 {
		t.Error("fonts listed with no folder")
	}
	if f, _ := Face(Go, false, 30); f == nil {
		t.Fatal("no face")
	} else if !goAlone(f) {
		t.Errorf("Go with no fonts of one's own is a %T", f)
	}
}

// The folder's fonts are tried in file-name order, after the chosen font and Go, for what those lack;
// what is not a font file is passed over, and a file that is not a font is listed with why.
func TestOwnFontsInNameOrder(t *testing.T) {
	dir := ownDir(t)
	open, comic := bundled(t, "opensans"), bundled(t, "comicneue")
	write(t, dir, "b-comic.ttf", comic)
	write(t, dir, "a-open.ttf", open)
	write(t, dir, "notes.txt", []byte("not a font"))
	write(t, dir, "broken.otf", []byte("not a font either"))
	before := Generation()
	Rescan()
	if Generation() == before {
		t.Error("new fonts did not change the generation")
	}

	own := Own()
	if len(own) != 3 || own[0].File != "a-open.ttf" || own[1].File != "b-comic.ttf" || own[2].File != "broken.otf" {
		t.Fatalf("listed %+v", own)
	}
	if own[0].Name != "Open Sans Regular" || own[0].Problem != "" || own[2].Problem == "" {
		t.Errorf("listed %+v", own)
	}

	const size = 30
	advOf := func(b []byte) (w [2]int) {
		f, _ := opentype.Parse(b)
		fc, _ := opentype.NewFace(f, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull})
		a, _ := fc.GlyphAdvance(dotlessJ)
		b2, _, _ := fc.GlyphBounds(dotlessJ)
		return [2]int{a.Round(), b2.Max.X.Round() - b2.Min.X.Round()}
	}
	if advOf(open) == advOf(comic) {
		t.Fatal("Open Sans and Comic Neue draw the test character alike; pick another")
	}
	// Go has no such character, so it comes from the folder. A font on offer keeps the folder's fonts
	// after itself and Go too.
	if f, _ := Face("inter", false, size); f == nil || len(f.(*Stack).layers) != 4 {
		t.Error("Inter's face does not have the folder's two fonts after Inter and Go")
	}
	chosen := []string{Go}
	check := func(want []byte, what string) {
		t.Helper()
		for _, v := range chosen {
			face, err := Face(v, false, size)
			if err != nil {
				t.Fatal(err)
			}
			a, _ := face.GlyphAdvance(dotlessJ)
			b, _, _ := face.GlyphBounds(dotlessJ)
			if got := [2]int{a.Round(), b.Max.X.Round() - b.Min.X.Round()}; got != advOf(want) {
				t.Errorf("with %q chosen, %c was not drawn from %s", v, dotlessJ, what)
			}
			// The chosen font's own letters are still its own: Go's for Go.
			goFace, _ := Face(Go, false, size)
			if v == Go && adv(face, 'W') != adv(goFace, 'W') {
				t.Error("W was not Go's")
			}
		}
	}
	check(open, "the first file by name")

	// Renamed so that Comic Neue comes first, it is the one drawn from.
	if err := os.Rename(filepath.Join(dir, "b-comic.ttf"), filepath.Join(dir, "0-comic.ttf")); err != nil {
		t.Fatal(err)
	}
	Rescan()
	check(comic, "the new first file")
}

// The setup page's upload keeps a font under a safe name, replaces one of the same name, and refuses
// what is not a font, a name it cannot keep, and anything too big; nothing refused is left behind.
func TestAddAndRemoveOwn(t *testing.T) {
	dir := ownDir(t)
	if err := AddOwn("My Font.ttf", bytes.NewReader(bundled(t, "opensans"))); err != nil {
		t.Fatal(err)
	}
	if own := Own(); len(own) != 1 || own[0].File != "My Font.ttf" || own[0].Problem != "" {
		t.Fatalf("listed %+v", own)
	}
	gen := Generation()
	if err := AddOwn("My Font.ttf", bytes.NewReader(bundled(t, "comicneue"))); err != nil {
		t.Fatal(err)
	}
	if own := Own(); len(own) != 1 || own[0].Name != "Comic Neue Regular" || Generation() == gen {
		t.Errorf("replacing a font left %+v", own)
	}

	for _, bad := range []struct {
		name string
		body io.Reader
	}{
		{"junk.ttf", strings.NewReader("this is not a font")},
		{"font.exe", bytes.NewReader(bundled(t, "opensans"))},
		{"../escape.ttf", strings.NewReader("x")},
		{"héllo.ttf", bytes.NewReader(bundled(t, "opensans"))},
		{"huge.ttf", io.LimitReader(zeros{}, MaxOwnFile+10)},
	} {
		if err := AddOwn(bad.name, bad.body); err == nil {
			t.Errorf("%s was kept", bad.name)
		}
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("the folder holds %d files after the refusals", len(entries))
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "escape.ttf")); err == nil {
		t.Error("a name wrote outside the folder")
	}

	if err := RemoveOwn("no such.ttf"); err == nil {
		t.Error("removed a font that is not there")
	}
	if err := RemoveOwn("../fonts/My Font.ttf"); err == nil {
		t.Error("removed by a path")
	}
	if err := RemoveOwn("My Font.ttf"); err != nil {
		t.Fatal(err)
	}
	if len(Own()) != 0 {
		t.Error("the font is still listed")
	}
}

// Over the number of files the folder takes, the rest are listed but not used.
func TestOwnFontsLimit(t *testing.T) {
	dir := ownDir(t)
	b := bundled(t, "comicneue")
	for i := range MaxOwnFiles + 1 {
		write(t, dir, string(rune('a'+i))+".ttf", b)
	}
	Rescan()
	own := Own()
	if len(own) != MaxOwnFiles+1 || own[MaxOwnFiles].Problem == "" || own[0].Problem != "" {
		t.Errorf("listed %+v", own)
	}
	if err := AddOwn("z.ttf", bytes.NewReader(b)); err == nil {
		t.Error("a font past the limit was taken")
	}
}

// The fonts feature/cjkfont fetches do not count toward the owner's limits: with as many fonts of the
// owner's own as the folder takes, the emoji fonts are still used, the color one first, and there is
// still no room for another of the owner's.
func TestFetchedFontsAreNotCountedAsTheOwners(t *testing.T) {
	dir := ownDir(t)
	b := bundled(t, "comicneue")
	for i := range MaxOwnFiles {
		write(t, dir, string(rune('a'+i))+".ttf", b)
	}
	emoji := bundled(t, "opensans")
	write(t, dir, "zz-noto-emoji.ttf", emoji)
	write(t, dir, "zz-fluent-emoji.ttf", emoji)
	Rescan()
	for _, o := range Own() {
		if o.Problem != "" {
			t.Errorf("%s is not used: %s", o.File, o.Problem)
		}
	}
	var used []string
	for _, e := range ownFonts() {
		used = append(used, e.file)
	}
	if n := len(used); n != MaxOwnFiles+2 || used[n-2] != "zz-fluent-emoji.ttf" || used[n-1] != "zz-noto-emoji.ttf" {
		t.Errorf("used %v", used)
	}
	if err := AddOwn("y.ttf", bytes.NewReader(b)); err == nil {
		t.Error("a font past the owner's limit was taken")
	}
	// With one of the owner's taken out, there is room for one again.
	if err := os.Remove(filepath.Join(dir, "a.ttf")); err != nil {
		t.Fatal(err)
	}
	Rescan()
	if err := AddOwn("y.ttf", bytes.NewReader(b)); err != nil {
		t.Errorf("with room for one of the owner's: %v", err)
	}
}

type zeros struct{}

func (zeros) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

// A font file whose reading panics (broken in a way the font reader does not expect) is refused like
// any other bad file rather than taking the daemon down.
func TestReadFontPanicIsRefused(t *testing.T) {
	if _, _, err := readFont(panicReader{}); err == nil {
		t.Error("a font whose reading panicked was taken")
	}
}

type panicReader struct{}

func (panicReader) ReadAt([]byte, int64) (int, error) { panic("broken") }

// A font of one's own that panics while drawing is dropped from the face at once, the next layer
// drawing the character instead, and is no longer used or offered as working, until its file is
// changed.
func TestOwnFontThatBreaksIsDropped(t *testing.T) {
	dir := ownDir(t)
	write(t, dir, "a-open.ttf", bundled(t, "opensans"))
	Rescan()
	face, _ := Face(Go, false, 30)
	s := face.(*Stack)
	if len(s.layers) != 2 {
		t.Fatalf("%d layers", len(s.layers))
	}
	// The font of one's own breaks: its face panics from now on.
	s.layers[1].Face = panicFace{s.layers[1].Face}
	gen := Generation()
	s.GlyphAdvance(dotlessJ) // Go, next, has none, and says so
	if len(s.layers) != 1 {
		t.Errorf("the broken layer is still in the stack: %d layers", len(s.layers))
	}
	if Generation() == gen {
		t.Error("the generation did not change, so the screen keeps the broken font's faces")
	}
	if own := Own(); len(own) != 1 || own[0].Problem != brokeProblem {
		t.Errorf("listed %+v", own)
	}
	if f, _ := Face(Go, false, 30); func() bool { _, ok := f.(*Stack); return ok }() {
		t.Error("a new face still has the broken font")
	}
	// Read again unchanged, it stays out.
	Rescan()
	if len(ownFonts()) != 0 {
		t.Error("the broken font is back in use unchanged")
	}
	// Put in again (a new time), it is tried again.
	later := time.Now().Add(time.Minute)
	if err := os.Chtimes(filepath.Join(dir, "a-open.ttf"), later, later); err != nil {
		t.Fatal(err)
	}
	Rescan()
	if own := Own(); len(own) != 1 || own[0].Problem != "" || len(ownFonts()) != 1 {
		t.Errorf("put in again, listed %+v", own)
	}
}

type panicFace struct{ font.Face }

func (panicFace) Glyph(fixed.Point26_6, rune) (image.Rectangle, image.Image, image.Point, fixed.Int26_6, bool) {
	panic("broken glyph")
}
func (panicFace) GlyphBounds(rune) (fixed.Rectangle26_6, fixed.Int26_6, bool) { panic("broken glyph") }
func (panicFace) GlyphAdvance(rune) (fixed.Int26_6, bool)                     { panic("broken glyph") }

// A file being copied in over SSH is checked half-written, so not used; once whole it is checked
// again on the next look, though the folder itself has not changed. So is a file replaced in place.
func TestRefreshSeesAFileChangeInPlace(t *testing.T) {
	dir := ownDir(t)
	path := filepath.Join(dir, "a.ttf")
	open := bundled(t, "opensans")
	write(t, dir, "a.ttf", open[:len(open)/3])
	Rescan()
	if own := Own(); len(own) != 1 || own[0].Problem == "" {
		t.Fatalf("a half-copied font was taken: %+v", own)
	}
	again := func() {
		own.mu.Lock()
		own.checked = time.Time{}.Add(time.Hour) // long enough ago
		own.mu.Unlock()
		Refresh()
	}
	// Finished in place: the same file, written on to its end.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.Write(open[len(open)/3:])
	f.Close()
	again()
	if own := Own(); len(own) != 1 || own[0].Problem != "" || own[0].Name != "Open Sans Regular" {
		t.Errorf("the finished font was not taken: %+v", own)
	}
	// Overwritten in place by another font.
	if err := os.WriteFile(path, bundled(t, "comicneue"), 0o644); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Minute)
	os.Chtimes(path, later, later)
	again()
	if own := Own(); len(own) != 1 || own[0].Name != "Comic Neue Regular" {
		t.Errorf("the font replaced in place is still read as the old one: %+v", own)
	}
}

// Two uploads at once cannot both pass the count check; what an upload cut off by a power cut left
// behind is cleared, at the first look at the folder and before an upload.
func TestUploadsOneAtATimeAndLeftoversCleared(t *testing.T) {
	dir := ownDir(t)
	b := bundled(t, "comicneue")
	for i := range MaxOwnFiles - 1 {
		write(t, dir, string(rune('a'+i))+".ttf", b)
	}
	Rescan()
	errs := make(chan error, 2)
	for _, name := range []string{"x.ttf", "y.ttf"} {
		go func() { errs <- AddOwn(name, bytes.NewReader(b)) }()
	}
	ok := 0
	for range 2 {
		if <-errs == nil {
			ok++
		}
	}
	if ok != 1 {
		t.Errorf("%d uploads were taken with room for one", ok)
	}

	left := filepath.Join(dir, uploadPrefix+"123")
	write(t, dir, uploadPrefix+"123", []byte("half a font"))
	loading.Lock()
	cleaned = false // as at a start
	loading.Unlock()
	Rescan()
	if _, err := os.Stat(left); err == nil {
		t.Error("the first look did not clear a leftover upload")
	}
	write(t, dir, uploadPrefix+"456", []byte("half a font"))
	AddOwn("z.ttf", strings.NewReader("not a font"))
	if _, err := os.Stat(filepath.Join(dir, uploadPrefix+"456")); err == nil {
		t.Error("an upload did not clear a leftover upload")
	}
}

// The drawing loop never waits on a load: while one is under way, Refresh returns at once and the
// fonts in use can still be had.
func TestRefreshDoesNotWaitForALoad(t *testing.T) {
	ownDir(t)
	loading.Lock()
	done := make(chan struct{})
	go func() {
		own.mu.Lock()
		own.checked = time.Time{}.Add(time.Hour)
		own.mu.Unlock()
		Refresh()
		ownFonts()
		Face(Go, false, 20)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Error("Refresh waited for a load under way")
	}
	loading.Unlock()
	<-done
}
