//go:build !dot && !spot

package display

import (
	"image"
	"image/color"
	"image/draw"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/dashboard"
)

// The media sheet: a finger resting on a media player's tile on the drawn dashboard, and lifting
// without sliding or scrolling, brings up what it is playing over the page, with its controls and
// volume, Music Assistant's favorites to put on it, and the other speakers to play them on as well.
// A tap on a favorite plays it; a tap on a speaker groups it in, or takes it out again. Done, or a
// tap beside the sheet, puts it away. The controls are there at once; the favorites and speakers
// follow when Home Assistant has said what they are.

// What a part of the media sheet is, for a tap.
const (
	mediaPartNone = iota
	mediaPartDone
	mediaPartPrev
	mediaPartPlay
	mediaPartNext
	mediaPartVolume
	mediaPartFavorite
	mediaPartSpeaker
	mediaPartPage        // the favorites' block, where a finger swipes to the next page or back
	mediaPartSpeakerPage // the speakers' block, likewise
)

// mediaSheet is the sheet while it is up: the player, and what Home Assistant has said it offers.
type mediaSheet struct {
	entity  string
	name    string
	lists   dashboard.MediaLists
	loading bool
	problem string
	// grouped is the speakers playing along as last asked from here, ahead of Home Assistant saying so.
	grouped map[string]bool
	// volume is a level just set from here, ahead of Home Assistant saying so; -1 for none.
	volume float64
	// lit is the favorite just tapped, drawn lit until litUntil, so a tap is seen to have landed.
	lit      int
	litUntil time.Time
	// page is the page of favorites shown, from 0, and speakerPage the page of speakers.
	page, speakerPage int
	// speakerVol is each speaker's volume as last set from here, ahead of Home Assistant saying so.
	speakerVol map[string]float64
	// groupBase and mainBase are where the speakers grouped in and the player stood when a finger came
	// down on the volume: the whole group moves by what the finger moves it by.
	groupBase map[string]float64
	mainBase  float64
}

// speakerVolume is how loud a speaker is, as set from here or as Home Assistant last said; -1 when it
// does not say.
func (m mediaSheet) speakerVolume(entity string) float64 {
	if v, ok := m.speakerVol[entity]; ok {
		return v
	}
	for _, sp := range m.lists.Speakers {
		if sp.Entity == entity {
			return sp.Volume
		}
	}
	return -1
}

// putOn is the favorite last put on each Music Assistant player from a sheet, by its URI, for the
// sheet to mark while that player still has music of Music Assistant's: Music Assistant says which
// track plays, not which playlist it came from.
var putOn sync.Map

// tapLit is how long a tapped favorite stays lit.
const tapLit = 500 * time.Millisecond

// sheetTap is how the sheet's taps reach Home Assistant; a variable so that a test can see what they
// ask for. Back, play or pause and next name the tile's own player: the dashboard sends them to Music
// Assistant's while it plays there (dashboard.Feature.Tap), and to the tile's own while the speaker
// plays from elsewhere, where Music Assistant's would start its own old queue.
var sheetTap = func(a dashboard.Action) { dashboard.Get().Tap(a) }

// mediaView is the sheet as one frame draws it: the sheet and what the player is doing now.
type mediaView struct {
	sheet mediaSheet
	now   dashboard.MediaNow
}

// mediaWords are the sheet's words, in the screen's language: the two lists, Done, the lists
// loading, no favorites, a player Music Assistant does not play to, and the player's states.
var mediaWords = map[string][]string{
	"": {"Play", "Speakers", "Done", "Loading…", "No favorites in Music Assistant yet",
		"Music Assistant does not play here: controls only", "Playing", "Paused", "Idle", "Off",
		"Favorites need a Home Assistant administrator's token"},
	"de": {"Abspielen", "Lautsprecher", "Fertig", "Lädt …", "Noch keine Favoriten in Music Assistant",
		"Music Assistant spielt hier nicht: nur Steuerung", "Spielt", "Pausiert", "Bereit", "Aus",
		"Für Favoriten braucht es ein Token eines Home-Assistant-Administrators"},
	"es": {"Reproducir", "Altavoces", "Listo", "Cargando…", "Aún no hay favoritos en Music Assistant",
		"Music Assistant no reproduce aquí: solo controles", "Reproduciendo", "En pausa", "Inactivo", "Apagado",
		"Los favoritos necesitan un token de administrador de Home Assistant"},
	"fr": {"Écouter", "Enceintes", "OK", "Chargement…", "Pas encore de favoris dans Music Assistant",
		"Music Assistant ne joue pas ici : commandes seulement", "Lecture", "En pause", "Inactif", "Éteint",
		"Les favoris demandent un jeton d'administrateur Home Assistant"},
	"it": {"Riproduci", "Altoparlanti", "Fatto", "Caricamento…", "Ancora nessun preferito in Music Assistant",
		"Music Assistant non riproduce qui: solo comandi", "In riproduzione", "In pausa", "Inattivo", "Spento",
		"I preferiti richiedono un token di amministratore di Home Assistant"},
}

