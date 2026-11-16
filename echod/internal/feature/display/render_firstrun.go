//go:build !dot && !spot

package display

import (
	"fmt"
	"image"
	"image/color"
	"time"

	"golang.org/x/image/font"
)

// firstRun draws first run's step (firstrun.go): the welcome card at either end, and between them a
// card with a title, a few lines and the step's buttons along its foot. The name step lays the rooms
// out over the lines' foot, and the Home Assistant step's key view puts the key's QR code at the right.
func (r *renderer) firstRun(s scene, v firstRunView) {
	switch v.step {
	case frHello:
		r.welcomeCard(tr("firstrun_tap_to_begin"))
		return
	case frDone:
		r.welcomeCard(tr("firstrun_tap_to_finish"))
		return
	}
	r.useFont()
	if v.typing != "" {
		fillCanvas(r.dst, walnut)
		title, goLabel := tr("firstrun_name_this_device"), tr("firstrun_save")
		if v.typing == "room" {
			title = tr("firstrun_the_room_it_is_in")
		}
		shown, message := v.text, false
		if v.typeErr != "" {
			shown, message = v.typeErr, true
		}
		r.keyboard(title, shown, message, v.shifted, v.symbols, goLabel)
		return
	}
	r.bevel(image.Rect(r.margin, r.s(40), r.w-r.margin, r.h-r.s(40)), shift(walnut, 10), true)
	left := r.margin + r.s(40)
	room := r.w - 2*r.margin - r.s(80)
	keyView := v.step == frHome && !v.home && v.adopt.IsZero() && v.showKey && v.key != "" && v.wifi.Address != ""
	var code image.Rectangle
	if keyView {
		// The QR code holds the key and nothing else: a phone reads it and pastes it into Home
		// Assistant's box. The typed key stays beside it.
		side := r.s(200)
		code = image.Rect(r.w-r.margin-r.s(40)-side, r.s(140), r.w-r.margin-r.s(40), r.s(140)+side)
		room = code.Min.X - left - r.s(24)
	}
	title, lines, note := firstRunText(v, s.now)
	r.text(r.title, title, left, r.s(118), amber)
	count := fmt.Sprintf(tr("firstrun_step_d_of_d"), v.number, v.of)
	r.text(r.small, count, r.w-r.margin-r.s(40)-r.width(r.small, count), r.s(110), dim)
	buttons := firstRunButtons(v)
	placed, y := r.frText(lines, note, room, r.frFloor(v, buttons))
	for _, l := range placed {
		r.text(l.face, l.text, left, l.y, l.col)
	}
	if keyView {
		// Large, in as many pieces as it takes to fit: it is read off the screen and typed.
		y += r.s(6)
		face, step, parts := r.frKeyLines(v.key, room)
		for _, part := range parts {
			r.text(face, part, left, y, amber)
			y += step
		}
		r.qrCode(v.key, code)
	}
	if v.step == frName && !v.home && !v.renaming {
		r.keyRow(r.frRoomRects(), frRoomLabels(), func(i int) bool {
			return frRooms[i] == v.room || (frRooms[i] == "Other" && v.room != "" && !isListedRoom(v.room))
		})
	}
	for i, b := range r.frButtonRects(buttons) {
		last := i == len(buttons)-1
		fill, ink := shift(walnut, 30), cream
		if last {
			fill, ink = amber, walnut
		}
		r.bevel(b, fill, true)
		label := buttons[i].label
		r.text(r.body, label, b.Min.X+(b.Dx()-r.width(r.body, label))/2, b.Min.Y+b.Dy()/2+r.s(12), ink)
	}
}

// placedLine is a line of words with where and how it is drawn.
type placedLine struct {
	face font.Face
	text string
	y    int // its baseline
	col  color.RGBA
}

