//go:build panel && !dot

package display

import (
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/lib/panelctl"
)

// The Apps row: the tablet's other apps, which the panel, as the home screen, otherwise leaves no way
// to (discussion #652). Its Open lists them, and a pick opens that app in a task of its own, over the
// panel, until Home or Back. Like every row it is behind the settings lock, and nothing is saved: the
// list is whatever the tablet's launcher would show.
//
// On the control line (panelctl): the app sends "apps begin", an "apps item <package> <label>" for each
// app, and "apps end" on every connection and whenever "apps list" asks; "apps open <package>" opens
// one. An app from before these lines sends no list, so the row is not shown.
const (
	tabletAppsList = "apps list"
	tabletAppsOpen = "apps open "
)

// tabletApp is one app the tablet's launcher shows.
type tabletApp struct {
	pkg, label string
}

var tabletApps struct {
	sync.Mutex
	apps    []tabletApp // the last whole list
	reading []tabletApp // a list coming in, between begin and end
	open    bool        // a list is coming in
}

func init() { panelctl.Handle("apps", appSaysApps) }

// appSaysApps is a line from the app that starts with "apps".
func appSaysApps(args []string) {
	if len(args) == 0 {
		return
	}
	tabletApps.Lock()
	switch args[0] {
	case "begin":
		tabletApps.reading, tabletApps.open = nil, true
	case "item":
		if tabletApps.open && len(args) >= 3 {
			tabletApps.reading = append(tabletApps.reading, tabletApp{pkg: args[1], label: strings.Join(args[2:], " ")})
		}
		tabletApps.Unlock()
		return
	case "end":
		if !tabletApps.open {
			tabletApps.Unlock()
			return
		}
		tabletApps.apps, tabletApps.reading, tabletApps.open = tabletApps.reading, nil, false
		slog.Info("apps: the tablet has these to open", "count", len(tabletApps.apps))
	default:
		tabletApps.Unlock()
		return
	}
	tabletApps.Unlock()
	androidSettingsChanged()
}

// tabletAppsRows is the General card's Apps row, when the app has said which apps there are.
func tabletAppsRows() []settingRow {
	tabletApps.Lock()
	n := len(tabletApps.apps)
	tabletApps.Unlock()
	if n == 0 {
		return nil
	}
	return []settingRow{{id: "tabletapps", label: tr("tablet_apps"), sub: tr("ui_open_another_app_on_this_tablet"),
		kind: ctlButton, button: tr("ui_open")}}
}

// tabletAppsTap is a tap on the row's Open: the list as it stands, and the app asked for it again, so
// an app installed since shows up as the answer comes.
func tabletAppsTap(d *Display) {
	tellPanelApp(tabletAppsList)
	d.openPicker("tabletapps")
}

// tabletAppsPicker is the list the row opens: the apps by their names.
func tabletAppsPicker() (pickerView, bool) {
	tabletApps.Lock()
	defer tabletApps.Unlock()
	p := pickerView{title: tr("tablet_apps"), cur: -1}
	for _, a := range tabletApps.apps {
		p.opts = append(p.opts, a.label)
	}
	return p, true
}

// chooseTabletApp opens the i'th app of the list.
func chooseTabletApp(i int) {
	tabletApps.Lock()
	if i < 0 || i >= len(tabletApps.apps) {
		tabletApps.Unlock()
		return
	}
	a := tabletApps.apps[i]
	tabletApps.Unlock()
	slog.Info("apps: opening one from the screen", "app", a.pkg)
	tellPanelApp(tabletAppsOpen + a.pkg)
}

// openTabletApps is a tap on the clock set to Apps: the apps list over the clock (apps_pop.go), the PIN
// asked first when the settings lock is on, as the Apps row does. False when the app has sent no list,
// so the tap does what Assist would.
func (d *Display) openTabletApps() bool {
	if len(tabletAppsRows()) == 0 {
		return false
	}
	slog.Info("screen: the apps list by a tap on the clock")
	// up: the PIN, when the lock asks for it, opens the list only if the settings have not come up over
	// the clock meanwhile; otherwise the right PIN opens nothing and the lock goes back on (gate).
	gate("tabletapps", tr("tablet_apps"), func() bool { return !d.sheetUp() }, func() {
		tellPanelApp(tabletAppsList)
		d.mu.Lock()
		d.appsPop, d.appsPopAt, d.pickScroll = true, time.Now(), 0
		d.mu.Unlock()
		d.wake()
	})
	return true
}
