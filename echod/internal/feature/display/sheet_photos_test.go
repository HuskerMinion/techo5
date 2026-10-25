//go:build !dot && !spot

package display

import (
	"errors"
	"fmt"
	"net"
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

// A folder that could not be opened says why under Couldn't open this folder, in words for the usual
// reasons, and Back is the line after it.
func TestFolderProblem(t *testing.T) {
	for _, c := range []struct {
		err  error
		want string
	}{
		{errors.New("hass: no access configured"), "Home Assistant isn't set up here"},
		{fmt.Errorf("hass: websocket: %w", &net.DNSError{Err: "no such host", Name: "homeassistant.local", IsNotFound: true}), "Address not found: use its IP"},
		{errors.New("hass: websocket auth: auth_invalid Invalid access token or password"), "Home Assistant refused the token"},
		{fmt.Errorf("hass: websocket: %w", &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}), "Can't reach Home Assistant"},
		{errors.New("hass: media_source/browse_media: Unknown source directory."), "hass: media_source/browse_media: Unknown source directory."},
		{errors.New("hass: GET /api/media_source: 401 Unauthorized"), "Home Assistant refused the token"},
		{errors.New("hass: GET /api/media_source: 403 Forbidden"), "Home Assistant refused the token"},
		// A port with 401 or 403 in it is not a status.
		{fmt.Errorf("hass: websocket: %w", &net.OpError{Op: "dial", Net: "tcp", Addr: &net.TCPAddr{IP: net.IPv4(192, 0, 2, 7), Port: 8403},
			Err: errors.New("connection refused")}), "Can't reach Home Assistant"},
		{errors.New("hass: GET /api/x: 500 Internal Server Error (port 14013)"), "hass: GET /api/x: 500 Internal Server Error (port 14013)"},
	} {
		if got := folderProblem(c.err); got != c.want {
			t.Errorf("folderProblem(%v) = %q, want %q", c.err, got, c.want)
		}
	}

	p := folderPicker(folderView{title: "Photos", problem: "Home Assistant isn't set up here"}, false)
	if len(p.opts) != 3 || p.opts[0] != "Couldn't open this folder" || p.opts[1] != "Home Assistant isn't set up here" ||
		p.opts[2] != "‹ Back" || p.cur != -1 {
		t.Errorf("problem list = %q (cur %d)", p.opts, p.cur)
	}
}
