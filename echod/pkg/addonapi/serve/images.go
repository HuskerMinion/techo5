package serve

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"image"
	"slices"
	"time"

	"github.com/HuskerMinion/techo5/echod/pkg/addonapi"
)

// An add-on's images (stage 2): the ones it put (image.put), by name, and the ones fetched from the
// addresses its scenes name, by address. Both are kept in RAM only, as bytes the display decodes off its
// draw path, and count together toward addonapi.MaxImageBudget, each by addonapi.ImageCost. A put that
// would pass the budget is refused rather than making room by letting another go: the add-on knows what
// it put and what its scenes show, and an image let go behind its back would leave a hole in the page
// with nothing to say why. Fetched images are the daemon's own copies, which it can fetch again, so they
// make room for a put, and for each other, the least recently asked for going first, but never one the
// page or the card shows now.
//
// Nothing about an image is written down or logged per image: a fetch that fails is said once a minute
// per add-on, and tried again after imageRetry.

// Fetching images by address: how many of an add-on's at once, how long one that failed (or found no
// room) is left before it is tried again, how long one may take, how long one let go to make room is
// left before it is fetched again, and how long after the display last asked for one it is kept from
// being let go for another.
var (
	fetchesAtOnce = 2
	imageRetry    = time.Minute
	fetchWithin   = 10 * time.Second
	refetchAfter  = 10 * time.Second
	keptFor       = 5 * time.Second
)

// maxNotBefore is how many addresses an add-on's store remembers not to fetch yet (failed, found no room,
// let go): past it the one due soonest is forgotten. A scene names at most addonapi.MaxImages, and an
// add-on that keeps naming new ones that fail must not grow the daemon's memory.
const maxNotBefore = 256

// keptImage is one image, put or fetched. Its bytes are never changed once kept, so the display may hold
// them while it decodes.
type keptImage struct {
	data []byte
	cost int
	key  string    // names the bytes, whoever's and wherever from: the same bytes, the same key
	used time.Time // last asked for by the display, for letting fetched ones go
}

// imageStore is one add-on's images, under the server's mu.
type imageStore struct {
	put      map[string]*keptImage
	fetched  map[string]*keptImage
	fetching map[string]bool
	// notBefore is the addresses not to fetch before a time: one that failed or found no room, for
	// imageRetry; one let go to make room, for refetchAfter. Held to maxNotBefore.
	notBefore map[string]time.Time
	used      int
	// gen goes up with every change to what the store holds, so a page drawn from it is laid out again
	// (Scene.Images); epoch goes up when the store is emptied, so a fetch that was on its way then is
	// not kept.
	gen, epoch uint64
	// retry is the timer that has the display lay the add-on's scenes out again when an address they
	// name may be fetched again, and retryAt when it fires.
	retry   *time.Timer
	retryAt time.Time
}

func (st *imageStore) init() {
	if st.put == nil {
		st.put, st.fetched = map[string]*keptImage{}, map[string]*keptImage{}
		st.fetching, st.notBefore = map[string]bool{}, map[string]time.Time{}
	}
}

// clear lets every image go, as the add-on's last connection closes or it stops.
func (st *imageStore) clear() {
	if st.retry != nil {
		st.retry.Stop()
	}
	epoch := st.epoch
	*st = imageStore{epoch: epoch + 1, gen: st.gen + 1}
}

// wait notes an address not to be fetched before at, forgetting those already due and, past
// maxNotBefore, the one due soonest.
func (st *imageStore) wait(u string, at, now time.Time) {
	if _, ok := st.notBefore[u]; !ok && len(st.notBefore) >= maxNotBefore {
		for x, t := range st.notBefore {
			if !t.After(now) {
				delete(st.notBefore, x)
			}
		}
		for len(st.notBefore) >= maxNotBefore {
			soonest, first := "", time.Time{}
			for x, t := range st.notBefore {
				if soonest == "" || t.Before(first) {
					soonest, first = x, t
				}
			}
			delete(st.notBefore, soonest)
		}
	}
	st.notBefore[u] = at
}

// imageKey names an add-on's image bytes for the display, which keeps what it decodes under it: the
// add-on's id, so one add-on's never stands for another's, and the bytes' SHA-256, so the same bytes
// fetched again, or put again, are the same key and are not decoded again.
func imageKey(id, kind string, data []byte) string {
	sum := sha256.Sum256(data)
	return id + "/" + kind + ":" + hex.EncodeToString(sum[:])
}

// shownURLs is the addresses the add-on's page and card show now. s.mu is held.
func shownURLs(a *addon) []string {
	var out []string
	if a.screen.page != nil {
		out = a.screen.page.Root.URLs()
	}
	if a.screen.card != nil {
		out = append(out, a.screen.card.Root.URLs()...)
	}
	return out
}

