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
	libPodcasts // the second row of tabs, under the first
	libAudiobooks
	libFavorites // the second row's icons, at its right end
	libRecent
	libQueue
	libBrowse
	libTabs // how many tabs there are
	libArtist
	libAlbum
	libPlaylist
	libPodcast   // a podcast, opened for its episodes
	libAudiobook // an audiobook, opened for its chapters
	libFolder    // a folder of the browse tree
)

// How many favorites of each kind, recently played items and queued items a page shows.
const (
	libFavoriteLimit = 50
	libRecentLimit   = 50
	libQueueLimit    = 100
)

// The audiobooks are read this many at a time, up to libAudiobookMax of them.
const (
	libAudiobookPage = 200
	libAudiobookMax  = 5000
)

// libKinds are the tabs' kinds, as Music Assistant's commands name them.
var libKinds = [...]string{libArtists: musicassistant.KindArtists, libAlbums: musicassistant.KindAlbums,
	libPlaylists: musicassistant.KindPlaylists, libTracks: musicassistant.KindTracks, libRadio: musicassistant.KindRadios,
	libPodcasts: musicassistant.KindPodcasts, libAudiobooks: musicassistant.KindAudiobooks,
	libFavorites: "", libRecent: "", libQueue: "", libBrowse: ""}

// libTypes are the tabs' items' media types, for an item that does not say its own.
var libTypes = [...]string{libArtists: "artist", libAlbums: "album", libPlaylists: "playlist", libTracks: "track", libRadio: "radio",
	libPodcasts: "podcast", libAudiobooks: "audiobook",
	libFavorites: "favorite", libRecent: "recent", libQueue: "queue", libBrowse: "browse"}

// libRowTabs is how many tabs the top row holds (artists to search); the rest sit on the row under it.
const libRowTabs = libSearch + 1

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
	queue    string               // the queue tab's Music Assistant queue, looked up as it loads
	path     string               // a folder's place in the browse tree; empty for its top
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
	// audiobookSort is the audiobooks' order: by author, title or newest (musicassistant.AudiobookByAuthor ...).
	audiobookSort int
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
	// menu is the action sheet held open over an item by a long press: play it now, next, or at the
	// end of the queue. While it is up it takes every tap.
	menu libMenu
}

// libMenu is the action sheet over an item a finger held: the item, its media type, its index on the
// page (section*libSecStride + i), which is drawn lit while the sheet is up, and the choices it offers
// (libMenuPlay ...), in the order drawn. Held on the queue tab, queue is that queue, page the tab's page
// and pos the item's place from what is playing (0) on.
type libMenu struct {
	open    bool
	entry   musicassistant.Entry
	kind    string
	item    int
	choices []int
	queue   string
	page    int64
	pos     int
}

// What the action sheet can offer. The first four play the item (with the enqueue mode in
// libMenuEnqueue); the favorites ones mark it or unmark it; the last three are for an item of the queue:
// play the queue from it, move it to play next, or take it out.
const (
	libMenuPlay = iota
	libMenuNext
	libMenuAdd
	libMenuRadio // Music Assistant's radio mode: the item, then more like it, without end
	libMenuFavorite
	libMenuUnfavorite
	libMenuPlayFrom
	libMenuMoveNext
	libMenuRemove
	libMenuKinds
)

// libMenuChoices is how many choices an item of a library page offers: the four ways to play it and
// one of the favorites ones.
const libMenuChoices = libMenuRadio + 2

var libMenuEnqueue = [...]string{libMenuPlay: libEnqueueReplace, libMenuNext: libEnqueueNext, libMenuAdd: libEnqueueAdd,
	libMenuRadio: libEnqueueRadio}

