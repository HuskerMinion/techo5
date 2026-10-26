//go:build !dot

package display

import "github.com/HuskerMinion/techo5/echod/internal/lib/locale"

// tr translates presentation text only. Settings IDs and option values stay stable.
func tr(key string) string { return locale.Text(screenLang(), key) }

// Lists are translated at draw time; their order and stored option values do not change.
func slideshowLabels() []string { return []string{tr("ui_off"), tr("background"), tr("screensaver")} }
func repeatLabels() []string {
	return []string{tr("once"), tr("every_day"), tr("weekdays"), tr("weekends")}
}
