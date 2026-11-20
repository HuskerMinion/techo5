package fonts

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"

	"github.com/HuskerMinion/techo5/echod/internal/layout"
)

// Fonts of the owner's own (#122): font files put in the device's fonts folder, by hand or from the
// setup page, used only for characters neither the chosen font nor Go has a glyph for - Korean,
// Chinese and Japanese, say, which none of the fonts on offer draw. Never the font the screen's words
// are drawn in. Tried in the order of their file names.
//
// A font of that kind is big (5 to 20 MB), and the device has little memory, so a file is never read
// into memory whole: it is kept open and its tables are read from it as glyphs are drawn, so what it
// costs is the page cache the kernel can drop again, not the daemon's heap.

// Dir is the folder they are kept in, beside the sounds folder.
var Dir = filepath.Join(layout.StateDir, "fonts")

// Limits on what the folder takes from the setup page; a file put there by hand over these is still
// skipped, so a full folder cannot take the device's memory or its storage either.
const (
	MaxOwnFile  = 32 << 20 // one file
	MaxOwnTotal = 96 << 20 // all of them
	MaxOwnFiles = 8
)

// Fetched is the fonts feature/cjkfont downloads into the folder: the Japanese font and the two emoji
// fonts. They are not the owner's, so they do not count toward the limits above, which are on what the
// owner puts there: an owner with eight fonts of their own still has emoji. They are still tried in the
// order of their names among the others, the color emoji font before the black-and-white one.
var Fetched = []string{"zz-noto-sans-jp.ttf", "zz-noto-emoji.ttf", "zz-fluent-emoji.ttf"}

// fetched is whether the file called name is one of Fetched.
func fetched(name string) bool { return slices.Contains(Fetched, name) }

// OwnFont is one of them as the setup page lists it: its file, its size, and the name the font gives
// itself, or why it is not used.
type OwnFont struct {
	File    string
	Size    int64
	Name    string
	Problem string
}

// ownEntry is one in use: its file, as it was when opened, kept open, and the font read from it.
type ownEntry struct {
	file string
	size int64
	mod  time.Time
	fh   *os.File
	font *sfnt.Font
	// color is its color glyphs, nil for a font with none (color.go).
	color *colorFont
}

func (e ownEntry) sig() fileSig { return fileSig{e.file, e.size, e.mod} }

// fileSig is a font file in the folder as a look at it finds it: its name, size and time.
type fileSig struct {
	name string
	size int64
	mod  time.Time
}

func (a fileSig) same(b fileSig) bool {
	return a.name == b.name && a.size == b.size && a.mod.Equal(b.mod)
}

// own is the fonts in use and the folder as last read. mu guards it and is only ever held for a
// moment, since the drawing loop takes it to make its faces: opening and checking the files is done
// outside it, one load at a time under loading, and only the result is swapped in under mu.
var own struct {
	mu      sync.Mutex
	listed  []OwnFont
	inUse   []ownEntry
	seen    []fileSig          // the folder's font files when last read
	bad     map[string]fileSig // files that went wrong while drawing (markBad), as they were then
	checked time.Time          // when the folder was last looked at
}

// loading is held through a load, so two never run at once.
var loading sync.Mutex

// cleaned is whether what uploads cut off by a power cut left behind has been cleared. Under loading.
var cleaned bool

// gen counts the changes to the fonts in use, so a screen can tell when to make its faces again.
var gen atomic.Uint64

// Generation is a number that changes whenever the fonts of the owner's own do.
func Generation() uint64 { return gen.Load() }

// refreshEvery is how often Refresh looks at the folder for a change made by hand.
const refreshEvery = 10 * time.Second

// Refresh reads the folder again if it has changed since it was last read (a file added, removed,
// replaced, or still being copied in over SSH: each file's size and time are compared, not only the
// folder's), looking at most every refreshEvery. Cheap enough to call on every frame, and never waits
// for a load another goroutine is doing.
func Refresh() { refresh(false) }

// refresh is Refresh, waiting for a load under way when wait is set.
func refresh(wait bool) {
	own.mu.Lock()
	now := time.Now()
	due := own.listed == nil || now.Sub(own.checked) >= refreshEvery
	if due {
		own.checked = now
	}
	own.mu.Unlock()
	if !due {
		return
	}
	if wait {
		loading.Lock()
	} else if !loading.TryLock() {
		return // another load is under way, and swaps in what it finds
	}
	defer loading.Unlock()
	files := scan()
	own.mu.Lock()
	same := own.listed != nil && slices.EqualFunc(files, own.seen, fileSig.same)
	own.mu.Unlock()
	if !same {
		load(files)
	}
}

