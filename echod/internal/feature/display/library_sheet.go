//go:build !dot && !spot

package display

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"log/slog"
	"slices"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/dashboard"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/lib/musicassistant"
)

// The music library: a page over the drawn dashboard to browse Music Assistant's library and play from
// it, opened from the media sheet's Library button or from a card whose tap navigates to
// dashboard.LibraryPath. Tabs along the top hold the artists, albums, playlists, tracks and radio
// stations, and a search; a swipe along a tab turns its pages, and the next of the library is asked for
// as the pages run out. An artist opens on its albums and top tracks, an album or a playlist on its
// tracks, each a page of its own that Back leaves for the one before, where it was. A track or a
// station plays at a tap; an artist, album or playlist plays from its page, in order or shuffled.
//
// The library is read from Music Assistant itself, with the token set up on the device, and played
// through Home Assistant, as the media sheet plays a favorite: on Music Assistant's player for the
// sheet's player, the card's player, or this device.

// What a library page shows: a tab, or what was opened from one.
const (
	libArtists = iota
	libAlbums
	libPlaylists
	libTracks
	libRadio
	libSearch
	libTabs // how many tabs there are
	libArtist
	libAlbum
	libPlaylist
)

// libKinds are the tabs' kinds, as Music Assistant's commands name them.
var libKinds = [...]string{libArtists: musicassistant.KindArtists, libAlbums: musicassistant.KindAlbums,
	libPlaylists: musicassistant.KindPlaylists, libTracks: musicassistant.KindTracks, libRadio: musicassistant.KindRadios}

// libTypes are the tabs' items' media types, for an item that does not say its own.
var libTypes = [...]string{libArtists: "artist", libAlbums: "album", libPlaylists: "playlist", libTracks: "track", libRadio: "radio"}

// What a part of the library page is, for a tap.
const (
	libPartNone = iota
	libPartDone
	libPartBack
	libPartTab // index: which tab
	libPartItem
	libPartPlay
	libPartShuffle
	libPartField // the search's field, which opens the keyboard
	libPartPage  // the items' block, where a finger swipes to the next page or back
)

// libSecStride packs an item's section and its place there into one index: section*libSecStride + i.
const libSecStride = 1 << 20

// libSection is one list on a page: its heading (a word, or -1 for none), whether it is a grid of
// pictures or rows, what kind its items are, and the items.
type libSection struct {
	word    int
	grid    bool
	kind    string
	entries []musicassistant.Entry
}

// libPage is one page of the library: a tab, or an artist, album or playlist opened from one, and how
// it stands. Its sections are never changed in place, only replaced: a frame may be drawing them.
type libPage struct {
	id       int64
	what     int
	entry    musicassistant.Entry // the artist, album or playlist the page is of
	sections []libSection
	loading  bool
	loaded   bool
	problem  string
	more     bool // a tab with more of the library to ask for
	page     int  // which of its pages is shown, from 0
	query    string
}

// librarySheet is the library while it is up.
type librarySheet struct {
	// target is the player music goes to, Music Assistant's; empty when there is none, or while
	// finding says it is still being looked for.
	target  string
	finding bool
	// set is whether Music Assistant is set up on the device, and base its address, for pictures.
	set  bool
	base string
	tab  int
	// roots are the tabs as they were left, and stack the pages opened from the one shown, the last on
	// top.
	roots [libTabs]libPage
	stack []libPage
	// typing is the keyboard up for a search, with text typed so far.
	typing           bool
	text             string
	shifted, symbols bool
	// lit is the item just tapped, drawn lit until litUntil, so a tap is seen to have landed.
	lit      int
	litUntil time.Time
}

// current is the page shown.
func (l *librarySheet) current() *libPage {
	if n := len(l.stack); n > 0 {
		return &l.stack[n-1]
	}
	return &l.roots[l.tab]
}

// byID is the page id, if it is still one of the sheet's.
func (l *librarySheet) byID(id int64) *libPage {
	for i := range l.stack {
		if l.stack[i].id == id {
			return &l.stack[i]
		}
	}
	for i := range l.roots {
		if l.roots[i].id == id {
			return &l.roots[i]
		}
	}
	return nil
}

// libraryView is the sheet as one frame draws it.
type libraryView struct {
	sheet librarySheet
}

var libIDs atomic.Int64

// newLibPage is a page of what, of entry, not yet asked for.
func newLibPage(what int, entry musicassistant.Entry) libPage {
	return libPage{id: libIDs.Add(1), what: what, entry: entry}
}

// libClient is the Music Assistant set up on this device and whether there is one; a variable so that
// a test can point it at a server of its own.
var libClient = func() (musicassistant.Client, bool) {
	m := config.Get().MusicAssistant
	return musicassistant.Client{URL: m.URL, Token: m.Token}, m.Set()
}

