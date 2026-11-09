//go:build !dot && !spot

package display

import (
	"context"
	"image"
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
// dashboard.LibraryPath. Pill tabs along the top hold the artists, albums, playlists, tracks and radio
// stations, and a search; each is a grid of covers that a finger scrolls up and down, and the next of
// the library is asked for as the grid nears its end. An artist opens on its albums and top tracks, an
// album or a playlist on its tracks, each a page of its own that Back leaves for the one before, where
// it was. A track or a station plays at a tap; an artist, album or playlist plays from its page, in
// order or shuffled. After a play the library goes, for what is playing (libLeave).
//
// The library is read from Music Assistant itself, with the token set up on the device, and played
// through Home Assistant, as the media sheet plays a favorite: on Music Assistant's player for the
// sheet's player, the card's player, or this device. Drawing and scrolling are in library_draw.go.

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
	libPartField   // the search's field, which opens the keyboard
	libPartContent // what scrolls up and down, where a finger drags it
	libPartRow     // index: a section shown as a row that scrolls along, where a finger drags it so
)

// libSecStride packs an item's section and its place there into one index: section*libSecStride + i.
const libSecStride = 1 << 20

// How a section is shown: a grid of covers, a row of covers that scrolls along, or tracks in rows.
const (
	libShowGrid = iota
	libShowRow
	libShowTracks
)

// libSection is one list on a page: its heading (a word, or -1 for none), how it is shown, what kind
// its items are, and the items.
type libSection struct {
	word    int
	show    int
	kind    string
	entries []musicassistant.Entry
	// derived is a tab made from the saved tracks' albums or artists, the library having none of its
	// own (libFromTracks).
	derived bool
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
	query    string
	// scroll is how far down the page is scrolled, and along how far each section shown as a row is,
	// in pixels; drawing keeps both within what there is (libClamp).
	scroll int
	along  []int
}

// libDrag is a finger on the library as it moves: where it came down, what it came down on, how far
// the page (or the row) was scrolled then, and which way it is scrolling once it has gone far enough
// to tell (0 not yet, libDragDown, libDragAlong).
type libDrag struct {
	at        image.Point
	row       int // the section shown as a row it came down on, or -1
	base      int
	way       int
	scrolling bool
}

const (
	libDragDown = 1 + iota
	libDragAlong
)

// librarySheet is the library while it is up.
type librarySheet struct {
	// opening tells one opening of the library from the next: a player looked up for one is not set
	// on another opened while the lookup took its time.
	opening uint64
	// target is the player music goes to, Music Assistant's; empty when there is none, or while
	// finding says it is still being looked for.
	target  string
	finding bool
	// set is whether Music Assistant is set up on the device, and base its address, for pictures.
	set  bool
	base string
	// from is the media player the library was opened for, a media sheet's or a card's; empty for none.
	from string
	tab  int
	// roots are the tabs as they were left, and stack the pages opened from the one shown, the last on
	// top.
	roots [libTabs]libPage
	stack []libPage
	// typing is the keyboard up for a search, with text typed so far.
	typing           bool
	text             string
	shifted, symbols bool
	// lit is the item just tapped, or Play or Shuffle (libLitPlay, libLitShuffle), drawn lit until
	// litUntil, so a tap is seen to have landed.
	lit      int
	litUntil time.Time
	drag     libDrag
}

// Play and Shuffle as lit, beside the items' own indexes.
const (
	libLitPlay    = -2
	libLitShuffle = -3
)

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

// isLit is whether which was tapped just now.
func (l *librarySheet) isLit(which int) bool { return l.lit == which && time.Now().Before(l.litUntil) }

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

// libAfter waits before the play after Home Assistant is asked to set the shuffle; a variable for the
// tests.
var libAfter = time.AfterFunc