func mediaWord(i int) string {
	w, ok := mediaWords[screenLang()]
	if !ok {
		w = mediaWords[""]
	}
	return w[i]
}

// problemText is what the sheet says when its lists could not be had. Music Assistant's library is
// read with its config entry, which only an administrator's token may read: a token without that
// right is told so in words, not as Home Assistant's "401 Unauthorized".
func problemText(err error) string {
	if s := err.Error(); strings.Contains(s, "401") || strings.Contains(s, "403") {
		return mediaWord(10)
	}
	return err.Error()
}

// stateWord is a player's state in words, for the line under its name when nothing is named.
func stateWord(state string) string {
	switch state {
	case "playing":
		return mediaWord(6)
	case "paused":
		return mediaWord(7)
	case "off":
		return mediaWord(9)
	}
	return mediaWord(8)
}

// openMedia puts the media sheet up for entity, and asks Home Assistant what it offers.
func (d *Display) openMedia(entity string) bool {
	now, ok := dashboard.Get().MediaNow(entity)
	if !ok {
		return false
	}
	slog.Info("dashboard media sheet", "entity", entity)
	d.mu.Lock()
	d.dashMedia = &mediaSheet{entity: entity, name: now.Name, loading: true, grouped: map[string]bool{}, volume: -1}
	d.mu.Unlock()
	d.wake()
	go func() {
		lists, err := dashboard.LoadMedia(entity, now.Name)
		d.mu.Lock()
		if m := d.dashMedia; m != nil && m.entity == entity {
			n := *m
			n.lists, n.loading = lists, false
			if err != nil {
				n.problem = problemText(err)
				slog.Warn("dashboard media sheet", "entity", entity, "err", err)
			}
			n.grouped = map[string]bool{}
			for _, member := range lists.Members {
				n.grouped[member] = true
			}
			d.dashMedia = &n
		}
		d.mu.Unlock()
		d.wake()
	}()
	return true
}

// mediaTap is a finger lifted at x, y while the media sheet is up, which the sheet always takes. It
// says whether the sheet was up.
func (d *Display) mediaTap(x, y int) bool {
	d.mu.Lock()
	m := d.dashMedia
	d.mu.Unlock()
	if m == nil || d.r == nil {
		return false
	}
	z, _, in := d.r.mediaAt(x, y)
	tap := func(entity, service string, data map[string]any) {
		slog.Info("dashboard media", "entity", entity, "service", service)
		sheetTap(dashboard.Action{Entity: entity, Service: service, Data: data})
	}
	switch {
	case !in || z.kind == mediaPartDone:
		d.mu.Lock()
		d.dashMedia = nil
		d.mu.Unlock()
	case z.kind == mediaPartPrev:
		tap(m.entity, "media_player.media_previous_track", nil)
	case z.kind == mediaPartPlay:
		tap(m.entity, "media_player.media_play_pause", nil)
	case z.kind == mediaPartNext:
		tap(m.entity, "media_player.media_next_track", nil)
	case z.kind == mediaPartVolume:
		// As a finger sliding along it: the speakers grouped in move with it.
		if s, ok := d.sliderAt(x, y); ok {
			d.sliderBegins(s)
			d.slideSheet(s, x, true)
		}
	case z.kind == mediaPartFavorite && z.index < len(m.lists.Favorites):
		f := m.lists.Favorites[z.index]
		tap(m.lists.Target, "music_assistant.play_media", map[string]any{"media_id": f.URI, "media_type": f.Kind, "enqueue": "replace"})
		putOn.Store(m.lists.Target, f.URI)
		d.editMedia(func(n *mediaSheet) { n.lists.Queued, n.lists.Elsewhere = true, false })
		i := z.index
		d.editMedia(func(n *mediaSheet) { n.lit, n.litUntil = i, time.Now().Add(tapLit) })
		time.AfterFunc(tapLit+50*time.Millisecond, d.wake) // to draw it unlit again
	case z.kind == mediaPartSpeaker && z.index < len(m.lists.Speakers):
		sp := m.lists.Speakers[z.index]
		if m.grouped[sp.Entity] {
			tap(sp.Entity, "media_player.unjoin", nil)
		} else {
			tap(m.lists.Group, "media_player.join", map[string]any{"group_members": []any{sp.Entity}})
		}
		d.editMedia(func(n *mediaSheet) { n.grouped[sp.Entity] = !n.grouped[sp.Entity] })
	}
	d.wake()
	return true
}

