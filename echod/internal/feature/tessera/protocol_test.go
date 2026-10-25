package tessera

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// The messages here are written for these tests from the shapes the add-on's documentation gives (its
// docs/PAGES.md and the screen_message action of its firmware): a hello, a layout of two pages and three
// tiles, and the small messages that follow one.

const (
	request = "0123456789abcdef"
	session = "fedcba9876543210"
	rev1    = "a1b2c3d4e5f60718"
	rev2    = "1122334455667788"
)

func testCaps() Capabilities {
	return Capabilities{MaxTiles: 64, MaxPages: 8, MaxBarItems: 6, TileSizes: []string{"single", "wide", "full"}, FreePages: true}
}

// newTestReceiver is a receiver whose sessions are known and whose saves are counted.
func newTestReceiver(t *testing.T) (*Receiver, *[]*Layout) {
	t.Helper()
	var saved []*Layout
	r := NewReceiver(testCaps(), func(l *Layout) error { saved = append(saved, l); return nil })
	r.newID = func() string { return session }
	return r, &saved
}

// packet is a message of the session, numbered seq, about revision rev.
func packet(seq int, rev, body string) string {
	return fmt.Sprintf(`{"v":2,"session":%q,"seq":%d,"rev":%q,%s}`, session, seq, rev, body)
}

// layoutMessages is a whole layout as the add-on sends one: begin, the pages, the tiles, commit.
func layoutMessages(rev string) []string {
	return []string{
		packet(1, rev, `"op":"begin","inbox":"text.kitchen_tessera_tile_settings","title":"Kitchen","pages":2,"tiles":3,"home":0,"keepalive":120,"clock_24h":true,"numbers":"1,234.5","group_min":4,"percent_space":false`),
		packet(2, rev, `"op":"page","p":0,"id":"p-home","title":"Home","home_control":false,"excluded":false,"items":[{"t":"clock"},{"t":"entity","e":"sensor.outside"}]`),
		packet(3, rev, `"op":"page","p":1,"id":"p-lights","title":"Lights","home_control":true,"excluded":true,"items":[],"dock":false,"icon":984580`),
		packet(4, rev, `"op":"tile","i":0,"slot":0,"entity":"light.ceiling","name":"Ceiling","state":"on","a":{"brightness":180},"o":{"size":"single","icon":983568}`),
		packet(5, rev, `"op":"tile","i":1,"slot":1,"entity":"sensor.outside","name":"Outside","state":"12.5","a":{"unit_of_measurement":"°C"},"o":{"size":"wide"}`),
		packet(6, rev, `"op":"tile","i":2,"slot":8,"entity":"switch.kettle","name":"Kettle","state":"off","a":{},"o":{}`),
		packet(7, rev, `"op":"commit"`),
	}
}

// hello starts a session, and answers what a hello is answered with.
func hello(t *testing.T, r *Receiver) Answer {
	t.Helper()
	return r.Receive(fmt.Sprintf(`{"v":2,"op":"hello","request":%q}`, request))
}

func TestParseTakesEveryKindOfMessage(t *testing.T) {
	var b Begin
	msgs := layoutMessages(rev1)
	if err := json.Unmarshal([]byte(msgs[0]), &b); err != nil {
		t.Fatal(err)
	}
	if b.Title != "Kitchen" || b.Pages != 2 || b.Tiles != 3 || b.Keepalive != 120 || !b.Clock24h || string(b.GroupMin) != "4" {
		t.Errorf("begin read as %+v", b)
	}
	var p Page
	if err := json.Unmarshal([]byte(msgs[2]), &p); err != nil {
		t.Fatal(err)
	}
	if p.P != 1 || p.ID != "p-lights" || !p.HomeControl || !p.Excluded || p.Dock == nil || *p.Dock || string(p.Icon) != "984580" {
		t.Errorf("page read as %+v", p)
	}
	var tile Tile
	if err := json.Unmarshal([]byte(msgs[3]), &tile); err != nil {
		t.Fatal(err)
	}
	if tile.I != 0 || tile.Slot == nil || *tile.Slot != 0 || tile.Entity != "light.ceiling" || tile.State != "on" ||
		!strings.Contains(string(tile.O), `"size":"single"`) {
		t.Errorf("tile read as %+v", tile)
	}
	var env envelope
	if err := json.Unmarshal([]byte(msgs[6]), &env); err != nil {
		t.Fatal(err)
	}
	if env.V != 2 || env.Op != "commit" || env.Session != session || env.Seq != 7 || env.Rev != rev1 {
		t.Errorf("envelope read as %+v", env)
	}
}