// makeRoom lets fetched images go, the least recently asked for first, until need more fits in the
// budget, and says whether it does. None the add-on's page or card shows now, or that the display asked
// for within keptFor, is let go: that would leave a hole in what is on the screen, and its next layout
// would fetch it again and let another go, round and round. keep is an address not to let go. One that
// is let go is not fetched again for refetchAfter.
func (st *imageStore) makeRoom(need int, keep string, shown []string, now time.Time) bool {
	for st.used+need > addonapi.MaxImageBudget {
		oldest := ""
		var at time.Time
		for u, k := range st.fetched {
			if u == keep || slices.Contains(shown, u) || now.Sub(k.used) < keptFor {
				continue
			}
			if oldest == "" || k.used.Before(at) {
				oldest, at = u, k.used
			}
		}
		if oldest == "" {
			return false
		}
		st.used -= st.fetched[oldest].cost
		delete(st.fetched, oldest)
		st.wait(oldest, now.Add(refetchAfter), now)
	}
	return true
}

// imageOp answers image.put and image.drop.
func (s *Server) imageOp(a *addon, req addonapi.Request) (any, error) {
	switch req.Op {
	case addonapi.OpImagePut:
		var p addonapi.ImagePut
		if err := args(req, &p); err != nil {
			return nil, err
		}
		if !addonapi.ValidName(p.Name) {
			return nil, bad("an image's name %q is not a name (lowercase letters, digits and underscores, starting with a letter)", p.Name)
		}
		if err := addonapi.ImageBytes(p.Data); err != nil {
			return nil, err
		}
		cfg, _, _ := image.DecodeConfig(bytes.NewReader(p.Data))
		cost := addonapi.ImageCost(cfg.Width, cfg.Height, len(p.Data))
		s.mu.Lock()
		st := &a.images
		st.init()
		old := st.put[p.Name]
		if old == nil && len(st.put) >= addonapi.MaxImageNames {
			s.mu.Unlock()
			return nil, addonapi.Errorf(addonapi.CodeTooLarge, "an add-on may have put %d images at once; drop one first (image.drop)",
				addonapi.MaxImageNames)
		}
		if old != nil {
			st.used -= old.cost
		}
		if !st.makeRoom(cost, "", shownURLs(a), s.Now()) {
			if old != nil {
				st.used += old.cost
			}
			used := st.used
			s.mu.Unlock()
			return nil, addonapi.Errorf(addonapi.CodeTooLarge,
				"the add-on's images would take %d bytes of its %d with this one (%d, its pixels at four bytes each), and those fetched by address that its page and card show now are not let go; drop some first (image.drop)",
				used+cost, addonapi.MaxImageBudget, cost)
		}
		st.gen++
		st.put[p.Name] = &keptImage{data: p.Data, cost: cost, key: imageKey(a.grant.ID, "n", p.Data), used: s.Now()}
		st.used += cost
		out := addonapi.ImageBudget{Used: st.used, Budget: addonapi.MaxImageBudget}
		s.imagesChangedLocked(a)
		s.mu.Unlock()
		return out, nil

	case addonapi.OpImageDrop:
		var d addonapi.ImageDrop
		if err := args(req, &d); err != nil {
			return nil, err
		}
		s.mu.Lock()
		st := &a.images
		k, ok := st.put[d.Name]
		if ok {
			st.used -= k.cost
			delete(st.put, d.Name)
			st.gen++
			s.imagesChangedLocked(a)
		}
		out := addonapi.ImageBudget{Used: st.used, Budget: addonapi.MaxImageBudget}
		s.mu.Unlock()
		if !ok {
			return nil, bad("the add-on has no image %q", d.Name)
		}
		return out, nil
	}
	return nil, addonapi.Errorf(addonapi.CodeUnknownOp, "there is no op %q", req.Op)
}

// imagesChangedLocked has the display told that the add-on's page and card may draw otherwise now. s.mu
// is held.
func (s *Server) imagesChangedLocked(a *addon) {
	if a.screen.page != nil {
		s.screenDirtyLocked(a, true)
	}
	if a.screen.card != nil {
		s.screenDirtyLocked(a, false)
	}
}

// ImageUsed is how much of addonapi.MaxImageBudget the add-on's images take now.
func (s *Server) ImageUsed(id string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if a := s.addons[id]; a != nil {
		return a.images.used
	}
	return 0
}