// Rescan reads the folder again now, checking again any file that was not used.
func Rescan() {
	loading.Lock()
	defer loading.Unlock()
	own.mu.Lock()
	own.checked = time.Now()
	own.mu.Unlock()
	load(scan())
}

// Own is the folder's font files, in the order they are tried, each with its name or what is wrong
// with it.
func Own() []OwnFont {
	refresh(true)
	own.mu.Lock()
	defer own.mu.Unlock()
	return slices.Clone(own.listed)
}

// ownFonts is the fonts in use, in order. Called under no lock.
func ownFonts() []ownEntry {
	own.mu.Lock()
	defer own.mu.Unlock()
	return slices.Clone(own.inUse)
}

// OwnDraws is whether a font in the folder other than the files named in skip has a glyph for r: one
// somebody put there for a script that ships with no font (feature/cjkfont).
func OwnDraws(r rune, skip ...string) bool {
	var buf sfnt.Buffer
	for _, e := range ownFonts() {
		if slices.Contains(skip, e.file) {
			continue
		}
		if i, err := glyphIndex(e.font, &buf, r); err == nil && i != 0 {
			return true
		}
	}
	return false
}

// fontFile is whether name is a font file by its name.
func fontFile(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".ttf", ".otf", ".ttc":
		return !strings.HasPrefix(name, ".")
	}
	return false
}

// uploadPrefix starts the name of a file the setup page is still receiving (AddOwn).
const uploadPrefix = ".upload-"

// scan is the folder's font files, in name order, as they are now. Called under loading.
func scan() []fileSig {
	entries, err := os.ReadDir(Dir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		slog.Warn("fonts: the fonts folder could not be read", "dir", Dir, "err", err)
	}
	if !cleaned {
		// The first look since the daemon started: an upload a power cut stopped is left behind under
		// its temporary name, up to MaxOwnFile of it. None can be under way yet (AddOwn looks first).
		cleaned = true
		removeUploads(entries)
	}
	var out []fileSig
	for _, e := range entries {
		if e.IsDir() || !fontFile(e.Name()) {
			continue
		}
		f := fileSig{name: e.Name()}
		if info, err := e.Info(); err == nil {
			f.size, f.mod = info.Size(), info.ModTime()
		}
		out = append(out, f)
	}
	return out
}

// removeUploads deletes the uploads' temporary files among entries, the folder's.
func removeUploads(entries []os.DirEntry) {
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), uploadPrefix) {
			os.Remove(filepath.Join(Dir, e.Name()))
		}
	}
}

// load reads the folder's font files (files, from scan): each in name order, opened and checked,
// within the limits. A file in use already and unchanged since (the same file, size and time) is kept
// as it is rather than opened and checked again. The files of the fonts no longer in use are left to
// be closed when nothing draws with them any more (an os.File closes itself once unreachable), since
// a face made a moment ago may still be drawing a frame. Called under loading, not own.mu, which the
// drawing loop waits on: only the result is swapped in under it.
func load(files []fileSig) {
	prev := ownFonts()
	listed := []OwnFont{}
	var inUse []ownEntry
	var total int64 // of the owner's fonts in use, not the fetched ones
	owners := 0     // the owner's fonts in use
	for _, sig := range files {
		o := OwnFont{File: sig.name, Size: sig.size}
		mine := !fetched(sig.name)
		switch {
		case mine && owners >= MaxOwnFiles:
			o.Problem = fmt.Sprintf("only the first %d fonts are used", MaxOwnFiles)
		case o.Size > MaxOwnFile:
			o.Problem = fmt.Sprintf("bigger than %d MB", MaxOwnFile>>20)
		case mine && total+o.Size > MaxOwnTotal:
			o.Problem = fmt.Sprintf("the fonts together would be over %d MB", MaxOwnTotal>>20)
		default:
			path := filepath.Join(Dir, sig.name)
			if i := slices.IndexFunc(prev, func(e ownEntry) bool { return e.sig().same(sig) && sameFile(path, e.fh) }); i >= 0 {
				o.Name = safeFontName(prev[i].font)
				if mine {
					total += o.Size
					owners++
				}
				inUse = append(inUse, prev[i])
				break
			}
			fh, f, name, err := openFont(path)
			if err != nil {
				o.Problem = err.Error()
				break
			}
			o.Name = name
			if mine {
				total += o.Size
				owners++
			}
			c, err := readColor(fh)
			if err != nil {
				// Its glyphs are still drawn, each as its own outline, in the text's color.
				slog.Warn("fonts: a font of your own's colors cannot be read", "file", o.File, "err", err)
			}
			inUse = append(inUse, ownEntry{file: sig.name, size: sig.size, mod: sig.mod, fh: fh, font: f, color: c})
		}
		if o.Problem != "" {
			slog.Warn("fonts: a font of your own is not used", "file", o.File, "why", o.Problem)
		}
		listed = append(listed, o)
	}

	own.mu.Lock()
	defer own.mu.Unlock()
	listed, inUse = dropBad(listed, inUse)
	changed := len(inUse) != len(own.inUse)
	for i := 0; !changed && i < len(inUse); i++ {
		changed = inUse[i].font != own.inUse[i].font
	}
	own.listed, own.inUse, own.seen = listed, inUse, files
	if changed {
		gen.Add(1)
		slog.Info("fonts: fonts of your own", "in use", len(inUse))
	}
}

