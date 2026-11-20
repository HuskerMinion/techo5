//go:build panel && !dot

package display

import (
	"log/slog"
	"sync"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/lib/panelctl"
)

// The Android settings row: the tablet's own Settings (Wi-Fi, display, apps), which the panel, as the
// home screen, otherwise leaves no way to (discussion #652). The app opens it in a task of its own, over the
// panel, until Home or Back.
//
// On the control line (panelctl): the app says "android settings can" on every connection, "android
// settings open" asks it to open them, and "android settings none" comes back when the tablet had
// nothing to open. An app from before these lines never says it can, so the row is not shown; and an
// app that can is never asked by an echod from before them, while it ignores lines it does not know.
const (
	androidSettingsOpen = "android settings open"
	// androidSettingsNoneFor is how long the row says the tablet had nothing to open.
	androidSettingsNoneFor = 10 * time.Second
)

var (
	// tellPanelApp is the control line, set apart so a test can read what the app is asked.
	tellPanelApp = panelctl.Tell
	// androidSettingsChanged redraws the screen for what the app said. A variable, for tests.
	androidSettingsChanged = func() { Get().wake() }

	androidSettings struct {
		sync.Mutex
		can  bool      // the app said it can open them
		none time.Time // when it last said there was nothing to open
	}
)

func init() { panelctl.Handle("android", appSaysAndroid) }

// appSaysAndroid is a line from the app that starts with "android".
func appSaysAndroid(args []string) {
	if len(args) < 2 || args[0] != "settings" {
		return
	}
	androidSettings.Lock()
	switch args[1] {
	case "can":
		androidSettings.can = true
	case "none":
		androidSettings.none = time.Now()
		slog.Warn("android settings: the tablet has none to open")
	default:
		androidSettings.Unlock()
		return
	}
	androidSettings.Unlock()
	androidSettingsChanged()
}

// androidSettingsRows is the General card's Android settings row, when the app can open them.
func androidSettingsRows(now time.Time) []settingRow {
	androidSettings.Lock()
	can, none := androidSettings.can, androidSettings.none
	androidSettings.Unlock()
	if !can {
		return nil
	}
	row := settingRow{id: "androidsettings", label: tr("android_settings"), sub: tr("this_tablet_s_own_settings_wi_fi_display_apps"),
		kind: ctlButton, button: tr("ui_open")}
	if !none.IsZero() && now.Sub(none) < androidSettingsNoneFor {
		row.sub = tr("ui_this_tablet_has_no_settings_screen_to_open")
	}
	return []settingRow{row}
}

// androidSettingsTap is a tap on the row's Open: the app is asked, once, and the last none forgotten.
func androidSettingsTap() {
	androidSettings.Lock()
	androidSettings.none = time.Time{}
	androidSettings.Unlock()
	slog.Info("android settings asked for from the screen")
	tellPanelApp(androidSettingsOpen)
}
