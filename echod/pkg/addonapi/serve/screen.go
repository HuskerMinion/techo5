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
}

// NopScreen is a screen that draws nothing: a build whose display has no add-on screen yet, or a test.
// A device with no screen at all (a Dot) has no Screen, so the screen's ops are refused there.
type NopScreen struct{}

func (NopScreen) PageChanged(string) {}
func (NopScreen) CardChanged(string) {}
func (NopScreen) Open(string)        {}
func (NopScreen) Close(string)       {}

// Scene is an add-on's page or card as the display draws it. Root is the server's and is never changed
// once given out (a patch makes a new tree), so the display may hold it while it draws; it must not
// change it.
type Scene struct {
	Root addonapi.Block
	// Rev goes up with each change of this scene, and At is when it was made: of several add-ons'
	// cards, the newest is the one with the latest At.
	Rev uint64
	At  time.Time
}

// screenState is one add-on's page and card, under the server's mu.
type screenState struct {
	page, card           *Scene
	rev                  uint64
	pageShown, cardShown bool

	dirtyPage, dirtyCard bool
	drawnAt              time.Time   // when the display was last told
	timer                *time.Timer // the next telling, nil with none due

	opens bucket // screen.open
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
		s.keepScene(a, set.On, set.Scene)
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
		s.keepSceneLocked(a, p.On, out)
		return nil, nil

	case addonapi.OpScreenOpen:
		if err := args(req, &addonapi.None{}); err != nil {
			return nil, err
		}
		s.mu.Lock()
		has := a.screen.page != nil
		ok := has && a.screen.opens.take(s.Now())
		s.mu.Unlock()
		switch {
		case !has:
			return nil, bad("the add-on has no page to open; scene.set sets one")
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
			a.screen.card = nil
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
func (s *Server) keepScene(a *addon, on string, root addonapi.Block) {
	a.sceneMu.Lock()
	defer a.sceneMu.Unlock()
	s.keepSceneLocked(a, on, root)
}

// keepSceneLocked keeps a scene, which nothing else holds, and has the display told: a.sceneMu is held.
func (s *Server) keepSceneLocked(a *addon, on string, root addonapi.Block) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.addons[a.grant.ID] != a {
		return // stopped meanwhile: kept, it would outlive the add-on
	}
	a.screen.rev++
	sc := &Scene{Root: root, Rev: a.screen.rev, At: s.Now()}
	if on == addonapi.OnCard {
		a.screen.card = sc
	} else {
		a.screen.page = sc
	}
	s.screenDirtyLocked(a, on == addonapi.OnPage)
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
	sc.page, sc.card, sc.pageShown, sc.cardShown = nil, nil, false, false
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
	return *sc, true
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

// Shown is the display saying the add-on's page or card came into view or went out of it, which the
// add-on is told (screen.state) when it changes. Touches, swipes and the back gesture reach an add-on
// only for what is in view: the display marks a page or a card hidden whenever a screen the device
// reserves covers it.
func (s *Server) Shown(id, on string, shown bool) {
	s.mu.Lock()
	a := s.addons[id]
	changed := false
	if a != nil {
		sc := &a.screen
		cur, has := &sc.pageShown, sc.page != nil
		if on == addonapi.OnCard {
			cur, has = &sc.cardShown, sc.card != nil
		}
		if *cur != shown && (has || !shown) && addonapi.ValidTarget(on) {
			*cur, changed = shown, true
		}
	}
	s.mu.Unlock()
	if changed {
		s.Emit(id, addonapi.EventScreenState, addonapi.ScreenStateEvent{On: on, Shown: shown})
	}
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

// Touch is a tap on a block of the add-on's page or card: the add-on is told, when the block is one of
// its buttons or chips in what is in view, and Touch says whether it was.
func (s *Server) Touch(id, on, block string) bool {
	sc, ok := s.shownWith(id, on)
	if !ok || !sc.Root.TapTarget(block) {
		return false
	}
	s.Emit(id, addonapi.EventTouch, addonapi.TouchEvent{ID: block, On: on})
	return true
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
// leaves.
func (s *Server) Back(id string) bool {
	if _, ok := s.shownWith(id, addonapi.OnPage); !ok {
		return false
	}
	s.Emit(id, addonapi.EventBack, addonapi.None{})
	return true
}