// sameFile is whether path is still the file fh has open, not another put in its place.
func sameFile(path string, fh *os.File) bool {
	a, err := os.Stat(path)
	if err != nil {
		return false
	}
	b, err := fh.Stat()
	return err == nil && os.SameFile(a, b)
}

// brokeProblem is what the setup page says of a font that went wrong while drawing (markBad).
const brokeProblem = "it went wrong while drawing, so it is not used; put it in again to try it again"

// dropBad takes out of inUse the fonts that went wrong while drawing and have not changed since,
// and says so in listed. A file gone from the folder is forgotten, so put back it is tried again.
// Called under own.mu.
func dropBad(listed []OwnFont, inUse []ownEntry) ([]OwnFont, []ownEntry) {
	if len(own.bad) == 0 {
		return listed, inUse
	}
	for file := range own.bad {
		if !slices.ContainsFunc(listed, func(o OwnFont) bool { return o.File == file }) {
			delete(own.bad, file)
		}
	}
	inUse = slices.DeleteFunc(inUse, func(e ownEntry) bool {
		b, ok := own.bad[e.file]
		if ok && !b.same(e.sig()) {
			delete(own.bad, e.file) // changed since: tried again
			return false
		}
		return ok
	})
	for i := range listed {
		if _, ok := own.bad[listed[i].File]; ok {
			listed[i].Problem = brokeProblem
		}
	}
	return listed, inUse
}

// markBad stops using the font of one's own f after it panicked while a character was drawn from it
// (a file broken in a way the checks before use did not find), so it is not tried again on the next
// frame, until its file is changed or put in again. The list is kept in memory only: after a restart the
// font is tried again, and dropped again should it break again.
func markBad(f *sfnt.Font, why string) {
	own.mu.Lock()
	defer own.mu.Unlock()
	i := slices.IndexFunc(own.inUse, func(e ownEntry) bool { return e.font == f })
	if i < 0 {
		return // dropped already
	}
	e := own.inUse[i]
	slog.Error("fonts: a font of your own went wrong while drawing; it is not used", "file", e.file, "err", why)
	if own.bad == nil {
		own.bad = map[string]fileSig{}
	}
	own.bad[e.file] = e.sig()
	own.listed, own.inUse = dropBad(slices.Clone(own.listed), slices.Clone(own.inUse))
	gen.Add(1)
}

// openFont opens a font file and checks it is one the screen can draw from (readFont), with the name
// it gives itself. The file is kept open, as the font reads from it.
func openFont(path string) (*os.File, *sfnt.Font, string, error) {
	fh, err := os.Open(path)
	if err != nil {
		return nil, nil, "", errors.New("it could not be opened")
	}
	f, name, err := readFont(fh)
	if err != nil {
		fh.Close()
		return nil, nil, "", err
	}
	return fh, f, name, nil
}

// readFont reads a font and checks it is one the screen can draw from: it reads, has glyphs, and a
// glyph's outline can be read. A collection (.ttc) is its first font. A file broken in a way that
// makes the font reader panic is refused like any other, rather than taking the daemon down, and
// again at every start, as the folder is read at each.
func readFont(r io.ReaderAt) (f *sfnt.Font, name string, err error) {
	defer func() {
		if p := recover(); p != nil {
			slog.Warn("fonts: reading a font panicked", "err", p)
			f, name, err = nil, "", errors.New("it is not a font this device can read")
		}
	}()
	f, err = parseFont(r)
	if err == nil {
		err = checkFont(f)
	}
	if err != nil {
		return nil, "", err
	}
	return f, fontName(f), nil
}

// safeFontName is fontName, "" should reading the name panic.
func safeFontName(f *sfnt.Font) (name string) {
	defer func() {
		if recover() != nil {
			name = ""
		}
	}()
	return fontName(f)
}

