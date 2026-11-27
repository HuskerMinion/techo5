package serve

import (
	"slices"
	"sync"
	"time"

	"github.com/HuskerMinion/techo5/echod/pkg/addonapi"
)

// The screen (stage 2, "screen1"): an add-on's page under the Add-ons tab and its card over the clock.
// The server keeps each add-on's scenes, checks every scene.set and scene.patch against the scene's
// limits and the add-on's own timers, and tells the display, through a Screen, that one changed, at
// most addonapi.SceneUpdatesPerSecond times a second: an add-on that sends faster has its frames
// merged, as only the latest is kept. The display reads the scene when it draws, and tells the server
// what the owner did with it (Touch, Swipe, Back, Shown, DismissCard), which reaches the add-on as an
// event, only for what it showed and only while it is in view.
//
// Scenes are in RAM only, owned by the add-on whose socket they came through (whose id is its uid's),
// and go when its last connection closes, when it stops and when it is removed. Nothing about them is
// written down or logged.

// Screen is the display's side of the add-on screen, which the display implements and gives the server
// (Server.SetScreen). Each call says only that something may have changed: the display reads what is
// there now (Server.Scene), so calls that come together, or late, draw the latest. They are made outside
// the server's lock, from the goroutine of a request or of a timer, and must return at once, handing the
// work to the display's own goroutine.
type Screen interface {
	// PageChanged is the add-on's page set, patched or gone (Scene answers false then).
	PageChanged(id string)
	// CardChanged is the add-on's card set, patched or gone. Of several add-ons' cards the newest
	// shows, and the others keep their place behind it.
	CardChanged(id string)
	// Open is the add-on asking for its page to be brought up (screen.open), from a command, say. It
	// waits behind whatever is more pressing (a ring, a call, a turn, the settings, any screen the
	// device reserves), as the dashboard and Cast do: the display decides when, if at all.
	Open(id string)
	// Close is the add-on asking for its page to be put away (screen.close), or its page gone with it.
	Close(id string)
	// Dropped is everything of the add-on's gone, as its last connection closed or it stopped: its
	// scenes and its images. The display lets go of what it made of them (its decoded pictures), which
	// it would otherwise keep until they are pushed out.
	Dropped(id string)
}

// NopScreen is a screen that draws nothing: a build whose display has no add-on screen yet, or a test.
// A device with no screen at all (a Dot) has no Screen, so the screen's ops are refused there.
type NopScreen struct{}

func (NopScreen) PageChanged(string) {}
func (NopScreen) CardChanged(string) {}
func (NopScreen) Open(string)        {}
func (NopScreen) Close(string)       {}
func (NopScreen) Dropped(string)     {}

// Scene is an add-on's page or card as the display draws it. Root is the server's and is never changed
// once given out (a patch makes a new tree), so the display may hold it while it draws; it must not
// change it.
type Scene struct {
	Root addonapi.Block
	// Rev goes up with each change of this scene, and At is when it was made: of several add-ons'
	// cards, the newest is the one with the latest At.
	Rev uint64
	At  time.Time
	// Set is the Rev of the scene.set this scene comes from, which its patches keep: a new Set is a new
	// scene, whose lists start at their tops.
	Set uint64
	// Images goes up whenever the add-on's images change (one put, dropped or fetched), so the display
	// lays the scene out again, with Server.Image, when either this or Rev is not what it laid out.
	Images uint64
}

// screenState is one add-on's page and card, under the server's mu.
type screenState struct {
	page, card *Scene
	rev        uint64
	// pageShown and cardShown are what the display last said of each (Shown), whether or not there is a
	// scene yet: a page brought up before the add-on set one is in view the moment it does. In view is
	// that and a scene. pageTold is what the add-on was last told of its page (screen.state); of its card
	// it is told nothing but that the owner swiped it away.
	pageShown, cardShown bool
	pageTold             bool
	// pageTaps and cardTaps are when each tappable block last changed, so a tap the display read off a
	// layout older than that is not sent for what is there now.
	pageTaps, cardTaps tapRevs

	dirtyPage, dirtyCard bool
	drawnAt              time.Time   // when the display was last told
	timer                *time.Timer // the next telling, nil with none due

	opens bucket // screen.open
	// askedAt is the last time a person asked something of the add-on (Asked): screen.open is allowed
	// for addonapi.ScreenOpenWindow after it, once. A successful open uses it up, and so does the owner
	// leaving the page (the way out, the back gesture), so one person's action brings the page up once
	// and a page the owner just put away is not brought straight back.
	askedAt time.Time
}

