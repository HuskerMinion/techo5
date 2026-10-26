//go:build !dot && !spot

package display

import (
	"bufio"
	"fmt"
	"image"
	"image/color"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/image/font"

	"github.com/HuskerMinion/techo5/echod/internal/layout"
	"github.com/HuskerMinion/techo5/echod/internal/lib/fonts"
	"github.com/HuskerMinion/techo5/echod/internal/lib/locale"
)

// The Commodore 64 skins (skin.go): the home clock is the computer's screen. A thin light blue border,
// the same all round, and the blue screen filling the rest with 8 by 8 characters. Each of their pixels
// is a whole number of the panel's, so they stay sharp and square: as large as lets the computer's 40
// columns and 25 rows fit (two on the Show 5, three on the Show 8, five on the Skylight), with as many
// more columns and rows as the panel has room for, rather than the computer's wide border. On it the
// start-up banner, READY., the clock as PRINT TI$ answers it in characters as tall as fit, the date and
// the weather, any timers and the next alarm as lines of their own, and the cursor blinking under them
// as the computer's does. Every line has an empty row above and below it.
//
// Commodore 64 shows the computer's own banner. TECHO5 BASIC shows the version running and this
// device's memory: all of it, and what is free, read as the screen is drawn.

// The skin's colors: the border, the screen, the characters, and the clock's white.
var (
	c64Border = rgb(0x7a6ccf)
	c64Screen = rgb(0x3e3196)
	c64Ink    = rgb(0x8f82e0)
	c64White  = rgb(0xffffff)
)

const (
	c64Cols, c64Rows = 40, 25 // the computer's screen, which the characters' size is chosen to fit

	// c64Blink is how long the cursor is lit and then dark: the computer's 20 frames of its 60.
	c64Blink = 333 * time.Millisecond

	// c64ClockMin and c64ClockMax bound how tall the clock's characters are, in rows.
	c64ClockMin, c64ClockMax = 4, 10
)

// c64Skin is whether a Commodore 64 skin is on.
func c64Skin() bool {
	sk := skin()
	return sk == skinC64 || sk == skinBASIC
}

// c64Grid is the screen's characters on a panel w by h inside a border b: the top left of the first,
// the size of a character cell, and how many columns and rows there are.
func c64Grid(w, h, b int) (x0, y0, cell, cols, rows int) {
	px := max(min((w-2*b)/(c64Cols*8), (h-2*b)/(c64Rows*8)), 1)
	cell = 8 * px
	cols, rows = (w-2*b)/cell, (h-2*b)/cell
	return (w - cols*cell) / 2, (h - rows*cell) / 2, cell, cols, rows
}

// c64Face is the pixel font with each pixel px of the panel's.
func (r *renderer) c64Face(cell int) font.Face {
	if f, ok := r.c64Faces[cell]; ok {
		return f
	}
	f, err := fonts.Face(fonts.C64, false, float64(cell)/0.5) // the family is drawn at half size (fonts.go)
	if err != nil {
		return r.small
	}
	if r.c64Faces == nil {
		r.c64Faces = map[int]font.Face{}
	}
	r.c64Faces[cell] = f
	return f
}

