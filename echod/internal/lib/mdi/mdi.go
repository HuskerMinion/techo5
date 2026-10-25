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
	once   sync.Once
	index  map[string]rune
	byRune map[rune]string
)

// Rune is the glyph for an icon, named as Home Assistant names it ("mdi:lightbulb") or bare
// ("lightbulb").
func Rune(name string) (rune, bool) {
	once.Do(load)
	r, ok := index[strings.TrimPrefix(strings.TrimSpace(name), "mdi:")]
	return r, ok
}

// Name is the icon a glyph is, as Rune names it bare ("lightbulb"): for an icon that arrives as its place in
// the font rather than by name, as a Tessera layout's do.
func Name(r rune) (string, bool) {
	once.Do(load)
	n, ok := byRune[r]
	return n, ok
}

// load reads the names once.
func load() {
	index = make(map[string]rune, 7500)
	byRune = make(map[rune]string, 7500)
	for _, line := range strings.Split(names, "\n") {
		// A checkout that turned the file's line ends into CRLF is still read right.
		n, hex, ok := strings.Cut(strings.TrimRight(line, "\r"), " ")
		if !ok {
			continue
		}
		if v, err := strconv.ParseUint(hex, 16, 32); err == nil {
			index[n] = rune(v)
			if _, had := byRune[rune(v)]; !had {
				byRune[rune(v)] = n
			}
		}
	}
}