// libFindPlayer is the Music Assistant player a library opened from a card plays on: the one for the
// card's media player, as its media sheet would find it, or for this device's own when the card has
// none. Empty when there is none. It asks Home Assistant, so it is for a goroutine of its own; a
// variable for the tests.
var libFindPlayer = func(entity string) string {
	if strings.HasPrefix(entity, "media_player.") {
		return dashboard.MusicTarget(entity)
	}
	if t := dashboard.MusicTarget(home.SpeakerEntity()); t != "" {
		return t
	}
	// Music Assistant names its player for this device by the device's own name, not its speaker's.
	return home.MusicAssistantPlayer()
}

// libTap is how the library's plays reach Home Assistant; the media sheet's, so that a test sees both.
func libTap(a dashboard.Action) { sheetTap(a) }

// libAfter waits before the play after a shuffle is set; a variable for the tests.
var libAfter = time.AfterFunc

// libShuffleFirst is how long a play waits after its shuffle is set: Music Assistant shuffles a queue
// as it is loaded when its shuffle is on, so the first track is a shuffled one too. Each call goes on a
// goroutine of its own, so without the wait the play could arrive first.
const libShuffleFirst = 400 * time.Millisecond

// libSearchLimit is how many of each kind a search finds.
const libSearchLimit = 12

// libWords are the library's words, in the screen's language.
var libWords = map[string][]string{
	"": {"Artists", "Albums", "Playlists", "Tracks", "Radio", "Search", "Back", "Play", "Shuffle", "Done",
		"Loading…", "Nothing here yet",
		"Set up the music library on the setup page (Sound & Voice, Music Assistant) to browse it here.",
		"No player found: browse only", "Top tracks", "Tap to search",
		"Search the music library", "No results", "Library"},
	"ja": {"アーティスト", "アルバム", "プレイリスト", "曲", "ラジオ", "検索", "戻る", "再生", "シャッフル", "完了",
		"読み込み中…", "まだ何もありません",
		"ここで閲覧するには、設定ページ（Sound & Voice、Music Assistant）で音楽ライブラリを設定してください。",
		"Music Assistantのプレーヤーがありません（閲覧のみ）", "人気曲", "タップして検索",
		"音楽ライブラリを検索", "見つかりません", "ライブラリ"},
	"de": {"Interpreten", "Alben", "Playlists", "Titel", "Radio", "Suche", "Zurück", "Abspielen", "Zufall", "Fertig",
		"Lädt …", "Hier ist noch nichts",
		"Richte die Musikbibliothek auf der Einrichtungsseite ein (Sound & Voice, Music Assistant), um sie hier zu durchsuchen.",
		"Kein Music-Assistant-Player zum Abspielen: nur ansehen", "Top-Titel", "Tippen zum Suchen",
		"Musikbibliothek durchsuchen", "Keine Treffer", "Bibliothek"},
	"es": {"Artistas", "Álbumes", "Listas", "Canciones", "Radio", "Buscar", "Atrás", "Reproducir", "Aleatorio", "Listo",
		"Cargando…", "Aún no hay nada",
		"Configura la biblioteca de música en la página de configuración (Sound & Voice, Music Assistant) para explorarla aquí.",
		"No hay reproductor de Music Assistant: solo explorar", "Canciones populares", "Toca para buscar",
		"Buscar en la biblioteca", "Sin resultados", "Biblioteca"},
	"fr": {"Artistes", "Albums", "Playlists", "Titres", "Radio", "Recherche", "Retour", "Écouter", "Aléatoire", "OK",
		"Chargement…", "Rien pour l'instant",
		"Configurez la bibliothèque musicale sur la page de configuration (Sound & Voice, Music Assistant) pour la parcourir ici.",
		"Aucun lecteur Music Assistant : consultation seulement", "Titres populaires", "Touchez pour chercher",
		"Chercher dans la bibliothèque", "Aucun résultat", "Bibliothèque"},
	"it": {"Artisti", "Album", "Playlist", "Brani", "Radio", "Cerca", "Indietro", "Riproduci", "Casuale", "Fatto",
		"Caricamento…", "Ancora niente qui",
		"Configura la libreria musicale nella pagina di configurazione (Sound & Voice, Music Assistant) per sfogliarla qui.",
		"Nessun lettore Music Assistant: solo consultazione", "Brani più ascoltati", "Tocca per cercare",
		"Cerca nella libreria", "Nessun risultato", "Libreria"},
	"pt": {"Artistas", "Álbuns", "Playlists", "Faixas", "Rádio", "Buscar", "Voltar", "Tocar", "Aleatório", "Pronto",
		"Carregando…", "Nada aqui ainda",
		"Configure a biblioteca de música na página de configuração (Sound & Voice, Music Assistant) para navegar nela aqui.",
		"Nenhum player do Music Assistant: só navegar", "Mais tocadas", "Toque para buscar",
		"Buscar na biblioteca", "Nenhum resultado", "Biblioteca"},
	"ru": {"Исполнители", "Альбомы", "Плейлисты", "Треки", "Радио", "Поиск", "Назад", "Играть", "Вперемешку", "Готово",
		"Загрузка…", "Здесь пока пусто",
		"Настройте музыкальную библиотеку на странице настройки (Sound & Voice, Music Assistant), чтобы просматривать её здесь.",
		"Нет плеера Music Assistant: только просмотр", "Популярные треки", "Нажмите для поиска",
		"Поиск в библиотеке", "Ничего не найдено", "Библиотека"},
}

