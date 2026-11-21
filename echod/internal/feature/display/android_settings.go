//go:build !dot && !panel

package display

import "time"

// androidSettingsRows: only a panel has a tablet of its own to open the settings of
// (android_settings_panel.go).
func androidSettingsRows(time.Time) []settingRow { return nil }

func androidSettingsTap() {}

// tabletAppsRows and the rest: only a panel has a tablet's other apps to open (tablet_apps_panel.go).
func tabletAppsRows() []settingRow         { return nil }
func tabletAppsTap(*Display)               {}
func tabletAppsPicker() (pickerView, bool) { return pickerView{}, false }
func chooseTabletApp(int)                  {}