// c64Home is the home clock as the computer's screen.
func (r *renderer) c64Home(s scene) {
	r.c64Drawn = true
	// The screen covers any photo behind the clock, so its words are drawn plainly: no halo, no gold.
	over := r.over
	r.over.photo, r.over.record = nil, false
	defer func() { r.over.photo, r.over.record = over.photo, over.record }()
	b := r.s(20)
	x0, y0, cell, cols, rows := c64Grid(r.w, r.h, b)
	r.fillRect(image.Rect(0, 0, r.w, r.h), c64Border)
	r.fillRect(image.Rect(b, b, r.w-b, r.h-b), c64Screen)

	// The clock as tall as the rows and columns allow: its five characters and AM or PM beside them.
	big := min(max(min(rows-17, (cols-8)/5), c64ClockMin), c64ClockMax)

	var lines []string
	for _, t := range runningTimers(s) {
		name := strings.ToUpper(t.Name)
		if name == "" {
			name = "TIMER"
		}
		lines = append(lines, fmt.Sprintf("%s %s", name, c64Left(t.Left)))
	}
	if next := s.alarms.Next; next != nil && next.At.Sub(s.now) < 24*time.Hour {
		lines = append(lines, strings.ToUpper(locale.Alarm(screenLang(), next.Snoozed)+" "+clockText(next.At)))
	}

	// On a panel taller than wide what the screen shows is centered among its rows, the rest left blue.
	off := 0
	if r.h > r.w {
		off = max(rows-(15+big+2*len(lines)), 0) / 2
		rows -= off
	}

	// left is where a line at row starts: the screen's edge, or just past the Call button when it is on
	// the clock and the line would run under it.
	left := func(row int) int {
		if !s.callButton {
			return 0
		}
		btn, y := r.callButtonRect(), y0+(row+off)*cell
		if y+cell <= btn.Min.Y || y >= btn.Max.Y {
			return 0
		}
		return (btn.Max.X-x0)/cell + 1
	}

	// put writes text at a row and column, scale rows tall; a column below 0 centers it.
	put := func(row, col int, text string, c color.RGBA, scale int) image.Rectangle {
		if col == 0 {
			col = left(row)
		}
		row += off
		n := utf8.RuneCountInString(text)
		if n > cols/scale {
			text = string([]rune(text)[:cols/scale])
			n = cols / scale
		}
		if col < 0 {
			col = (cols - n*scale) / 2
		}
		x, y := x0+col*cell, y0+row*cell
		r.text(r.c64Face(cell*scale), text, x, y+7*cell*scale/8, c)
		return image.Rect(x, y, x+n*cell*scale, y+cell*scale)
	}

	top, second := c64Banner(skin())
	put(1, -1, top, c64Ink, 1)
	put(3, -1, second, c64Ink, 1)
	put(5, 0, "READY.", c64Ink, 1)
	put(7, 0, "PRINT TI$", c64Ink, 1)

	row := 9
	clock := put(row, -1, clockHM(s.now), c64White, big)
	if ampm := clockSuffix(s.now); ampm != "" {
		// Beside the figures' foot, a column clear of them.
		put(row+big-1, (clock.Max.X-x0)/cell+1, strings.ToUpper(ampm), c64Ink, 1)
	}
	row += big + 1

	date := strings.ToUpper(locale.ShortDate(s.now, screenLang()))
	line := date
	if w := s.weather; w.Temp != "" || w.Condition != "" {
		line += "   " + strings.ToUpper(strings.TrimSpace(w.Temp+" "+conditionWords(w.Condition)))
	}
	r.setDateAt(put(row, -1, line, c64Ink, 1).Inset(-r.s(16)))
	row += 2

	// Then a line for each running timer and the next alarm, while there is room for them and the
	// prompt under them.
	for _, l := range lines {
		if row+4 > rows {
			break
		}
		put(row, 0, l, c64Ink, 1)
		row += 2
	}
	if row+2 <= rows {
		put(row, 0, "READY.", c64Ink, 1)
		row += 2
	}
	if row < rows && s.now.UnixMilli()/c64Blink.Milliseconds()%2 == 0 {
		x := x0 + left(row)*cell
		r.fillRect(image.Rect(x, y0+(row+off)*cell, x+cell, y0+(row+off+1)*cell), c64Ink)
	}
	r.alertBadge(s)
}

// c64Left is a timer's time left as the screen shows it: minutes and seconds, hours when there are any.
func c64Left(d time.Duration) string {
	d = d.Round(time.Second)
	if d >= time.Hour {
		return fmt.Sprintf("%d:%02d:%02d", int(d.Hours()), int(d.Minutes())%60, int(d.Seconds())%60)
	}
	return fmt.Sprintf("%d:%02d", int(d.Minutes()), int(d.Seconds())%60)
}

// c64Banner is the start-up banner's two lines: the computer's own, or TECHO5's with the version
// running and this device's memory.
func c64Banner(sk string) (string, string) {
	if sk != skinBASIC {
		return "**** COMMODORE 64 BASIC V2 ****", "64K RAM SYSTEM  38911 BASIC BYTES FREE"
	}
	top := "**** TECHO5 BASIC " + strings.ToUpper(layout.Version) + " ****"
	if len(top) > c64Cols {
		top = "TECHO5 BASIC " + strings.ToUpper(layout.Version)
	}
	total, free := memory()
	second := fmt.Sprintf("%dM RAM SYSTEM  %d BYTES FREE", total>>20, free)
	if len(second) > c64Cols {
		second = fmt.Sprintf("%dM RAM  %d BYTES FREE", total>>20, free)
	}
	return top, second
}

// memory is the device's memory and how much of it is free to use, in bytes, from /proc/meminfo;
// read again at most every few seconds, as the screen redraws often.
func memory() (total, free uint64) {
	mem.mu.Lock()
	defer mem.mu.Unlock()
	if time.Since(mem.at) < 5*time.Second {
		return mem.total, mem.free
	}
	mem.at = time.Now()
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return mem.total, mem.free
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		n, err := strconv.ParseUint(strings.TrimSuffix(strings.TrimSpace(v), " kB"), 10, 64)
		if err != nil {
			continue
		}
		switch k {
		case "MemTotal":
			mem.total = n << 10
		case "MemAvailable":
			mem.free = n << 10
		}
	}
	return mem.total, mem.free
}

var mem struct {
	mu          sync.Mutex
	at          time.Time
	total, free uint64
}