// The words by what they say; the tabs' are their own numbers.
const (
	libWordBack = iota + libTabs
	libWordPlay
	libWordShuffle
	libWordDone
	libWordLoading
	libWordEmpty
	libWordSetUp
	libWordNoPlayer
	libWordTopTracks
	libWordTapToSearch
	libWordSearchTitle
	libWordNoResults
	libWordLibrary
)

func libWord(i int) string {
	w, ok := libWords[screenLang()]
	if !ok {
		w = libWords[""]
	}
	return w[i]
}

// openLibrary puts the library up. From a media sheet, target is the player it plays on; from a card,
// target is empty and the player is found for entity, the card's media player, or for this device when
// the card names none.
func (d *Display) openLibrary(target, entity string, fromCard bool) {
	c, set := libClient()
	slog.Info("dashboard music library", "target", target, "entity", entity, "set", set)
	l := &librarySheet{target: target, finding: fromCard, set: set, base: c.URL}
	for i := range l.roots {
		l.roots[i] = newLibPage(i, musicassistant.Entry{})
	}
	libPics.setChanged(d.wake)
	d.mu.Lock()
	d.dashMedia = nil // the library is over the page in its place, and Done puts both away
	d.dashLibrary = l
	d.mu.Unlock()
	d.wake()
	if fromCard {
		go func() {
			t := libFindPlayer(entity)
			d.editLibrary(func(n *librarySheet) { n.target, n.finding = t, false })
			d.wake()
		}()
	}
	if set {
		d.libLoad(l.roots[libArtists].id)
	}
}

// editLibrary changes the library up now, if it is still up, without changing the one a frame is
// drawing.
func (d *Display) editLibrary(change func(*librarySheet)) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.dashLibrary == nil {
		return
	}
	n := *d.dashLibrary
	n.stack = slices.Clone(n.stack)
	change(&n)
	d.dashLibrary = &n
}

// libraryViewNow is the library for a frame; nil without one.
func (d *Display) libraryViewNow() *libraryView {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.dashLibrary == nil {
		return nil
	}
	return &libraryView{sheet: *d.dashLibrary}
}

// libLimit is how many items a tab asks for at a time: a few pages' worth, as the page last held.
func (d *Display) libLimit() int {
	per := 0
	if d.r != nil {
		d.r.zmu.Lock()
		per = d.r.libPerPage
		d.r.zmu.Unlock()
	}
	if per <= 0 {
		return 48
	}
	return min(max(per*4, 24), 120)
}

// libLoad asks Music Assistant for what page id shows, or for the next of it, and puts it on the page
// when it comes. A page already being asked for is left to that.
func (d *Display) libLoad(id int64) {
	c, set := libClient()
	if !set {
		return
	}
	limit := d.libLimit()
	var p libPage
	ok := false
	d.editLibrary(func(n *librarySheet) {
		pg := n.byID(id)
		if pg == nil || pg.loading || (pg.loaded && !pg.more && pg.problem == "") {
			return
		}
		pg.loading, pg.problem = true, ""
		p, ok = *pg, true
	})
	if !ok {
		return
	}
	d.wake()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		sections, more, err := fetchLibPage(ctx, c, p, limit)
		d.editLibrary(func(n *librarySheet) {
			pg := n.byID(id)
			if pg == nil {
				return
			}
			pg.loading, pg.loaded = false, true
			if err != nil {
				pg.problem = err.Error()
				slog.Warn("dashboard music library", "what", pg.what, "err", err)
				if len(pg.sections) == 0 {
					pg.sections = sections
				}
				pg.more = false
				return
			}
			pg.more = more
			if pg.what < libSearch && len(pg.sections) == 1 {
				// The next of a tab, after what it has.
				s := pg.sections[0]
				s.entries = slices.Concat(s.entries, sections[0].entries)
				pg.sections = []libSection{s}
				return
			}
			pg.sections = sections
		})
		d.wake()
	}()
}

