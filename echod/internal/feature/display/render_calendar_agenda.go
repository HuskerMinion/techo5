//go:build !dot && !spot

package display

import (
	"image"
	"strconv"
)

// calendarAgendaRefined is Coming up with one line an event: its start, its title and, when it has one,
// its place by a pin. Each day is headed by its name, its date or how many days off it is, and its
// weather with the high and the low; today's events carry their countdown, the next one's filled in
// the accent. Twice the events of the two-line list fit, and a tap does what it does there.
func (r *renderer) calendarAgendaRefined(v calendarView, l calLook) {
	x := r.calButton("Done", r.w-r.margin, calDone)
	x = r.calButton("Month", x, calMonthView)
	r.text(r.title, clipText(r, r.title, "Coming up", x-r.margin-r.s(10)), r.margin, r.margin+r.s(34), cream)

	rows := agendaRows(v.events, v.now)
	top, bottom := r.margin+r.s(52), r.h-r.s(4)
	height := func(a agendaRow) int {
		if a.ev == nil {
			return r.s(29)
		}
		return r.s(33)
	}
	most, used := len(rows), 0
	for most > 0 && used+height(rows[most-1]) <= bottom-top {
		most--
		used += height(rows[most])
	}
	r.calMu.Lock()
	r.calAgendaMax = most
	r.calMu.Unlock()
	if len(rows) == 0 {
		msg := "Nothing in the next two weeks"
		if !v.loaded {
			msg = "Reading the calendar…"
		}
		r.text(r.body, msg, r.margin, top+r.s(80), dim)
		return
	}

	right := r.w - r.margin
	timeW := max(r.width(r.tiny, clockText(atHour(v.now, 10))), r.width(r.tiny, "All day")) + r.s(14)
	first := min(max(v.scroll, 0), most)
	filled := true // the first countdown is the next event's, in the accent
	y, shown := top, 0
	defer func() {
		// A swipe moves a page of these rows less one, however many the page held.
		r.calMu.Lock()
		r.calAgendaStep = max(shown-1, 1)
		r.calMu.Unlock()
	}()
	for i := first; i < len(rows) && y+height(rows[i]) <= bottom; i++ {
		shown++
		row := rows[i]
		h := height(row)
		if row.ev == nil {
			if i+1 < len(rows) && y+h+height(rows[i+1]) > bottom {
				break // a heading with none of its day's events under it is left for the next page
			}
			ahead := daysBetween(v.now, row.day)
			name := agendaDayName(row.day, v.now)
			r.text(r.tiny, name, r.margin, y+r.s(22), amber)
			extra := "in " + strconv.Itoa(ahead) + " days"
			if ahead < 2 {
				extra = row.day.Format("Mon, Jan 2")
			}
			r.text(r.micro, extra, r.margin+r.width(r.tiny, name)+r.s(12), y+r.s(22), dim)
			r.dayWeather(v.forecast, row.day, right, y+r.s(22), r.micro, 22, false)
			r.fxLine(float64(r.margin), float64(y+h-r.s(3)), float64(right), float64(y+h-r.s(3)), float64(r.s(2)), ember, 1)
			r.calHitAdd(calHit{r: image.Rect(r.margin, y, right, y+h), kind: calDay, day: row.day})
			y += h
			continue
		}
		e := *row.ev
		c := v.color(e.Calendar)
		box := image.Rect(r.margin, y+r.s(2), right, y+h-r.s(1))
		r.roundRect(box, r.s(8), calWash(10))
		cy := (box.Min.Y + box.Max.Y) / 2
		r.roundRect(image.Rect(box.Min.X+r.s(8), cy-r.s(9), box.Min.X+r.s(13), cy+r.s(9)), r.s(2), c)
		base := cy + capHeight(r.tiny)/2
		tx := box.Min.X + r.s(24)
		when := "All day"
		if !e.AllDay {
			when = clockText(e.Start)
			if !sameDay(e.Start, row.day) {
				when = "Until " + clockText(e.End)
			}
		}
		r.text(r.tiny, when, tx, base, dim)
		tx += timeW
		chipAt := right - r.s(8)
		if l.countdowns && !e.AllDay && sameDay(row.day, v.now) {
			if t := countdownText(v.now, e.Start, e.End); t != "" {
				chipAt = r.countChip(t, chipAt, cy, filled, r.micro) - r.s(10)
				filled = false
			}
		}
		if icon := keywordIcon(e.Summary); icon != "" && l.icons {
			r.mdiAt(icon, tx+r.s(11), cy, 22, c)
			tx += r.s(30)
		}
		title := clipText(r, r.tiny, e.Summary, chipAt-tx-r.s(10))
		r.text(r.tiny, title, tx, base, cream)
		tx += r.width(r.tiny, title) + r.s(18)
		if e.Location != "" && chipAt-tx > r.s(60) {
			r.mdiAt("map-marker", tx+r.s(8), cy, 18, dim)
			r.text(r.micro, clipText(r, r.micro, e.Location, chipAt-tx-r.s(22)), tx+r.s(20), cy+capHeight(r.micro)/2, dim)
		}
		r.calHitAdd(calHit{r: box, kind: calEvent, ev: e})
		y += h
	}
	// A list longer than the page: a bar at the edge shows where in it the page is, and a swipe moves it.
	if most > 0 {
		r.scrollBar(top, bottom, float64(first)/float64(len(rows)), float64(len(rows)-most)/float64(len(rows)))
	}
}

// scrollBar is the thin bar at the right edge of a list or a day's hours, from top to bottom, its thumb
// at from for a part long, both as parts of the whole.
func (r *renderer) scrollBar(top, bottom int, from, long float64) {
	x := r.w - r.s(18)
	r.roundRect(image.Rect(x, top, x+r.s(4), bottom), r.s(2), calWash(14))
	y0 := top + int(float64(bottom-top)*from)
	y1 := top + int(float64(bottom-top)*min(from+long, 1))
	if y1-y0 < r.s(12) {
		y1 = min(y0+r.s(12), bottom)
	}
	r.roundRect(image.Rect(x, y0, x+r.s(4), y1), r.s(2), dim)
}
