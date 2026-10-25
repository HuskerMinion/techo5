package tessera

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"slices"
	"sync"
	"time"
)

// The wire protocol, as Tessera's add-on speaks it to a screen (its docs/PAGES.md and the screen_message
// action of its firmware). Every message is one compact JSON object, at most MaxMessage bytes, handed to
// the screen_message action, and every one is answered with the screen's status and what it can do.
//
// A conversation starts with a hello, which the screen answers with a session of its own making. Every
// later message carries that session, a sequence number one past the last and the revision of the layout
// it belongs to. A layout comes as a begin, its pages, its tiles and a commit; nothing of it is used
// before the commit, so a layout cut off half way leaves the last one standing. After that only small
// messages come: a tile's new state, a page's top bar, a value shared by several bar items, a change of
// titles and names, and a ping now and then that checks the screen still holds the same revision.
//
// The status words are protocol, not text for people: the add-on reads them, in English, whatever
// language anything else is in.

// Protocol is the version of the page protocol this speaks, the second: a screen that answers a hello
// with another one is sent nothing.
const Protocol = 2

// MaxMessage is the most bytes one message may have. The add-on never sends more, and a screen refuses
// more.
const MaxMessage = 4096

// The status words.
const (
	StatusReady    = "Ready for tile configuration"
	StatusLoading  = "Loading tiles"
	StatusSynced   = "Synced"
	StatusResend   = "Resend needed"
	sessionPrefix  = "Session:"
	errPrefix      = "Error: "
	errProtocol    = errPrefix + "protocol version"
	errTooLarge    = errPrefix + "message too large"
	errUnreadable  = errPrefix + "invalid message"
	errInvalid     = errPrefix + "invalid layout"
	errIncomplete  = errPrefix + "incomplete layout"
	defaultBarStep = 6
)

// Capabilities is what the screen tells the add-on it can take, in every answer. The add-on sends nothing
// a screen did not say it takes, so this is also what keeps it to what the screen can draw.
type Capabilities struct {
	MaxTiles    int
	MaxPages    int
	MaxBarItems int

	// TileSizes are the sizes a tile may have: single, wide, tall, square, full and spans like "3x2".
	TileSizes []string

	// Features are the newer options taken, by the names the add-on gives them.
	Features []string

	// The older options, each a flag of its own.
	BarValues, AppearanceUpdates, GroupLamps, TileKeys, TileRepeats, FreePages bool
}

// Answer is what the screen_message action answers with. Home Assistant hands it to the add-on as the
// action's response.
type Answer struct {
	Status   string `json:"status"`
	Rev      string `json:"rev"`
	Protocol int    `json:"protocol"`
	Request  string `json:"request"`
	Session  string `json:"session"`
	Seq      uint32 `json:"seq"`
	Applied  bool   `json:"applied"`

	BarValues         int `json:"bar_values"`
	AppearanceUpdates int `json:"appearance_updates"`
	GroupLamps        int `json:"group_lamps"`
	TileKeys          int `json:"tile_keys"`
	TileRepeats       int `json:"tile_repeats"`
	FreePages         int `json:"free_pages"`

	MaxTiles    int `json:"max_tiles"`
	MaxPages    int `json:"max_pages"`
	MaxBarItems int `json:"max_bar_items"`

	Features  []string `json:"features"`
	TileSizes []string `json:"tile_sizes"`
}

// envelope is what every message carries, whatever it is.
type envelope struct {
	V       int    `json:"v"`
	Op      string `json:"op"`
	Request string `json:"request"`
	Empty   bool   `json:"empty"`
	Session string `json:"session"`
	Seq     uint32 `json:"seq"`
	Rev     string `json:"rev"`
}

// Begin opens a layout: how many pages and tiles are coming, the title, the home page, and the region's
// ways of writing a clock and a number, which are kept as they came.
type Begin struct {
	Inbox     string `json:"inbox"`
	Title     string `json:"title"`
	Pages     int    `json:"pages"`
	Tiles     int    `json:"tiles"`
	Home      int    `json:"home"`
	Keepalive int    `json:"keepalive"`
	Clock24h  bool   `json:"clock_24h"`
	Grid      []int  `json:"grid,omitempty"`

	Numbers      json.RawMessage `json:"numbers,omitempty"`
	GroupMin     json.RawMessage `json:"group_min,omitempty"`
	PercentSpace json.RawMessage `json:"percent_space,omitempty"`
}