// tapRevs says, for a scene, from which revision on a tap on each of its blocks is a tap on what is
// there now: set is the scene.set's revision, and changed the revision of the last patch of a block
// (of it, of something in it, or of something it is in), by id.
type tapRevs struct {
	set     uint64
	changed map[string]uint64
}

// reset is a new scene, set at rev.
func (t *tapRevs) reset(rev uint64) { t.set, t.changed = rev, nil }

// patched notes a patch at rev of the block id in root (the patched scene): every block with an id in it
// and around it changed. Ids patched away are forgotten once there are more than the scene can hold.
func (t *tapRevs) patched(root *addonapi.Block, id string, rev uint64) {
	if t.changed == nil {
		t.changed = map[string]uint64{}
	}
	for _, x := range touchedBy(root, id) {
		t.changed[x] = rev
	}
	if len(t.changed) > 2*addonapi.MaxBlocks {
		for x := range t.changed {
			if _, ok := root.Find(x); !ok {
				delete(t.changed, x)
			}
		}
	}
}

// current is whether a tap on block, read off the layout of revision rev, is a tap on what is there now.
func (t *tapRevs) current(block string, rev, now uint64) bool {
	return rev >= t.set && rev <= now && rev >= t.changed[block]
}

// touchedBy is the ids of the blocks a patch of id changes: it, everything in it, and everything it is in.
func touchedBy(root *addonapi.Block, id string) []string {
	var out []string
	var all func(b *addonapi.Block)
	all = func(b *addonapi.Block) {
		if b.ID != "" {
			out = append(out, b.ID)
		}
		for i := range b.Children {
			all(&b.Children[i])
		}
		for s := range b.Sections {
			for i := range b.Sections[s].Items {
				all(&b.Sections[s].Items[i])
			}
		}
	}
	var path []string
	var find func(b *addonapi.Block) bool
	find = func(b *addonapi.Block) bool {
		if b.ID == id {
			all(b)
			return true
		}
		if b.ID != "" {
			path = append(path, b.ID)
		}
		for i := range b.Children {
			if find(&b.Children[i]) {
				return true
			}
		}
		for s := range b.Sections {
			for i := range b.Sections[s].Items {
				if find(&b.Sections[s].Items[i]) {
					return true
				}
			}
		}
		if b.ID != "" {
			path = path[:len(path)-1]
		}
		return false
	}
	find(root)
	return append(out, path...)
}

// sceneEvery is how often the display is told of an add-on's scenes, at most: addonapi's, as a
// variable for tests.
var sceneEvery = time.Second / addonapi.SceneUpdatesPerSecond

// SetScreen gives the server the display's side of the add-on screen; nil, as on a device without a
// screen, refuses the screen's ops (unavailable).
func (s *Server) SetScreen(sc Screen) {
	s.mu.Lock()
	s.screen = sc
	s.mu.Unlock()
}

func (s *Server) screenSink() Screen {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.screen
}

