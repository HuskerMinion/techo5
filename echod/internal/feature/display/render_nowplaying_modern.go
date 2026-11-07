//go:build !dot && !spot

package display

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"time"
)

// modernMusicLayout leaves room above the cover for the clock and weather and below it for
// Done and Favorites. The rest of the page is one column for the track and its controls.
func (r *renderer) modernMusicLayout() (cover, details image.Rectangle) {
	side := min(r.w*42/100, r.h-r.s(168))
	top := (r.h - side) / 2
	cover = image.Rect(r.margin, top, r.margin+side, top+side)
	details = image.Rect(cover.Max.X+r.s(36), top, r.w-r.margin, top+side)
	return
}

// musicButtons is shared by drawing and touch hit testing.
func (r *renderer) musicButtons(modern bool) (back, play, next image.Rectangle) {
	if !modern {
		return r.transportButtons()
	}
	_, details := r.modernMusicLayout()
	size, gap := r.s(62), r.s(24)
	x := details.Min.X + (details.Dx()-3*size-2*gap)/2
	y := r.h/2 + r.s(105)
	back = image.Rect(x, y, x+size, y+size)
	play = back.Add(image.Pt(size+gap, 0))
	next = play.Add(image.Pt(size+gap, 0))
	return
}

func (r *renderer) modernNowPlaying(s scene) {
	ground := color.RGBA{16, 21, 28, 255}
	surface := color.RGBA{32, 42, 54, 255}
	ink := color.RGBA{242, 246, 250, 255}
	quiet := color.RGBA{174, 188, 201, 255}
	accent := color.RGBA{163, 226, 211, 255}
	draw.Draw(r.dst, r.dst.Rect, image.NewUniform(ground), image.Point{}, draw.Src)

	cover, details := r.modernMusicLayout()
	if art := s.radio.Thumb; art != nil && art.Bounds().Size() == cover.Size() {
		if s.radio.Logo {
			r.roundRect(cover, r.s(12), surface)
		}
		draw.Draw(r.dst, cover, art, art.Bounds().Min, draw.Over)
	} else {
		r.roundRect(cover, r.s(12), surface)
		r.iconIn(cover, "music-note", 88, quiet)
	}

	rd := s.radio
	source := rd.Now
	if source == "" {
		source = rd.Chosen
	}
	headline, label := rd.Title, source
	if headline == "" {
		headline, label = source, ""
	}
	if headline == "" {
		headline = tr("music")
	}
	if s.paused {
		label = tr("drawer_paused")
	} else if label == "" {
		label = tr("now_playing")
	}
	x, width, middle := details.Min.X, details.Dx(), r.h/2
	r.text(r.small, r.clipTo(r.small, label, width), x, middle-r.s(113), accent)
	y := middle - r.s(69)
	lines := r.wrap(r.title, headline, width)
	if len(lines) > 2 {
		lines[1] += " " + lines[2]
		lines = lines[:2]
	}
	for _, line := range lines {
		r.text(r.title, r.clipTo(r.title, line, width), x, y, ink)
		y += r.s(47)
	}
	r.text(r.small, r.clipTo(r.small, rd.Artist, width), x, y-r.s(8), quiet)
	if s.hasLyric {
		line := s.lyric.Line
		if line == "" {
			line = "· · ·"
		}
		r.text(r.tiny, r.clipTo(r.tiny, line, width), x, middle+r.s(40), ink)
		r.text(r.micro, r.clipTo(r.micro, s.lyric.Next, width), x, middle+r.s(61), quiet)
	}

	if p := s.musicPosition; p.Dur > 0 {
		elapsed := p.Now(s.now)
		bar := image.Rect(x, middle+r.s(69), details.Max.X, middle+r.s(75))
		r.roundRect(bar, bar.Dy()/2, surface)
		bar.Max.X = bar.Min.X + int(float64(bar.Dx())*float64(elapsed)/float64(p.Dur))
		if !bar.Empty() {
			r.roundRect(bar, min(bar.Dy(), bar.Dx())/2, accent)
		}
		total := musicTime(p.Dur)
		y := middle + r.s(94)
		r.text(r.micro, musicTime(elapsed), x, y, quiet)
		r.text(r.micro, total, details.Max.X-r.width(r.micro, total), y, quiet)
	}

	back, play, next := r.musicButtons(true)
	r.roundRect(back, back.Dy()/2, surface)
	r.roundRect(play, play.Dy()/2, accent)
	r.roundRect(next, next.Dy()/2, surface)
	toggle := "play"
	if s.playing && !s.paused {
		toggle = "pause"
	}
	r.iconIn(back, "skip-previous", transportIcon, ink)
	r.iconIn(play, toggle, transportIcon, ground)
	r.iconIn(next, "skip-next", transportIcon, ink)
	r.footControls(r.doneButton().Min.Y - r.s(10))
	onPhoto(func() {
		r.cornerClockDated(s)
		r.weatherCornerAt(s, false)
		r.musicFooter(s)
	})
}

func musicTime(d time.Duration) string {
	seconds := int64(d / time.Second)
	if seconds >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", seconds/3600, seconds/60%60, seconds%60)
	}
	return fmt.Sprintf("%d:%02d", seconds/60, seconds%60)
}