// frText lays a step's lines and its note out under the title, from s(170): in the usual size, or
// smaller when that would run into what is under them (floor, the top of the buttons or the rooms), as
// a longer language's words can. It returns the lines and the baseline after the last. wrapLines, not
// wrap: a Japanese paragraph has no spaces to break on.
func (r *renderer) frText(lines []string, note string, room, floor int) ([]placedLine, int) {
	lay := func(face font.Face, pitch int) ([]placedLine, int) {
		var out []placedLine
		y := r.s(170)
		for _, para := range lines {
			for _, line := range r.wrapLines(face, para, room) {
				out = append(out, placedLine{face, line, y, cream})
				y += pitch
			}
		}
		if note != "" {
			for _, line := range r.wrapLines(r.tiny, note, room) {
				out = append(out, placedLine{r.tiny, line, y, dim})
				y += r.s(32)
			}
		}
		return out, y
	}
	out, y := lay(r.small, r.s(40))
	if len(out) > 0 && out[len(out)-1].y+r.s(10) > floor {
		out, y = lay(r.tiny, r.s(32))
	}
	return out, y
}

// frKeyLines are the key in the key view: large, in as many pieces as it takes to fit room, or in the
// lines' size when the large one takes more than two; with the face and the step between them.
func (r *renderer) frKeyLines(key string, room int) (font.Face, int, []string) {
	face, step := r.body, r.s(44)
	parts := splitToFit(key, func(p string) bool { return r.width(face, p) <= room })
	if len(parts) > 2 {
		face, step = r.small, r.s(40)
		parts = splitToFit(key, func(p string) bool { return r.width(face, p) <= room })
	}
	return face, step, parts
}

// frFloor is the top of what is under a step's lines: the rooms on the name step, else the buttons,
// else the card's foot.
func (r *renderer) frFloor(v firstRunView, buttons []frButton) int {
	switch {
	case v.step == frName && !v.home && !v.renaming:
		return r.frRoomRects()[0].Min.Y
	case len(buttons) > 0:
		return r.h - r.s(64) - r.s(72)
	}
	return r.h - r.s(40)
}

// frRoomLabels are the rooms' buttons. The rooms are saved as they are, and Home Assistant shows them
// as its areas, so they keep their words; Other is a button, not a room, and is in the screen's.
func frRoomLabels() []string {
	labels := append([]string(nil), frRooms...)
	labels[len(labels)-1] = tr("firstrun_other_room")
	return labels
}

// isListedRoom is whether room is one of the rooms offered, rather than one typed under Other.
func isListedRoom(room string) bool {
	for _, k := range frRooms[:len(frRooms)-1] {
		if k == room {
			return true
		}
	}
	return false
}

// frRoomRects are where the name step's rooms go: two rows of four, over the buttons.
func (r *renderer) frRoomRects() []image.Rectangle {
	left := r.margin + r.s(40)
	room := r.w - 2*r.margin - r.s(80)
	gap, h := r.s(14), r.s(50)
	per := (len(frRooms) + 1) / 2
	w := (room - (per-1)*gap) / per
	top := r.h - r.s(64) - r.s(72) - r.s(16) - 2*h - gap
	out := make([]image.Rectangle, len(frRooms))
	for i := range frRooms {
		x, y := left+(i%per)*(w+gap), top+(i/per)*(h+gap)
		out[i] = image.Rect(x, y, x+w, y+h)
	}
	return out
}

// frButtonRects are where a step's buttons go: along the card's foot, from the right, the last one
// rightmost, each as wide as its label needs. Taps are read against the same rectangles.
func (r *renderer) frButtonRects(buttons []frButton) []image.Rectangle {
	r.useFont()
	bottom := r.h - r.s(64)
	top := bottom - r.s(72)
	gap := r.s(24)
	right := r.w - r.margin - r.s(40)
	out := make([]image.Rectangle, len(buttons))
	for i := len(buttons) - 1; i >= 0; i-- {
		w := max(r.s(180), r.width(r.body, buttons[i].label)+r.s(56))
		out[i] = image.Rect(right-w, top, right, bottom)
		right -= w + gap
	}
	return out
}