// libShuffleFirst is how long a play waits after Home Assistant is asked to set the shuffle: each call
// goes on a goroutine of its own, so without the wait the play could arrive first. Music Assistant
// shuffles a queue as it is loaded when its shuffle is on, so the first track is a shuffled one too.
const libShuffleFirst = 400 * time.Millisecond

// libSearchLimit is how many of each kind a search finds.
const libSearchLimit = 12

// libWords are the library's words, in the screen's language.
var libWords = map[string][]string{
	"": {"Artists", "Albums", "Playlists", "Tracks", "Radio", "Search", "Back", "Play", "Shuffle", "Done",
		"Loading…", "Nothing here yet",
		"Set up the music library on the setup page (Sound & Voice, Music Assistant) to browse it here.",
		"No player found: browse only", "Top tracks", "Tap to search",
		"Search the music library", "No results", "Library",
		"From your saved tracks", "No stations saved in Music Assistant", "Search stations", "Save music in Music Assistant, or search for it here."},
	"ja": {"アーティスト", "アルバム", "プレイリスト", "曲", "ラジオ", "検索", "戻る", "再生", "シャッフル", "完了",
		"読み込み中…", "まだ何もありません",
		"ここで閲覧するには、設定ページ（Sound & Voice、Music Assistant）で音楽ライブラリを設定してください。",
		"Music Assistantのプレーヤーがありません（閲覧のみ）", "人気曲", "タップして検索",
		"音楽ライブラリを検索", "見つかりません", "ライブラリ",
		"保存した曲から", "Music Assistantに保存された局はありません", "局を検索", "Music Assistantに音楽を保存するか、ここで検索してください。"},
	"de": {"Interpreten", "Alben", "Playlists", "Titel", "Radio", "Suche", "Zurück", "Abspielen", "Zufall", "Fertig",
		"Lädt …", "Hier ist noch nichts",
		"Richte die Musikbibliothek auf der Einrichtungsseite ein (Sound & Voice, Music Assistant), um sie hier zu durchsuchen.",
		"Kein Music-Assistant-Player zum Abspielen: nur ansehen", "Top-Titel", "Tippen zum Suchen",
		"Musikbibliothek durchsuchen", "Keine Treffer", "Bibliothek",
		"Aus deinen gespeicherten Titeln", "Keine Sender in Music Assistant gespeichert", "Sender suchen", "Speichere Musik in Music Assistant oder suche sie hier."},
	"es": {"Artistas", "Álbumes", "Listas", "Canciones", "Radio", "Buscar", "Atrás", "Reproducir", "Aleatorio", "Listo",
		"Cargando…", "Aún no hay nada",
		"Configura la biblioteca de música en la página de configuración (Sound & Voice, Music Assistant) para explorarla aquí.",
		"No hay reproductor de Music Assistant: solo explorar", "Canciones populares", "Toca para buscar",
		"Buscar en la biblioteca", "Sin resultados", "Biblioteca",
		"De tus canciones guardadas", "No hay emisoras guardadas en Music Assistant", "Buscar emisoras", "Guarda música en Music Assistant o búscala aquí."},
	"fr": {"Artistes", "Albums", "Playlists", "Titres", "Radio", "Recherche", "Retour", "Écouter", "Aléatoire", "OK",
		"Chargement…", "Rien pour l'instant",
		"Configurez la bibliothèque musicale sur la page de configuration (Sound & Voice, Music Assistant) pour la parcourir ici.",
		"Aucun lecteur Music Assistant : consultation seulement", "Titres populaires", "Touchez pour chercher",
		"Chercher dans la bibliothèque", "Aucun résultat", "Bibliothèque",
		"D'après vos titres enregistrés", "Aucune station enregistrée dans Music Assistant", "Chercher des stations", "Enregistrez de la musique dans Music Assistant, ou cherchez-la ici."},
	"it": {"Artisti", "Album", "Playlist", "Brani", "Radio", "Cerca", "Indietro", "Riproduci", "Casuale", "Fatto",
		"Caricamento…", "Ancora niente qui",
		"Configura la libreria musicale nella pagina di configurazione (Sound & Voice, Music Assistant) per sfogliarla qui.",
		"Nessun lettore Music Assistant: solo consultazione", "Brani più ascoltati", "Tocca per cercare",
		"Cerca nella libreria", "Nessun risultato", "Libreria",
		"Dai tuoi brani salvati", "Nessuna stazione salvata in Music Assistant", "Cerca stazioni", "Salva musica in Music Assistant o cercala qui."},
	"pt": {"Artistas", "Álbuns", "Playlists", "Faixas", "Rádio", "Buscar", "Voltar", "Tocar", "Aleatório", "Pronto",
		"Carregando…", "Nada aqui ainda",
		"Configure a biblioteca de música na página de configuração (Sound & Voice, Music Assistant) para navegar nela aqui.",
		"Nenhum player do Music Assistant: só navegar", "Mais tocadas", "Toque para buscar",
		"Buscar na biblioteca", "Nenhum resultado", "Biblioteca",
		"Das suas faixas salvas", "Nenhuma estação salva no Music Assistant", "Buscar estações", "Salve música no Music Assistant ou busque aqui."},
	"ru": {"Исполнители", "Альбомы", "Плейлисты", "Треки", "Радио", "Поиск", "Назад", "Играть", "Вперемешку", "Готово",
		"Загрузка…", "Здесь пока пусто",
		"Настройте музыкальную библиотеку на странице настройки (Sound & Voice, Music Assistant), чтобы просматривать её здесь.",
		"Нет плеера Music Assistant: только просмотр", "Популярные треки", "Нажмите для поиска",
		"Поиск в библиотеке", "Ничего не найдено", "Библиотека",
		"Из сохранённых треков", "В Music Assistant нет сохранённых станций", "Найти станции", "Сохраните музыку в Music Assistant или найдите её здесь."},
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
	libWordFromTracks
	libWordNoStations
	libWordSearchStations
	libWordEmptyHint
)