// screenOp answers scene.set, scene.patch, screen.open, screen.close and card.clear.
func (s *Server) screenOp(c *conn, req addonapi.Request) (any, error) {
	a := c.a
	sink := s.screenSink()
	if sink == nil {
		return nil, addonapi.Errorf(addonapi.CodeUnavailable, "this device has no screen for an add-on")
	}
	switch req.Op {
	case addonapi.OpSceneSet:
		var set addonapi.SceneSet
		if err := args(req, &set); err != nil {
			return nil, err
		}
		if err := addonapi.ValidScene(set.On, set.Scene, s.ownTimer(a.grant.ID)); err != nil {
			return nil, err
		}
		s.keepScene(a, set.On, set.Scene, "")
		return nil, nil

	case addonapi.OpScenePatch:
		var p addonapi.ScenePatch
		if err := args(req, &p); err != nil {
			return nil, err
		}
		if !addonapi.ValidTarget(p.On) {
			return nil, bad("a scene is shown on the %q or the %q", addonapi.OnPage, addonapi.OnCard)
		}
		// Held from reading the scene to keeping the patched one, so two connections' patches cannot
		// each start from the same scene and one be lost.
		a.sceneMu.Lock()
		defer a.sceneMu.Unlock()
		s.mu.Lock()
		cur := a.screen.page
		if p.On == addonapi.OnCard {
			cur = a.screen.card
		}
		s.mu.Unlock()
		if cur == nil {
			return nil, bad("the add-on has no %s to patch; scene.set sets one", p.On)
		}
		out, found, err := cur.Root.Patched(p.ID, p.Block)
		switch {
		case err != nil:
			return nil, err
		case !found:
			return nil, bad("the %s has no block %q", p.On, p.ID)
		}
		// The whole scene is held to its limits again, and the timers of what is new to it checked:
		// one the rest of it was tied to may have ended since, which is not this patch's fault.
		if err := addonapi.ValidScene(p.On, out, nil); err != nil {
			return nil, err
		}
		own := s.ownTimer(a.grant.ID)
		for _, t := range p.Block.Timers() {
			if !own(t) {
				return nil, bad("a progress names %q, which is not one of the add-on's timers", t)
			}
		}
		s.keepSceneLocked(a, p.On, out, p.ID)
		return nil, nil

	case addonapi.OpScreenOpen:
		if err := args(req, &addonapi.None{}); err != nil {
			return nil, err
		}
		s.mu.Lock()
		now := s.Now()
		has := a.screen.page != nil
		asked := !a.screen.askedAt.IsZero() && now.Sub(a.screen.askedAt) <= addonapi.ScreenOpenWindow
		ok := has && asked && a.screen.opens.take(now)
		if ok {
			// Used up: one person's action brings the page up once.
			a.screen.askedAt = time.Time{}
		}
		s.mu.Unlock()
		switch {
		case !has:
			return nil, bad("the add-on has no page to open; scene.set sets one")
		case !asked:
			return nil, addonapi.Errorf(addonapi.CodeBusy,
				"screen.open is only in answer to a person: within %v of one of the add-on's commands, or a tap on its tile, its card or its page",
				addonapi.ScreenOpenWindow)
		case !ok:
			s.strike(a)
			return nil, addonapi.Errorf(addonapi.CodeRateLimited, "screen.open %d at once, then once every %v",
				addonapi.ScreenOpenBurst, addonapi.ScreenOpenEvery)
		}
		sink.Open(a.grant.ID)
		return nil, nil

	case addonapi.OpScreenClose:
		if err := args(req, &addonapi.None{}); err != nil {
			return nil, err
		}
		sink.Close(a.grant.ID)
		return nil, nil

	case addonapi.OpCardClear:
		if err := args(req, &addonapi.None{}); err != nil {
			return nil, err
		}
		a.sceneMu.Lock()
		defer a.sceneMu.Unlock()
		s.mu.Lock()
		if a.screen.card != nil {
			// Out of view at once, so no tap reaches it; the display says when the next is in view.
			a.screen.card, a.screen.cardShown = nil, false
			s.screenDirtyLocked(a, false)
		}
		s.mu.Unlock()
		return nil, nil
	}
	return nil, addonapi.Errorf(addonapi.CodeUnknownOp, "there is no op %q", req.Op)
}

// ownTimer says whether a timer id is one of the add-on's own, asking the device once, the first time
// it is needed.
func (s *Server) ownTimer(id string) func(string) bool {
	var once sync.Once
	var mine []string
	return func(t string) bool {
		once.Do(func() {
			for _, x := range s.Device.Timers(id) {
				mine = append(mine, x.ID)
			}
		})
		return slices.Contains(mine, t)
	}
}

// keepScene keeps a scene the add-on set, under its scene lock.
func (s *Server) keepScene(a *addon, on string, root addonapi.Block, patched string) {
	a.sceneMu.Lock()
	defer a.sceneMu.Unlock()
	s.keepSceneLocked(a, on, root, patched)
}

// keepSceneLocked keeps a scene, which nothing else holds, and has the display told: a.sceneMu is held.
// patched is the id of the block a patch replaced, "" for a scene.set. A page that comes while the
// display shows its add-on's page waiting for one is in view at once, and the add-on is told.
func (s *Server) keepSceneLocked(a *addon, on string, root addonapi.Block, patched string) {
	s.mu.Lock()
	if s.addons[a.grant.ID] != a {
		s.mu.Unlock()
		return // stopped meanwhile: kept, it would outlive the add-on
	}
	st := &a.screen
	st.rev++
	sc := &Scene{Root: root, Rev: st.rev, At: s.Now(), Set: st.rev}
	cur, taps := &st.page, &st.pageTaps
	if on == addonapi.OnCard {
		cur, taps = &st.card, &st.cardTaps
	}
	if patched != "" && *cur != nil {
		sc.Set = (*cur).Set
		taps.patched(&sc.Root, patched, st.rev)
	} else {
		taps.reset(st.rev)
	}
	*cur = sc
	s.screenDirtyLocked(a, on == addonapi.OnPage)
	tell := s.pageToldLocked(a)
	s.mu.Unlock()
	s.tellPage(a.grant.ID, tell)
}