// fetchLibPage asks for page p's items: the next limit of a tab, a search's findings, an artist's
// albums and top tracks, an album's or a playlist's tracks. more is whether a tab has more after them.
// What came of an artist's two asks is kept when only one of them failed.
func fetchLibPage(ctx context.Context, c musicassistant.Client, p libPage, limit int) ([]libSection, bool, error) {
	switch p.what {
	case libArtists, libAlbums, libPlaylists, libTracks, libRadio:
		offset := 0
		if len(p.sections) > 0 {
			offset = len(p.sections[0].entries)
		}
		got, err := c.Library(ctx, libKinds[p.what], offset, limit, "")
		if err != nil {
			return nil, false, err
		}
		return []libSection{{word: -1, grid: p.what != libTracks, kind: libTypes[p.what], entries: got}}, len(got) >= limit, nil
	case libSearch:
		f, err := c.SearchAll(ctx, p.query, libSearchLimit)
		if err != nil {
			return nil, false, err
		}
		return []libSection{
			{word: libArtists, grid: true, kind: "artist", entries: f.Artists},
			{word: libAlbums, grid: true, kind: "album", entries: f.Albums},
			{word: libPlaylists, grid: true, kind: "playlist", entries: f.Playlists},
			{word: libTracks, kind: "track", entries: f.Tracks},
			{word: libRadio, grid: true, kind: "radio", entries: f.Radio},
		}, false, nil
	case libArtist:
		albums, aerr := c.ArtistAlbums(ctx, p.entry)
		top, terr := c.TopTracks(ctx, p.entry)
		out := []libSection{{word: libAlbums, grid: true, kind: "album", entries: albums},
			{word: libWordTopTracks, kind: "track", entries: top}}
		if aerr != nil && terr != nil {
			return nil, false, aerr
		}
		if aerr != nil || terr != nil {
			return out, false, firstErr(aerr, terr)
		}
		return out, false, nil
	case libAlbum:
		got, err := c.AlbumTracks(ctx, p.entry)
		return []libSection{{word: -1, kind: "track", entries: got}}, false, err
	case libPlaylist:
		got, err := c.PlaylistTracks(ctx, p.entry)
		return []libSection{{word: -1, kind: "track", entries: got}}, false, err
	}
	return nil, false, nil
}

