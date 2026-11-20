//go:build !dot

package cjkfont

import (
	"github.com/HuskerMinion/techo5/echod/internal/lib/fonts"
	"github.com/HuskerMinion/techo5/echod/internal/update"
)

// emojiArchive is the emoji font as tools/fonts/make_emoji_font.py builds it, published with the
// release that first carried it: NotoEmoji-Regular.ttf and its license, OFL.txt. Its pin is a file of
// its own, as tools/release.ps1 reads one pin per file.
var emojiArchive = update.Binary{
	URL:    "https://github.com/HuskerMinion/techo5/releases/download/v1.6.46/techo5-noto-emoji.tar.gz",
	SHA256: "e1880f0b981cdab279687ca70ea30c6ec49e13e60b0cae847b0c6a9bed6649db",
	Size:   529601,
}

// emojiFont is Noto Emoji, the black-and-white one, fetched the first time the screen has an emoji to
// draw that no font has (lib/fonts, emoji.go): a calendar event, a notification, a song title. A
// screen with no emoji to draw never fetches it.
var emojiFont = fetched{what: "the emoji font", archive: emojiArchive, ttf: "NotoEmoji-Regular.ttf",
	file: "zz-noto-emoji.ttf", notice: "OFL.txt", license: "noto-emoji-OFL.txt", probe: '😀', want: fonts.EmojiMissed}