// Tile is one tile: where it sits and what it shows. The first message places it (Slot, or In and K for a
// key of a bedside clock, or Bar for a tile of the top bar alone); a state message after that changes what
// it shows and leaves where it sits. A, O and X are the entity's attributes, the tile's options (its kind,
// size, icon and color) and its extras (a forecast, a history), kept as they came until something draws
// them.
type Tile struct {
	I      int             `json:"i"`
	Slot   *int            `json:"slot,omitempty"`
	In     *int            `json:"in,omitempty"`
	K      json.RawMessage `json:"k,omitempty"`
	Bar    int             `json:"bar,omitempty"`
	Entity string          `json:"entity"`
	Name   string          `json:"name"`
	State  string          `json:"state"`
	A      json.RawMessage `json:"a,omitempty"`
	O      json.RawMessage `json:"o,omitempty"`
	X      json.RawMessage `json:"x,omitempty"`

	// Icon is the icon worked out from the state, which a state message carries beside the options.
	Icon json.RawMessage `json:"icon,omitempty"`
}

// Page is one page: its stable id, its title, whether it has a Home control, whether it is left out of the
// page dots and swipes (a detail page), and its top bar's items.
type Page struct {
	P           int             `json:"p"`
	ID          string          `json:"id"`
	Title       string          `json:"title"`
	HomeControl bool            `json:"home_control"`
	Excluded    bool            `json:"excluded"`
	Items       json.RawMessage `json:"items"`
	Dock        *bool           `json:"dock,omitempty"`
	Icon        json.RawMessage `json:"icon,omitempty"`
}

// barValue is one value for several items of the top bars: each target is a page times the step (W, six
// unless it says) plus the item's place in that page's bar.
type barValue struct {
	Item    json.RawMessage `json:"item"`
	Targets []int           `json:"targets"`
	W       int             `json:"w"`
}

// appearance changes titles, names and backgrounds only, from the revision named Base to the message's own.
type appearance struct {
	Base  string  `json:"base"`
	Title *string `json:"title"`
	Pages []struct {
		P     int    `json:"p"`
		Title string `json:"title"`
	} `json:"pages"`
	Tiles []struct {
		I          int             `json:"i"`
		Name       string          `json:"name"`
		Background json.RawMessage `json:"background"`
	} `json:"tiles"`
}

// Layout is a whole layout as the add-on sent it, with the latest state of every tile. It is what is saved
// and what something draws.
type Layout struct {
	Rev   string `json:"rev"`
	Begin Begin  `json:"begin"`
	Pages []Page `json:"pages"`
	Tiles []Tile `json:"tiles"`

	// Backgrounds are the tiles' backgrounds an appearance message changed, by tile, which until then are
	// in the tile's options.
	Backgrounds map[int]json.RawMessage `json:"backgrounds,omitempty"`

	// gotPages and gotTiles are which pages and tiles of a layout being sent have come.
	gotPages, gotTiles []bool
}

// complete is whether every page and every tile the begin promised has come.
func (l *Layout) complete() bool {
	return !slices.Contains(l.gotPages, false) && !slices.Contains(l.gotTiles, false)
}

func (l *Layout) clone() *Layout {
	if l == nil {
		return nil
	}
	b, err := json.Marshal(l)
	if err != nil {
		return nil
	}
	var out Layout
	if json.Unmarshal(b, &out) != nil {
		return nil
	}
	return &out
}

