package addons

import (
	"strings"
	"testing"
)

// A root add-on finds root's tools on its PATH; one that runs as its own user does not.
func TestARootAddonFindsRootsTools(t *testing.T) {
	if p := addonPath(true); !strings.Contains(p, "/usr/sbin") || !strings.Contains(p, ":/sbin") {
		t.Errorf("a root add-on's %s", p)
	}
	if p := addonPath(false); strings.Contains(p, "sbin") {
		t.Errorf("an add-on of its own user's %s", p)
	}
}
