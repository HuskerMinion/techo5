//go:build !dot && !spot

package display

import (
	"fmt"
	"image"
	"image/draw"
	"strings"

	"github.com/HuskerMinion/techo5/echod/internal/lib/wifi"
)

// The Wi-Fi pages: a list of the networks the radio hears with a Connect button each, and a
// keyboard for the passphrase. They open from the Connections card, and on their own when a device has
// no address a while after boot — a fresh unit, or one carried to another house.

// wifiState is what the pages show.
type wifiState struct {
	status   wifi.Status
	nets     []wifi.Network
	scanning bool
	page     int
	pick     *wifi.Network // the network being joined; nil is the list
	text     string        // the passphrase so far
	shift    bool
	symbols  bool
	busy     string // "Connecting to X…" while a join runs
	err      string // why the last join failed
}

const (
	wifiRowTop    = 118
	wifiRowHeight = 44
	wifiRows      = 6
	wifiDoneBar   = 64

	keyTop    = 150
	keyH      = 62
	keyGap    = 6
	keyW      = 88
	keyRows   = 4
	keyMargin = 13
)

var (
	keyRowsLetters = []string{"qwertyuiop", "asdfghjkl", "zxcvbnm"}
	keyRowsSymbols = []string{"1234567890", "-_.,!?@#/", "$%&*()'~+"}
)

// wifiHit is what a tap on the network list landed on.
type wifiHit struct {
	row    int // a network row, or -1
	rescan bool
	done   bool
	more   bool
}

func (r *renderer) wifiListHit(x, y int) wifiHit {
	h := wifiHit{row: -1}
	switch {
	case y >= r.h-wifiDoneBar:
		if x < r.w/2 {
			h.rescan = true
		} else {
			h.done = true
		}
	case y >= wifiRowTop && (y-wifiRowTop)/wifiRowHeight < wifiRows:
		h.row = (y - wifiRowTop) / wifiRowHeight
	}
	return h
}

// keyAt maps a tap on the keyboard page to a key: a character, or "shift", "symbols", "space",
// "backspace", "cancel", "join"; empty for nothing.
func (r *renderer) keyAt(x, y int, symbols bool) string {
	if y < keyTop {
		return ""
	}
	row := (y - keyTop) / (keyH + keyGap)
	if row >= keyRows {
		return ""
	}
	pitch := keyW + keyGap
	switch row {
	case 0, 1, 2:
		letters := keyRowsLetters[row]
		if symbols {
			letters = keyRowsSymbols[row]
		}
		x0 := keyMargin + (10-len(letters))*pitch/2
		if row == 2 && !symbols {
			// Shift on the left, backspace on the right, the letters between.
			if x >= keyMargin && x < keyMargin+pitch+keyW/2 {
				return "shift"
			}
			if x >= r.w-keyMargin-pitch-keyW/2 {
				return "backspace"
			}
		} else if row == 2 && symbols && x >= r.w-keyMargin-pitch-keyW/2 {
			return "backspace"
		}
		i := (x - x0) / pitch
		if i < 0 || i >= len(letters) || x < x0 {
			return ""
		}
		return string(letters[i])
	default:
		// [symbols] [    space    ] [cancel] [join]
		switch {
		case x < keyMargin+pitch*2:
			return "symbols"
		case x < keyMargin+pitch*6:
			return "space"
		case x < keyMargin+pitch*8:
			return "cancel"
		default:
			return "join"
		}
	}
}

func (r *renderer) wifiPage(s scene) {
	w := s.wifi
	if w.pick != nil {
		r.keyboardPage(s)
		return
	}
	r.text(r.body, "Wi-Fi", r.margin, 52, amber)
	t := clockHM(s.now)
	r.text(r.small, t, r.w-r.margin-r.width(r.small, t), 52, dim)
	line := "Not connected"
	if w.status.Connected {
		line = "Connected to " + w.status.SSID
		if w.status.Address != "" {
			line += "  ·  " + w.status.Address
		}
	} else if w.status.SSID != "" {
		line = w.status.SSID + ": " + w.status.State
	}
	if w.busy != "" {
		line = w.busy
	} else if w.err != "" {
		line = w.err
	}
	r.text(r.tiny, line, r.margin, 92, dim)

	if len(w.nets) == 0 {
		msg := "No networks heard yet"
		if w.scanning {
			msg = "Scanning" + "..."[:int(s.now.UnixMilli()/400%4)]
		}
		r.text(r.small, msg, r.margin, wifiRowTop+34, dim)
	}
	start, end, more := pageWith(len(w.nets), w.page, wifiRows)
	for i, n := range w.nets[start:end] {
		top := wifiRowTop + i*wifiRowHeight
		draw.Draw(r.dst, image.Rect(r.margin, top+wifiRowHeight-1, r.w-r.margin, top+wifiRowHeight), image.NewUniform(ember), image.Point{}, draw.Src)
		c := cream
		if n.SSID == w.status.SSID && w.status.Connected {
			c = amber
		}
		r.text(r.small, n.SSID, r.margin, top+31, c)
		info := bars(n.Signal)
		if n.Secured {
			info += "  ·  locked"
		}
		r.text(r.tiny, info, r.w-r.margin-buttonWide-buttonGap-r.width(r.tiny, info), top+29, dim)
		label := "Connect"
		if n.SSID == w.status.SSID && w.status.Connected {
			label = "Joined"
		}
		r.bevel(image.Rect(r.w-r.margin-buttonWide, top+7, r.w-r.margin, top+wifiRowHeight-7), shift(ember, 12), true)
		r.text(r.tiny, label, r.w-r.margin-buttonWide+(buttonWide-r.width(r.tiny, label))/2, top+29, cream)
	}
	if more {
		top := wifiRowTop + (wifiRows-1)*wifiRowHeight
		r.text(r.small, "More", r.margin, top+31, dim)
		r.bevel(image.Rect(r.w-r.margin-buttonWide, top+7, r.w-r.margin, top+wifiRowHeight-7), shift(ember, 12), true)
		r.text(r.tiny, "Next", r.w-r.margin-buttonWide+(buttonWide-r.width(r.tiny, "Next"))/2, top+29, cream)
	}

	// The bar: rescan on the left, done on the right.
	top := r.h - wifiDoneBar
	draw.Draw(r.dst, image.Rect(0, top, r.w, r.h), image.NewUniform(ember), image.Point{}, draw.Src)
	draw.Draw(r.dst, image.Rect(r.w/2-1, top+10, r.w/2+1, r.h-10), image.NewUniform(dim), image.Point{}, draw.Src)
	r.text(r.body, "Rescan", (r.w/2-r.width(r.body, "Rescan"))/2, top+45, cream)
	r.text(r.body, "Done", r.w/2+(r.w/2-r.width(r.body, "Done"))/2, top+45, cream)
}

