//go:build !dot

package cjkfont

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
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

	ja := entries[0]
	if wanted(ja) {
		t.Fatal("wanted in English")
	}
	if err := config.Set().Screen().Language("ja"); err != nil {
		t.Fatal(err)
	}
	if !wanted(ja) {
		t.Fatal("not wanted in Japanese")
	}
	if err := fetch(context.Background(), ja); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "fonts", ja.file)); string(b) != "font" {
		t.Errorf("font %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "licenses", "noto-sans-jp-OFL.txt")); string(b) != "OFL" {
		t.Errorf("license %q", b)
	}
	if left, _ := os.ReadDir(workDir); len(left) != 0 {
		t.Errorf("left in the work folder: %v", left)
	}
	if wanted(ja) {
		t.Error("wanted again once here")
	}
	if fetched != 1 {
		t.Errorf("fetched %d times", fetched)
	}
}

// The Hebrew font is fetched the first time Hebrew is drawn, whatever the screen language.
func TestTheFontIsFetchedWhenHebrewIsDrawn(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	dir := t.TempDir()
	wasFonts, wasDownload, wasLicense, wasWork := fontsDir, download, licenseDir, workDir
	t.Cleanup(func() { fontsDir, download, licenseDir, workDir = wasFonts, wasDownload, wasLicense, wasWork })
	fontsDir = func() string { return filepath.Join(dir, "fonts") }
	licenseDir, workDir = filepath.Join(dir, "licenses"), filepath.Join(dir, "work")
	download = func(_ context.Context, b update.Binary, to string, _ func(float32)) error {
		if b != hebrewArchive {
			t.Errorf("fetched %+v", b)
		}
		return os.WriteFile(to, tgz(t, map[string]string{"NotoSansHebrew-Regular.ttf": "font", "OFL.txt": "OFL"}), 0o644)
	}

	he := entries[1]
	if fonts.SawHebrew() {
		t.Skip("Hebrew already drawn in this test binary")
	}
	if wanted(he) {
		t.Fatal("wanted before any Hebrew is drawn")
	}
	fonts.VisualOrder("Kitchen")
	if wanted(he) {
		t.Fatal("wanted for English")
	}
	fonts.VisualOrder("מטבח")
	if !wanted(he) {
		t.Fatal("not wanted once Hebrew is drawn")
	}
	if err := fetch(context.Background(), he); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "fonts", he.file)); string(b) != "font" {
		t.Errorf("font %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "licenses", he.license)); string(b) != "OFL" {
		t.Errorf("license %q", b)
	}
	if wanted(he) {
		t.Error("wanted again once here")
	}
}

func TestAnArchiveWithNoFontIsRefused(t *testing.T) {
	dir := t.TempDir()
	wasFonts, wasLicense := fontsDir, licenseDir
	t.Cleanup(func() { fontsDir, licenseDir = wasFonts, wasLicense })
	fontsDir, licenseDir = func() string { return dir }, dir
	p := filepath.Join(dir, "a.tar.gz")
	os.WriteFile(p, tgz(t, map[string]string{"OFL.txt": "OFL"}), 0o644)
	if err := unpack(p, entries[0]); err == nil {
		t.Error("an archive with no font was taken")
	}
}