// Receiver is the screen's side of the conversation: the session, the layout in force, the one being
// sent, and the status. Safe for use from several goroutines, as Home Assistant may call the action
// while an earlier call is still being answered.
type Receiver struct {
	caps Capabilities

	// save keeps a layout that has just come into force; newID makes a session. Both are replaced in tests.
	save  func(*Layout) error
	newID func() string

	// changed hears every status, applied every layout that comes into force (it must not change it), and
	// drawn every message that changed what the screen shows: a layout, a tile's state, a top bar, a title.
	changed func(status string)
	applied func(*Layout)
	drawn   func()

	mu      sync.Mutex
	status  string
	request string
	session string
	seq     uint32
	active  *Layout
	pending *Layout
	// shown is set by a message that changed what the layout in force shows, for drawn; lazy by one whose
	// change is kept on disk within saveEvery rather than at once.
	shown, lazy bool
	// stamp numbers the statuses as they are set, and gen the layouts as they are to be kept, so that
	// neither is handed on out of order by two calls at once.
	stamp, gen uint64

	// said is the newest status changed heard.
	sayMu sync.Mutex
	said  uint64

	// What is kept on disk: the newest layout written, when, and one waiting for its turn.
	keepMu  sync.Mutex
	wrote   uint64
	wroteAt time.Time
	waiting *Layout
	waitGen uint64
	timer   *time.Timer
}

// saveEvery is how often a change of titles alone is written: the add-on sends one for every edit in its
// editor, and the last of a burst is the one that counts. A variable so a test need not wait.
var saveEvery = 5 * time.Second

// NewReceiver is a receiver that says it takes caps, and saves each layout with save (nil saves nothing).
func NewReceiver(caps Capabilities, save func(*Layout) error) *Receiver {
	return &Receiver{caps: caps, save: save, newID: randomID, status: StatusReady}
}

// takes changes what the screen says it takes, from the next answer on: for a grid known only once the
// device is.
func (r *Receiver) takes(caps Capabilities) {
	r.mu.Lock()
	r.caps = caps
	r.mu.Unlock()
}

// randomID is sixteen hex digits, the form every id of the protocol has.
func randomID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// Restore puts back a layout kept from before. It is in force until the add-on sends another, and a begin
// for the same revision is answered as applied, so the add-on sends only states. The status stays Ready:
// there is no session yet, and Ready is what has the add-on start one.
func (r *Receiver) Restore(l *Layout) {
	if l == nil || len(l.Pages) == 0 || len(l.Pages) != l.Begin.Pages || len(l.Tiles) != l.Begin.Tiles {
		return
	}
	r.mu.Lock()
	r.active = l.clone()
	r.mu.Unlock()
}

// with runs fn on the layout in force, nil while there is none, under the lock: fn must not keep it or
// change it.
func (r *Receiver) with(fn func(*Layout)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	fn(r.active)
}

// Status is the status word as it stands.
func (r *Receiver) Status() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.status
}

// Layout is a copy of the layout in force, nil while there is none.
func (r *Receiver) Layout() *Layout {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.active.clone()
}

// Receive takes one message and answers it.
func (r *Receiver) Receive(raw string) Answer {
	r.mu.Lock()
	r.shown, r.lazy = false, false
	answer, saved := r.receive(raw)
	r.stamp++
	status, stamp, shown, lazy := r.status, r.stamp, r.shown, r.lazy
	// Copied only when it is to be kept: states come all day.
	var applied *Layout
	var gen uint64
	if saved {
		r.gen++
		applied, gen = r.active.clone(), r.gen
	}
	r.mu.Unlock()

	// Written outside the lock, so a frame being drawn does not wait for the disk.
	if saved {
		r.keep(gen, applied, lazy)
	}
	r.say(stamp, status)
	if saved && r.applied != nil {
		r.applied(applied)
	}
	if (saved || shown) && r.drawn != nil {
		r.drawn()
	}
	slog.Debug("tessera: answered", "status", answer.Status, "seq", answer.Seq, "rev", answer.Rev, "applied", answer.Applied)
	return answer
}

// say hands status to changed, unless a newer one has been handed on already.
func (r *Receiver) say(stamp uint64, status string) {
	if r.changed == nil {
		return
	}
	r.sayMu.Lock()
	defer r.sayMu.Unlock()
	if stamp <= r.said {
		return
	}
	r.said = stamp
	r.changed(status)
}

