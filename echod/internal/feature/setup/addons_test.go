//go:build !panel

package setup

import (
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/feature/addons"
	"github.com/HuskerMinion/techo5/echod/internal/feature/addons/catalog"
	"github.com/HuskerMinion/techo5/echod/internal/update"
)

// The warning, word for word as it was agreed. Changing it is a decision, not an edit: this test is
// where that shows.
func TestTheAddonWarningIsTheAgreedText(t *testing.T) {
	text := strings.Join(strings.Fields(addonWarning), " ")
	want := "<p><strong>Add-ons run on this device with access to its microphones, speaker, network and your Home " +
		"Assistant connection.</strong> They are made and kept by their authors, not by TECHO5. Being listed here means " +
		"it was looked over when it was added. It doesn't mean it's safe, that it will keep working, or that later " +
		"versions were checked. Only install add-ons from people you trust.</p>"
	if text != want {
		t.Errorf("the warning is\n%s\nnot\n%s", text, want)
	}
	box := strings.Join(strings.Fields(addonsBox("Plex <player>", "Some One")), " ")
	if !strings.Contains(box, "I understand the risk and want to install <strong>Plex &lt;player&gt;</strong> by <strong>Some One</strong>.") ||
		!strings.Contains(box, `name="understood" value="yes" required`) {
		t.Errorf("the box: %s", box)
	}
}

func plexListing(runs bool, why string, root bool) addons.Listing {
	return addons.Listing{
		Addon: catalog.Addon{ID: "techo5-plex", Name: "Plex player", Author: "HuskerMinion", Summary: "Plays Plex.",
			Repo: "https://github.com/HuskerMinion/techo5-plex"},
		Version: catalog.Version{Version: "0.2.0", MinTECHO5: map[string]string{"show": "v1.6.52"}, Root: root,
			Ports:    []catalog.Port{{Proto: "tcp", Port: 32500, Why: "control"}, {Proto: "udp", From: 32410, To: 32414, Why: "discovery"}},
			MemoryMB: 40, DiskMB: 12},
		Runs: runs, Why: why, AsRoot: root, Shown: "abc123",
	}
}

// Every install on offer has what it asks for, the warning and its box, in the form that installs it.
func TestAnAvailableAddonShowsTheWarningAndTheBox(t *testing.T) {
	w := httptest.NewRecorder()
	addonsAvailable(w, "tok", []addons.Listing{plexListing(true, "", false)})
	body := strings.Join(strings.Fields(w.Body.String()), " ")
	for _, want := range []string{
		`value="addon-install"`, `name="id" value="techo5-plex"`, `name="version" value="0.2.0"`, `name="shown" value="abc123"`,
		"Add-ons run on this device", "I understand the risk and want to install <strong>Plex player</strong> by <strong>HuskerMinion</strong>.",
		"32500/tcp (control)", "32410-32414/udp (discovery)", "Runs as a user of its own, not as root.",
		"about 40 MB of memory and 12 MB of storage", "Needs TECHO5 Echo Show v1.6.52 or later",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("lacks %q:\n%s", want, body)
		}
	}
	if i, j := strings.Index(body, "Add-ons run on this device"), strings.Index(body, "<button"); i < 0 || j < i {
		t.Error("the button comes before the warning")
	}

	// Root is said in bold.
	w = httptest.NewRecorder()
	addonsAvailable(w, "tok", []addons.Listing{plexListing(true, "", true)})
	if !strings.Contains(w.Body.String(), "<strong>Runs as root: it can do anything on this device.</strong>") {
		t.Errorf("root is not said in bold:\n%s", w.Body.String())
	}

	// One that does not run here is shown without a form, with the reason.
	w = httptest.NewRecorder()
	addonsAvailable(w, "tok", []addons.Listing{plexListing(false, "needs TECHO5 v9.0.0 or later", false)})
	if body := w.Body.String(); strings.Contains(body, "<form") || !strings.Contains(body, "needs TECHO5 v9.0.0 or later") {
		t.Errorf("an add-on that does not run here:\n%s", body)
	}
}

// The extra files an install downloads are listed by name and size above the box; a listing without
// any says nothing of them.
func TestAnAvailableAddonListsItsExtraFiles(t *testing.T) {
	l := plexListing(true, "", true)
	l.Files = []catalog.File{
		{Name: "netbird.tar.gz", Binary: update.Binary{Size: 14_900_000}},
		{Name: "a<b>.bin", Binary: update.Binary{Size: 300}},
	}
	w := httptest.NewRecorder()
	addonsAvailable(w, "tok", []addons.Listing{l})
	body := strings.Join(strings.Fields(w.Body.String()), " ")
	want := "Downloads beside its program, checked as the program is: <code>netbird.tar.gz</code> (14.2 MB), <code>a&lt;b&gt;.bin</code> (1 kB)."
	if !strings.Contains(body, want) {
		t.Errorf("lacks %q:\n%s", want, body)
	}
	if i, j := strings.Index(body, "netbird.tar.gz"), strings.Index(body, "I understand the risk"); i < 0 || j < i {
		t.Error("the files are not listed above the box")
	}
	w = httptest.NewRecorder()
	addonsAvailable(w, "tok", []addons.Listing{plexListing(true, "", false)})
	if strings.Contains(w.Body.String(), "Downloads beside") {
		t.Error("a listing without extra files mentions them")
	}
}