func TestHelloGrantsASessionAndSaysWhatTheScreenTakes(t *testing.T) {
	r, _ := newTestReceiver(t)
	a := hello(t, r)
	if a.Protocol != Protocol || a.Request != request || a.Session != session || a.Status != "Session:"+session {
		t.Fatalf("hello answered %+v", a)
	}
	if a.Seq != 0 || a.Applied || a.Rev != "" {
		t.Errorf("a screen with no layout answered seq %d applied %v rev %q", a.Seq, a.Applied, a.Rev)
	}
	if !slices.Equal(a.TileSizes, []string{"single", "wide", "full"}) || a.MaxTiles != 64 || a.MaxPages != 8 || a.MaxBarItems != 6 {
		t.Errorf("capabilities %+v", a)
	}
	if a.FreePages != 1 || a.BarValues != 0 || a.AppearanceUpdates != 0 || a.TileKeys != 0 || a.TileRepeats != 0 || a.GroupLamps != 0 {
		t.Errorf("flags %+v", a)
	}
	// The add-on reads these as JSON, where an absent list and an empty one are not the same.
	b, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"features":[]`, `"protocol":2`, `"free_pages":1`, `"tile_sizes":["single","wide","full"]`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("answer %s lacks %s", b, want)
		}
	}
	if r.Status() != "Session:"+session {
		t.Errorf("status %q", r.Status())
	}
}

func TestAnotherProtocolIsRefused(t *testing.T) {
	r, _ := newTestReceiver(t)
	if a := r.Receive(`{"v":1,"op":"hello","request":"0123456789abcdef"}`); a.Status != "Error: protocol version" || a.Session != "" {
		t.Errorf("a first-protocol hello answered %+v", a)
	}
}

func TestAWholeLayoutComesIntoForceAtCommit(t *testing.T) {
	r, saved := newTestReceiver(t)
	hello(t, r)
	msgs := layoutMessages(rev1)
	for i, m := range msgs[:len(msgs)-1] {
		a := r.Receive(m)
		if a.Status != StatusLoading || a.Seq != uint32(i+1) || a.Rev != rev1 || a.Session != session || a.Applied {
			t.Fatalf("message %d answered %+v", i+1, a)
		}
		if r.Layout() != nil {
			t.Fatalf("a layout in force before its commit, after message %d", i+1)
		}
	}
	a := r.Receive(msgs[len(msgs)-1])
	if a.Status != StatusSynced || !a.Applied || a.Seq != 7 || a.Rev != rev1 {
		t.Fatalf("commit answered %+v", a)
	}
	l := r.Layout()
	if l == nil || l.Rev != rev1 || l.Begin.Title != "Kitchen" || len(l.Pages) != 2 || len(l.Tiles) != 3 {
		t.Fatalf("layout in force %+v", l)
	}
	if l.Pages[1].ID != "p-lights" || l.Tiles[2].Entity != "switch.kettle" || *l.Tiles[2].Slot != 8 {
		t.Errorf("pages %+v tiles %+v", l.Pages, l.Tiles)
	}
	if len(*saved) != 1 || (*saved)[0].Rev != rev1 {
		t.Errorf("saved %d layouts", len(*saved))
	}
}

func TestACommitBeforeEverythingCameIsRefused(t *testing.T) {
	r, saved := newTestReceiver(t)
	hello(t, r)
	msgs := layoutMessages(rev1)
	for _, m := range msgs[:5] { // begin, both pages, two of the three tiles
		r.Receive(m)
	}
	a := r.Receive(packet(6, rev1, `"op":"commit"`))
	if a.Status != "Error: incomplete layout" || a.Applied || r.Layout() != nil || len(*saved) != 0 {
		t.Errorf("an incomplete commit answered %+v, layout %v, %d saved", a, r.Layout(), len(*saved))
	}
}

func TestMessagesOutOfTheSessionChangeNothing(t *testing.T) {
	r, _ := newTestReceiver(t)
	msgs := layoutMessages(rev1)
	// Before any hello: a session from before a restart.
	if a := r.Receive(msgs[0]); a.Status != StatusResend {
		t.Errorf("a begin with no session answered %+v", a)
	}
	hello(t, r)
	r.Receive(msgs[0])
	// The same sequence number again, as a retried call would carry.
	if a := r.Receive(msgs[0]); a.Status != StatusResend || a.Seq != 1 {
		t.Errorf("a repeated message answered %+v", a)
	}
	// Another session.
	other := strings.Replace(msgs[1], session, "0000000000000000", 1)
	if a := r.Receive(other); a.Status != StatusResend || a.Seq != 1 {
		t.Errorf("another session's message answered %+v", a)
	}
}

func TestATileOutOfRangeIsRefused(t *testing.T) {
	r, _ := newTestReceiver(t)
	hello(t, r)
	r.Receive(layoutMessages(rev1)[0])
	a := r.Receive(packet(2, rev1, `"op":"tile","i":3,"slot":0,"entity":"light.x","name":"X","state":"on"`))
	if a.Status != "Error: invalid layout" || a.Seq != 1 {
		t.Errorf("tile 3 of 3 answered %+v", a)
	}
}

func TestTooLargeAndUnreadableMessages(t *testing.T) {
	r, _ := newTestReceiver(t)
	if a := r.Receive(strings.Repeat(" ", MaxMessage+1)); a.Status != "Error: message too large" {
		t.Errorf("a message past %d bytes answered %+v", MaxMessage, a)
	}
	if a := r.Receive(`{"v":2,`); a.Status != "Error: invalid message" {
		t.Errorf("half a message answered %+v", a)
	}
}

// committed is a receiver holding the test layout, and the sequence number of its commit.
func committed(t *testing.T) (*Receiver, *[]*Layout, int) {
	t.Helper()
	r, saved := newTestReceiver(t)
	hello(t, r)
	msgs := layoutMessages(rev1)
	for _, m := range msgs {
		r.Receive(m)
	}
	if r.Layout() == nil {
		t.Fatal("the test layout did not come into force")
	}
	return r, saved, len(msgs)
}

func TestStateChangesWhatATileShows(t *testing.T) {
	r, saved, seq := committed(t)
	a := r.Receive(packet(seq+1, rev1, `"op":"state","i":0,"entity":"light.ceiling","name":"Ceiling light","state":"off","a":{},"icon":983567`))
	if a.Status != StatusSynced || a.Seq != uint32(seq+1) || !a.Applied {
		t.Fatalf("state answered %+v", a)
	}
	tile := r.Layout().Tiles[0]
	if tile.State != "off" || tile.Name != "Ceiling light" || string(tile.Icon) != "983567" || tile.Slot == nil || *tile.Slot != 0 {
		t.Errorf("tile after its state %+v", tile)
	}
	if !strings.Contains(string(tile.O), `"size":"single"`) {
		t.Errorf("a state took the tile's options: %s", tile.O)
	}
	if len(*saved) != 1 {
		t.Errorf("a state was saved: %d saves", len(*saved))
	}
	// A state for a revision the screen does not hold has the add-on send the layout again.
	if a := r.Receive(packet(seq+2, rev2, `"op":"state","i":0,"entity":"light.ceiling","name":"Ceiling","state":"on"`)); a.Status != StatusResend || a.Rev != rev1 {
		t.Errorf("a state of another revision answered %+v", a)
	}
}

func TestBarAndBarValue(t *testing.T) {
	r, _, seq := committed(t)
	if a := r.Receive(packet(seq+1, rev1, `"op":"bar","p":1,"items":[{"t":"text","v":"a"},{"t":"text","v":"b"}]`)); a.Status != StatusSynced {
		t.Fatalf("bar answered %+v", a)
	}
	// Page 0's second item and page 1's first, six to a page.
	if a := r.Receive(packet(seq+2, rev1, `"op":"bar_value","item":{"t":"text","v":"21 °C"},"targets":[1,6]`)); a.Status != StatusSynced {
		t.Fatalf("bar_value answered %+v", a)
	}
	l := r.Layout()
	if !strings.Contains(string(l.Pages[0].Items), `"21 °C"`) || !strings.Contains(string(l.Pages[1].Items), `"21 °C"`) ||
		!strings.Contains(string(l.Pages[1].Items), `"b"`) || !strings.Contains(string(l.Pages[0].Items), `"clock"`) {
		t.Errorf("bars %s and %s", l.Pages[0].Items, l.Pages[1].Items)
	}
	// One target past the bar's items changes none of them.
	if a := r.Receive(packet(seq+3, rev1, `"op":"bar_value","item":{"t":"text","v":"x"},"targets":[0,9]`)); a.Status != "Error: invalid layout" {
		t.Errorf("a target past the bar answered %+v", a)
	}
	if strings.Contains(string(r.Layout().Pages[0].Items), `"x"`) {
		t.Error("a refused bar_value changed an item")
	}
}

func TestAppearanceRenamesAndMovesTheRevision(t *testing.T) {
	r, saved, seq := committed(t)
	a := r.Receive(packet(seq+1, rev2, `"op":"appearance","base":"`+rev1+`","title":"Kitchen screen","pages":[{"p":1,"title":"Lamps"}],"tiles":[{"i":2,"name":"Kettle plug","background":"blue"}]`))
	if a.Status != StatusSynced || a.Rev != rev2 || !a.Applied {
		t.Fatalf("appearance answered %+v", a)
	}
	l := r.Layout()
	if l.Rev != rev2 || l.Begin.Title != "Kitchen screen" || l.Pages[1].Title != "Lamps" || l.Tiles[2].Name != "Kettle plug" ||
		string(l.Backgrounds[2]) != `"blue"` {
		t.Errorf("layout after appearance %+v", l)
	}
	// Written once its turn comes, or at a clean stop: the commit was written a moment ago.
	if len(*saved) != 1 {
		t.Errorf("appearance written at once: %d saves", len(*saved))
	}
	r.Flush()
	if len(*saved) != 2 || (*saved)[1].Rev != rev2 || (*saved)[1].Begin.Title != "Kitchen screen" {
		t.Errorf("appearance not saved: %d saves", len(*saved))
	}
}

func TestPingSaysWhetherTheRevisionIsHeld(t *testing.T) {
	r, _, seq := committed(t)
	if a := r.Receive(packet(seq+1, rev1, `"op":"ping"`)); a.Status != StatusSynced || !a.Applied || a.Rev != rev1 {
		t.Errorf("ping of the held revision answered %+v", a)
	}
	if a := r.Receive(packet(seq+2, rev2, `"op":"ping"`)); a.Status != StatusResend || a.Rev != rev1 {
		t.Errorf("ping of another revision answered %+v", a)
	}
}

func TestAnUnknownOpIsTakenAndIgnored(t *testing.T) {
	r, _, seq := committed(t)
	if a := r.Receive(packet(seq+1, rev1, `"op":"saver","what":"clock"`)); a.Status != StatusSynced || a.Seq != uint32(seq+1) {
		t.Errorf("an unknown op answered %+v", a)
	}
}

// After a restart the kept layout is put back. The add-on starts a new session, its begin for the same
// revision is answered as applied, and no tile comes again.
func TestAKeptLayoutSurvivesARestart(t *testing.T) {
	file := filepath.Join(t.TempDir(), "tessera-layout.json")
	first := NewReceiver(testCaps(), func(l *Layout) error { return Save(file, l) })
	first.newID = func() string { return session }
	hello(t, first)
	for _, m := range layoutMessages(rev1) {
		first.Receive(m)
	}

	kept, err := Load(file)
	if err != nil || kept == nil {
		t.Fatalf("loading the kept layout: %v %v", kept, err)
	}
	second, _ := newTestReceiver(t)
	second.Restore(kept)
	if second.Status() != StatusReady {
		t.Errorf("status after a restore %q, want %q", second.Status(), StatusReady)
	}
	hello(t, second)
	a := second.Receive(layoutMessages(rev1)[0])
	if a.Status != StatusSynced || !a.Applied || a.Rev != rev1 {
		t.Fatalf("a begin of the kept revision answered %+v", a)
	}
	l := second.Layout()
	if l == nil || len(l.Tiles) != 3 || l.Tiles[1].State != "12.5" || l.Pages[0].ID != "p-home" {
		t.Errorf("restored layout %+v", l)
	}
	// A begin for another revision starts over, and the kept one stays until that one's commit.
	b := second.Receive(packet(2, rev2, `"op":"begin","title":"Kitchen","pages":1,"tiles":0,"home":0`))
	if b.Status != StatusLoading || b.Applied || second.Layout().Rev != rev1 {
		t.Errorf("a begin of a new revision answered %+v", b)
	}
}

func TestLoadWithNoFileIsNoLayout(t *testing.T) {
	l, err := Load(filepath.Join(t.TempDir(), "none.json"))
	if l != nil || err != nil {
		t.Errorf("no file loaded as %v, %v", l, err)
	}
}

// A state that leaves out the entity or the name leaves them as they were.
func TestAStateWithoutNamesKeepsThem(t *testing.T) {
	r, _, seq := committed(t)
	if a := r.Receive(packet(seq+1, rev1, `"op":"state","i":0,"state":"off"`)); a.Status != StatusSynced {
		t.Fatalf("state answered %+v", a)
	}
	if tile := r.Layout().Tiles[0]; tile.Entity != "light.ceiling" || tile.Name != "Ceiling" || tile.State != "off" {
		t.Errorf("tile after a state without names %+v", tile)
	}
}

// A begin for the revision held takes the region's ways of writing things and leaves where the pages and
// tiles are; one that counts them differently is a layout sent again. What is kept after either is a
// layout a restart takes.
func TestABeginOfTheHeldRevisionTakesOnlyTheRegion(t *testing.T) {
	r, saved, seq := committed(t)
	a := r.Receive(packet(seq+1, rev1, `"op":"begin","title":"Kitchen","pages":2,"tiles":3,"home":1,"grid":[3,3],"clock_24h":false,"numbers":"1.234,5"`))
	if a.Status != StatusSynced || !a.Applied {
		t.Fatalf("a begin of the held revision answered %+v", a)
	}
	b := r.Layout().Begin
	if b.Clock24h || string(b.Numbers) != `"1.234,5"` || b.Home != 0 || b.Grid != nil || b.Pages != 2 || b.Tiles != 3 {
		t.Errorf("begin after the region changed %+v", b)
	}
	r.Receive(packet(seq+2, rev2, `"op":"appearance","base":"`+rev1+`","title":"Kitchen screen"`))
	r.Flush()
	kept := (*saved)[len(*saved)-1]
	again := NewReceiver(testCaps(), nil)
	again.Restore(kept)
	if again.Layout() == nil {
		t.Errorf("the kept layout is not taken after a restart: %+v", kept.Begin)
	}

	a = r.Receive(packet(seq+3, rev2, `"op":"begin","title":"Kitchen","pages":1,"tiles":0,"home":0`))
	if a.Status != StatusLoading || a.Applied {
		t.Errorf("a begin of the held revision with other counts answered %+v", a)
	}
	if l := r.Layout(); len(l.Pages) != 2 || l.Begin.Pages != 2 || l.Begin.Tiles != 3 {
		t.Errorf("the layout in force changed: %+v", l.Begin)
	}
}

// A change of titles alone is written at most once every saveEvery, the newest of a burst.
func TestTitlesAreWrittenAtMostSoOften(t *testing.T) {
	was := saveEvery
	saveEvery = 50 * time.Millisecond
	t.Cleanup(func() { saveEvery = was })
	var mu sync.Mutex
	var saved []*Layout
	r := NewReceiver(testCaps(), func(l *Layout) error {
		mu.Lock()
		saved = append(saved, l)
		mu.Unlock()
		return nil
	})
	r.newID = func() string { return session }
	hello(t, r)
	msgs := layoutMessages(rev1)
	for _, m := range msgs {
		r.Receive(m)
	}
	seq := len(msgs)
	r.Receive(packet(seq+1, rev2, `"op":"appearance","base":"`+rev1+`","title":"One"`))
	r.Receive(packet(seq+2, rev1, `"op":"appearance","base":"`+rev2+`","title":"Two"`))
	count := func() (int, *Layout) {
		mu.Lock()
		defer mu.Unlock()
		return len(saved), saved[len(saved)-1]
	}
	if n, _ := count(); n != 1 {
		t.Fatalf("%d saves straight after the titles, want only the commit's", n)
	}
	deadline := time.Now().Add(2 * time.Second)
	for n, _ := count(); n < 2 && time.Now().Before(deadline); n, _ = count() {
		time.Sleep(10 * time.Millisecond)
	}
	if n, l := count(); n != 2 || l.Begin.Title != "Two" || l.Rev != rev1 {
		t.Errorf("%d saves, the last titled %q", n, l.Begin.Title)
	}
	r.Flush()
	if n, _ := count(); n != 2 {
		t.Errorf("a flush with nothing waiting wrote: %d saves", n)
	}
}

// The layout is written outside the receiver's lock, so a frame being drawn does not wait for the disk.
func TestTheLayoutIsWrittenOutsideTheLock(t *testing.T) {
	var r *Receiver
	r = NewReceiver(testCaps(), func(l *Layout) error {
		r.with(func(*Layout) {}) // what a frame does
		return nil
	})
	r.newID = func() string { return session }
	done := make(chan struct{})
	go func() {
		hello(t, r)
		for _, m := range layoutMessages(rev1) {
			r.Receive(m)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("writing the layout waited on the receiver's lock")
	}
}

// changed hears the newest status last: one set earlier and handed on later is dropped.
func TestStatusesAreHeardInOrder(t *testing.T) {
	r, _ := newTestReceiver(t)
	var heard []string
	r.changed = func(s string) { heard = append(heard, s) }
	r.say(2, StatusSynced)
	r.say(1, StatusLoading)
	r.say(3, StatusResend)
	if strings.Join(heard, ",") != StatusSynced+","+StatusResend {
		t.Errorf("heard %q", heard)
	}
}

// What a message holds is the home's: the log has its op and size, never its states or names.
func TestMessagesAreLoggedByOpAndSize(t *testing.T) {
	var buf bytes.Buffer
	was := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(was) })
	r, _, seq := committed(t)
	r.Receive(packet(seq+1, rev1, `"op":"state","i":0,"entity":"light.ceiling","name":"Secret name","state":"off"`))
	out := buf.String()
	if strings.Contains(out, "Secret name") || !strings.Contains(out, "op=state") {
		t.Errorf("log:\n%s", out)
	}
}

// A kept layout leaves nothing half written beside it.
func TestSaveLeavesNoTemporaryFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "tessera-layout.json")
	r, _, _ := committed(t)
	if err := Save(file, r.Layout()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(file + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("the temporary file is left: %v", err)
	}
	if l, err := Load(file); err != nil || l == nil || l.Rev != rev1 {
		t.Errorf("loaded %v, %v", l, err)
	}
}

// A plugin's tile comes through as one: its plugin, its tile and its options, no entity behind it and
// nothing for a tap to send to Home Assistant.
func TestAPluginTileIsPlaced(t *testing.T) {
	slot := func(n int) *int { return &n }
	l := &Layout{
		Rev:   "plugin0123456789",
		Begin: Begin{Pages: 1, Tiles: 1, Home: 0},
		Pages: []Page{{P: 0, Title: "Home", Items: json.RawMessage(`[]`)}},
		Tiles: []Tile{{I: 0, Slot: slot(0), Entity: "plugin:world_clock.clocks", Name: "World clock", State: "ok",
			A: json.RawMessage(`{}`), O: json.RawMessage(`{"size":"wide","plugin":{"zone_1":"Asia/Tokyo","face":"calm"}}`)}},
	}
	p := Place(l, 4, 3).Pages[0].Tiles[0]
	if p.Plugin != "world_clock" || p.PluginTile != "clocks" || p.Taps || p.Gone {
		t.Errorf("placed %+v", p)
	}
	if p.PluginOptions["zone_1"] != "Asia/Tokyo" || p.PluginOptions["face"] != "calm" {
		t.Errorf("options %v", p.PluginOptions)
	}
	if _, _, ok := pluginTile("plugin:world_clock"); ok {
		t.Error("a plugin with no tile was taken for a tile")
	}
}