// keep writes l, numbered gen, unless a newer one is written already. A change of titles alone (lazy)
// waits until saveEvery has passed since the last write, and only the newest of those is written.
func (r *Receiver) keep(gen uint64, l *Layout, lazy bool) {
	if r.save == nil || l == nil {
		return
	}
	r.keepMu.Lock()
	defer r.keepMu.Unlock()
	if gen <= r.wrote || gen < r.waitGen {
		return
	}
	if wait := saveEvery - time.Since(r.wroteAt); lazy && wait > 0 {
		r.waiting, r.waitGen = l, gen
		if r.timer == nil {
			r.timer = time.AfterFunc(wait, r.Flush)
		}
		return
	}
	r.write(gen, l)
}

// write is keep's write, under keepMu.
func (r *Receiver) write(gen uint64, l *Layout) {
	r.waiting = nil
	if r.timer != nil {
		r.timer.Stop()
		r.timer = nil
	}
	if err := r.save(l); err != nil {
		slog.Warn("tessera: saving the layout failed", "err", err)
	}
	r.wrote, r.wroteAt = gen, time.Now()
}

// Flush writes a layout still waiting its turn: when the turn comes, and when the daemon stops.
func (r *Receiver) Flush() {
	r.keepMu.Lock()
	defer r.keepMu.Unlock()
	if r.waiting != nil {
		r.write(r.waitGen, r.waiting)
	}
}

// receive is Receive under the lock. saved says a layout came into force or changed and is to be kept.
func (r *Receiver) receive(raw string) (Answer, bool) {
	if len(raw) > MaxMessage {
		slog.Debug("tessera: received", "bytes", len(raw))
		return r.answer(errTooLarge), false
	}
	var env envelope
	if err := json.Unmarshal([]byte(raw), &env); err != nil || env.Op == "" {
		slog.Debug("tessera: received", "bytes", len(raw))
		return r.answer(errUnreadable), false
	}
	// What a message holds is not logged: its states, names and attributes are the home's.
	slog.Debug("tessera: received", "op", env.Op, "bytes", len(raw))
	if env.V != Protocol {
		return r.answer(errProtocol), false
	}
	if env.Op == "hello" {
		return r.hello(env), false
	}

	// Anything else belongs to a session, and comes in order. One that does not is from before a restart, or
	// another add-on, or a call that was retried: it changes nothing and the add-on is told to start again.
	if r.session == "" || env.Session != r.session || env.Seq <= r.seq {
		slog.Debug("tessera: out of session", "op", env.Op, "session", env.Session, "seq", env.Seq, "have", r.seq)
		return r.answer(StatusResend), false
	}

	status, saved := r.apply(env, []byte(raw))
	if status == StatusSynced || status == StatusLoading {
		r.seq = env.Seq
	}
	// A refused message is answered with the revision held, not the one it was about: the add-on compares
	// them, and only an answer with its own revision, session and sequence counts as taken.
	return r.answer(status), saved
}

// hello grants a new session. Whatever layout was half sent is dropped: the add-on starts it over.
func (r *Receiver) hello(env envelope) Answer {
	r.request, r.session, r.seq, r.pending = env.Request, r.newID(), 0, nil
	if env.Empty {
		slog.Info("tessera: the add-on has no tiles for this screen yet")
	}
	slog.Info("tessera: session granted", "session", r.session)
	return r.answer(sessionPrefix + r.session)
}

