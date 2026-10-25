//go:build !dot

package display

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
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
	art := settingRow{id: "weatherart", label: "Weather art", sub: "A landscape for the weather, in place of photos", kind: ctlToggle, on: home.Get().SlideshowArt()}
	if art.on {
		return []settingRow{art} // the photos' own settings wait until it is off again
	}
	rows := []settingRow{
		art,
		{id: "photofolder", label: "Photo folder", kind: ctlChoice, value: folder},
		{id: "photoevery", label: "Time per photo", kind: ctlChoice, value: everyOptions[everyIndex()]},
		{id: "shuffle", label: "Shuffle photos", kind: ctlToggle, on: shuffle},
		{id: "subfolders", label: "Include subfolders", sub: "Every folder inside the one chosen", kind: ctlToggle, on: subfolders},
		{id: "wholephoto", label: "Show whole photo", sub: "All of it, with blurred sides; off fills the screen", kind: ctlToggle, on: home.Get().SlideshowWholePhoto()},
	}
	if home.PairsHere() {
		rows = append(rows, settingRow{id: "photopairs", label: "Photo pairs", sub: "Two tall photos side by side", kind: ctlToggle, on: home.Get().SlideshowPairs()})
	}
	if home.IsImmich(source) {
		// Immich's own: the day and place it knows, and its favorites. A person and the days to show are
		// set on the setup page or in Home Assistant, which have a keyboard.
		rows = append(rows,
			settingRow{id: "photocaption", label: "Date and place", sub: "On the screensaver, under each photo", kind: ctlToggle, on: home.Get().SlideshowCaption()},
			settingRow{id: "immichfav", label: "Favorites only", sub: "The photos marked as favorites in Immich", kind: ctlToggle, on: home.Get().ImmichFavorites()},
		)
	}
	return rows
}

// The Time per photo row's choices. everyIndex is the one in force, the nearest below a value set
// from Home Assistant, which takes any number of seconds.
var (
	everyOptions   = []string{"15 seconds", "30 seconds", "1 minute", "2 minutes", "5 minutes", "15 minutes", "1 hour"}
	everyDurations = []time.Duration{15 * time.Second, 30 * time.Second, time.Minute, 2 * time.Minute, 5 * time.Minute, 15 * time.Minute, time.Hour}
)

func everyIndex() int {
	d := home.Get().SlideshowEvery()
	i := 0
	for j, o := range everyDurations {
		if o <= d {
			i = j
		}
	}
	return i
}

// folderLabel is a media source id as the Photo folder row names it: its last folder.
func folderLabel(id string) string {
	if id == "" {
		return "None chosen"
	}
	if id == home.LocalPhotos {
		return "On this device"
	}
	if home.IsImmich(id) {
		return home.ImmichSourceName(id)
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
		return pickerView{title: title, opts: []string{"Couldn't open this folder", f.problem, "‹ Back"}, cur: -1}
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
		switch {
		case err != nil && home.IsImmich(id):
			d.folder.problem = err.Error() // Immich's own, not Home Assistant's
		case err != nil:
			d.folder.problem = folderProblem(err)
		default:
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

// folderProblem is the line under Couldn't open this folder: why, in words, for the reasons a folder
// list most often fails - no Home Assistant access given yet, an address the device can't look up or
// reach, a token Home Assistant turned down - and Home Assistant's own words for any other.
func folderProblem(err error) string {
	s := err.Error()
	var dns *net.DNSError
	var nerr net.Error
	switch {
	case strings.Contains(s, "no access configured"):
		return "Home Assistant isn't set up here"
	case errors.As(err, &dns):
		return "Address not found: use its IP"
	case strings.Contains(s, "auth_invalid") || refusedStatus.MatchString(s):
		return "Home Assistant refused the token"
	case errors.As(err, &nerr):
		return "Can't reach Home Assistant"
	}
	return s
}

// refusedStatus is an HTTP status of 401 or 403 in an error, as "GET /api/...: 401 Unauthorized" has
// it: a number standing alone, so a port like :8403 in an address is not taken for one.
var refusedStatus = regexp.MustCompile(`(^|\s)40[13](\s|$)`)

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
		if i == 2 {
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