// pageToldLocked brings what the add-on was told of its page in line with whether it is in view, and
// says what to tell it: nil for nothing. s.mu is held.
func (s *Server) pageToldLocked(a *addon) *bool {
	in := a.screen.pageShown && a.screen.page != nil
	if in == a.screen.pageTold {
		return nil
	}
	a.screen.pageTold = in
	return &in
}

// tellPage tells the add-on its page came into view or went out of it, when there is something to tell.
func (s *Server) tellPage(id string, shown *bool) {
	if shown != nil {
		s.Emit(id, addonapi.EventScreenState, addonapi.ScreenStateEvent{On: addonapi.OnPage, Shown: *shown})
	}
}

// screenDirtyLocked notes that the add-on's page or card changed, and has the display told of it when
// sceneEvery has passed since it was last told: what changes before then is told with it, the latest
// only. s.mu is held.
func (s *Server) screenDirtyLocked(a *addon, page bool) {
	sc := &a.screen
	if page {
		sc.dirtyPage = true
	} else {
		sc.dirtyCard = true
	}
	if sc.timer != nil {
		return
	}
	wait := max(sceneEvery-time.Since(sc.drawnAt), 0)
	sc.timer = time.AfterFunc(wait, func() { s.screenTell(a) })
}

// screenTell tells the display what of the add-on's changed.
func (s *Server) screenTell(a *addon) {
	s.mu.Lock()
	sc := &a.screen
	page, card := sc.dirtyPage, sc.dirtyCard
	sc.dirtyPage, sc.dirtyCard, sc.timer, sc.drawnAt = false, false, nil, time.Now()
	sink := s.screen
	open := s.addons[a.grant.ID] == a
	s.mu.Unlock()
	if sink == nil || !open {
		return // gone: screenDrop told the display already
	}
	if page {
		sink.PageChanged(a.grant.ID)
	}
	if card {
		sink.CardChanged(a.grant.ID)
	}
}

// screenDrop takes away the add-on's page and card, as its last connection closes or it stops: a page
// with buttons that nothing answers would only mislead. It sets them again after hello.
func (s *Server) screenDrop(a *addon) {
	s.mu.Lock()
	sc := &a.screen
	page, card := sc.page != nil, sc.card != nil
	sc.page, sc.card, sc.pageShown, sc.cardShown, sc.pageTold = nil, nil, false, false, false
	sc.pageTaps, sc.cardTaps = tapRevs{}, tapRevs{}
	// Its images with them: they are there for its scenes, and it puts them again after hello.
	a.images.clear()
	sc.dirtyPage, sc.dirtyCard = false, false
	if sc.timer != nil {
		sc.timer.Stop()
		sc.timer = nil
	}
	sink := s.screen
	s.mu.Unlock()
	if sink == nil {
		return
	}
	if page {
		sink.Close(a.grant.ID)
		sink.PageChanged(a.grant.ID)
	}
	if card {
		sink.CardChanged(a.grant.ID)
	}
	sink.Dropped(a.grant.ID)
}

// The display's half: what it reads, and what the owner did.

// Scene is the add-on's page or card (addonapi.OnPage, addonapi.OnCard) as it is now, and whether it
// has one.
func (s *Server) Scene(id, on string) (Scene, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.addons[id]
	if a == nil {
		return Scene{}, false
	}
	sc := a.screen.page
	if on == addonapi.OnCard {
		sc = a.screen.card
	}
	if sc == nil {
		return Scene{}, false
	}
	out := *sc
	out.Images = a.images.gen
	return out, true
}

// Screens is the running add-ons granted the screen, sorted: the tiles under the Add-ons tab. One that
// has set no page yet is among them; its tile opens on an empty page under its name bar.
func (s *Server) Screens() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for id, a := range s.addons {
		if a.grant.Has(addonapi.CapScreen) {
			out = append(out, id)
		}
	}
	slices.Sort(out)
	return out
}