// editMedia changes the sheet up now, if it is still up, without changing the one a frame is drawing.
func (d *Display) editMedia(change func(*mediaSheet)) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.dashMedia == nil {
		return
	}
	n := *d.dashMedia
	n.grouped = maps.Clone(d.dashMedia.grouped)
	n.speakerVol = maps.Clone(d.dashMedia.speakerVol)
	n.groupBase = maps.Clone(d.dashMedia.groupBase)
	if n.grouped == nil {
		n.grouped = map[string]bool{}
	}
	if n.speakerVol == nil {
		n.speakerVol = map[string]float64{}
	}
	change(&n)
	d.dashMedia = &n
}

// mediaViewNow is the sheet with what its player is doing now, for a frame; nil without a sheet.
func (d *Display) mediaViewNow() *mediaView {
	d.mu.Lock()
	m := d.dashMedia
	d.mu.Unlock()
	if m == nil {
		return nil
	}
	now, _ := dashboard.Get().MediaNow(m.entity)
	if now.Name == "" {
		now.Name = m.name
	}
	if m.volume >= 0 {
		now.Volume = m.volume
	}
	return &mediaView{sheet: *m, now: now}
}

// mediaSheet draws the media sheet over the drawn dashboard and keeps where its parts are for mediaAt.
func (r *renderer) mediaSheet(v *mediaView, th dashboard.Theme) {
	if v == nil {
		r.setMedia(nil, image.Rectangle{})
		return
	}
	m, now := v.sheet, v.now
	pal := r.palette(th)
	fc := r.faces()
	draw.Draw(r.dst, r.dst.Rect, image.NewUniform(color.RGBA{0, 0, 0, 150}), image.Point{}, draw.Over)

	pad, gap := r.s(18), r.s(10)
	titleH, subH, ctrlH, labelH, chipH, btnH := r.s(34), r.s(28), r.s(56), r.s(30), r.s(44), r.s(46)
	w := min(r.w-2*r.s(24), r.s(900))
	inner := w - 2*pad
	cols := 4
	chipW := (inner - gap*(cols-1)) / cols
	rows := func(n int) int { return (n + cols - 1) / cols }

	// What there is room for: the controls always, then the speakers, then as many rows of favorites
	// as are left.
	fixed := pad + titleH + subH + ctrlH + gap + btnH + pad
	speakerRows := min(rows(len(m.lists.Speakers)), 2)
	speakersPer := max(speakerRows*cols, 1)
	speakerPages := (len(m.lists.Speakers) + speakersPer - 1) / speakersPer
	speakerPage := min(max(m.speakerPage, 0), max(speakerPages-1, 0))
	if speakerRows > 0 {
		fixed += labelH + speakerRows*(chipH+gap)
	}
	if speakerPages > 1 {
		fixed += r.s(16)
	}
	favRows := rows(len(m.lists.Favorites))
	room := (r.h - r.s(16) - fixed - labelH) / (chipH + gap)
	favRows = max(min(favRows, room), 0)
	// The favorites a page holds, and the pages there are: more than fit are paged through.
	perPage := max(favRows*cols, 1)
	pages := (len(m.lists.Favorites) + perPage - 1) / perPage
	page := min(max(m.page, 0), max(pages-1, 0))
	dotsH := 0
	if pages > 1 {
		dotsH = r.s(16)
	}
	// Without Music Assistant there is no music to choose, and no list to say so about.
	music := m.loading || m.lists.MusicAssistant
	notice := m.loading || m.lists.Target == "" || len(m.lists.Favorites) == 0
	h := fixed
	switch {
	case !music:
	case notice:
		h += labelH + r.s(30)
	default:
		h += labelH + favRows*(chipH+gap) + dotsH
	}
	x0, y0 := (r.w-w)/2, max((r.h-h)/2, r.s(8))
	card := image.Rect(x0, y0, x0+w, y0+h)
	r.roundFill(card, pal.rad, pal.card, pal.card)

	var zones []mediaZone
	y := y0 + pad
	r.text(fc.labelBold, r.fit(fc.labelBold, now.Name, inner), x0+pad, y+r.s(26), pal.text)
	y += titleH
	line := now.Title
	if now.Artist != "" && line != "" {
		line += " · " + now.Artist
	}
	if line == "" || (now.State != "playing" && now.State != "paused") {
		line = stateWord(now.State)
	}
	r.text(fc.sub, r.fit(fc.sub, line, inner), x0+pad, y+r.s(20), pal.sub)
	y += subH

	// The controls, and the volume beside them.
	bs := ctrlH
	icons := []struct {
		name string
		kind int
	}{{"skip-previous", mediaPartPrev}, {"play", mediaPartPlay}, {"skip-next", mediaPartNext}}
	if now.Playing() {
		icons[1].name = "pause"
	}
	x := x0 + pad
	for i, ic := range icons {
		b := image.Rect(x, y, x+bs, y+bs)
		fill := lerp(pal.card, pal.text, 0.12)
		fg := pal.text
		if i == 1 {
			fill, fg = pal.accent, pal.bg
		}
		r.roundFill(b, float64(bs)/2, fill, fill)
		is := 32
		r.mdiIcon(ic.name, b.Min.X+(bs-r.s(is))/2, b.Min.Y+(bs-r.s(is))/2, is, fg)
		zones = append(zones, mediaZone{r: b, kind: ic.kind})
		x += bs + gap
	}
	if now.Volume >= 0 {
		x += gap
		r.mdiIcon("volume-high", x, y+(bs-r.s(28))/2, 28, pal.sub)
		x += r.s(28) + gap
		bar := image.Rect(x, y+bs/2-r.s(7), x0+w-pad, y+bs/2+r.s(7))
		track := lerp(pal.card, pal.text, 0.15)
		r.roundFill(bar, float64(bar.Dy())/2, track, track)
		fillW := int(float64(bar.Dx())*min(max(now.Volume, 0), 1) + 0.5)
		if fillW > 0 {
			r.roundFill(image.Rect(bar.Min.X, bar.Min.Y, bar.Min.X+fillW, bar.Max.Y), float64(bar.Dy())/2, pal.accent, pal.accent)
		}
		// The whole height of the row is the volume's, so a finger need not find the thin bar.
		zones = append(zones, mediaZone{r: image.Rect(bar.Min.X, y, bar.Max.X, y+bs), kind: mediaPartVolume})
	}
	y += ctrlH + gap

	chips := func(names []string, on func(int) bool, icon func(int) string, level func(int) float64, kind, first, max int) {
		for i := first; i < len(names) && i < first+max; i++ {
			name, k := names[i], i-first
			cx := x0 + pad + (k%cols)*(chipW+gap)
			cy := y + (k/cols)*(chipH+gap)
			b := image.Rect(cx, cy, cx+chipW, cy+chipH)
			fill, fg := lerp(pal.card, pal.text, 0.1), pal.text
			if on(i) {
				fill, fg = pal.accent, pal.bg
			}
			r.roundFill(b, pal.rad*0.6, fill, fill)
			tx := b.Min.X + r.s(12)
			if ic := icon(i); ic != "" {
				r.mdiIcon(ic, tx, b.Min.Y+(chipH-r.s(22))/2, 22, fg)
				tx += r.s(30)
			}
			r.text(fc.sub, r.fit(fc.sub, name, b.Max.X-tx-r.s(8)), tx, b.Min.Y+chipH/2+r.s(7), fg)
			// A level along the foot, as a tile shows one: a finger sliding along the button sets it.
			if v := level(i); v >= 0 {
				bar := image.Rect(b.Min.X+r.s(10), b.Max.Y-r.s(8), b.Max.X-r.s(10), b.Max.Y-r.s(4))
				track := lerp(fill, fg, 0.3)
				draw.Draw(r.dst, bar, image.NewUniform(track), image.Point{}, draw.Src)
				fw := int(float64(bar.Dx())*min(v, 1) + 0.5)
				draw.Draw(r.dst, image.Rect(bar.Min.X, bar.Min.Y, bar.Min.X+fw, bar.Max.Y), image.NewUniform(fg), image.Point{}, draw.Src)
			}
			zones = append(zones, mediaZone{r: b, kind: kind, index: i})
		}
	}

	// The favorites, or why there are none to show.
	if music {
		r.text(fc.label, mediaWord(0), x0+pad, y+r.s(22), pal.sub)
		y += labelH
	}
	switch {
	case !music:
	case m.loading:
		r.text(fc.sub, mediaWord(3), x0+pad, y+r.s(20), pal.sub)
		y += r.s(30)
	case m.lists.Target == "":
		r.text(fc.sub, r.fit(fc.sub, mediaWord(5), inner), x0+pad, y+r.s(20), pal.sub)
		y += r.s(30)
	case m.problem != "" && len(m.lists.Favorites) == 0:
		r.text(fc.sub, r.fit(fc.sub, m.problem, inner), x0+pad, y+r.s(20), pal.sub)
		y += r.s(30)
	case len(m.lists.Favorites) == 0:
		r.text(fc.sub, mediaWord(4), x0+pad, y+r.s(20), pal.sub)
		y += r.s(30)
	default:
		names := make([]string, len(m.lists.Favorites))
		for i, f := range m.lists.Favorites {
			names[i] = f.Name
		}
		playing := func(i int) bool {
			if i == m.lit && time.Now().Before(m.litUntil) {
				return true // just tapped
			}
			f := m.lists.Favorites[i]
			if on, _ := putOn.Load(m.lists.Target); m.lists.Queued && on == f.URI {
				return true // put on from here and still Music Assistant's there
			}
			return now.Playing() && now.Title != "" && strings.EqualFold(f.Name, now.Title)
		}
		icon := func(i int) string {
			if m.lists.Favorites[i].Kind == "radio" {
				return "radio"
			}
			return "playlist-music"
		}
		top := y
		chips(names, playing, icon, func(int) float64 { return -1 }, mediaPartFavorite, page*perPage, perPage)
		y += favRows * (chipH + gap)
		if pages > 1 {
			y = r.pageDots(x0, w, y, pages, page, pal)
		}
		// The block a swipe pages through; after the favorites, so a tap on one finds the favorite.
		zones = append(zones, mediaZone{r: image.Rect(x0+pad, top, x0+w-pad, y), kind: mediaPartPage})
	}

	// The speakers to play along.
	if speakerRows > 0 {
		r.text(fc.label, mediaWord(1), x0+pad, y+r.s(22), pal.sub)
		y += labelH
		names := make([]string, len(m.lists.Speakers))
		for i, s := range m.lists.Speakers {
			names[i] = s.Name
		}
		grouped := func(i int) bool { return m.grouped[m.lists.Speakers[i].Entity] }
		level := func(i int) float64 {
			if !grouped(i) {
				return -1
			}
			return m.speakerVolume(m.lists.Speakers[i].Entity)
		}
		top := y
		chips(names, grouped, func(int) string { return "speaker" }, level, mediaPartSpeaker, speakerPage*speakersPer, speakersPer)
		y += speakerRows * (chipH + gap)
		if speakerPages > 1 {
			y = r.pageDots(x0, w, y, speakerPages, speakerPage, pal)
		}
		zones = append(zones, mediaZone{r: image.Rect(x0+pad, top, x0+w-pad, y), kind: mediaPartSpeakerPage})
	}

	done := mediaWord(2)
	bw := max(r.width(fc.button, done)+2*r.s(28), r.s(140))
	btn := image.Rect(x0+w-pad-bw, y0+h-pad-btnH, x0+w-pad, y0+h-pad)
	r.roundFill(btn, pal.rad, pal.accent, pal.accent)
	r.text(fc.button, done, btn.Min.X+(bw-r.width(fc.button, done))/2, btn.Min.Y+btnH/2+r.s(9), pal.bg)
	zones = append(zones, mediaZone{r: btn, kind: mediaPartDone})
	r.setMedia(zones, card)
	r.zmu.Lock()
	r.mediaPages, r.mediaSpeakerPages = pages, speakerPages
	r.zmu.Unlock()
}