// Image is the bytes of the image an image block of the add-on's shows, and a key that names them
// (imageKey), which is another key whenever the bytes are others and never another add-on's: the display
// decodes and scales them off its draw path, and keeps what it made under the key. false is an image
// there is none of yet: a name not put, or an address not fetched yet, whose fetch this starts (once a
// minute at most, when it fails), and the display is told when it has come (PageChanged, CardChanged).
// Inline bytes (a block's Data) are the block's own.
func (s *Server) Image(id string, b *addonapi.Block) ([]byte, string, bool) {
	if b == nil || b.Type != addonapi.BlockImage {
		return nil, "", false
	}
	if len(b.Data) > 0 {
		return b.Data, imageKey(id, "d", b.Data), true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.addons[id]
	if a == nil {
		return nil, "", false
	}
	st := &a.images
	st.init()
	now := s.Now()
	switch {
	case b.Name != "":
		k := st.put[b.Name]
		if k == nil {
			return nil, "", false
		}
		k.used = now
		return k.data, k.key, true
	case b.URL != "":
		if k := st.fetched[b.URL]; k != nil {
			k.used = now
			return k.data, k.key, true
		}
		s.fetchLocked(a, b.URL, now)
	}
	return nil, "", false
}

// fetchLocked starts fetching an image's address for the add-on, unless it is on its way, is not to be
// fetched yet (it failed, found no room or was let go), or as many as fetchesAtOnce are on their way
// already (it is asked for again when one of them is done, as the display lays the page out again).
// s.mu is held.
func (s *Server) fetchLocked(a *addon, u string, now time.Time) {
	st := &a.images
	if s.FetchImage == nil || st.fetching[u] || len(st.fetching) >= fetchesAtOnce {
		return
	}
	if at, ok := st.notBefore[u]; ok {
		if now.Before(at) {
			s.retryLocked(a, at)
			return
		}
		delete(st.notBefore, u)
	}
	// Checked again here, as the scene was when it was set: the fetcher's dialer is what holds a name
	// to the LAN, but an address that is a number is refused before anything is dialed.
	if addonapi.ImageURL(u) != nil {
		st.wait(u, now.Add(imageRetry), now)
		return
	}
	st.fetching[u] = true
	epoch := st.epoch
	ctx := a.ctx
	go s.fetchImage(ctx, a, u, epoch)
}

// retryLocked has the add-on's scenes laid out again at at, when an address they name may be fetched
// again: one timer for the add-on, at the soonest. s.mu is held.
func (s *Server) retryLocked(a *addon, at time.Time) {
	st := &a.images
	if st.retry != nil && !st.retryAt.After(at) {
		return
	}
	if st.retry != nil {
		st.retry.Stop()
	}
	epoch := st.epoch
	st.retryAt = at
	st.retry = time.AfterFunc(max(at.Sub(s.Now()), 0), func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if st.epoch != epoch || s.addons[a.grant.ID] != a {
			return
		}
		st.retry, st.retryAt = nil, time.Time{}
		// Laid out again, the scenes ask for what they show, and what is due is fetched.
		st.gen++
		s.imagesChangedLocked(a)
	})
}

// fetchImage fetches one address and keeps what came, when it is an image within the limits and there
// is room for it. Either way the display lays the add-on's scenes out again: with the image, or to ask
// for the next of them that waited for this one's place among those fetched at once.
func (s *Server) fetchImage(ctx context.Context, a *addon, u string, epoch uint64) {
	ctx, cancel := context.WithTimeout(ctx, fetchWithin)
	b, err := s.FetchImage(ctx, u)
	cancel()
	cost := 0
	if err == nil {
		if err = addonapi.ImageBytes(b); err == nil {
			cfg, _, _ := image.DecodeConfig(bytes.NewReader(b))
			cost = addonapi.ImageCost(cfg.Width, cfg.Height, len(b))
		}
	}
	s.mu.Lock()
	st := &a.images
	if st.epoch != epoch || s.addons[a.grant.ID] != a {
		s.mu.Unlock()
		return // let go meanwhile, with everything else the add-on had
	}
	delete(st.fetching, u)
	now := s.Now()
	if err == nil && !st.makeRoom(cost, u, shownURLs(a), now) {
		err = addonapi.Errorf(addonapi.CodeTooLarge, "no room among the add-on's images for one of %d bytes: those its page and card show take the rest", cost)
	}
	st.gen++
	if err != nil {
		st.wait(u, now.Add(imageRetry), now)
		s.retryLocked(a, now.Add(imageRetry))
		s.imagesChangedLocked(a)
		s.mu.Unlock()
		s.warn(a.grant.ID, "image", "addonapi: an add-on's image could not be fetched", "err", err)
		return
	}
	delete(st.notBefore, u)
	st.fetched[u] = &keptImage{data: b, cost: cost, key: imageKey(a.grant.ID, "u", b), used: now}
	st.used += cost
	s.imagesChangedLocked(a)
	s.mu.Unlock()
}

// Fetching is the addresses of the add-on's images on their way, for tests.
func (s *Server) fetching(id string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	if a := s.addons[id]; a != nil {
		for u := range a.images.fetching {
			out = append(out, u)
		}
	}
	slices.Sort(out)
	return out
}