// Shown is the display saying the add-on's page or card came into view or went out of it. Touches,
// swipes and the back gesture reach an add-on only for what is in view: the display marks a page or a
// card hidden whenever a screen the device reserves covers it. It is kept whether or not the add-on has
// set the scene yet, so a page brought up while it is still waited for is in view as soon as it comes.
// The add-on is told when its page comes into view or goes (screen.state); of its card, which is a
// glance over the clock that comes and goes with the clock, it is told nothing but that the owner swiped
// it away (DismissCard), so an add-on cannot learn from its card when the room is looked at.
func (s *Server) Shown(id, on string, shown bool) {
	if !addonapi.ValidTarget(on) {
		return
	}
	s.mu.Lock()
	a := s.addons[id]
	var tell *bool
	if a != nil {
		if on == addonapi.OnCard {
			a.screen.cardShown = shown
		} else {
			a.screen.pageShown = shown
			tell = s.pageToldLocked(a)
		}
	}
	s.mu.Unlock()
	s.tellPage(id, tell)
}

// DismissCard is the owner swiping the add-on's card away: it goes, and the add-on is told, until it
// sets the card again.
func (s *Server) DismissCard(id string) {
	s.mu.Lock()
	a := s.addons[id]
	had := a != nil && a.screen.card != nil
	if had {
		a.screen.card, a.screen.cardShown = nil, false
	}
	s.mu.Unlock()
	if had {
		s.Emit(id, addonapi.EventScreenState, addonapi.ScreenStateEvent{On: addonapi.OnCard, Shown: false, Dismissed: true})
	}
}

// shownWith is the add-on's scene on that surface, when it is in view.
func (s *Server) shownWith(id, on string) (*Scene, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.addons[id]
	if a == nil {
		return nil, false
	}
	if on == addonapi.OnCard {
		return a.screen.card, a.screen.card != nil && a.screen.cardShown
	}
	return a.screen.page, a.screen.page != nil && a.screen.pageShown
}

// Touch is a tap on a block of the add-on's page or card, as the display found it in the layout of the
// scene's revision rev: the add-on is told, when the block is one of its buttons or chips, or a list item
// with an id, in what is in view, and unchanged since rev (a new scene, or a patch of the block, of
// something in it or of something it is in, came after the layout the finger was on), and Touch says
// whether it was. A tap is a person asking something of the add-on, so it may bring its page up for a
// moment after (screen.open).
func (s *Server) Touch(id, on, block string, rev uint64) bool {
	sc, ok := s.shownWith(id, on)
	if !ok || !sc.Root.TapTarget(block) {
		return false
	}
	s.mu.Lock()
	current := false
	if a := s.addons[id]; a != nil {
		taps := &a.screen.pageTaps
		if on == addonapi.OnCard {
			taps = &a.screen.cardTaps
		}
		current = taps.current(block, rev, sc.Rev)
	}
	s.mu.Unlock()
	if !current {
		return false
	}
	s.Asked(id)
	s.Emit(id, addonapi.EventTouch, addonapi.TouchEvent{ID: block, On: on})
	return true
}

// Asked is a person asking something of the add-on: one of its commands sent to it, or a tap on its tile,
// its card or its page. For addonapi.ScreenOpenWindow after it, the add-on may bring its page up
// (screen.open); at no other time.
func (s *Server) Asked(id string) {
	s.mu.Lock()
	if a := s.addons[id]; a != nil {
		a.screen.askedAt = s.Now()
	}
	s.mu.Unlock()
}

// Swipe is a swipe left or right across the add-on's page while it is in view.
func (s *Server) Swipe(id, direction string) bool {
	if direction != addonapi.SwipeLeft && direction != addonapi.SwipeRight {
		return false
	}
	if _, ok := s.shownWith(id, addonapi.OnPage); !ok {
		return false
	}
	s.Emit(id, addonapi.EventSwipe, addonapi.SwipeEvent{Direction: direction})
	return true
}

// Back is the back gesture on the add-on's page while it is in view. What it does is the add-on's (a
// step back within its page, or screen.close); the name bar's own way out is the daemon's and always
// leaves. Somebody going back is not asking for the page: what an earlier tap allowed (screen.open) is
// used up.
func (s *Server) Back(id string) bool {
	if _, ok := s.shownWith(id, addonapi.OnPage); !ok {
		return false
	}
	s.Left(id)
	s.Emit(id, addonapi.EventBack, addonapi.None{})
	return true
}

// Left is the owner putting the add-on's page away by the name bar's way out: what an earlier tap or
// command allowed (screen.open) is used up, so the add-on cannot bring straight back a page somebody just
// put away.
func (s *Server) Left(id string) {
	s.mu.Lock()
	if a := s.addons[id]; a != nil {
		a.screen.askedAt = time.Time{}
	}
	s.mu.Unlock()
}