// pageWith is pageOf for a page of rows rows: the list's slice, and whether a More row is needed.
func pageWith(n, page, rows int) (start, end int, more bool) {
	if n <= rows {
		return 0, n, false
	}
	per := rows - 1
	pages := (n + per - 1) / per
	page %= pages
	start = page * per
	end = start + per
	if end > n {
		end = n
	}
	return start, end, true
}

// bars is a signal strength as a word.
func bars(dbm int) string {
	switch {
	case dbm >= -55:
		return "strong"
	case dbm >= -70:
		return "good"
	case dbm >= -80:
		return "weak"
	}
	return "faint"
}

func (r *renderer) keyboardPage(s scene) {
	w := s.wifi
	title := "Password for " + w.pick.SSID
	r.text(r.small, title, r.margin, 46, amber)
	// The field.
	field := image.Rect(r.margin, 66, r.w-r.margin, 126)
	r.bevel(field, shift(walnut, 8), false)
	shown := w.text
	if w.busy != "" {
		shown = w.busy
	} else if w.err != "" {
		shown = w.err
	}
	for r.width(r.small, shown+"|") > field.Dx()-30 && len(shown) > 1 {
		shown = shown[1:]
	}
	c := cream
	if w.busy != "" || w.err != "" {
		c = dim
	} else {
		shown += "|"
	}
	r.text(r.small, shown, field.Min.X+15, 109, c)

	pitch := keyW + keyGap
	for row := 0; row < 3; row++ {
		letters := keyRowsLetters[row]
		if w.symbols {
			letters = keyRowsSymbols[row]
		}
		x0 := keyMargin + (10-len(letters))*pitch/2
		y0 := keyTop + row*(keyH+keyGap)
		for i, ch := range letters {
			label := string(ch)
			if w.shift && !w.symbols {
				label = strings.ToUpper(label)
			}
			r.key(image.Rect(x0+i*pitch, y0, x0+i*pitch+keyW, y0+keyH), label, false)
		}
		if row == 2 {
			if !w.symbols {
				r.key(image.Rect(keyMargin, y0, x0-keyGap, y0+keyH), "Shift", w.shift)
			}
			r.key(image.Rect(x0+len(letters)*pitch, y0, r.w-keyMargin, y0+keyH), "Del", false)
		}
	}
	y0 := keyTop + 3*(keyH+keyGap)
	sym := "123"
	if w.symbols {
		sym = "abc"
	}
	r.key(image.Rect(keyMargin, y0, keyMargin+pitch*2-keyGap, y0+keyH), sym, w.symbols)
	r.key(image.Rect(keyMargin+pitch*2, y0, keyMargin+pitch*6-keyGap, y0+keyH), "space", false)
	r.key(image.Rect(keyMargin+pitch*6, y0, keyMargin+pitch*8-keyGap, y0+keyH), "Cancel", false)
	r.key(image.Rect(keyMargin+pitch*8, y0, r.w-keyMargin, y0+keyH), "Connect", true)
}

// key draws one key.
func (r *renderer) key(rect image.Rectangle, label string, lit bool) {
	fill := shift(ember, 14)
	ink := cream
	if lit {
		fill, ink = amber, walnut
	}
	r.bevel(rect, fill, true)
	face := r.small
	if r.width(face, label) > rect.Dx()-10 {
		face = r.tiny
	}
	r.text(face, label, rect.Min.X+(rect.Dx()-r.width(face, label))/2, rect.Min.Y+rect.Dy()/2+12, ink)
}

// wifiSummary is a line for the connection.
func wifiSummary(st wifi.Status) string {
	switch {
	case st.Connected && st.Address != "":
		return fmt.Sprintf("%s  ·  %s", st.SSID, st.Address)
	case st.Connected:
		return st.SSID + "  ·  no address yet"
	case st.SSID != "":
		return st.SSID + "  ·  " + st.State
	}
	return "not connected"
}
