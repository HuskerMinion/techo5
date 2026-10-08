//go:build !dot

package display

import "github.com/HuskerMinion/techo5/echod/internal/config"

// screenLang is the screen's Language setting, which the clock's day and date, the forecast and the
// weather's words are written in (lib/locale): empty, for every language, is English.
func screenLang() string { return config.Get().Screen.Language }