// pageDots draws which page of how many a list is on, a dot each and this one lit, across the sheet
// x0 and w wide at y, and is where the sheet goes on under them.
func (r *renderer) pageDots(x0, w, y, pages, page int, pal dashPal) int {
	ds, dg := r.s(8), r.s(10)
	dx := x0 + (w-(pages*ds+(pages-1)*dg))/2
	for i := 0; i < pages; i++ {
		c := lerp(pal.card, pal.sub, 0.6)
		if i == page {
			c = pal.accent
		}
		dot := image.Rect(dx+i*(ds+dg), y, dx+i*(ds+dg)+ds, y+ds)
		r.roundFill(dot, float64(ds)/2, c, c)
	}
	return y + r.s(16)
}

// mediaPageSwipe is a finger that came down on the favorites at from and lifted at x, y: a swipe along
// them, to the left for the next page and to the right for the one before, turns the page. It says
// whether it was one; anything else is a tap.
func (d *Display) mediaPageSwipe(from image.Point, x, y int) bool {
	if d.r == nil {
		return false
	}
	z, _, in := d.r.mediaAt(from.X, from.Y)
	speakers := z.kind == mediaPartSpeaker || z.kind == mediaPartSpeakerPage
	if !in || (z.kind != mediaPartFavorite && z.kind != mediaPartPage && !speakers) {
		return false
	}
	dx, dy := x-from.X, y-from.Y
	if abs(dx) < d.r.s(pageSwipe) || abs(dx) < abs(dy) {
		return false
	}
	d.r.zmu.Lock()
	pages, speakerPages := d.r.mediaPages, d.r.mediaSpeakerPages
	d.r.zmu.Unlock()
	step := 1
	if dx > 0 {
		step = -1
	}
	d.editMedia(func(n *mediaSheet) {
		if speakers {
			n.speakerPage = min(max(n.speakerPage+step, 0), max(speakerPages-1, 0))
		} else {
			n.page = min(max(n.page+step, 0), max(pages-1, 0))
		}
	})
	d.wake()
	return true
}

