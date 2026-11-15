//go:build !dot

// Package cjkfont fetches a font the screen needs when none of the screen's fonts draw what it shows:
// Japanese (#270) and Hebrew. The fonts on offer draw Latin only, and a character none of them has is
// drawn from the device's own fonts folder (lib/fonts, own.go). So when the screen is set to Japanese,
// or the first time it draws Hebrew, and no font somebody put in that folder draws it already, the
// daemon downloads a Noto Sans cut down to that script (tools/fonts/make_cjk_font.py, about 0.6 MB;
// tools/fonts/make_hebrew_font.py, about 10 KB) into the folder, checked against the size and SHA-256
// it is built with. No image carries either.
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

// entry is one font fetched into the fonts folder.
type entry struct {
	what    string        // what it draws, for the log
	archive update.Binary // the archive its build script writes, published with a release
	member  string        // the font's file in the archive; OFL.txt is its license
	file    string        // its file in the fonts folder: last in name order, so a font of the owner's own is tried first
	license string        // its license's file beside the others'
	probe   rune          // a character it is for, which the screen's own fonts do not draw
	needed  func() bool   // whether the screen needs it now
}

// archive is the Japanese font as tools/fonts/make_cjk_font.py builds it, published with the release
// that first carried it: NotoSansJP-Regular.ttf and its license, OFL.txt.
var archive = update.Binary{
	URL:    "https://github.com/HuskerMinion/techo5/releases/download/v1.6.8/techo5-noto-sans-jp.tar.gz",
	SHA256: "85fac8fd07ca165316df85c75c251d68dd3f1519a94e127046be66b2934f4a42",
	Size:   632659,
}

// languages are the screen languages the Japanese font is fetched for.
var languages = map[string]bool{"ja": true}

// entries are the fonts on offer: Japanese for a screen set to Japanese, Hebrew once Hebrew is drawn.
var entries = []entry{
	{
		what: "Japanese", archive: archive, member: "NotoSansJP-Regular.ttf",
		file: "zz-noto-sans-jp.ttf", license: "noto-sans-jp-OFL.txt", probe: 'あ',
		needed: func() bool { return languages[config.Get().Screen.Language] },
	},
	{
		what: "Hebrew", archive: hebrewArchive, member: "NotoSansHebrew-Regular.ttf",
		file: "zz-noto-sans-hebrew.ttf", license: "noto-sans-hebrew-OFL.txt", probe: 'א',
		needed: fonts.SawHebrew,
	},
}

// checkEvery is how often the screen is looked at.
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

// Run looks at the screen every minute and fetches each font once it is wanted.
func (f *Feature) Run(ctx context.Context) error {
	for {
		for _, e := range entries {
			if !wanted(e) {
				continue
			}
			if err := fetch(ctx, e); err != nil {
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

// wanted is whether e is to be fetched: the screen needs it, it is not here yet, and no font of the
// owner's own draws its script already.
func wanted(e entry) bool {
	if !e.needed() {
		return false
	}
	if _, err := os.Stat(filepath.Join(fontsDir(), e.file)); err == nil {
		return false
	}
	fonts.Refresh()
	return !fonts.OwnDraws(e.probe, e.file)
}

// fetch downloads the archive, puts its font in the fonts folder and its license beside the others',
// and has the folder read again.
func fetch(ctx context.Context, e entry) error {
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return err
	}
	slog.Info("cjkfont: downloading the "+e.what+" font", "bytes", e.archive.Size)
	tgz := filepath.Join(workDir, "font.tar.gz")
	if err := download(ctx, e.archive, tgz, nil); err != nil {
		return fmt.Errorf("cjkfont: downloading: %w", err)
	}
	defer os.Remove(tgz)
	if err := unpack(tgz, e); err != nil {
		return fmt.Errorf("cjkfont: unpacking: %w", err)
	}
	fonts.Rescan()
	slog.Info("cjkfont: the "+e.what+" font is in the fonts folder", "file", e.file)
	return nil
}

// unpack writes the font and its license, each whole and then renamed into place.
func unpack(tgz string, e entry) error {
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
		case e.member:
			to = filepath.Join(fontsDir(), e.file)
		case "OFL.txt":
			to = filepath.Join(licenseDir, e.license)
		default:
			continue
		}
		if h.Typeflag != tar.TypeReg {
			return fmt.Errorf("%s is not a file", h.Name)
		}
		if err := writeWhole(to, tr); err != nil {
			return err
		}
		font = font || h.Name == e.member
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