func firstErr(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

// libraryTap is a finger lifted at x, y while the library is up, which it always takes. It says
// whether the library was up.
func (d *Display) libraryTap(x, y int) bool {
	d.mu.Lock()
	l := d.dashLibrary
	d.mu.Unlock()
	if l == nil || d.r == nil {
		return false
	}
	if l.typing {
		d.libraryKey(d.r.keyAt(x, y, l.symbols))
		return true
	}
	z, _, _ := d.r.libraryAt(x, y)
	p := l.current()
	switch z.kind {
	case libPartDone:
		d.mu.Lock()
		d.dashLibrary = nil
		d.mu.Unlock()
	case libPartBack:
		d.editLibrary(func(n *librarySheet) {
			if len(n.stack) > 0 {
				n.stack = n.stack[:len(n.stack)-1]
			}
		})
	case libPartTab:
		tab := z.index
		var id int64
		ask := false
		d.editLibrary(func(n *librarySheet) {
			n.tab, n.stack = tab, nil
			// A tab not yet read, or one that could not be, is asked for; one read is shown as it was
			// left, and asks for more only as its pages are turned.
			p := n.roots[tab]
			id, ask = p.id, tab != libSearch && (!p.loaded || p.problem != "")
		})
		if ask {
			d.libLoad(id)
		}
	case libPartField:
		d.editLibrary(func(n *librarySheet) {
			n.typing, n.text, n.shifted, n.symbols = true, n.roots[libSearch].query, false, false
		})
	case libPartPlay, libPartShuffle:
		d.libPlay(l.target, p.entry, libEntryType(p.entry, libPageType(p.what)), z.kind == libPartShuffle)
	case libPartItem:
		sec, i := z.index/libSecStride, z.index%libSecStride
		if sec >= len(p.sections) || i >= len(p.sections[sec].entries) {
			break
		}
		e := p.sections[sec].entries[i]
		switch kind := libEntryType(e, p.sections[sec].kind); kind {
		case "artist", "album", "playlist":
			what := map[string]int{"artist": libArtist, "album": libAlbum, "playlist": libPlaylist}[kind]
			np := newLibPage(what, e)
			d.editLibrary(func(n *librarySheet) { n.stack = append(n.stack, np) })
			d.libLoad(np.id)
		default:
			if l.target == "" {
				break
			}
			d.libPlay(l.target, e, kind, false)
			at := z.index
			d.editLibrary(func(n *librarySheet) { n.lit, n.litUntil = at, time.Now().Add(tapLit) })
			time.AfterFunc(tapLit+50*time.Millisecond, d.wake) // to draw it unlit again
		}
	}
	d.wake()
	return true
}

// libPageType is the media type a page of what plays as.
func libPageType(what int) string {
	switch what {
	case libArtist:
		return "artist"
	case libAlbum:
		return "album"
	case libPlaylist:
		return "playlist"
	}
	return ""
}

// libEntryType is e's media type: its own, or else the kind of the list it is in.
func libEntryType(e musicassistant.Entry, kind string) string {
	if e.MediaType != "" {
		return e.MediaType
	}
	return kind
}

// libPlay plays e on target through Home Assistant, as the media sheet plays a favorite, replacing what
// was queued. An artist, album or playlist played from its page sets the queue's shuffle first, on or
// off, so that Play plays it in order after a Shuffle.
func (d *Display) libPlay(target string, e musicassistant.Entry, kind string, shuffle bool) {
	if target == "" || e.URI == "" {
		return
	}
	slog.Info("dashboard music library play", "target", target, "uri", e.URI, "type", kind, "shuffle", shuffle)
	play := func() {
		libTap(dashboard.Action{Entity: target, Service: "music_assistant.play_media",
			Data: map[string]any{"media_id": e.URI, "media_type": kind, "enqueue": "replace"}})
	}
	putOn.Store(target, e.URI)
	if kind == "artist" || kind == "album" || kind == "playlist" {
		libTap(dashboard.Action{Entity: target, Service: "media_player.shuffle_set", Data: map[string]any{"shuffle": shuffle}})
		libAfter(libShuffleFirst, play)
		return
	}
	play()
}

// libraryKey is a key on the search's keyboard: Cancel puts it away, Search searches.
func (d *Display) libraryKey(k string) {
	var search int64
	d.editLibrary(func(n *librarySheet) {
		switch k {
		case "":
		case "shift":
			n.shifted = !n.shifted
		case "symbols":
			n.symbols = !n.symbols
		case "space":
			n.text += " "
		case "backspace":
			if _, size := utf8.DecodeLastRuneInString(n.text); size > 0 {
				n.text = n.text[:len(n.text)-size]
			}
		case "cancel":
			n.typing = false
		case "join":
			n.typing = false
			q := strings.TrimSpace(n.text)
			if q == "" {
				return
			}
			p := newLibPage(libSearch, musicassistant.Entry{})
			p.query = q
			n.roots[libSearch], n.tab, n.stack = p, libSearch, nil
			search = p.id
		default:
			if n.shifted && !n.symbols {
				k = strings.ToUpper(k)
				n.shifted = false
			}
			if utf8.RuneCountInString(n.text) < 60 {
				n.text += k
			}
		}
	})
	if search != 0 {
		d.libLoad(search)
	}
	d.wake()
}

// libraryPageSwipe is a finger that came down on the library's items at from and lifted at x, y: a
// swipe along them, to the left for the next page and to the right for the one before, turns the page,
// and asks for more of a tab as its last pages come up. It says whether it was one.
func (d *Display) libraryPageSwipe(from image.Point, x, y int) bool {
	d.mu.Lock()
	l := d.dashLibrary
	d.mu.Unlock()
	if l == nil || l.typing || d.r == nil {
		return false
	}
	z, _, in := d.r.libraryAt(from.X, from.Y)
	if !in || (z.kind != libPartItem && z.kind != libPartPage) {
		return false
	}
	dx, dy := x-from.X, y-from.Y
	if abs(dx) < d.r.s(pageSwipe) || abs(dx) < abs(dy) {
		return false
	}
	d.r.zmu.Lock()
	pages := d.r.libPages
	d.r.zmu.Unlock()
	step := 1
	if dx > 0 {
		step = -1
	}
	var id int64
	near := false
	d.editLibrary(func(n *librarySheet) {
		p := n.current()
		p.page = min(max(p.page+step, 0), max(pages-1, 0))
		id, near = p.id, p.more && p.page >= pages-2
	})
	if near {
		d.libLoad(id)
	}
	d.wake()
	return true
}

// libParts are the library page's parts where they were last drawn, under zmu, for the touch
// goroutine: its zones and the page itself, how many pages the list shown has and about how many items
// one holds, and whether the keyboard was drawn instead.
type libParts struct {
	libZones   []mediaZone
	libCard    image.Rectangle
	libPages   int
	libPerPage int
}

func (r *renderer) setLibrary(zones []mediaZone, card image.Rectangle, pages, per int) {
	r.zmu.Lock()
	r.libZones, r.libCard, r.libPages = zones, card, pages
	if per > 0 {
		r.libPerPage = per
	}
	r.zmu.Unlock()
}

// libraryAt is the part of the library page under x, y, and whether that is on the page at all.
func (r *renderer) libraryAt(x, y int) (mediaZone, float64, bool) {
	r.zmu.Lock()
	zones, card := slices.Clone(r.libZones), r.libCard
	r.zmu.Unlock()
	return mediaHit(zones, card, x, y)
}

// libLine is a line of a library page as it is laid out: a section's heading, a row of its grid, or
// one of its rows.
type libLine struct {
	heading      int // a word, or -1
	sec          int
	first, count int
	grid         bool
	h            int
}

// libLayout is how a page's items are placed on the panel: cols pictures a row, each side square with
// the names under it in a cell cellH high, or rows rowH high.
type libLayout struct {
	cols, side, cellW, cellH, rowH, gap, headH int
}

// libPaginate lays the sections out in lines and breaks them into pages of at most room high: a
// heading goes with the line after it.
func libPaginate(sections []libSection, lay libLayout, room int) [][]libLine {
	var lines []libLine
	for si, s := range sections {
		if len(s.entries) == 0 {
			continue
		}
		if s.word >= 0 {
			lines = append(lines, libLine{heading: s.word, sec: si, h: lay.headH})
		}
		if s.grid {
			for i := 0; i < len(s.entries); i += lay.cols {
				lines = append(lines, libLine{heading: -1, sec: si, first: i, count: min(lay.cols, len(s.entries)-i), grid: true, h: lay.cellH})
			}
		} else {
			for i := range s.entries {
				lines = append(lines, libLine{heading: -1, sec: si, first: i, count: 1, h: lay.rowH})
			}
		}
	}
	var pages [][]libLine
	var cur []libLine
	used := 0
	for i, ln := range lines {
		need := ln.h
		if ln.heading >= 0 && i+1 < len(lines) {
			need += lay.gap + lines[i+1].h
		}
		if len(cur) > 0 && used+need > room {
			pages, cur, used = append(pages, cur), nil, 0
		}
		cur = append(cur, ln)
		used += ln.h + lay.gap
	}
	if len(cur) > 0 {
		pages = append(pages, cur)
	}
	return pages
}

// libIcon is the picture drawn for an item without one of its own, or until its own has come.
func libIcon(kind string) string {
	switch kind {
	case "artist":
		return "account-music"
	case "album":
		return "album"
	case "playlist":
		return "playlist-music"
	case "radio":
		return "radio"
	}
	return "music-note"
}

// librarySheet draws the library over the drawn dashboard and keeps where its parts are for
// libraryAt.
func (r *renderer) librarySheet(v *libraryView, th dashboard.Theme) {
	if v == nil {
		r.setLibrary(nil, image.Rectangle{}, 0, 0)
		return
	}
	l := v.sheet
	whole := r.dst.Rect
	if l.typing {
		// The keyboard is the whole screen's, as the Wi-Fi passphrase's is; every tap on it is its.
		r.useFont()
		fillCanvas(r.dst, walnut)
		r.keyboard(libWord(libWordSearchTitle), l.text, false, l.shifted, l.symbols, libWord(libSearch))
		r.setLibrary(nil, whole, 0, 0)
		return
	}
	pal := r.palette(th)
	fc := r.faces()
	washOver(r.dst, whole, color.RGBA{0, 0, 0, 150})
	m := r.s(8)
	card := whole.Inset(m)
	r.roundFill(card, pal.rad, pal.card, pal.card)
	pad, gap := r.s(14), r.s(10)
	x0, x1 := card.Min.X+pad, card.Max.X-pad
	inner := x1 - x0
	headH := r.s(46)
	y := card.Min.Y + pad
	p := l.current()
	var zones []mediaZone

	// A button along the top: lit or not, an icon before its word or none, and off when it does
	// nothing.
	button := func(b image.Rectangle, icon, word string, lit, off bool, kind, index int) {
		fill, fg := lerp(pal.card, pal.text, 0.1), pal.text
		if lit {
			fill, fg = pal.accent, pal.bg
		}
		if off {
			fill, fg = lerp(pal.card, pal.text, 0.05), lerp(pal.card, pal.sub, 0.6)
		}
		r.roundFill(b, pal.rad*0.6, fill, fill)
		tx := b.Min.X + (b.Dx()-r.width(fc.sub, word))/2
		if icon != "" {
			// The icon and the word together, in the middle.
			iw, tw := r.s(22)+r.s(6), r.width(fc.sub, word)
			start := b.Min.X + max((b.Dx()-iw-tw)/2, r.s(8))
			r.mdiIcon(icon, start, b.Min.Y+(b.Dy()-r.s(22))/2, 22, fg)
			tx = start + iw
		}
		r.text(fc.sub, r.fit(fc.sub, word, b.Dx()-r.s(12)), tx, b.Min.Y+b.Dy()/2+r.s(7), fg)
		if !off {
			zones = append(zones, mediaZone{r: b, kind: kind, index: index})
		}
	}
	wordW := func(w string, icon bool) int {
		n := r.width(fc.sub, w) + 2*r.s(16)
		if icon {
			n += r.s(28)
		}
		return max(n, r.s(84))
	}

	// Done at the right, always.
	doneW := wordW(libWord(libWordDone), false)
	done := image.Rect(x1-doneW, y, x1, y+headH)
	button(done, "", libWord(libWordDone), true, false, libPartDone, 0)
	right := done.Min.X - gap

	if !l.set {
		r.text(fc.labelBold, r.fit(fc.labelBold, libWord(libWordLibrary), right-x0), x0, y+r.s(32), pal.text)
		ty := y + headH + r.s(40)
		for _, line := range r.wrapLines(fc.label, libWord(libWordSetUp), inner) {
			r.text(fc.label, line, x0, ty, pal.sub)
			ty += r.s(36)
		}
		r.setLibrary(zones, whole, 0, 0)
		return
	}

	left := x0
	if len(l.stack) > 0 {
		// An opened page: Back, its name, and Play and Shuffle.
		bw := wordW(libWord(libWordBack), true)
		button(image.Rect(left, y, left+bw, y+headH), "chevron-left", libWord(libWordBack), false, false, libPartBack, 0)
		left += bw + gap
		off := l.target == ""
		sw := wordW(libWord(libWordShuffle), true)
		button(image.Rect(right-sw, y, right, y+headH), "shuffle-variant", libWord(libWordShuffle), false, off, libPartShuffle, 0)
		right -= sw + gap
		pw := wordW(libWord(libWordPlay), true)
		button(image.Rect(right-pw, y, right, y+headH), "play", libWord(libWordPlay), !off, off, libPartPlay, 0)
		right -= pw + gap
		title := dashboard.CleanName(p.entry.Name)
		if by := dashboard.CleanName(p.entry.By()); p.what == libAlbum && by != "" {
			title += " · " + by
		}
		r.text(fc.labelBold, r.fit(fc.labelBold, title, right-left), left, y+r.s(33), pal.text)
	} else {
		// The tabs, as wide as their words, or shared out evenly when they will not all fit so.
		widths := make([]int, libTabs)
		sum := 0
		for i := range widths {
			widths[i] = wordW(libWord(i), i == libSearch)
			sum += widths[i]
		}
		if room := right - left - gap*(libTabs-1); sum > room {
			for i := range widths {
				widths[i] = room / libTabs
			}
		}
		x := left
		for i, w := range widths {
			icon := ""
			if i == libSearch {
				icon = "magnify"
			}
			button(image.Rect(x, y, x+w, y+headH), icon, libWord(i), i == l.tab, false, libPartTab, i)
			x += w + gap
		}
	}
	y += headH + gap

	// The foot: the page dots, and a note when there is nothing to play on.
	footH := r.s(24)
	foot := card.Max.Y - pad - footH
	// note says there is nothing to play on, at the left of the foot, up to the page dots when there
	// are pages.
	note := func(pages int) {
		if l.finding || l.target != "" {
			return
		}
		room := inner
		if pages > 1 {
			n := min(pages, 15)
			room = (card.Dx()-(n*r.s(8)+(n-1)*r.s(10)))/2 - pad - gap
		}
		r.text(fc.sub, r.fit(fc.sub, libWord(libWordNoPlayer), room), x0, foot+r.s(18), pal.sub)
	}

	// The search's field.
	if len(l.stack) == 0 && l.tab == libSearch {
		field := image.Rect(x0, y, x1, y+r.s(46))
		fill := lerp(pal.card, pal.text, 0.08)
		r.roundFill(field, pal.rad*0.6, fill, fill)
		r.mdiIcon("magnify", field.Min.X+r.s(12), field.Min.Y+(field.Dy()-r.s(24))/2, 24, pal.sub)
		shown, c := p.query, pal.text
		if shown == "" {
			shown, c = libWord(libWordTapToSearch), pal.sub
		}
		r.text(fc.sub, r.fit(fc.sub, shown, field.Dx()-r.s(56)), field.Min.X+r.s(46), field.Min.Y+field.Dy()/2+r.s(7), c)
		zones = append(zones, mediaZone{r: field, kind: libPartField})
		y = field.Max.Y + gap
	}

	room := foot - gap - y
	textH := r.s(46)
	cols := max(2, (inner+gap)/(r.s(120)+gap))
	cellW := (inner - gap*(cols-1)) / cols
	gridRows := max(1, (room+gap)/(r.s(120)+textH+gap))
	side := max(min(cellW, (room+gap)/gridRows-gap-textH), r.s(40))
	lay := libLayout{cols: cols, side: side, cellW: cellW, cellH: side + textH, rowH: r.s(52), gap: gap, headH: r.s(30)}

	has := false
	for _, s := range p.sections {
		has = has || len(s.entries) > 0
	}
	status := ""
	switch {
	case has:
	case p.loading || (!p.loaded && p.what != libSearch):
		status = libWord(libWordLoading)
	case p.problem != "":
		status = p.problem
	case p.what == libSearch && p.query == "":
	case p.what == libSearch:
		status = libWord(libWordNoResults)
	default:
		status = libWord(libWordEmpty)
	}
	if !has {
		// Nothing to list: what is coming, what went wrong, or, for a search not yet made, nothing.
		r.text(fc.label, r.fit(fc.label, status, inner), x0, y+r.s(36), pal.sub)
		note(0)
		r.setLibrary(zones, whole, 0, 0)
		return
	}

	pages := libPaginate(p.sections, lay, room)
	page := min(max(p.page, 0), max(len(pages)-1, 0))
	per := 0
	for _, ln := range pages[0] {
		per += ln.count
	}
	client := musicassistant.Client{URL: l.base}
	lit := func(index int) bool { return index == l.lit && time.Now().Before(l.litUntil) }
	top := y
	for _, ln := range pages[page] {
		s := p.sections[ln.sec]
		switch {
		case ln.heading >= 0:
			r.text(fc.label, libWord(ln.heading), x0, y+r.s(23), pal.sub)
		case ln.grid:
			for k := 0; k < ln.count; k++ {
				i := ln.first + k
				e := s.entries[i]
				cx := x0 + k*(cellW+gap)
				pic := image.Rect(cx+(cellW-side)/2, y, cx+(cellW-side)/2+side, y+side)
				kind := libEntryType(e, s.kind)
				if img := libPics.picture(client.ImageURL(e, 256), side); img != nil {
					r.drawLibPicture(img, pic)
				} else {
					fill := lerp(pal.card, pal.text, 0.12)
					r.roundFill(pic, pal.rad*0.6, fill, fill)
					r.mdiIconIn(libIcon(kind), pic, 44, pal.sub)
				}
				index := ln.sec*libSecStride + i
				nameC := pal.text
				if lit(index) {
					nameC = pal.accent
				}
				tw := cellW - r.s(4)
				r.text(fc.sub, r.fit(fc.sub, dashboard.CleanName(e.Name), tw), cx+r.s(2), y+side+r.s(21), nameC)
				if kind == "album" || kind == "playlist" {
					if by := dashboard.CleanName(e.By()); by != "" {
						r.text(fc.sub, r.fit(fc.sub, by, tw), cx+r.s(2), y+side+r.s(42), pal.sub)
					}
				}
				zones = append(zones, mediaZone{r: image.Rect(cx, y, cx+cellW, y+ln.h), kind: libPartItem, index: index})
			}
		default:
			i := ln.first
			e := s.entries[i]
			row := image.Rect(x0, y, x1, y+ln.h)
			index := ln.sec*libSecStride + i
			fill := lerp(pal.card, pal.text, 0.06)
			fg := pal.text
			if lit(index) {
				fill, fg = pal.accent, pal.bg
			}
			r.roundFill(row, pal.rad*0.5, fill, fill)
			ps := ln.h - r.s(8)
			pic := image.Rect(row.Min.X+r.s(4), row.Min.Y+r.s(4), row.Min.X+r.s(4)+ps, row.Min.Y+r.s(4)+ps)
			if img := libPics.picture(client.ImageURL(e, 256), ps); img != nil {
				r.drawLibPicture(img, pic)
			} else {
				r.mdiIconIn(libIcon(libEntryType(e, s.kind)), pic, 24, lerp(fill, fg, 0.6))
			}
			tx := pic.Max.X + r.s(12)
			tw := row.Max.X - r.s(12) - tx
			r.text(fc.sub, r.fit(fc.sub, dashboard.CleanName(e.Name), tw), tx, row.Min.Y+r.s(22), fg)
			sub := dashboard.CleanName(e.By())
			if e.Album != nil && e.Album.Name != "" && p.what != libAlbum {
				if sub != "" {
					sub += " · "
				}
				sub += dashboard.CleanName(e.Album.Name)
			}
			if sub != "" {
				r.text(fc.sub, r.fit(fc.sub, sub, tw), tx, row.Min.Y+r.s(44), lerp(fill, fg, 0.65))
			}
			zones = append(zones, mediaZone{r: row, kind: libPartItem, index: index})
		}
		y += ln.h + gap
	}
	// The block a swipe pages through; after the items, so a tap on one finds the item.
	zones = append(zones, mediaZone{r: image.Rect(x0, top, x1, foot), kind: libPartPage})
	if n := len(pages); n > 1 && n <= 15 {
		r.pageDots(card.Min.X, card.Dx(), foot+r.s(8), n, page, pal)
	} else if n > 15 {
		t := fmt.Sprintf("%d / %d", page+1, n)
		r.text(fc.sub, t, card.Min.X+(card.Dx()-r.width(fc.sub, t))/2, foot+r.s(18), pal.sub)
	}
	if p.problem != "" {
		// A next part that would not come: said at the foot, under what did.
		w := r.width(fc.sub, p.problem)
		r.text(fc.sub, r.fit(fc.sub, p.problem, inner*2/5), x1-min(w, inner*2/5), foot+r.s(18), pal.sub)
	}
	note(len(pages))
	r.setLibrary(zones, whole, len(pages), per)
}

// tileAction does what a tile's card says. A card that navigates to dashboard.LibraryPath opens the
// music library, to play on the card's media player when it names one.
func (d *Display) tileAction(t *dashTile) {
	a := *t.action
	if a.View == dashboard.LibraryPath {
		entity := a.Entity
		if entity == "" {
			entity = t.entity
		}
		d.openLibrary("", entity, true)
		return
	}
	dashboard.Get().Tap(a)
}

// libraryUp is whether the music library is up.
func (d *Display) libraryUp() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.dashLibrary != nil
}
