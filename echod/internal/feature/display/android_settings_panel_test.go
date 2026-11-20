//go:build panel && !dot

package display

import (
	"testing"
	"time"
)

// androidSettingsFor puts the row's state back after a test, and keeps the screen and the app out of it.
func androidSettingsFor(t *testing.T) *[]string {
	t.Helper()
	androidSettings.Lock()
	can, none := androidSettings.can, androidSettings.none
	androidSettings.can, androidSettings.none = false, time.Time{}
	androidSettings.Unlock()
	var told []string
	tell, changed := tellPanelApp, androidSettingsChanged
	tellPanelApp = func(line string) { told = append(told, line) }
	androidSettingsChanged = func() {}
	t.Cleanup(func() {
		tellPanelApp, androidSettingsChanged = tell, changed
		androidSettings.Lock()
		androidSettings.can, androidSettings.none = can, none
		androidSettings.Unlock()
	})
	return &told
}

func generalRow(t *testing.T, id string) (settingRow, bool) {
	t.Helper()
	for _, r := range generalRows(sheetView{st: settings{version: "v0.1.1", now: time.Now()}, now: time.Now()}) {
		if r.id == id {
			return r, true
		}
	}
	return settingRow{}, false
}

// An app from before the line never says it can open Android's settings: no row for it.
func TestAndroidSettingsRowNeedsAnAppThatCan(t *testing.T) {
	androidSettingsFor(t)
	if r, ok := generalRow(t, "androidsettings"); ok {
		t.Fatalf("an app that never said it can: row %+v", r)
	}
	appSaysAndroid([]string{"settings", "maybe"})
	appSaysAndroid([]string{"display", "can"})
	appSaysAndroid([]string{"settings"})
	if _, ok := generalRow(t, "androidsettings"); ok {
		t.Fatal("a line that is not \"android settings can\" showed the row")
	}
	appSaysAndroid([]string{"settings", "can"})
	r, ok := generalRow(t, "androidsettings")
	if !ok {
		t.Fatal("no Android settings row after the app said it can")
	}
	if r.kind != ctlButton || r.label != "Android settings" || r.button != "Open" ||
		r.sub != "This tablet's own settings: Wi-Fi, display, apps" {
		t.Errorf("row %+v", r)
	}
}

// Open asks the app once, with the line it knows; the row is not a saved setting.
func TestAndroidSettingsOpenAsksTheApp(t *testing.T) {
	told := androidSettingsFor(t)
	appSaysAndroid([]string{"settings", "can"})
	d := &Display{poke: make(chan struct{}, 1)}
	d.rowTap("androidsettings", partMain, 0)
	if len(*told) != 1 || (*told)[0] != "android settings open" {
		t.Fatalf("the app was told %q; want one \"android settings open\"", *told)
	}
}

// A tablet with nothing to open says so on the row for a while, and Open tried again clears it.
func TestAndroidSettingsNoneSaysSoForAWhile(t *testing.T) {
	told := androidSettingsFor(t)
	redrawn := 0
	androidSettingsChanged = func() { redrawn++ }
	appSaysAndroid([]string{"settings", "can"})
	appSaysAndroid([]string{"settings", "none"})
	if redrawn != 2 {
		t.Errorf("redrawn %d times; want one for each line", redrawn)
	}
	none := "This tablet has no settings screen to open"
	if r, _ := generalRow(t, "androidsettings"); r.sub != none {
		t.Errorf("after none: sub %q", r.sub)
	}
	rows := androidSettingsRows(time.Now().Add(androidSettingsNoneFor + time.Second))
	if len(rows) != 1 || rows[0].sub == none {
		t.Errorf("long after none: %+v", rows)
	}
	androidSettingsTap()
	if r, _ := generalRow(t, "androidsettings"); r.sub == none || len(*told) != 1 {
		t.Errorf("after Open again: sub %q, told %q", r.sub, *told)
	}
}
