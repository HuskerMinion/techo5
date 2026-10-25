//go:build panel

package layout

import "testing"

// A panel is no Echo board: main's Show code asks these to pick athena's or the Show 8's paths, and
// on a tablet every one of them has to say no, or the daemon would go looking for hardware an app
// cannot reach.
func TestPanelIsNoShowBoard(t *testing.T) {
	for name, got := range map[string]bool{
		"Checkers":       Checkers(),
		"Crown":          Crown(),
		"Show8":          Show8(),
		"Athena":         Athena(),
		"AthenaMainline": AthenaMainline(),
	} {
		if got {
			t.Errorf("%s() = true on a panel", name)
		}
	}
	if v, ok := idmeFallback("serial"); ok || v != "" {
		t.Errorf("idmeFallback = %q, %v on a panel; want nothing", v, ok)
	}
}

// The state directory is the files directory the app started echod in, whichever user of the tablet
// it belongs to; anything that is not an app files directory falls back to the first user's.
func TestStateDirIsTheAppsFilesDirectory(t *testing.T) {
	t.Chdir(t.TempDir())
	for home, want := range map[string]string{
		"/data/user/10/app.techo5.panel/files":       "/data/user/10/app.techo5.panel/files",
		"/data/user/0/app.techo5.panel/files/":       "/data/user/0/app.techo5.panel/files",
		"/data/data/app.techo5.panel/files":          "/data/data/app.techo5.panel/files",
		"/home/someone":                              "/data/user/0/app.techo5.panel/files",
		"":                                           "/data/user/0/app.techo5.panel/files",
		"data/user/10/app.techo5.panel/files":        "/data/user/0/app.techo5.panel/files",
		"/data/user/10/app.techo5.panel/cache":       "/data/user/0/app.techo5.panel/files",
		"/data/user/10/other.app.techo5.panel/files": "/data/user/0/app.techo5.panel/files",
	} {
		t.Setenv("HOME", home)
		if got := stateDir(); got != want {
			t.Errorf("HOME=%q: StateDir %q, want %q", home, got, want)
		}
	}
}