func libWord(i int) string {
	w, ok := libWords[screenLang()]
	if !ok {
		w = libWords[""]
	}
	return w[i]
}

// openLibrary puts the library up. From a media sheet, target is the player it plays on and entity the
// sheet's own; from a card, target is empty and the player is found for entity, the card's media
// player, or for this device when the card names none.
func (d *Display) openLibrary(target, entity string, fromCard bool) {
	c, set := libClient()
	slog.Info("dashboard music library", "target", target, "entity", entity, "set", set)
	l := &librarySheet{opening: libOpenings.Add(1), target: target, finding: fromCard, set: set, base: c.URL, from: entity, lit: -1}
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
		opening := l.opening
		go func() {
			t := libFindPlayer(entity)
			d.editLibrary(func(n *librarySheet) {
				if n.opening == opening {
					n.target, n.finding = t, false
				}
			})
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

// libraryViewNow is the library for a frame; nil without one. A tab scrolled near the end of what it
// has asks for the next of the library here, so it is there by the time the finger gets to it.
func (d *Display) libraryViewNow() *libraryView {
	d.mu.Lock()
	if d.dashLibrary == nil {
		d.mu.Unlock()
		return nil
	}
	v := &libraryView{sheet: *d.dashLibrary}
	d.mu.Unlock()
	if p := v.sheet.current(); p.more && !p.loading && d.libNearEnd() {
		d.libLoad(p.id)
	}
	return v
}

// libNearEnd is whether the page last drawn was scrolled to within a screen of its end.
func (d *Display) libNearEnd() bool {
	if d.r == nil {
		return false
	}
	d.r.zmu.Lock()
	defer d.r.zmu.Unlock()
	return d.r.libContentH > 0 && d.r.libScrolled+2*d.r.libViewH >= d.r.libContentH
}

// libLimit is how many items a tab asks for at a time: a few screens' worth, as the page last showed.
func libLimit(visible int) int {
	if visible <= 0 {
		return 48
	}
	return min(max(visible*4, 24), 120)
}

// libLoad asks Music Assistant for what page id shows, or for the next of it, and puts it on the page
// when it comes. A page already being asked for is left to that.
func (d *Display) libLoad(id int64) {
	c, set := libClient()
	if !set {
		return
	}
	visible := 0
	if d.r != nil {
		d.r.zmu.Lock()
		visible = d.r.libVisible
		d.r.zmu.Unlock()
	}
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
		sections, more, err := fetchLibPage(ctx, c, p, libLimit(visible))
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
		full := len(got) >= limit
		got = usable(got)
		if offset == 0 && len(got) == 0 && (p.what == libArtists || p.what == libAlbums) {
			// A library of saved tracks alone has no artists or albums of its own: theirs stand in.
			artists, albums, err := libFromTracks(ctx, c)
			if err != nil {
				return nil, false, err
			}
			from := artists
			if p.what == libAlbums {
				from = albums
			}
			return []libSection{{word: -1, show: libShowGrid, kind: libTypes[p.what], entries: from, derived: len(from) > 0}}, false, nil
		}
		return []libSection{{word: -1, show: libShowGrid, kind: libTypes[p.what], entries: got}}, full, nil
	case libSearch:
		f, err := c.SearchAll(ctx, p.query, libSearchLimit)
		if err != nil {
			return nil, false, err
		}
		return []libSection{
			{word: libArtists, show: libShowRow, kind: "artist", entries: usable(f.Artists)},
			{word: libAlbums, show: libShowRow, kind: "album", entries: usable(f.Albums)},
			{word: libPlaylists, show: libShowRow, kind: "playlist", entries: usable(f.Playlists)},
			{word: libTracks, show: libShowTracks, kind: "track", entries: usable(f.Tracks)},
			{word: libRadio, show: libShowRow, kind: "radio", entries: usable(f.Radio)},
		}, false, nil
	case libArtist:
		albums, aerr := c.ArtistAlbums(ctx, p.entry)
		top, terr := c.TopTracks(ctx, p.entry)
		out := []libSection{{word: libAlbums, show: libShowRow, kind: "album", entries: usable(albums)},
			{word: libWordTopTracks, show: libShowTracks, kind: "track", entries: usable(top)}}
		if aerr != nil && terr != nil {
			return nil, false, aerr
		}
		if aerr != nil || terr != nil {
			return out, false, firstErr(aerr, terr)
		}
		return out, false, nil
	case libAlbum:
		got, err := c.AlbumTracks(ctx, p.entry)
		return []libSection{{word: -1, show: libShowTracks, kind: "track", entries: usable(got)}}, false, err
	case libPlaylist:
		got, err := c.PlaylistTracks(ctx, p.entry)
		return []libSection{{word: -1, show: libShowTracks, kind: "track", entries: usable(got)}}, false, err
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
			// left, and asks for more only as it is scrolled.
			p := n.roots[tab]
			id, ask = p.id, tab != libSearch && (!p.loaded || p.problem != "")
		})
		if ask {
			d.libLoad(id)
		}
	case libPartGoSearch:
		d.editLibrary(func(n *librarySheet) { n.tab, n.stack = libSearch, nil })
	case libPartField:
		d.editLibrary(func(n *librarySheet) {
			n.typing, n.text, n.shifted, n.symbols = true, n.roots[libSearch].query, false, false
		})
	case libPartPlay, libPartShuffle:
		lit := libLitPlay
		if z.kind == libPartShuffle {
			lit = libLitShuffle
		}
		d.libPlayTapped(l, p.entry, libEntryType(p.entry, libPageType(p.what)), z.kind == libPartShuffle, lit)
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
			d.libPlayTapped(l, e, kind, false, z.index)
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

// libLeaveAfter waits, after a play, for the tap to be seen lit before the library goes; a variable
// for the tests.
var libLeaveAfter = time.AfterFunc

// libPlayTapped plays e from a tap, lights what was tapped (lit), and then leaves the library for what
// is playing (libLeave). A tap that showed nothing was tapped again, and when Music Assistant was slow
// both plays went through, the first heard for a few seconds before the second replaced it.
func (d *Display) libPlayTapped(l *librarySheet, e musicassistant.Entry, kind string, shuffle bool, lit int) {
	if l.target == "" || e.URI == "" {
		return
	}
	d.libPlay(l.target, e, kind, shuffle)
	d.editLibrary(func(n *librarySheet) { n.lit, n.litUntil = lit, time.Now().Add(tapLit) })
	target, from := l.target, l.from
	libLeaveAfter(tapLit, func() { d.libLeave(target, from) })
}

// libIsOwn is whether target is this device's own player: its speaker in Home Assistant, or Music
// Assistant's player for it. It asks Home Assistant; a variable for the tests.
var libIsOwn = func(target string) bool {
	own := home.SpeakerEntity()
	if target == own {
		return true
	}
	if t := dashboard.MusicTarget(own); t != "" && t == target {
		return true
	}
	return target == home.MusicAssistantPlayer()
}

// libOpenSheet puts the media sheet of entity up; a variable for the tests.
var libOpenSheet = func(d *Display, entity string) bool { return d.openMedia(entity) }

// libLeave puts the library away after a play, for what is playing: this device's Now Playing page
// when the music is on this device, or the media sheet of the player the library came from, with what
// it plays and its controls. Opened from a card with no player of its own, it only goes. A library
// already put away, or opened again since, is left alone.
func (d *Display) libLeave(target, from string) {
	own := libIsOwn(target)
	d.mu.Lock()
	l := d.dashLibrary
	if l == nil || l.target != target || l.from != from {
		d.mu.Unlock()
		return
	}
	d.dashLibrary = nil
	if own {
		// The Now Playing page, even one put away for an earlier track: the music asked for here brings
		// it back, as a tap on the playing word does.
		d.away, d.awayTrack, d.awayStation, d.awayPlaying = false, "", "", false
		d.musicQuiet.reopened(time.Now())
	}
	d.mu.Unlock()
	slog.Info("dashboard music library leaves", "target", target, "own", own, "from", from)
	switch {
	case own:
		// The dashboard goes, and the idle screen is Now Playing as the stream comes.
		d.closeDashboard()
	case strings.HasPrefix(from, "media_player."):
		libOpenSheet(d, from)
	}
	d.wake()
}

// libQueueOf is the Music Assistant queue target plays from; a variable for the tests.
var libQueueOf = dashboard.MusicQueue

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
	if kind != "artist" && kind != "album" && kind != "playlist" {
		play()
		return
	}
	go func() {
		if libQueueShuffle(target, shuffle) {
			play() // Music Assistant has answered, so the shuffle is set before the play is asked for
			return
		}
		libTap(dashboard.Action{Entity: target, Service: "media_player.shuffle_set", Data: map[string]any{"shuffle": shuffle}})
		libAfter(libShuffleFirst, play)
	}()
}

// libQueueShuffle sets the shuffle of target's queue through Music Assistant's own API, and says
// whether it did. Home Assistant's shuffle_set is refused by some of Music Assistant's players
// ("Invalid or unsupported command"), and the queue is what Music Assistant shuffles. Without a queue
// to name, or with a token that may not set it, Home Assistant's is left to try.
func libQueueShuffle(target string, on bool) bool {
	c, set := libClient()
	if !set {
		return false
	}
	q := libQueueOf(target)
	if q == "" {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := c.SetShuffle(ctx, q, on); err != nil {
		slog.Warn("dashboard music library shuffle", "queue", q, "err", err)
		return false
	}
	return true
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

// libOpenings counts the library's openings, for librarySheet.opening.
var libOpenings atomic.Uint64
