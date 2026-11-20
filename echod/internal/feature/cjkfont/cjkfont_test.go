//go:build !dot

package cjkfont

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/lib/fonts"
	"github.com/HuskerMinion/techo5/echod/internal/update"
)

func tgz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg})
		tw.Write([]byte(body))
	}
	tw.Close()
	gz.Close()
	return b.Bytes()
}

// The font is fetched for Japanese only, once, into the fonts folder with its license beside the others.
func TestTheFontIsFetchedForJapanese(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	dir := t.TempDir()
	wasFonts, wasDownload, wasLicense, wasWork := fontsDir, download, licenseDir, workDir
	t.Cleanup(func() { fontsDir, download, licenseDir, workDir = wasFonts, wasDownload, wasLicense, wasWork })
	fontsDir = func() string { return filepath.Join(dir, "fonts") }
	licenseDir, workDir = filepath.Join(dir, "licenses"), filepath.Join(dir, "work")
	fetched := 0
	download = func(_ context.Context, b update.Binary, to string, _ func(float32)) error {
		fetched++
		if b != archive {
			t.Errorf("fetched %+v", b)
		}
		return os.WriteFile(to, tgz(t, map[string]string{"NotoSansJP-Regular.ttf": "font", "OFL.txt": "OFL"}), 0o644)
	}

	if japanese.wanted() {
		t.Fatal("wanted in English")
	}
	if err := config.Set().Screen().Language("ja"); err != nil {
		t.Fatal(err)
	}
	if !japanese.wanted() {
		t.Fatal("not wanted in Japanese")
	}
	if err := japanese.fetch(context.Background()); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "fonts", fontName)); string(b) != "font" {
		t.Errorf("font %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "licenses", "noto-sans-jp-OFL.txt")); string(b) != "OFL" {
		t.Errorf("license %q", b)
	}
	if left, _ := os.ReadDir(workDir); len(left) != 0 {
		t.Errorf("left in the work folder: %v", left)
	}
	if japanese.wanted() {
		t.Error("wanted again once here")
	}
	if fetched != 1 {
		t.Errorf("fetched %d times", fetched)
	}
}

func TestAnArchiveWithNoFontIsRefused(t *testing.T) {
	dir := t.TempDir()
	wasFonts, wasLicense := fontsDir, licenseDir
	t.Cleanup(func() { fontsDir, licenseDir = wasFonts, wasLicense })
	fontsDir, licenseDir = func() string { return dir }, dir
	p := filepath.Join(dir, "a.tar.gz")
	os.WriteFile(p, tgz(t, map[string]string{"OFL.txt": "OFL"}), 0o644)
	if err := japanese.unpack(p); err == nil {
		t.Error("an archive with no font was taken")
	}
}

// The emoji font is fetched once the screen has had an emoji no font draws, and not before: into the
// fonts folder under its own name, with its license beside the others'.
func TestTheEmojiFontIsFetchedOnceAnEmojiWasMissed(t *testing.T) {
	dir := t.TempDir()
	wasFonts, wasDownload, wasLicense, wasWork := fontsDir, download, licenseDir, workDir
	t.Cleanup(func() { fontsDir, download, licenseDir, workDir = wasFonts, wasDownload, wasLicense, wasWork })
	fontsDir = func() string { return filepath.Join(dir, "fonts") }
	licenseDir, workDir = filepath.Join(dir, "licenses"), filepath.Join(dir, "work")
	download = func(_ context.Context, b update.Binary, to string, _ func(float32)) error {
		if b != emojiArchive {
			t.Errorf("fetched %+v", b)
		}
		return os.WriteFile(to, tgz(t, map[string]string{"NotoEmoji-Regular.ttf": "emoji", "OFL.txt": "OFL"}), 0o644)
	}
	missed := false
	ft := emojiFont
	ft.want = func() bool { return missed }

	if ft.wanted() {
		t.Fatal("wanted with no emoji missed")
	}
	missed = true
	if !ft.wanted() {
		t.Fatal("not wanted once an emoji was missed")
	}
	if err := ft.fetch(context.Background()); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "fonts", "zz-noto-emoji.ttf")); string(b) != "emoji" {
		t.Errorf("font %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "licenses", "noto-emoji-OFL.txt")); string(b) != "OFL" {
		t.Errorf("license %q", b)
	}
	if ft.wanted() {
		t.Error("wanted again once here")
	}
}

// The color emoji font is fetched once an emoji was missed, or on a screen that has the black-and-white
// one already: into the fonts folder under a name that comes before that one's, with its license
// beside the others'.
func TestTheColorEmojiFontIsFetchedWithTheOther(t *testing.T) {
	dir := t.TempDir()
	wasFonts, wasDownload, wasLicense, wasWork := fontsDir, download, licenseDir, workDir
	t.Cleanup(func() { fontsDir, download, licenseDir, workDir = wasFonts, wasDownload, wasLicense, wasWork })
	fontsDir = func() string { return filepath.Join(dir, "fonts") }
	licenseDir, workDir = filepath.Join(dir, "licenses"), filepath.Join(dir, "work")
	download = func(_ context.Context, b update.Binary, to string, _ func(float32)) error {
		if b != colorEmojiArchive {
			t.Errorf("fetched %+v", b)
		}
		return os.WriteFile(to, tgz(t, map[string]string{"FluentEmojiFlat.ttf": "color", "LICENSE": "MIT"}), 0o644)
	}
	if colorEmojiFont.wanted() {
		t.Fatal("wanted with no emoji missed and no emoji font here")
	}
	os.MkdirAll(fontsDir(), 0o755)
	os.WriteFile(filepath.Join(fontsDir(), emojiFont.file), []byte("emoji"), 0o644)
	if !colorEmojiFont.wanted() {
		t.Fatal("not wanted beside the black-and-white emoji font")
	}
	if err := colorEmojiFont.fetch(context.Background()); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "fonts", "zz-fluent-emoji.ttf")); string(b) != "color" {
		t.Errorf("font %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "licenses", "fluent-emoji-LICENSE.txt")); string(b) != "MIT" {
		t.Errorf("license %q", b)
	}
	if colorEmojiFont.wanted() {
		t.Error("wanted again once here")
	}
	if colorEmojiFont.file >= emojiFont.file {
		t.Errorf("%s is not tried before %s", colorEmojiFont.file, emojiFont.file)
	}
}

// The three fonts are looked at, the Japanese one first.
func TestTheFontsAreLookedAt(t *testing.T) {
	got := all()
	if len(got) != 3 || got[0].file != fontName || got[1].file != "zz-noto-emoji.ttf" || got[2].file != "zz-fluent-emoji.ttf" {
		t.Errorf("the fonts are %+v", got)
	}
}

// Each font fetched here is one lib/fonts knows is not the owner's, so none counts toward the owner's
// limit on fonts of their own.
func TestTheFetchedFontsAreNotCountedAsTheOwners(t *testing.T) {
	for _, ft := range all() {
		if !slices.Contains(fonts.Fetched, ft.file) {
			t.Errorf("%s is not in fonts.Fetched", ft.file)
		}
	}
	if len(fonts.Fetched) != len(all()) {
		t.Errorf("fonts.Fetched is %v", fonts.Fetched)
	}
}