// apply takes a message of a session, and says the status it leaves and whether a layout is to be saved.
func (r *Receiver) apply(env envelope, raw []byte) (string, bool) {
	switch env.Op {
	case "begin":
		return r.begin(env, raw)
	case "page":
		var p Page
		if r.pending == nil || r.pending.Rev != env.Rev || json.Unmarshal(raw, &p) != nil ||
			p.P < 0 || p.P >= len(r.pending.Pages) {
			return r.refuse(errInvalid), false
		}
		if p.Items == nil {
			p.Items = json.RawMessage("[]")
		}
		r.pending.Pages[p.P], r.pending.gotPages[p.P] = p, true
		return r.set(StatusLoading), false
	case "tile":
		var t Tile
		if r.pending == nil || r.pending.Rev != env.Rev || json.Unmarshal(raw, &t) != nil ||
			t.I < 0 || t.I >= len(r.pending.Tiles) {
			return r.refuse(errInvalid), false
		}
		r.pending.Tiles[t.I], r.pending.gotTiles[t.I] = t, true
		return r.set(StatusLoading), false
	case "commit":
		if r.pending == nil || r.pending.Rev != env.Rev {
			return r.refuse(errInvalid), false
		}
		if !r.pending.complete() {
			r.pending = nil
			return r.refuse(errIncomplete), false
		}
		r.active, r.pending = r.pending, nil
		r.active.gotPages, r.active.gotTiles = nil, nil
		slog.Info("tessera: layout in force", "rev", r.active.Rev, "pages", len(r.active.Pages), "tiles", len(r.active.Tiles))
		return r.set(StatusSynced), true
	}

	// The rest is about the layout in force, and only that one.
	if r.active == nil || r.active.Rev != env.Rev && env.Op != "appearance" {
		return r.set(StatusResend), false
	}
	switch env.Op {
	case "state":
		return r.state(raw), false
	case "bar":
		var p Page
		if json.Unmarshal(raw, &p) != nil || p.P < 0 || p.P >= len(r.active.Pages) {
			return r.refuse(errInvalid), false
		}
		if p.Items == nil {
			p.Items = json.RawMessage("[]")
		}
		r.active.Pages[p.P].Items = p.Items
		r.shown = true
		return r.set(StatusSynced), false
	case "bar_value":
		return r.barValue(raw), false
	case "appearance":
		return r.appearance(env, raw)
	case "ping":
		return r.set(StatusSynced), false
	}

	// Something newer than this screen: taken and left alone, as unknown fields are. The add-on sends an
	// option only to a screen that named it, so this is a message nothing here asked for.
	slog.Debug("tessera: message not taken", "op", env.Op)
	return r.set(StatusSynced), false
}

// begin opens a layout, or finds it is the one already in force: then nothing more of it comes, only its
// states.
func (r *Receiver) begin(env envelope, raw []byte) (string, bool) {
	var b Begin
	if json.Unmarshal(raw, &b) != nil || len(env.Rev) == 0 ||
		b.Pages < 1 || b.Pages > r.caps.MaxPages || b.Tiles < 0 || b.Tiles > r.caps.MaxTiles ||
		b.Home < 0 || b.Home >= b.Pages {
		r.pending = nil
		return r.refuse(errInvalid), false
	}
	if a := r.active; a != nil && a.Rev == env.Rev && a.Begin.Pages == b.Pages && a.Begin.Tiles == b.Tiles {
		r.pending = nil
		// The region may have changed with no change to the layout's revision: its ways of writing a
		// clock and a number, and the title and keepalive beside them. Where the pages and tiles are
		// is the revision's, and stays. A begin that counts them differently is a layout sent again.
		a.Begin.Inbox, a.Begin.Title, a.Begin.Keepalive, a.Begin.Clock24h = b.Inbox, b.Title, b.Keepalive, b.Clock24h
		a.Begin.Numbers, a.Begin.GroupMin, a.Begin.PercentSpace = b.Numbers, b.GroupMin, b.PercentSpace
		return r.set(StatusSynced), false
	}
	r.pending = &Layout{
		Rev: env.Rev, Begin: b,
		Pages: make([]Page, b.Pages), Tiles: make([]Tile, b.Tiles),
		gotPages: make([]bool, b.Pages), gotTiles: make([]bool, b.Tiles),
	}
	return r.set(StatusLoading), false
}

// state is a tile's new state: what it shows, never where it sits.
func (r *Receiver) state(raw []byte) string {
	var t Tile
	if json.Unmarshal(raw, &t) != nil || t.I < 0 || t.I >= len(r.active.Tiles) {
		return r.refuse(errInvalid)
	}
	old := &r.active.Tiles[t.I]
	// A state that leaves out the entity or the name leaves them as they were.
	if t.Entity != "" {
		old.Entity = t.Entity
	}
	if t.Name != "" {
		old.Name = t.Name
	}
	old.State = t.State
	if t.A != nil {
		old.A = t.A
	}
	if t.X != nil {
		old.X = t.X
	}
	if t.Icon != nil {
		old.Icon = t.Icon
	}
	r.shown = true
	return r.set(StatusSynced)
}

