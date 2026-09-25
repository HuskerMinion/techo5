//go:build spot

package display

import (
	"fmt"
	"image/color"
	"math"
	"strings"
	"time"

	"golang.org/x/image/font"

	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
)

// Music on the round screen. While something plays or sits paused the idle face is now playing:
// the cover (or the station's logo) fills the circle behind the song, the artist and the station, a
// tap plays or pauses and a sideways swipe steps to the next station on the list. Music on the dial
// opens the station list, a ring you turn to scroll: a tap plays the one in the middle, a tap on the
// list's name at the top changes list (Home Assistant's favorites, stations near you, popular
// worldwide), and while anything plays the first row stops it.
//
// The rain map is the weather face's other side: a sideways swipe there turns between the forecast
// and the radar, and "radar" by voice opens it.

const (
	// radioIdle is how long the station list stays up untouched; radioCueFor how long Home
	// Assistant naming a station holds the now-playing face before its stream arrives.
	radioIdle   = 15 * time.Second
	radioCueFor = 20 * time.Second

	// radarStep is how long each frame of the rain map's loop shows; the newest holds for three.
	radarStep = 600 * time.Millisecond
)

var (
	colMusic = color.RGBA{60, 203, 127, 255}
	colRadar = color.RGBA{64, 214, 230, 255}
)

// radioRows is the station list as the ring shows it: a stop row first while anything plays.
func radioRows(rd home.Radio, active bool) []string {
	if !active {
		return rd.Stations
	}
	return append([]string{stopRow}, rd.Stations...)
}

const stopRow = "\x00stop"

// currentStation is the station playing, or the one tapped last until Home Assistant says.
func currentStation(rd home.Radio) string {
	if rd.Now != "" {
		return rd.Now
	}
	return rd.Chosen
}

// musicState is what the room's music is doing: this player's own stream, or the one it carries for
// Music Assistant when that is what is being heard. The face is drawn from it and taps are matched
// against it, so the rows a finger lands on are the rows on the screen.
func musicState() (playing, paused bool) { return media.Get().ScreenState() }

// stopMusic ends what plays, radio or otherwise, so the face goes back to the clock. What "stop" means
// for whoever has the music is home's to decide — a stream this device did not start is asked to stop,
// and its own is ended rather than paused — so this asks home rather than working it out again here. The
// gate used to be this player's own stream, which a carried one never satisfies: the row did nothing at
// all while Music Assistant was playing.
func stopMusic() { home.Get().Stop() }

// doneAt is the face's Done: left of play and pause, at the same height, where a thumb finds it.
const doneX, doneY, doneR = center - 88.0, 420.0, 25.0

// onDone reports whether a tap at x, y is on Done.
func onDone(x, y int) bool {
	dx, dy := float64(x)-doneX, float64(y)-doneY
	// The pill is two discs and the band between them; a little slack round it for a thumb.
	return dx >= -(18+doneR+8) && dx <= 18+doneR+8 && dy >= -(doneR+8) && dy <= doneR+8
}

// togglePlay is a tap on now playing. It goes the way the Show's tap goes, which is the only way it can
// work for a carried stream: this player's own stream is not what is playing, so asking it to pause
// would do nothing at all.
func togglePlay() {
	media.Get().Transport(media.TransportToggle)
}

// stepStation plays the station after (or before) the one playing, round the current list.
func stepStation(by int) {
	rd := home.Get().Radio()
	if len(rd.Stations) == 0 {
		return
	}
	i := -1
	cur := currentStation(rd)
	for k, s := range rd.Stations {
		if strings.EqualFold(s, cur) {
			i = k
		}
	}
	i = ((i+by)%len(rd.Stations) + len(rd.Stations)) % len(rd.Stations)
	home.Get().Play(rd.Stations[i])
}