// firstRunText is a step's title, its lines, and a smaller note under them, in the screen's language.
// Names, the network and addresses go into the words as they are.
func firstRunText(v firstRunView, now time.Time) (string, []string, string) {
	switch v.step {
	case frWifi:
		if !v.wifi.Connected {
			return tr("firstrun_title_wifi"), []string{
				fmt.Sprintf(tr("firstrun_wifi_none"), v.name),
				tr("firstrun_wifi_needed"),
			}, ""
		}
		lines := []string{fmt.Sprintf(tr("firstrun_wifi_connected_to"), v.wifi.SSID)}
		if v.wifi.Address != "" {
			lines = append(lines, fmt.Sprintf(tr("firstrun_wifi_address"), v.wifi.Address))
		}
		return tr("firstrun_title_wifi"), lines, ""
	case frZone:
		at := now.Format("3:04 PM")
		if clock24.Load() {
			at = now.Format("15:04")
		}
		return tr("firstrun_title_zone"), []string{
			fmt.Sprintf(tr("firstrun_zone_is"), zoneValue()),
			fmt.Sprintf(tr("firstrun_zone_clock"), at),
		}, ""
	case frName:
		// One line: the rooms take the rest of the card.
		title := tr("firstrun_title_name")
		switch {
		case v.renaming:
			return title, []string{fmt.Sprintf(tr("firstrun_name_is"), v.name), tr("firstrun_renaming")}, ""
		case v.home:
			return title, []string{fmt.Sprintf(tr("firstrun_name_is"), v.name), tr("firstrun_room_from_home")}, ""
		case v.room == "":
			return title, []string{fmt.Sprintf(tr("firstrun_name_which_room"), v.name)}, ""
		}
		return title, []string{fmt.Sprintf(tr("firstrun_name_and_room"), v.name, v.room)}, ""
	case frHome:
		switch {
		case v.homeJust:
			return "Home Assistant", []string{tr("firstrun_home_added_going")}, ""
		case v.home:
			return "Home Assistant", []string{tr("firstrun_home_added")}, ""
		case v.wifi.Address == "":
			return "Home Assistant", []string{tr("firstrun_home_no_network")}, ""
		case v.unkeyed:
			return "Home Assistant", []string{tr("firstrun_home_add_it")}, tr("firstrun_home_unkeyed_note")
		case !v.adopt.IsZero():
			left := max(1, int(v.adopt.Sub(now).Minutes()+0.999))
			return "Home Assistant", []string{
				tr("firstrun_home_add_it"),
				fmt.Sprintf(tr("firstrun_home_open_d"), left),
			}, tr("firstrun_home_adopt_note")
		case v.showKey && v.key != "":
			return "Home Assistant", []string{fmt.Sprintf(tr("firstrun_home_esphome"), v.wifi.Address)}, ""
		}
		return "Home Assistant", []string{tr("firstrun_home_choose"), tr("firstrun_home_alone")}, ""
	case frUpdates:
		var lines []string
		switch {
		case !v.checked:
			lines = append(lines, tr("firstrun_updates_looking"))
		case v.offered == "":
			lines = append(lines, tr("firstrun_updates_current"))
		case v.auto:
			lines = append(lines, fmt.Sprintf(tr("firstrun_updates_tonight"), v.offered))
		default:
			lines = append(lines, fmt.Sprintf(tr("firstrun_updates_ready"), v.offered))
		}
		if v.auto {
			lines = append(lines, tr("firstrun_updates_auto"))
		} else {
			lines = append(lines, tr("firstrun_updates_manual"))
		}
		return tr("firstrun_title_updates"), lines, ""
	case frSound:
		return tr("firstrun_title_sound"), []string{tr("firstrun_sound_played")}, ""
	}
	return "", nil, ""
}
