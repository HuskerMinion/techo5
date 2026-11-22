//go:build !dot

package display

import (
	"strings"

	"github.com/HuskerMinion/techo5/echod/internal/lib/locale"
)

// tr translates presentation text only. Settings IDs and option values stay stable.
func tr(key string) string { return locale.Text(screenLang(), key) }

// Lists are translated at draw time; their order and stored option values do not change.
func slideshowLabels() []string { return []string{tr("ui_off"), tr("background"), tr("screensaver")} }
func repeatLabels() []string {
	return []string{tr("once"), tr("every_day"), tr("weekdays"), tr("weekends")}
}

// namesTimer is whether a timer's name already says it is a timer, in English or in the screen's
// language, so the screen does not say it twice: "Pasta timer", or "Cronometru paste" in Romanian,
// is done as it is named, where "Pasta" is the pasta timer.
func namesTimer(name string) bool {
	n := strings.ToLower(name)
	return strings.Contains(n, "timer") || strings.Contains(n, strings.ToLower(tr("timer")))
}