// showsNowPlaying is whether the idle face belongs to music: something playing or paused, or a
// station Home Assistant just named that is still on its way.
func (d *Display) showsNowPlaying() bool {
	if playing, paused := media.Get().Playing(); playing || paused {
		return true
	}
	// A stream this player is only carrying is still what the room is doing, so the face is its as
	// much as the radio's: the state it draws was already being worked out and only this gate was
	// missing, because a carried stream never satisfies Playing().
	if media.Get().ExternalPlaying() {
		return true
	}
	if _, _, _, ok := media.Get().Held(); ok {
		return true
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return time.Since(d.radioCue) < radioCueFor
}

// pickStation is a tap on the station list: stop, or play the row.
func pickStation(sel int) {
	rd := home.Get().Radio()
	playing, paused := musicState()
	rows := radioRows(rd, playing || paused)
	if sel < 0 || sel >= len(rows) {
		return
	}
	if rows[sel] == stopRow {
		stopMusic()
		return
	}
	home.Get().Play(rows[sel])
}

// nowPlayingFace is the idle face while music plays or waits paused, laid out as the Show's: the time
// and date along the top, the station's picture (the song's cover when it has one, else the station's
// logo) in the middle, then the station, the song and the artist.
func (r *roundRenderer) nowPlayingFace(s roundScene) {
	rd := s.radio
	r.centered(r.title, clockText(s.now), 84, colText)
	r.dateWeather(s.weather, s.now, 114)

	const artY, artR = 200.0, 72.0
	r.discAt(center, artY, artR+3, color.RGBA{44, 50, 60, 255})
	if rd.Thumb != nil {
		if rd.Logo {
			r.discAt(center, artY, artR, color.RGBA{236, 240, 244, 255}) // a logo reads best on white
		}
		r.discImage(rd.Thumb, center, artY, artR)
	} else {
		r.discAt(center, artY, artR, color.RGBA{24, 28, 34, 255})
		r.notesMark(center, artY, 38, colMusic)
	}
	if s.paused {
		// Paused: the picture dims under a play mark.
		r.discAt(center, artY, artR, color.RGBA{0, 0, 0, 140})
		r.triangle(center-16, artY-26, center-16, artY+26, center+28, artY, colText)
	}

	station := currentStation(rd)
	if station == "" {
		station = "Music"
	}
	label := "PLAYING"
	if s.paused {
		label = "PAUSED"
	}
	label = clip(r.label, r, label+" · "+strings.ToUpper(station), 330)
	r.centered(r.label, label, 304, colMusic)
	if rd.Title != "" {
		r.centered(r.title, clip(r.title, r, rd.Title, 350), 340, colText)
		if rd.Artist != "" {
			r.centered(r.body, clip(r.body, r, rd.Artist, 330), 370, colDim)
		}
	} else {
		r.centered(r.title, clip(r.title, r, station, 350), 344, colText)
	}
	// Done on the left: the music ends and the face goes back to the clock. A pause keeps this face up
	// with play on it, and so does a stop from Music Assistant, which looks the same from here.
	r.discAt(doneX-18, doneY, doneR, color.RGBA{36, 42, 52, 255})
	r.discAt(doneX+18, doneY, doneR, color.RGBA{36, 42, 52, 255})
	r.line(doneX-18, doneY, doneX+18, doneY, 2*doneR, color.RGBA{36, 42, 52, 255})
	r.centered2(r.small, "Done", int(doneX), int(doneY)+6, colText)
	// The star on the right saves what is playing to favorites, and fills in once it has.
	r.discAt(starX, starY, starR, color.RGBA{36, 42, 52, 255})
	starColor := color.RGBA{150, 158, 170, 255}
	if spotStarFilled(rd) {
		starColor = colMusic
	}
	r.starMark(starX, starY, 14, starColor)
	// Play or pause at the bottom; a tap anywhere else does the same.
	const by = 420.0
	r.discAt(center, by, 25, color.RGBA{36, 42, 52, 255})
	if s.paused {
		r.triangle(center-8, by-13, center-8, by+13, center+14, by, colText)
	} else {
		r.line(center-7, by-11, center-7, by+11, 6, colText)
		r.line(center+7, by-11, center+7, by+11, 6, colText)
	}
}

// notesMark is two beamed notes, centered at x, y, u half their size.
func (r *roundRenderer) notesMark(x, y, u float64, c color.RGBA) {
	w := math.Max(u*0.12, 2.5)
	r.discAt(x-0.55*u, y+0.55*u, 0.28*u, c)
	r.discAt(x+0.45*u, y+0.4*u, 0.28*u, c)
	r.line(x-0.3*u, y+0.5*u, x-0.3*u, y-0.6*u, w, c)
	r.line(x+0.7*u, y+0.35*u, x+0.7*u, y-0.75*u, w, c)
	r.line(x-0.3*u, y-0.6*u, x+0.7*u, y-0.75*u, w*2, c)
}

// radioList is the station list's ring.
func (r *roundRenderer) radioList(s roundScene) {
	r.clear()
	rd := s.radio
	rows := radioRows(rd, s.playing || s.paused)
	r.sourceButtons(rd)

	switch {
	case len(rows) == 0 && rd.Loading:
		r.centered(r.title, "Loading…", 250, colDim)
		return
	case len(rows) == 0:
		msg := "No stations"
		if rd.Problem != "" {
			msg = rd.Problem
		} else if !rd.Configured {
			msg = "Radio needs a Home Assistant token"
		}
		r.paragraph(r.body, msg, 230, colDim, 3)
		return
	}

	sel := min(max(s.radioSel, 0), len(rows)-1)
	name := func(i int) string {
		if rows[i] == stopRow {
			return "Stop the music"
		}
		return rows[i]
	}
	cur := currentStation(rd)
	r.centered(r.small, fmt.Sprintf("%d of %d", sel+1, len(rows)), 150, colDim)
	if sel > 0 {
		r.centered(r.body, clip(r.body, r, name(sel-1), 300), 200, colDim)
	}
	col := colText
	if rows[sel] == stopRow {
		col = colMuted
	} else if strings.EqualFold(rows[sel], cur) {
		col = colMusic
	}
	y := r.paragraph(r.title, name(sel), 262, col, 2)
	if sel+1 < len(rows) {
		r.centered(r.body, clip(r.body, r, name(sel+1), 300), max(y+14, 318), colDim)
	}
	hint := "turn · tap to play"
	if rows[sel] == stopRow {
		hint = "turn · tap to stop"
	}
	r.centered(r.small, hint, 392, colDim)

	// Where in the list, round the ring.
	const from, span = 1.25 * math.Pi, 1.5 * math.Pi
	frac := 0.0
	if len(rows) > 1 {
		frac = float64(sel) / float64(len(rows)-1)
	}
	r.ringAt(center, center, 196, 202, from, from+span, color.RGBA{44, 50, 60, 255})
	kx, ky := center+199*math.Sin(from+span*frac), center-199*math.Cos(from+span*frac)
	r.discAt(kx, ky, 11, colMusic)
}

// sourceButtons are the lists along the top of the station list, the one shown lit: a tap on one
// shows it.
func (r *roundRenderer) sourceButtons(rd home.Radio) {
	sources := home.RadioSources()
	if len(sources) < 2 {
		r.centered(r.label, strings.ToUpper(home.SourceLabel(rd.Source)), 118, colMusic)
		return
	}
	for i, src := range sources {
		x0, x1 := sourceButton(i, len(sources))
		name := shortSource(src)
		cx := (x0 + x1) / 2
		if src == rd.Source {
			r.line(float64(x0+14), sourceY, float64(x1-14), sourceY, 30, colMusic)
			r.centered2(r.label, name, cx, sourceY+7, colBackground)
		} else {
			r.line(float64(x0+14), sourceY, float64(x1-14), sourceY, 30, color.RGBA{36, 42, 52, 255})
			r.centered2(r.label, name, cx, sourceY+7, colText)
		}
	}
}

// sourceY is the source buttons' row; sourceButton the x span of button i of n.
const sourceY = 120

func sourceButton(i, n int) (x0, x1 int) {
	const width = 360
	w := width / n
	x0 = center - width/2 + i*w
	return x0, x0 + w
}

// sourceAt is which list button a tap at x, y is on, or "".
func sourceAt(x, y int) string {
	sources := home.RadioSources()
	if len(sources) < 2 || y < sourceY-26 || y > sourceY+26 {
		return ""
	}
	for i, src := range sources {
		if x0, x1 := sourceButton(i, len(sources)); x >= x0 && x < x1 {
			return src
		}
	}
	return ""
}

func shortSource(src string) string {
	switch src {
	case "local":
		return "Local"
	case "popular":
		return "Popular"
	}
	return "Favorites"
}

// clip shortens a line to fit width pixels in face.
func clip(face font.Face, r *roundRenderer, s string, width int) string {
	if r.width(face, s) <= width {
		return s
	}
	rs := []rune(s)
	for len(rs) > 1 && r.width(face, string(rs)+"…") > width {
		rs = rs[:len(rs)-1]
	}
	return string(rs) + "…"
}

// radarFace is the rain map filling the circle, home marked in the middle, the loop's time on top
// and the credits the map's sources ask for at the bottom.
func (r *roundRenderer) radarFace(s roundScene) {
	r.clear()
	v := s.radar
	if len(v.Frames) == 0 {
		r.centered(r.label, "RADAR", 150, colRadar)
		msg := "Loading the rain map…"
		if !v.Loading && v.Problem != "" {
			msg = "No rain map: " + v.Problem
		}
		r.paragraph(r.body, msg, 240, colDim, 3)
		r.centered(r.small, "swipe for the forecast", 400, colDim)
		return
	}
	n := len(v.Frames)
	cycle := int64(n+2) * radarStep.Milliseconds()
	i := int(s.now.UnixMilli() % cycle / radarStep.Milliseconds())
	if i >= n {
		i = n - 1
	}
	f := v.Frames[i]
	r.coverCircle(f.Image, false)

	// Home: the frame is centered on it, and the circle crops round the middle.
	r.ringAt(center, center, 6, 10, 0, 2*math.Pi, color.RGBA{0, 0, 0, 200})
	r.ringAt(center, center, 7, 9, 0, 2*math.Pi, colRadar)

	label := "Radar " + clockHM(f.At.Local())
	if i == n-1 {
		label += " (latest)"
	}
	w := r.width(r.label, label)
	r.line(float64(center-w/2-10), 62, float64(center+w/2+10), 62, 32, color.RGBA{0, 0, 0, 160})
	r.centered(r.label, label, 69, colText)
	credit := "RainViewer · © OpenStreetMap"
	cw := r.width(r.tiny, credit)
	r.line(float64(center-cw/2-8), 420, float64(center+cw/2+8), 420, 24, color.RGBA{0, 0, 0, 160})
	r.centered(r.tiny, credit, 425, colDim)
}