// libMenuFor is what the sheet offers for e, of media type kind: on the queue tab (onQueue), at pos
// from what is playing, its queue's choices; elsewhere the ways to play it. Either way, a kind that can
// be a favorite can be marked so, or unmarked when the library has it.
func libMenuFor(e musicassistant.Entry, kind string, onQueue bool, pos int) []int {
	var out []int
	switch {
	case !onQueue:
		out = []int{libMenuPlay, libMenuNext, libMenuAdd, libMenuRadio}
	case pos > 0:
		out = []int{libMenuPlayFrom}
		if pos > 1 {
			out = append(out, libMenuMoveNext) // the next one already is
		}
		out = append(out, libMenuRemove)
	}
	switch kind {
	case "track", "album", "artist", "playlist", "radio", "podcast", "audiobook":
		if !e.Favorite {
			out = append(out, libMenuFavorite)
		} else if _, ok := musicassistant.LibraryID(e); ok {
			out = append(out, libMenuUnfavorite)
		}
	}
	return out
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
func (l *librarySheet) isLit(which int) bool {
	return l.lit == which && time.Now().Before(l.litUntil) || l.menu.open && l.menu.item == which
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
	"": {"Artists", "Albums", "Playlists", "Tracks", "Radio", "Search", "Podcasts", "Audiobooks", "Favorites", "Recent", "Queue", "Browse", "Back", "Play", "Shuffle", "Done",
		"Loading…", "Nothing here yet",
		"Set up the music library on the setup page (Sound & Voice, Music Assistant) to browse it here.",
		"No player found: browse only", "Top tracks", "Tap to search",
		"Search the music library", "No results", "Library",
		"From your saved tracks", "No stations saved in Music Assistant", "Search stations", "Save music in Music Assistant, or search for it here.",
		"Play next", "Add to queue", "By author", "By title", "Newest", "Resume at %s", "Start over", "Start radio",
		"Add to favorites", "Remove from favorites", "Play from here", "Remove from queue"},
	"ja": {"アーティスト", "アルバム", "プレイリスト", "曲", "ラジオ", "検索", "ポッドキャスト", "オーディオブック", "お気に入り", "最近再生", "キュー", "フォルダ", "戻る", "再生", "シャッフル", "完了",
		"読み込み中…", "まだ何もありません",
		"ここで閲覧するには、設定ページ（Sound & Voice、Music Assistant）で音楽ライブラリを設定してください。",
		"Music Assistantのプレーヤーがありません（閲覧のみ）", "人気曲", "タップして検索",
		"音楽ライブラリを検索", "見つかりません", "ライブラリ",
		"保存した曲から", "Music Assistantに保存された局はありません", "局を検索", "Music Assistantに音楽を保存するか、ここで検索してください。",
		"次に再生", "キューに追加", "作者順", "タイトル順", "新着順", "%s から再開", "最初から", "ラジオを開始",
		"お気に入りに追加", "お気に入りから削除", "ここから再生", "キューから削除"},
	"de": {"Interpreten", "Alben", "Playlists", "Titel", "Radio", "Suche", "Podcasts", "Hörbücher", "Favoriten", "Zuletzt gehört", "Warteschlange", "Ordner", "Zurück", "Abspielen", "Zufall", "Fertig",
		"Lädt …", "Hier ist noch nichts",
		"Richte die Musikbibliothek auf der Einrichtungsseite ein (Sound & Voice, Music Assistant), um sie hier zu durchsuchen.",
		"Kein Music-Assistant-Player zum Abspielen: nur ansehen", "Top-Titel", "Tippen zum Suchen",
		"Musikbibliothek durchsuchen", "Keine Treffer", "Bibliothek",
		"Aus deinen gespeicherten Titeln", "Keine Sender in Music Assistant gespeichert", "Sender suchen", "Speichere Musik in Music Assistant oder suche sie hier.",
		"Als Nächstes abspielen", "Zur Warteschlange hinzufügen", "Nach Autor", "Nach Titel", "Neueste", "Weiter bei %s", "Von vorn", "Radio starten",
		"Zu Favoriten hinzufügen", "Aus Favoriten entfernen", "Ab hier abspielen", "Aus der Warteschlange entfernen"},
	"es": {"Artistas", "Álbumes", "Listas", "Canciones", "Radio", "Buscar", "Podcasts", "Audiolibros", "Favoritos", "Recientes", "Cola", "Carpetas", "Atrás", "Reproducir", "Aleatorio", "Listo",
		"Cargando…", "Aún no hay nada",
		"Configura la biblioteca de música en la página de configuración (Sound & Voice, Music Assistant) para explorarla aquí.",
		"No hay reproductor de Music Assistant: solo explorar", "Canciones populares", "Toca para buscar",
		"Buscar en la biblioteca", "Sin resultados", "Biblioteca",
		"De tus canciones guardadas", "No hay emisoras guardadas en Music Assistant", "Buscar emisoras", "Guarda música en Music Assistant o búscala aquí.",
		"Reproducir a continuación", "Añadir a la cola", "Por autor", "Por título", "Recientes", "Reanudar en %s", "Desde el principio", "Iniciar radio",
		"Añadir a favoritos", "Quitar de favoritos", "Reproducir desde aquí", "Quitar de la cola"},
	"fr": {"Artistes", "Albums", "Playlists", "Titres", "Radio", "Recherche", "Podcasts", "Livres audio", "Favoris", "Récents", "File d'attente", "Dossiers", "Retour", "Écouter", "Aléatoire", "OK",
		"Chargement…", "Rien pour l'instant",
		"Configurez la bibliothèque musicale sur la page de configuration (Sound & Voice, Music Assistant) pour la parcourir ici.",
		"Aucun lecteur Music Assistant : consultation seulement", "Titres populaires", "Touchez pour chercher",
		"Chercher dans la bibliothèque", "Aucun résultat", "Bibliothèque",
		"D'après vos titres enregistrés", "Aucune station enregistrée dans Music Assistant", "Chercher des stations", "Enregistrez de la musique dans Music Assistant, ou cherchez-la ici.",
		"Écouter ensuite", "Ajouter à la file d'attente", "Par auteur", "Par titre", "Récents", "Reprendre à %s", "Depuis le début", "Lancer la radio",
		"Ajouter aux favoris", "Retirer des favoris", "Écouter à partir d'ici", "Retirer de la file d'attente"},
	"it": {"Artisti", "Album", "Playlist", "Brani", "Radio", "Cerca", "Podcast", "Audiolibri", "Preferiti", "Recenti", "Coda", "Cartelle", "Indietro", "Riproduci", "Casuale", "Fatto",
		"Caricamento…", "Ancora niente qui",
		"Configura la libreria musicale nella pagina di configurazione (Sound & Voice, Music Assistant) per sfogliarla qui.",
		"Nessun lettore Music Assistant: solo consultazione", "Brani più ascoltati", "Tocca per cercare",
		"Cerca nella libreria", "Nessun risultato", "Libreria",
		"Dai tuoi brani salvati", "Nessuna stazione salvata in Music Assistant", "Cerca stazioni", "Salva musica in Music Assistant o cercala qui.",
		"Riproduci dopo", "Aggiungi alla coda", "Per autore", "Per titolo", "Recenti", "Riprendi da %s", "Dall'inizio", "Avvia radio",
		"Aggiungi ai preferiti", "Rimuovi dai preferiti", "Riproduci da qui", "Rimuovi dalla coda"},
	"pt": {"Artistas", "Álbuns", "Playlists", "Faixas", "Rádio", "Buscar", "Podcasts", "Audiolivros", "Favoritos", "Recentes", "Fila", "Pastas", "Voltar", "Tocar", "Aleatório", "Pronto",
		"Carregando…", "Nada aqui ainda",
		"Configure a biblioteca de música na página de configuração (Sound & Voice, Music Assistant) para navegar nela aqui.",
		"Nenhum player do Music Assistant: só navegar", "Mais tocadas", "Toque para buscar",
		"Buscar na biblioteca", "Nenhum resultado", "Biblioteca",
		"Das suas faixas salvas", "Nenhuma estação salva no Music Assistant", "Buscar estações", "Salve música no Music Assistant ou busque aqui.",
		"Tocar em seguida", "Adicionar à fila", "Por autor", "Por título", "Recentes", "Retomar em %s", "Do início", "Iniciar rádio",
		"Adicionar aos favoritos", "Remover dos favoritos", "Tocar a partir daqui", "Remover da fila"},
	"ru": {"Исполнители", "Альбомы", "Плейлисты", "Треки", "Радио", "Поиск", "Подкасты", "Аудиокниги", "Избранное", "Недавние", "Очередь", "Папки", "Назад", "Играть", "Вперемешку", "Готово",
		"Загрузка…", "Здесь пока пусто",
		"Настройте музыкальную библиотеку на странице настройки (Sound & Voice, Music Assistant), чтобы просматривать её здесь.",
		"Нет плеера Music Assistant: только просмотр", "Популярные треки", "Нажмите для поиска",
		"Поиск в библиотеке", "Ничего не найдено", "Библиотека",
		"Из сохранённых треков", "В Music Assistant нет сохранённых станций", "Найти станции", "Сохраните музыку в Music Assistant или найдите её здесь.",
		"Играть следующим", "Добавить в очередь", "По автору", "По названию", "Новые", "Продолжить с %s", "С начала", "Запустить радио",
		"Добавить в избранное", "Убрать из избранного", "Играть отсюда", "Убрать из очереди"},
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
	libWordPlayNext
	libWordAddToQueue
	libWordSortAuthor // the audiobooks' three orders, in turn: author, title, newest
	libWordSortTitle
	libWordSortRecent
	libWordResumeAt // "Resume at 1:02:03", with the time put in
	libWordStartOver
	libWordStartRadio
	libWordAddFavorite
	libWordRemoveFavorite
	libWordPlayFrom // play the queue from this item on
	libWordRemoveFromQueue
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
	// The player and the sheet's entity are Home Assistant entity ids (they can carry a room or a name),
	// kept out of the log and so out of a diagnostics bundle.
	slog.Info("dashboard music library", "set", set)
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
	target := ""
	d.editLibrary(func(n *librarySheet) {
		target = n.target
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
		if p.what == libQueue {
			p.queue = libQueueOf(target)
		}
		sections, more, err := fetchLibPage(ctx, c, p, libLimit(visible))
		d.editLibrary(func(n *librarySheet) {
			pg := n.byID(id)
			if pg == nil {
				return
			}
			pg.loading, pg.loaded = false, true
			// The queue the page was read from, for a tap or a hold on one of its items.
			pg.queue = p.queue
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
	case libFavorites:
		// Each kind's favorites, as a search shows what it found: a row of each, the tracks as tracks.
		kinds := []struct {
			word, show int
			kind, typ  string
		}{
			{libTracks, libShowTracks, musicassistant.KindTracks, "track"},
			{libAlbums, libShowRow, musicassistant.KindAlbums, "album"},
			{libArtists, libShowRow, musicassistant.KindArtists, "artist"},
			{libPlaylists, libShowRow, musicassistant.KindPlaylists, "playlist"},
			{libRadio, libShowRow, musicassistant.KindRadios, "radio"},
			{libPodcasts, libShowRow, musicassistant.KindPodcasts, "podcast"},
			{libAudiobooks, libShowRow, musicassistant.KindAudiobooks, "audiobook"},
		}
		var out []libSection
		var firstErr error
		failed := 0
		for _, k := range kinds {
			got, err := c.Favorites(ctx, k.kind, libFavoriteLimit)
			if err != nil {
				failed++
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			out = append(out, libSection{word: k.word, show: k.show, kind: k.typ, entries: usable(got)})
		}
		if failed == len(kinds) {
			return nil, false, firstErr
		}
		return out, false, nil
	case libRecent:
		got, err := c.RecentlyPlayed(ctx, libRecentLimit)
		return []libSection{{word: -1, show: libShowTracks, kind: "", entries: usable(got)}}, false, err
	case libQueue:
		if p.queue == "" {
			return nil, false, nil // no queue to show: the page says there is nothing yet
		}
		got, err := c.Queue(ctx, p.queue, libQueueLimit)
		return []libSection{{word: -1, show: libShowTracks, kind: "track", entries: usable(got)}}, false, err
	case libBrowse, libFolder:
		got, err := c.Browse(ctx, p.path)
		return []libSection{{word: -1, show: libShowTracks, kind: "", entries: usable(got)}}, false, err
	case libAudiobooks:
		// The whole list at once, so it can be put in order by author and series; a library of a few
		// thousand books is still a few reads of the server.
		var all []musicassistant.Entry
		for offset := 0; offset < libAudiobookMax; offset += libAudiobookPage {
			got, err := c.Library(ctx, libKinds[libAudiobooks], offset, libAudiobookPage, "")
			if err != nil {
				return nil, false, err
			}
			all = append(all, got...)
			if len(got) < libAudiobookPage {
				break
			}
		}
		return []libSection{{word: -1, show: libShowGrid, kind: libTypes[libAudiobooks], entries: usable(musicassistant.AudiobookOrder(all))}}, false, nil
	case libArtists, libAlbums, libPlaylists, libTracks, libRadio, libPodcasts:
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
			{word: libPodcasts, show: libShowRow, kind: "podcast", entries: usable(f.Podcasts)},
			{word: libAudiobooks, show: libShowRow, kind: "audiobook", entries: usable(f.Audiobooks)},
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
	case libPodcast:
		got, err := c.PodcastEpisodes(ctx, p.entry)
		return []libSection{{word: -1, show: libShowTracks, kind: "podcast_episode", entries: usable(got)}}, false, err
	case libAudiobook:
		_, chapters, err := c.Audiobook(ctx, p.entry)
		if err != nil {
			return nil, false, err
		}
		var got []musicassistant.Entry
		for _, ch := range chapters {
			got = append(got, musicassistant.ChapterEntry(p.entry, ch))
		}
		return []libSection{{word: -1, show: libShowTracks, kind: "chapter", entries: usable(got)}}, false, nil
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
	if l.menu.open {
		// The action sheet takes every tap: a choice plays the item so, and anywhere else puts it away.
		m := l.menu
		d.editLibrary(func(n *librarySheet) { n.menu = libMenu{} })
		if z.kind == libPartMenu && z.index >= 0 && z.index < len(m.choices) {
			d.libMenuChose(l, m, m.choices[z.index])
		}
		d.wake()
		return true
	}
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
			if tab == libFavorites || tab == libRecent || tab == libQueue {
				// What these show moves on by itself; each look reads it afresh.
				r := &n.roots[tab]
				r.loaded, r.sections, r.problem, r.scroll = false, nil, "", 0
			}
			p := n.roots[tab]
			id, ask = p.id, tab != libSearch && (!p.loaded || p.problem != "")
		})
		if ask {
			d.libLoad(id)
		}
	case libPartSort:
		d.editLibrary(func(n *librarySheet) {
			n.audiobookSort = (n.audiobookSort + 1) % musicassistant.AudiobookOrders
			if sec := n.roots[libAudiobooks].sections; len(sec) > 0 {
				sec[0].entries = musicassistant.SortAudiobooks(sec[0].entries, n.audiobookSort)
			}
		})
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
		if spot, ok := libResumeOffer(p); ok {
			// The book was left part way: Play goes on from there, and the other button starts it over.
			if z.kind == libPartPlay {
				d.libPlayFrom(l, p.entry, float64(spot), lit)
			} else {
				libForgetSpot(p.entry.URI)
				d.libPlayFrom(l, p.entry, 0, lit)
			}
			break
		}
		d.libPlayTapped(l, p.entry, libEntryType(p.entry, libPageType(p.what)), z.kind == libPartShuffle, lit, libEnqueueReplace)
	case libPartItem:
		sec, i := z.index/libSecStride, z.index%libSecStride
		if sec >= len(p.sections) || i >= len(p.sections[sec].entries) {
			break
		}
		if p.what == libQueue {
			// A tap plays the queue from the item tapped; the one playing (the first) is left playing.
			if e := p.sections[sec].entries[i]; i > 0 && e.QueueItemID != "" {
				d.libPlayFromQueue(l, p.queue, e, i, z.index)
			}
			break
		}
		e := p.sections[sec].entries[i]
		switch kind := libEntryType(e, p.sections[sec].kind); kind {
		case "folder":
			np := newLibPage(libFolder, e)
			np.path = e.Path
			d.editLibrary(func(n *librarySheet) { n.stack = append(n.stack, np) })
			d.libLoad(np.id)
		case "artist", "album", "playlist", "podcast", "audiobook":
			what := map[string]int{"artist": libArtist, "album": libAlbum, "playlist": libPlaylist, "podcast": libPodcast, "audiobook": libAudiobook}[kind]
			np := newLibPage(what, e)
			if what == libAudiobook {
				libCheckBookNow() // a book just paused offers to resume as its page opens
			}
			d.editLibrary(func(n *librarySheet) { n.stack = append(n.stack, np) })
			d.libLoad(np.id)
		case "chapter":
			// A chapter plays its book from the top and is then moved to where the chapter starts.
			d.libPlayChapter(l, p.entry, e, z.index)
		default:
			d.libPlayTapped(l, e, kind, false, z.index, libEnqueueReplace)
		}
	}
	d.wake()
	return true
}

// libraryHold is a finger held still on the library and lifted at x, y. On a track, an album, a
// playlist or an artist it opens the action sheet of what to do with it: play it now, next, or at the
// end of the queue. It says whether it did; anything else (a station, which only plays; a part that
// is not an item; no player to play on) is left to be the tap it was before.
func (d *Display) libraryHold(x, y int) bool {
	d.mu.Lock()
	l := d.dashLibrary
	d.mu.Unlock()
	if l == nil || d.r == nil || l.typing || l.menu.open || l.target == "" {
		return false
	}
	z, _, _ := d.r.libraryAt(x, y)
	if z.kind != libPartItem {
		return false
	}
	p := l.current()
	sec, i := z.index/libSecStride, z.index%libSecStride
	if sec >= len(p.sections) || i >= len(p.sections[sec].entries) {
		return false
	}
	e := p.sections[sec].entries[i]
	kind := libEntryType(e, p.sections[sec].kind)
	m := libMenu{open: true, entry: e, kind: kind, item: z.index}
	if p.what == libQueue {
		// Any item of the queue, whatever its kind: play from it, move it up, take it out.
		if e.QueueItemID == "" || p.queue == "" {
			return false
		}
		m.queue, m.page, m.pos = p.queue, p.id, i
		m.choices = libMenuFor(e, kind, true, i)
	} else {
		switch kind {
		case "track", "album", "playlist", "artist":
		default:
			return false
		}
		if e.URI == "" {
			return false
		}
		m.choices = libMenuFor(e, kind, false, 0)
	}
	if len(m.choices) == 0 {
		return false // the item playing, which is already a favorite and not the library's
	}
	slog.Info("dashboard music library hold", "type", kind, "queue", m.queue != "")
	d.editLibrary(func(n *librarySheet) { n.menu = m })
	d.wake()
	return true
}

// libMenuChose does what was chosen on the action sheet m.
func (d *Display) libMenuChose(l *librarySheet, m libMenu, choice int) {
	switch choice {
	case libMenuPlay, libMenuNext, libMenuAdd, libMenuRadio:
		d.libPlayTapped(l, m.entry, m.kind, false, m.item, libMenuEnqueue[choice])
	case libMenuFavorite, libMenuUnfavorite:
		d.libSetFavorite(m.entry, m.kind, choice == libMenuFavorite)
	case libMenuPlayFrom:
		d.libPlayFromQueue(l, m.queue, m.entry, m.pos, m.item)
	case libMenuMoveNext, libMenuRemove:
		d.libEditQueue(m.queue, m.page, m.entry, choice == libMenuRemove)
	}
}

// libNextTrack is Home Assistant's next track on target; a variable for the tests.
var libNextTrack = func(target string) {
	libTap(dashboard.Action{Entity: target, Service: "media_player.media_next_track"})
}

// libPlayFromQueue plays queue (target's) from its item e, pos from what is playing, lights it (lit),
// and leaves the library for what is playing, as a play does. Music Assistant asks for the player itself
// here, so a token whose user may not use it is refused; then the item is moved to play next, which it
// allows, and Home Assistant skips to it.
func (d *Display) libPlayFromQueue(l *librarySheet, queue string, e musicassistant.Entry, pos, lit int) {
	c, set := libClient()
	if !set || l.target == "" || queue == "" || e.QueueItemID == "" {
		return
	}
	target, from := l.target, l.from
	slog.Info("dashboard music library play from the queue", "pos", pos)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		err := c.PlayQueueItem(ctx, queue, e.QueueItemID)
		if !musicassistant.IsForbidden(err) {
			if err != nil {
				slog.Warn("dashboard music library play from the queue", "err", err)
			}
			return
		}
		if pos > 1 {
			if err := c.MoveNext(ctx, queue, e.QueueItemID); err != nil {
				slog.Warn("dashboard music library play from the queue: moving it up", "err", err)
				return
			}
		}
		libNextTrack(target)
	}()
	d.editLibrary(func(n *librarySheet) { n.lit, n.litUntil = lit, time.Now().Add(tapLit) })
	libLeaveAfter(tapLit, func() { d.libLeave(target, from) })
}

// libEditQueue moves e up to play next in queue, or takes it out (remove), then reads the queue's page
// (page) again to show it as it now stands.
func (d *Display) libEditQueue(queue string, page int64, e musicassistant.Entry, remove bool) {
	c, set := libClient()
	if !set || queue == "" || e.QueueItemID == "" {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		var err error
		if remove {
			err = c.RemoveQueueItem(ctx, queue, e.QueueItemID)
		} else {
			err = c.MoveNext(ctx, queue, e.QueueItemID)
		}
		if err != nil {
			// Most likely the item was already on its way to the player; the page shows the queue as it is.
			slog.Warn("dashboard music library queue edit", "remove", remove, "err", err)
		}
		d.libReload(page)
	}()
}

// libReload reads page id again, keeping what it shows until the new answer replaces it.
func (d *Display) libReload(id int64) {
	d.editLibrary(func(n *librarySheet) {
		if pg := n.byID(id); pg != nil && !pg.loading {
			pg.loaded, pg.problem = false, ""
		}
	})
	d.libLoad(id)
}

// libSetFavorite marks e (of media type kind) as one of the library's favorites, or unmarks it. Each
// page's copy of it is marked to match, so its sheet offers the other choice next time; the favorites
// tab, which lists them, is read again.
func (d *Display) libSetFavorite(e musicassistant.Entry, kind string, on bool) {
	c, set := libClient()
	if !set || e.URI == "" {
		return
	}
	slog.Info("dashboard music library favorite", "type", kind, "on", on)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := c.SetFavorite(ctx, e, kind, on); err != nil {
			slog.Warn("dashboard music library favorite", "on", on, "err", err)
			return
		}
		var favorites int64
		d.editLibrary(func(n *librarySheet) {
			libMarkFavorite(n, e.URI, on)
			favorites = n.roots[libFavorites].id
		})
		if l := d.libraryViewNow(); l != nil && l.sheet.roots[libFavorites].loaded {
			d.libReload(favorites)
		}
		d.wake()
	}()
}

// libMarkFavorite marks every entry with uri on n's pages as a favorite or not. A section holding one is
// replaced, not changed in place, as a frame may be drawing it.
func libMarkFavorite(n *librarySheet, uri string, on bool) {
	mark := func(p *libPage) {
		for s := range p.sections {
			idx := slices.IndexFunc(p.sections[s].entries, func(e musicassistant.Entry) bool { return e.URI == uri })
			if idx < 0 {
				continue
			}
			secs := slices.Clone(p.sections)
			entries := slices.Clone(secs[s].entries)
			for i := range entries {
				if entries[i].URI == uri {
					entries[i].Favorite = on
				}
			}
			secs[s].entries = entries
			p.sections = secs
		}
	}
	for i := range n.roots {
		mark(&n.roots[i])
	}
	for i := range n.stack {
		mark(&n.stack[i])
	}
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
	case libPodcast:
		return "podcast"
	case libAudiobook:
		return "audiobook"
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
func (d *Display) libPlayTapped(l *librarySheet, e musicassistant.Entry, kind string, shuffle bool, lit int, enqueue string) {
	if l.target == "" || e.URI == "" {
		return
	}
	d.libPlay(l.target, e, kind, shuffle, enqueue)
	if kind == "audiobook" && enqueue == libEnqueueReplace {
		libWatchBook(l.target, e.URI, e.Duration)
	}
	d.editLibrary(func(n *librarySheet) { n.lit, n.litUntil = lit, time.Now().Add(tapLit) })
	// Play replaces the queue and leaves the library for what is now playing; queueing more (next, add)
	// keeps the library up so the owner can go on adding.
	if enqueue != libEnqueueReplace && enqueue != libEnqueueRadio {
		return
	}
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
		// Kept as it was left, for the Now Playing page's Library button to come back to.
		libLast.Store(l)
		// The Now Playing page, even one put away for an earlier track: the music asked for here brings
		// it back, as a tap on the playing word does.
		d.away, d.awayTrack, d.awayStation, d.awayPlaying = false, "", "", false
		d.musicQuiet.reopened(time.Now())
	}
	d.mu.Unlock()
	slog.Info("dashboard music library leaves", "own", own)
	switch {
	case own:
		// The dashboard goes, and the idle screen is Now Playing as the stream comes.
		d.closeDashboard()
	case strings.HasPrefix(from, "media_player."):
		libOpenSheet(d, from)
	}
	d.wake()
}

// libLast is the library as it was left by the last play on this device's own player, for the Now
// Playing page's Library button; nil before one.
var libLast atomic.Pointer[librarySheet]

// nowPlayingLibraryWord is the word on the Now Playing page's Library button: empty, for no button,
// without Music Assistant set up or a dashboard the library can be drawn over.
func nowPlayingLibraryWord() string {
	if _, set := libClient(); !set {
		return ""
	}
	if m := dashboard.Get().Mode(); m != config.DashboardDrawn && m != config.DashboardTessera {
		return ""
	}
	return libWord(libWordLibrary)
}

// reopenLibrary is the Now Playing page's Library button: the dashboard up with the library over it,
// as the last play on this device left it, on the page and at the place it was; a fresh library on
// this device's player when there is none to go back to. What moves on by itself (the favorites, what
// was played lately, the queue) is read afresh if that is what it shows.
func (d *Display) reopenLibrary() {
	if !d.openDashboard() {
		return
	}
	last := libLast.Load()
	if last == nil {
		slog.Info("dashboard music library from now playing", "back", false)
		d.openLibrary("", "", true)
		return
	}
	slog.Info("dashboard music library from now playing", "back", true)
	l := *last
	l.opening = libOpenings.Add(1)
	l.menu, l.typing, l.drag, l.lit = libMenu{}, false, libDrag{}, -1
	l.stack = slices.Clone(l.stack)
	// A page still being read when the library went never got its answer (it had no library to go
	// to): it is asked for again.
	unstick := func(p *libPage) {
		if p.loading {
			p.loading, p.loaded = false, false
		}
	}
	for i := range l.roots {
		unstick(&l.roots[i])
	}
	for i := range l.stack {
		unstick(&l.stack[i])
	}
	cur := l.current()
	id, fresh := cur.id, len(l.stack) == 0 && (l.tab == libFavorites || l.tab == libRecent || l.tab == libQueue)
	ask := !cur.loaded && (len(l.stack) > 0 || l.tab != libSearch) // a search not yet typed waits
	libPics.setChanged(d.wake)
	d.mu.Lock()
	d.dashMedia = nil
	d.dashLibrary = &l
	d.mu.Unlock()
	switch {
	case fresh:
		d.libReload(id)
	case ask:
		d.libLoad(id)
	}
	d.wake()
}

// libQueueOf is the Music Assistant queue target plays from; a variable for the tests.
var libQueueOf = dashboard.MusicQueue

// Music Assistant's play_media enqueue modes: replace the queue and play now, play after the current
// track, or add to the end of the queue.
const (
	libEnqueueReplace = "replace"
	libEnqueueNext    = "next"
	libEnqueueAdd     = "add"
	// libEnqueueRadio is not one of Music Assistant's modes but the library's own: replace the queue
	// and play now, as libEnqueueReplace, with radio mode on, so more like it follows without end.
	libEnqueueRadio = "radio"
)

// libPlay plays e on target through Home Assistant, as the media sheet plays a favorite. With
// libEnqueueReplace it replaces what was queued and plays now; libEnqueueNext and libEnqueueAdd leave
// what is playing and only add to the queue. An artist, album or playlist replacing the queue sets its
// shuffle first, on or off, so that Play plays it in order after a Shuffle.
func (d *Display) libPlay(target string, e musicassistant.Entry, kind string, shuffle bool, enqueue string) {
	if target == "" || e.URI == "" {
		return
	}
	slog.Info("dashboard music library play", "uri", e.URI, "type", kind, "shuffle", shuffle, "enqueue", enqueue)
	play := func() {
		data := map[string]any{"media_id": e.URI, "media_type": kind, "enqueue": enqueue}
		if enqueue == libEnqueueRadio {
			data["enqueue"], data["radio_mode"] = libEnqueueReplace, true
		}
		libTap(dashboard.Action{Entity: target, Service: "music_assistant.play_media", Data: data})
	}
	container := kind == "artist" || kind == "album" || kind == "playlist"
	// Next and add only append: the queue, and so what is playing, is left as it is, so no putOn and no
	// shuffle. Only replacing the queue marks what is now on and may shuffle a container first.
	if enqueue != libEnqueueReplace || !container {
		if enqueue == libEnqueueReplace || enqueue == libEnqueueRadio {
			putOn.Store(target, e.URI)
		}
		play()
		return
	}
	putOn.Store(target, e.URI)
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
		slog.Warn("dashboard music library shuffle", "err", err)
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

// libPlayChapter plays the book from where chapter starts (see libPlayFrom).
func (d *Display) libPlayChapter(l *librarySheet, book, chapter musicassistant.Entry, lit int) {
	d.libPlayFrom(l, book, chapter.Start, lit)
}
