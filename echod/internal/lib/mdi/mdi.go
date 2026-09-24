// Package mdi is Material Design Icons, the icon set Home Assistant uses, as a font: every icon a
// dashboard can name ("mdi:lightbulb") is a glyph in it, drawn like a letter.
//
// The font is the Pictogrammers' webfont, version 7.4.47, under the Apache License 2.0 (NOTICE).
package mdi

import (
	_ "embed"
	"strconv"
	"strings"
	"sync"
)

// Font is the icon font, TrueType.
//
//go:embed materialdesignicons.ttf
var Font []byte

//go:embed names.txt
var names string

var (
	once  sync.Once
	index map[string]rune
)

// Rune is the glyph for an icon, named as Home Assistant names it ("mdi:lightbulb") or bare
// ("lightbulb").
func Rune(name string) (rune, bool) {
	once.Do(func() {
		index = make(map[string]rune, 7500)
		for _, line := range strings.Split(names, "\n") {
			// A checkout that turned the file's line ends into CRLF is still read right.
			n, hex, ok := strings.Cut(strings.TrimRight(line, "\r"), " ")
			if !ok {
				continue
			}
			if v, err := strconv.ParseUint(hex, 16, 32); err == nil {
				index[n] = rune(v)
			}
		}
	})
	r, ok := index[strings.TrimPrefix(strings.TrimSpace(name), "mdi:")]
	return r, ok
}
