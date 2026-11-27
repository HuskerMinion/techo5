package serve

import (
	"bytes"
	"context"
	"hash/fnv"
	"image"
	"slices"
	"strconv"
	"time"

	"github.com/HuskerMinion/techo5/echod/pkg/addonapi"
)

// An add-on's images (stage 2): the ones it put (image.put), by name, and the ones fetched from the
// addresses its scenes name, by address. Both are kept in RAM only, as bytes the display decodes off its
// draw path, and count together toward addonapi.MaxImageBudget, each by addonapi.ImageCost. A put that
// would pass the budget is refused rather than making room by letting another go: the add-on knows what
// it put and what its scenes show, and an image let go behind its back would leave a hole in the page
// with nothing to say why. Fetched images are the daemon's own copies, which it can fetch again, so they
// make room for a put, and for each other, the least recently asked for going first.
//
// Nothing about an image is written down or logged per image: a fetch that fails is said once a minute
// per add-on, and tried again after imageRetry.

// Fetching images by address: how many of an add-on's at once, how long one that failed is left before
// it is tried again, and how long one may take.
var (
	fetchesAtOnce = 2
	imageRetry    = time.Minute
	fetchWithin   = 10 * time.Second
)

// keptImage is one image, put or fetched. Its bytes are never changed once kept, so the display may hold
// them while it decodes.
type keptImage struct {
	data []byte
	cost int
	rev  uint64    // the store's gen when it was kept: a put of the same name again is another image
	used time.Time // last asked for by the display, for letting fetched ones go
}

// imageStore is one add-on's images, under the server's mu.
type imageStore struct {
	put      map[string]*keptImage
	fetched  map[string]*keptImage
	fetching map[string]bool
	failed   map[string]time.Time
	used     int
	// gen goes up with every change to what the store holds, so a page drawn from it is laid out again
	// (Scene.Images); epoch goes up when the store is emptied, so a fetch that was on its way then is
	// not kept.
	gen, epoch uint64
}

func (st *imageStore) init() {
	if st.put == nil {
		st.put, st.fetched = map[string]*keptImage{}, map[string]*keptImage{}
		st.fetching, st.failed = map[string]bool{}, map[string]time.Time{}
	}
}

// clear lets every image go, as the add-on's last connection closes or it stops.
func (st *imageStore) clear() {
	epoch := st.epoch
	*st = imageStore{epoch: epoch + 1, gen: st.gen + 1}
}

// makeRoom lets fetched images go, the least recently asked for first, until need more fits in the
// budget, and says whether it does. keep is an address not to let go.
func (st *imageStore) makeRoom(need int, keep string) bool {
	for st.used+need > addonapi.MaxImageBudget {
		oldest := ""
		var at time.Time
		for u, k := range st.fetched {
			if u != keep && (oldest == "" || k.used.Before(at)) {
				oldest, at = u, k.used
			}
		}
		if oldest == "" {
			return false
		}
		st.used -= st.fetched[oldest].cost
		delete(st.fetched, oldest)
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
		if !st.makeRoom(cost, "") {
			if old != nil {
				st.used += old.cost
			}
			used := st.used
			s.mu.Unlock()
			return nil, addonapi.Errorf(addonapi.CodeTooLarge,
				"the add-on's images would take %d bytes of its %d with this one (%d, its pixels at four bytes each); drop some first (image.drop)",
				used+cost, addonapi.MaxImageBudget, cost)
		}
		st.gen++
		st.put[p.Name] = &keptImage{data: p.Data, cost: cost, rev: st.gen, used: s.Now()}
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

// Image is the bytes of the image an image block of the add-on's shows, and a key that names them, which
// is another key whenever the bytes are others: the display decodes and scales them off its draw path,
// and keeps what it made under the key. false is an image there is none of yet: a name not put, or an
// address not fetched yet, whose fetch this starts (once a minute at most, when it fails), and the
// display is told when it has come (PageChanged, CardChanged). Inline bytes (a block's Data) are the
// block's own.
func (s *Server) Image(id string, b *addonapi.Block) ([]byte, string, bool) {
	if b == nil || b.Type != addonapi.BlockImage {
		return nil, "", false
	}
	if len(b.Data) > 0 {
		h := fnv.New64a()
		h.Write(b.Data)
		return b.Data, "d:" + strconv.FormatUint(h.Sum64(), 16), true
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
		return k.data, "n:" + strconv.FormatUint(k.rev, 10) + ":" + b.Name, true
	case b.URL != "":
		if k := st.fetched[b.URL]; k != nil {
			k.used = now
			return k.data, "u:" + strconv.FormatUint(k.rev, 10) + ":" + b.URL, true
		}
		s.fetchLocked(a, b.URL, now)
	}
	return nil, "", false
}

// fetchLocked starts fetching an image's address for the add-on, unless it is on its way, failed
// within imageRetry, or as many as fetchesAtOnce are on their way already (it is asked for again when
// one of them comes, as the display lays the page out again). s.mu is held.
func (s *Server) fetchLocked(a *addon, u string, now time.Time) {
	st := &a.images
	if s.FetchImage == nil || st.fetching[u] || len(st.fetching) >= fetchesAtOnce {
		return
	}
	if at, ok := st.failed[u]; ok && now.Sub(at) < imageRetry {
		return
	}
	// Checked again here, as the scene was when it was set: the fetcher's dialer is what holds a name
	// to the LAN, but an address that is a number is refused before anything is dialed.
	if addonapi.ImageURL(u) != nil {
		st.failed[u] = now
		return
	}
	st.fetching[u] = true
	epoch := st.epoch
	ctx := a.ctx
	go s.fetchImage(ctx, a, u, epoch)
}

// fetchImage fetches one address and keeps what came, when it is an image within the limits and there
// is room for it.
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
	if err == nil && !st.makeRoom(cost, u) {
		err = addonapi.Errorf(addonapi.CodeTooLarge, "no room among the add-on's images")
	}
	if err != nil {
		st.failed[u] = s.Now()
		s.mu.Unlock()
		s.warn(a.grant.ID, "image", "addonapi: an add-on's image could not be fetched", "err", err)
		return
	}
	delete(st.failed, u)
	st.gen++
	st.fetched[u] = &keptImage{data: b, cost: cost, rev: st.gen, used: s.Now()}
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