// An add-on whose every version needs a newer TECHO5 is shown grayed, with its newest version's number
// and that it needs a newer TECHO5, and nothing of what it asks, which this build did not read.
func TestAnAddonForANewerTECHO5IsShownGrayed(t *testing.T) {
	l := addons.Listing{
		Addon: catalog.Addon{ID: "techo5-next", Name: "Next thing", Author: "HuskerMinion", Summary: "Does the next thing.",
			Repo: "https://github.com/HuskerMinion/techo5-next", Skipped: []string{"1.0.0"}},
		Version: catalog.Version{Version: "1.0.0"}, Why: "needs a newer TECHO5", NeedsNewer: true,
	}
	w := httptest.NewRecorder()
	addonsAvailable(w, "tok", []addons.Listing{l})
	body := strings.Join(strings.Fields(w.Body.String()), " ")
	for _, want := range []string{`style="opacity:.6"`, "Next thing <span class=\"note\">1.0.0, by HuskerMinion</span>",
		"Not on this device: needs a newer TECHO5.", `href="https://github.com/HuskerMinion/techo5-next"`} {
		if !strings.Contains(body, want) {
			t.Errorf("lacks %q:\n%s", want, body)
		}
	}
	for _, not := range []string{"<form", "Needs TECHO5 ", "memory", "Runs as"} {
		if strings.Contains(body, not) {
			t.Errorf("has %q:\n%s", not, body)
		}
	}
}

// An installed add-on has Stop, Remove (keeping its state unless asked), and an update that asks for
// more shows the warning and the box again; one that asks for nothing more does not.
func TestAnInstalledAddon(t *testing.T) {
	s := addons.Status{ID: "techo5-plex", Name: "Plex player", Author: "HuskerMinion", Version: "0.1.0", Enabled: true,
		State: addons.StateRunning, Page: "http://{device}:32501/",
		Update: &addons.Offer{Version: "0.2.0", More: []string{"port 32600/tcp"}}}
	w := httptest.NewRecorder()
	addonsInstalled(w, "tok", addons.Get(), []addons.Status{s})
	body := strings.Join(strings.Fields(w.Body.String()), " ")
	for _, want := range []string{`value="addon-stop"`, `value="addon-remove"`, `name="deletestate" value="yes"`,
		`value="addon-update"`, "port 32600/tcp", "I understand the risk and want to install <strong>Plex player</strong>",
		`data-addon-page="http://{device}:32501/"`} {
		if !strings.Contains(body, want) {
			t.Errorf("lacks %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, `name="deletestate" value="yes" checked`) {
		t.Error("deleting the state is ticked already")
	}

	s.Update.More = nil
	w = httptest.NewRecorder()
	addonsInstalled(w, "tok", addons.Get(), []addons.Status{s})
	if body := w.Body.String(); strings.Contains(body, "I understand the risk") || !strings.Contains(body, "asks for nothing more") {
		t.Errorf("an update asking for nothing more:\n%s", body)
	}

	s.Update, s.Withdrawn, s.AsRoot = nil, "its author stopped keeping it", true
	w = httptest.NewRecorder()
	addonsInstalled(w, "tok", addons.Get(), []addons.Status{s})
	if body := w.Body.String(); !strings.Contains(body, "No longer listed") || !strings.Contains(body, "<strong>Runs as root.</strong>") {
		t.Errorf("a withdrawn root add-on:\n%s", body)
	}
}

// One stopped for its storage offers Start, which runs it again, not Stop, which would do nothing.
func TestAnAddonStoppedForItsStorageOffersStart(t *testing.T) {
	s := addons.Status{ID: "techo5-plex", Name: "Plex player", Author: "HuskerMinion", Version: "0.1.0", Enabled: true,
		State: addons.StateTooBig, Problem: "it keeps 900 MB"}
	w := httptest.NewRecorder()
	addonsInstalled(w, "tok", addons.Get(), []addons.Status{s})
	if body := w.Body.String(); !strings.Contains(body, `value="addon-start"`) || strings.Contains(body, `value="addon-stop"`) {
		t.Errorf("a card stopped for its storage:\n%s", body)
	}
}

// A form sent without the box installs nothing, whatever the browser did with the box's required.
func TestAnInstallWithoutTheBoxIsRefused(t *testing.T) {
	r := httptest.NewRequest("POST", "/setup/save", strings.NewReader(url.Values{"id": {"techo5-plex"}, "version": {"0.1.0"}}.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if problem := saveAddonInstall(r); !strings.Contains(problem, "tick the box") {
		t.Errorf("an install without the box: %q", problem)
	}
	r = httptest.NewRequest("POST", "/setup/save", strings.NewReader(url.Values{"id": {"../../etc"}, "understood": {"yes"}}.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if problem := saveAddonInstall(r); problem == "" {
		t.Error("an install of something that cannot be an add-on was taken")
	}
}

// The card's forms come back to it, on System.
func TestTheAddonFormsComeBackToTheCard(t *testing.T) {
	for _, what := range []string{"addon-install", "addon-update", "addon-start", "addon-stop", "addon-remove", "addon-check"} {
		if sectionOf(what) != "addons" || tabFor[what] != "system" {
			t.Errorf("%s comes back to %s on %s", what, sectionOf(what), tabFor[what])
		}
		if _, ok := saves[what]; !ok {
			t.Errorf("%s has no save", what)
		}
	}
}