// pageSwipe is how far a finger goes along the favorites to turn a page.
const pageSwipe = 40

// setMedia keeps the media sheet's parts where they were drawn, for the touch goroutine.
func (p *paint) setMedia(zones []mediaZone, card image.Rectangle) {
	p.zmu.Lock()
	p.mediaZones, p.mediaCard = zones, card
	p.zmu.Unlock()
}

// mediaAt is the part of the media sheet under x, y, how far along it the finger is (for the volume),
// and whether that is on the sheet at all.
func (p *paint) mediaAt(x, y int) (mediaZone, float64, bool) {
	p.zmu.Lock()
	zones, card := slices.Clone(p.mediaZones), p.mediaCard
	p.zmu.Unlock()
	return mediaHit(zones, card, x, y)
}

// mediaHit is the part of a sheet drawn as zones and card under x, y.
func mediaHit(zones []mediaZone, card image.Rectangle, x, y int) (mediaZone, float64, bool) {
	pt := image.Pt(x, y)
	if !pt.In(card) {
		return mediaZone{}, 0, false
	}
	for _, z := range zones {
		if pt.In(z.r) {
			frac := float64(x-z.r.Min.X) / float64(max(z.r.Dx()-1, 1))
			return z, min(max(frac, 0), 1), true
		}
	}
	return mediaZone{kind: mediaPartNone}, 0, true
}
