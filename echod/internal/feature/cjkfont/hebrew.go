//go:build !dot

package cjkfont

import "github.com/HuskerMinion/techo5/echod/internal/update"

// hebrewArchive is the Hebrew font as tools/fonts/make_hebrew_font.py builds it, published with the
// release that first carries it: NotoSansHebrew-Regular.ttf and its license, OFL.txt.
var hebrewArchive = update.Binary{
	URL:    "https://github.com/HuskerMinion/techo5/releases/download/v1.6.41/techo5-noto-sans-hebrew.tar.gz",
	SHA256: "48f0d3d3320aa7d807d262a0265fd2d0d8a3e99e1d22160489a1c0c9e9170f62",
	Size:   10327,
}