// barValue sets one value on several items, all of them or, when one is out of reach, none.
func (r *Receiver) barValue(raw []byte) string {
	var v barValue
	if json.Unmarshal(raw, &v) != nil || len(v.Targets) == 0 || v.Item == nil {
		return r.refuse(errInvalid)
	}
	step := v.W
	if step <= 0 {
		step = defaultBarStep
	}
	bars := make([][]json.RawMessage, len(r.active.Pages))
	for i, p := range r.active.Pages {
		if json.Unmarshal(p.Items, &bars[i]) != nil {
			return r.refuse(errInvalid)
		}
	}
	for _, t := range v.Targets {
		page, item := t/step, t%step
		if t < 0 || page >= len(bars) || item >= len(bars[page]) {
			return r.refuse(errInvalid)
		}
	}
	touched := map[int]bool{}
	for _, t := range v.Targets {
		bars[t/step][t%step] = v.Item
		touched[t/step] = true
	}
	for page := range touched {
		b, err := json.Marshal(bars[page])
		if err != nil {
			return r.refuse(errInvalid)
		}
		r.active.Pages[page].Items = b
	}
	r.shown = true
	return r.set(StatusSynced)
}

// appearance changes titles, names and backgrounds of the layout in force, all of them or none, and moves it
// to the message's revision.
func (r *Receiver) appearance(env envelope, raw []byte) (string, bool) {
	var a appearance
	if json.Unmarshal(raw, &a) != nil || a.Base != r.active.Rev || env.Rev == "" {
		return r.set(StatusResend), false
	}
	for _, p := range a.Pages {
		if p.P < 0 || p.P >= len(r.active.Pages) {
			return r.refuse(errInvalid), false
		}
	}
	for _, t := range a.Tiles {
		if t.I < 0 || t.I >= len(r.active.Tiles) {
			return r.refuse(errInvalid), false
		}
	}
	if a.Title != nil {
		r.active.Begin.Title = *a.Title
	}
	for _, p := range a.Pages {
		r.active.Pages[p.P].Title = p.Title
	}
	for _, t := range a.Tiles {
		r.active.Tiles[t.I].Name = t.Name
		if t.Background != nil {
			if r.active.Backgrounds == nil {
				r.active.Backgrounds = map[int]json.RawMessage{}
			}
			r.active.Backgrounds[t.I] = t.Background
		}
	}
	r.active.Rev = env.Rev
	r.lazy = true
	return r.set(StatusSynced), true
}

// set is a status the add-on may go on from.
func (r *Receiver) set(status string) string {
	r.status = status
	return status
}

// refuse is a status that stops the add-on; the layout in force stays.
func (r *Receiver) refuse(status string) string {
	slog.Info("tessera: message refused", "status", status)
	r.status = status
	return status
}

// answer is the reply to any message: the status, the revision held (the one being sent, while it is), the
// session, and everything the screen can take.
func (r *Receiver) answer(status string) Answer {
	r.status = status
	rev := ""
	if r.active != nil {
		rev = r.active.Rev
	}
	if r.pending != nil {
		rev = r.pending.Rev
	}
	flag := func(b bool) int {
		if b {
			return 1
		}
		return 0
	}
	features := slices.Clone(r.caps.Features)
	if features == nil {
		features = []string{}
	}
	return Answer{
		Status:   status,
		Rev:      rev,
		Protocol: Protocol,
		Request:  r.request,
		Session:  r.session,
		Seq:      r.seq,
		Applied:  r.pending == nil && r.active != nil,

		BarValues:         flag(r.caps.BarValues),
		AppearanceUpdates: flag(r.caps.AppearanceUpdates),
		GroupLamps:        flag(r.caps.GroupLamps),
		TileKeys:          flag(r.caps.TileKeys),
		TileRepeats:       flag(r.caps.TileRepeats),
		FreePages:         flag(r.caps.FreePages),

		MaxTiles:    r.caps.MaxTiles,
		MaxPages:    r.caps.MaxPages,
		MaxBarItems: r.caps.MaxBarItems,
		Features:    features,
		TileSizes:   slices.Clone(r.caps.TileSizes),
	}
}
