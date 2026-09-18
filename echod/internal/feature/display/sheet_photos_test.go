//go:build !dot && !spot

package display

import (
	"strings"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

func TestFolderLabel(t *testing.T) {
	for id, want := range map[string]string{
		"":                             "None chosen",
		"media-source://media_source":  "All photos",
		"media-source://media_source/": "All photos",
		"media-source://media_source/photos/2007/Beach%20Trip": "Beach Trip",
		"media-source://media_source/photos/2007/Beach Trip":   "Beach Trip",
		"media-source://immich/album-1":                        "album-1",
	} {
		if got := folderLabel(id); got != want {
			t.Errorf("folderLabel(%q) = %q, want %q", id, got, want)
		}
	}
}

// The folder list offers Use this folder first (highlighted), Back when there is somewhere to go
// back to, then the folders; and a tap on each line does what that line says.
func TestFolderPicker(t *testing.T) {
	f := folderView{id: "root/2007", title: "2007", stack: []string{"root"}, photos: 3,
		folders: []hass.Media{{Title: "Beach Trip", ID: "root/2007/beach"}, {Title: "Garden", ID: "root/2007/garden"}}}
	p := folderPicker(f, false)
	if len(p.opts) != 4 || p.cur != 0 || !strings.HasPrefix(p.opts[0], "Use this folder") || p.opts[1] != "‹ Back" ||
		!strings.HasPrefix(p.opts[2], "Beach Trip") {
		t.Fatalf("list = %q (cur %d)", p.opts, p.cur)
	}
	if !strings.Contains(p.opts[0], "3 photos + 2 folders") {
		t.Errorf("Use line = %q, want the counts", p.opts[0])
	}

	top := folderPicker(folderView{id: "root", title: "photos", folders: f.folders}, false)
	if top.opts[1] == "‹ Back" {
		t.Error("the top folder offers Back")
	}

	demo := folderPicker(f, true)
	for _, o := range demo.opts {
		if strings.Contains(o, "Beach") || strings.Contains(o, "Garden") {
			t.Errorf("demo shows %q", o)
		}
	}
	if demo.title != "Photos" {
		t.Errorf("demo title = %q", demo.title)
	}

	if p := folderPicker(folderView{loading: true, title: "2007"}, false); len(p.opts) != 1 || p.cur != -1 {
		t.Errorf("loading list = %q", p.opts)
	}
}
