//go:build !dot

package display

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// The slideshow's photo folder, picked on the screen: a list of Home Assistant's media folders to
// tap through, a level at a time, with Use this folder at the top of each and Back to the one above.

// photosRoot is where the folder list starts: every media source Home Assistant has.
const photosRoot = "media-source://media_source"

// folderView is the folder open in the list, and how it was reached.
type folderView struct {
	id, title string
	stack     []string // the folders above, nearest last, for Back
	folders   []hass.Media
	photos    int
	loading   bool
	problem   string
}

// slideshowRows are the Display card's rows for where the photos come from, shown while the
// slideshow is on.
func slideshowRows(demo bool) []settingRow {
	source, shuffle, subfolders := home.Get().SlideshowSettings()
	folder := folderLabel(source)
	if demo && source != "" {
		folder = "Photos" // folder names are often people and places
	}
	return []settingRow{
		{id: "photofolder", label: "Photo folder", kind: ctlChoice, value: folder},
		{id: "shuffle", label: "Shuffle photos", kind: ctlToggle, on: shuffle},
		{id: "subfolders", label: "Include subfolders", sub: "Every folder inside the one chosen", kind: ctlToggle, on: subfolders},
	}
}

// folderLabel is a media source id as the Photo folder row names it: its last folder.
func folderLabel(id string) string {
	if id == "" {
		return "None chosen"
	}
	rest := strings.TrimRight(strings.TrimPrefix(id, "media-source://"), "/")
	name := rest[strings.LastIndex(rest, "/")+1:]
	if u, err := url.PathUnescape(name); err == nil {
		name = u
	}
	if name == "" || name == "media_source" {
		return "All photos"
	}
	return name
}

// folderPicker is the folder list as a list of choices: Use this folder, Back when there is a folder
// above, then the folders inside.
func folderPicker(f folderView, demo bool) pickerView {
	title := f.title
	if demo {
		title = "Photos"
	}
	switch {
	case f.loading:
		return pickerView{title: title, opts: []string{"Opening…"}, cur: -1}
	case f.problem != "":
		return pickerView{title: title, opts: []string{"Couldn't open this folder", "‹ Back"}, cur: -1}
	}
	use := "Use this folder"
	switch {
	case f.photos > 0 && len(f.folders) > 0:
		use += fmt.Sprintf(" · %d photos + %d folders", f.photos, len(f.folders))
	case f.photos > 0:
		use += fmt.Sprintf(" · %d photos", f.photos)
	case len(f.folders) > 0:
		use += fmt.Sprintf(" · %d folders", len(f.folders))
	}
	p := pickerView{title: title, opts: []string{use}, cur: 0}
	if len(f.stack) > 0 {
		p.opts = append(p.opts, "‹ Back")
	}
	for i, c := range f.folders {
		name := c.Title
		if demo {
			name = fmt.Sprintf("Folder %d", i+1)
		}
		p.opts = append(p.opts, name+"  ›")
	}
	return p
}

// openFolder shows a folder in the list, asking Home Assistant for what is in it.
func (d *Display) openFolder(id string, stack []string) {
	d.mu.Lock()
	d.folder = folderView{id: id, title: folderLabel(id), stack: stack, loading: true}
	d.picker, d.pickScroll = "folder", 0
	d.mu.Unlock()
	d.wake()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		m, err := home.Get().BrowseFolder(ctx, id)
		d.mu.Lock()
		if d.folder.id != id || !d.folder.loading {
			d.mu.Unlock()
			return // moved on meanwhile
		}
		d.folder.loading = false
		if err != nil {
			d.folder.problem = err.Error()
		} else {
			if m.Title != "" && id != photosRoot {
				d.folder.title = m.Title
			}
			for _, c := range m.Children {
				switch {
				case c.CanExpand:
					d.folder.folders = append(d.folder.folders, c)
				case c.CanPlay:
					d.folder.photos++
				}
			}
		}
		d.mu.Unlock()
		d.wake()
	}()
}

// folderChoice is a tap on the folder list's i'th line.
func (d *Display) folderChoice(i int) {
	d.mu.Lock()
	f := d.folder
	d.mu.Unlock()
	up := func() {
		if n := len(f.stack); n > 0 {
			d.openFolder(f.stack[n-1], f.stack[:n-1])
		}
	}
	switch {
	case f.loading:
		d.reopen("folder")
	case f.problem != "":
		if i == 1 {
			up()
			return
		}
		d.reopen("folder")
	case i == 0:
		home.Get().SetSlideshowSource(f.id)
	case len(f.stack) > 0 && i == 1:
		up()
	default:
		k := i - 1
		if len(f.stack) > 0 {
			k--
		}
		if k >= 0 && k < len(f.folders) {
			d.openFolder(f.folders[k].ID, append(append([]string{}, f.stack...), f.id))
		}
	}
}

// reopen puts a list back up that a tap took down.
func (d *Display) reopen(id string) {
	d.mu.Lock()
	d.picker = id
	d.mu.Unlock()
}
