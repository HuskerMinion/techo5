//go:build !dot

// Package cjkfont fetches a font the screen needs when none of the screen's fonts draw it: Japanese
// (#270) and emoji (#639, emoji.go and coloremoji.go). The fonts on offer draw Latin only, and a character none of them
// has is drawn from the device's own fonts folder (lib/fonts, own.go). So when the screen is set to
// Japanese, and no font somebody put in that folder draws Japanese already, the daemon downloads a
// Noto Sans JP cut down to everyday Japanese (tools/fonts/make_cjk_font.py, about 0.6 MB) into the
// folder, checked against the size and SHA-256 it is built with; the emoji font comes the same way.
// No image carries either.
package cjkfont

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
	"github.com/HuskerMinion/techo5/echod/internal/lib/fonts"
	"github.com/HuskerMinion/techo5/echod/internal/service"
	"github.com/HuskerMinion/techo5/echod/internal/update"
)

func init() {
	component.Register(component.Device, &Feature{}, component.Order(62),
		component.Supervise(service.Restart(time.Minute, time.Hour)))
}

// archive is the font as tools/fonts/make_cjk_font.py builds it, published with the release that
// first carried it: NotoSansJP-Regular.ttf and its license, OFL.txt.
var archive = update.Binary{
	URL:    "https://github.com/HuskerMinion/techo5/releases/download/v1.6.8/techo5-noto-sans-jp.tar.gz",
	SHA256: "85fac8fd07ca165316df85c75c251d68dd3f1519a94e127046be66b2934f4a42",
	Size:   632659,
}

// fontName is the file in the fonts folder: last in name order, so a font of the owner's own is tried
// first.
const fontName = "zz-noto-sans-jp.ttf"

// probe is a character the font is for, which the screen's own fonts do not draw.
const probe = 'あ'

// languages are the screen languages it is fetched for.
var languages = map[string]bool{"ja": true}

// fetched is one font this fetches: from its archive, the font file and its license, each put where
// it goes, once want says it is wanted.
type fetched struct {
	what    string // for the log
	archive update.Binary
	ttf     string // the font in the archive
	file    string // its name in the fonts folder
	notice  string // its license in the archive
	license string // its license's name beside the others'
	probe   rune   // a character it draws, which the screen's own fonts do not
	want    func() bool
}

// japanese is the font for a Japanese screen.
var japanese = fetched{what: "the Japanese font", archive: archive, ttf: "NotoSansJP-Regular.ttf", file: fontName,
	notice: "OFL.txt", license: "noto-sans-jp-OFL.txt", probe: probe, want: func() bool { return languages[config.Get().Screen.Language] }}

// all are the fonts fetched, in the order they are looked at.
func all() []fetched { return []fetched{japanese, emojiFont, colorEmojiFont} }

// checkEvery is how often the screen language is looked at.
const checkEvery = time.Minute

var (
	licenseDir = filepath.Join(layout.StateDir, "licenses")
	workDir    = filepath.Join(layout.StateDir, "cjkfont")
	// fontsDir is lib/fonts' folder; download is update.Download. Variables, for tests.
	fontsDir = func() string { return fonts.Dir }
	download = update.Download
)

type Feature struct{}

func (*Feature) Name() string { return "cjkfont" }

// Run looks every minute at whether a font is wanted, and fetches each once it is.
func (f *Feature) Run(ctx context.Context) error {
	for {
		for _, ft := range all() {
			if !ft.wanted() {
				continue
			}
			if err := ft.fetch(ctx); err != nil {
				if ctx.Err() != nil {
					return nil
				}
				return err // the supervisor tries again later
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(checkEvery):
		}
	}
}

// wanted is whether the font is to be fetched: the screen wants it, it is not here yet, and no font of
// the owner's own draws what it is for already (the ones fetched here are not the owner's: the color
// emoji font and the black-and-white one are each fetched whichever of the two is here).
func (ft fetched) wanted() bool {
	if !ft.want() {
		return false
	}
	if _, err := os.Stat(filepath.Join(fontsDir(), ft.file)); err == nil {
		return false
	}
	fonts.Refresh()
	var ours []string
	for _, o := range all() {
		ours = append(ours, o.file)
	}
	return !fonts.OwnDraws(ft.probe, ours...)
}

// fetch downloads the archive, puts its font in the fonts folder and its license beside the others',
// and has the folder read again.
func (ft fetched) fetch(ctx context.Context) error {
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return err
	}
	slog.Info("cjkfont: downloading "+ft.what, "bytes", ft.archive.Size)
	tgz := filepath.Join(workDir, "font.tar.gz")
	if err := download(ctx, ft.archive, tgz, nil); err != nil {
		return fmt.Errorf("cjkfont: downloading: %w", err)
	}
	defer os.Remove(tgz)
	if err := ft.unpack(tgz); err != nil {
		return fmt.Errorf("cjkfont: unpacking: %w", err)
	}
	fonts.Rescan()
	slog.Info("cjkfont: "+ft.what+" is in the fonts folder", "file", ft.file)
	return nil
}

// unpack writes the font and its license, each whole and then renamed into place.
func (ft fetched) unpack(tgz string) error {
	f, err := os.Open(tgz)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	font := false
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		var to string
		switch h.Name {
		case ft.ttf:
			to = filepath.Join(fontsDir(), ft.file)
		case ft.notice:
			to = filepath.Join(licenseDir, ft.license)
		default:
			continue
		}
		if h.Typeflag != tar.TypeReg {
			return fmt.Errorf("%s is not a file", h.Name)
		}
		if err := writeWhole(to, tr); err != nil {
			return err
		}
		font = font || h.Name == ft.ttf
	}
	if !font {
		return errors.New("no font in it")
	}
	return nil
}

func writeWhole(to string, r io.Reader) error {
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return err
	}
	// Not a font file by its name until it is whole, so the fonts folder never reads half of one.
	tmp := to + ".part"
	w, err := os.Create(tmp)
	if err != nil {
		return err
	}
	_, err = io.Copy(w, r)
	if err == nil {
		err = w.Sync()
	}
	if cerr := w.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, to)
}
