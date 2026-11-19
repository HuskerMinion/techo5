//go:build !dot

package cjkfont

import (
	"os"
	"path/filepath"

	"github.com/HuskerMinion/techo5/echod/internal/lib/fonts"
	"github.com/HuskerMinion/techo5/echod/internal/update"
)

// colorEmojiArchive is the color emoji font as tools/fonts/make_color_emoji_font.py builds it,
// published with the release that first carried it: FluentEmojiFlat.ttf and its license, LICENSE. Its
// pin is a file of its own, as tools/release.ps1 reads one pin per file.
var colorEmojiArchive = update.Binary{
	URL:    "https://github.com/HuskerMinion/techo5/releases/download/v1.6.46/techo5-fluent-emoji.tar.gz",
	SHA256: "c9aef474417da31355de19c36c1dc85067674e81b994c677ce69d2086c43c0b8",
	Size:   496859,
}

// colorEmojiFont is Microsoft's Fluent Emoji, the Flat art in color, fetched when the black-and-white
// Noto Emoji is (emoji.go), and on a screen that has that one already. Its name puts it before Noto
// Emoji in the fonts folder, so an emoji is drawn from it in color, and from Noto Emoji only when
// Fluent has none (the letters a flag is made of, say).
var colorEmojiFont = fetched{what: "the color emoji font", archive: colorEmojiArchive, ttf: "FluentEmojiFlat.ttf",
	file: "zz-fluent-emoji.ttf", notice: "LICENSE", license: "fluent-emoji-LICENSE.txt", probe: '😀',
	want: func() bool { return fonts.EmojiMissed() || here(emojiFont) }}

// here is whether ft is in the fonts folder.
func here(ft fetched) bool {
	_, err := os.Stat(filepath.Join(fontsDir(), ft.file))
	return err == nil
}
