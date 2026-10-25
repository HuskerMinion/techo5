//go:build panel

package diag

// coresHere is false on a panel: the app runs as an ordinary Android app, which may not write the
// hotplug governor's files. A tablet on a MediaTek chip (the Fire HD 8 2018 is the Echo Show 5's
// MT8163) has /proc/hps all the same, so the hold would fail at every start and the number would do
// nothing.
const coresHere = false
