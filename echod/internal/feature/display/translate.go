//go:build !dot

package display

import "github.com/HuskerMinion/techo5/echod/internal/lib/locale"

// tr translates presentation text only. Settings IDs and option values stay stable.
func tr(key string) string { return locale.Text(screenLang(), key) }