func parseFont(r io.ReaderAt) (*sfnt.Font, error) {
	c, err := opentype.ParseCollectionReaderAt(r)
	if err != nil {
		return nil, errors.New("it is not a font this device can read")
	}
	if c.NumFonts() < 1 {
		return nil, errors.New("it has no font in it")
	}
	f, err := c.Font(0)
	if err != nil {
		return nil, errors.New("it is not a font this device can read")
	}
	return f, nil
}

// checkFont draws a few of a font's glyphs, to be sure its outlines are ones this device reads (some
// kinds of font read but cannot be drawn), before it is used.
func checkFont(f *sfnt.Font) error {
	n := f.NumGlyphs()
	if n < 2 {
		return errors.New("it has no glyphs")
	}
	var b sfnt.Buffer
	for _, g := range []int{1, n / 2, n - 1} {
		if _, err := f.LoadGlyph(&b, sfnt.GlyphIndex(g), fixed.I(32), nil); err != nil {
			return errors.New("its glyphs are not of a kind this device can draw")
		}
	}
	if _, err := opentype.NewFace(f, &opentype.FaceOptions{Size: 24, DPI: 72, Hinting: font.HintingFull}); err != nil {
		return errors.New("it is not a font this device can draw")
	}
	return nil
}

// fontName is the name a font gives itself, its full name or else its family's.
func fontName(f *sfnt.Font) string {
	var b sfnt.Buffer
	for _, id := range []sfnt.NameID{sfnt.NameIDFull, sfnt.NameIDFamily} {
		if s, err := f.Name(&b, id); err == nil && s != "" {
			return s
		}
	}
	return ""
}

// AddOwn keeps a font file sent from the setup page as name, if it is one: the name made safe, the
// file no bigger than the limits allow and readable as a font, else nothing is kept and the error
// says why. A file of the same name is replaced.
func AddOwn(name string, r io.Reader) error {
	name = filepath.Base(strings.TrimSpace(name))
	if !fontFile(name) {
		return errors.New("a font file's name ends in .ttf, .otf or .ttc")
	}
	if !safeName(name) {
		return errors.New("use letters, numbers, spaces, dots, dashes and underscores in the file's name")
	}
	// One upload at a time: two at once would each pass the count and size checks below.
	adding.Lock()
	defer adding.Unlock()
	if err := os.MkdirAll(Dir, 0o755); err != nil {
		return errors.New("the fonts folder could not be made")
	}
	// What an upload cut off by a power cut left behind; none is under way while this one is.
	if entries, err := os.ReadDir(Dir); err == nil {
		removeUploads(entries)
	}
	var total int64
	files := 0
	for _, o := range Own() {
		if o.File != name && o.Problem == "" && !fetched(o.File) {
			total += o.Size
			files++
		}
	}
	if files >= MaxOwnFiles {
		return fmt.Errorf("there are %d fonts already; remove one first", files)
	}
	tmp, err := os.CreateTemp(Dir, uploadPrefix+"*")
	if err != nil {
		return errors.New("the font could not be saved")
	}
	defer os.Remove(tmp.Name())
	n, err := io.Copy(tmp, io.LimitReader(r, MaxOwnFile+1))
	if err != nil {
		tmp.Close()
		return errors.New("the font did not arrive whole")
	}
	if n > MaxOwnFile {
		tmp.Close()
		return fmt.Errorf("a font can be at most %d MB", MaxOwnFile>>20)
	}
	if total+n > MaxOwnTotal {
		tmp.Close()
		return fmt.Errorf("the fonts together can be at most %d MB; remove one first", MaxOwnTotal>>20)
	}
	_, _, err = readFont(tmp)
	if cerr := tmp.Close(); err == nil && cerr != nil {
		err = errors.New("the font could not be saved")
	}
	if err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), filepath.Join(Dir, name)); err != nil {
		return errors.New("the font could not be saved")
	}
	slog.Info("fonts: a font of your own was added", "file", name, "bytes", n)
	Rescan()
	return nil
}

// RemoveOwn deletes one of the folder's font files, named as Own lists it.
func RemoveOwn(file string) error {
	if !slices.ContainsFunc(Own(), func(o OwnFont) bool { return o.File == file }) {
		return errors.New("there is no such font")
	}
	if err := os.Remove(filepath.Join(Dir, file)); err != nil {
		return errors.New("the font could not be removed")
	}
	slog.Info("fonts: a font of your own was removed", "file", file)
	Rescan()
	return nil
}

// adding is held through an upload (AddOwn).
var adding sync.Mutex

// safeName is a file name of plain characters only, so it can be shown and named in a form as it is.
func safeName(name string) bool {
	if name == "" || len(name) > 100 || strings.HasPrefix(name, ".") {
		return false
	}
	for _, c := range name {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', strings.ContainsRune(" ._-", c):
		default:
			return false
		}
	}
	return true
}
