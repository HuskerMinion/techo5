//go:build !dot && !panel

package display

import "time"

// androidSettingsRows: only a panel has a tablet of its own to open the settings of
// (android_settings_panel.go).
func androidSettingsRows(time.Time) []settingRow { return nil }

func androidSettingsTap() {}
