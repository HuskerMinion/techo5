package wake

import (
	"context"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/layout"

	"github.com/HuskerMinion/techo5/echod/internal/lib/hook"
)

// Library is the models a device has and the ones Home Assistant is offering it.
//
// Everything offered can be advertised without being downloaded: the published collections run to
// hundreds of words, and a device does not need the ones nobody chose. A model arrives when it is
// selected, which is the one moment somebody is waiting for it.
//
// The two halves are kept apart because they change for different reasons. Ours changes when a
// selection downloads something or a purge deletes it. Theirs changes only when Home Assistant says
// so, and has to be remembered: the offer carries the URL and the selection that acts on it carries
// only the id, so they arrive in separate messages and this is where they meet.
type Library struct {
	dir string

	// Changed fires after the list of models is read again (Reload): a word added, removed or fetched.
	// Listeners must not block.
	Changed hook.Hook[struct{}]

	// ours is cached because reading it parses every model to work out which engine runs it.
	muOurs sync.Mutex
	ours   []Model

	muOffers sync.Mutex
	offers   map[string]esphome.ExternalWakeWord

	// muOwn keeps two models sent from the setup page at once from both counting the same room, and a
	// removal from deleting a model a selection is about to load (own.go).
	muOwn sync.Mutex
	// selecting is the ids the latest selection asked for (Ensure), then the ones it loaded (Loaded).
	// Guarded by muOwn.
	selecting []string

	// failed is the hash each id last failed to download as, under muOffers. A model that cannot be
	// fetched is not tried again on every connect, only once Home Assistant offers a different one.
	failed map[string]string
}

// adoptTimeout bounds a download, since it happens while Home Assistant waits for a selection to be
// confirmed.
const adoptTimeout = 20 * time.Second

// There is one set of models on one disk, so there is one library. Everything that asks what the device
// can hear — the answer to Home Assistant, detection, the cache diagnostics — has to agree, and passing
// an instance between them is how they end up not agreeing.
var (
	once sync.Once
	lib  *Library
)

// Lib is the device's library, built on first use.
func Lib() *Library {
	once.Do(func() { lib = NewLibrary(layout.ModelDir) })
	return lib
}

// NewLibrary is for a library somewhere other than the device's own directory, which in practice means
// a test.
func NewLibrary(dir string) *Library {
	l := &Library{dir: dir, offers: map[string]esphome.ExternalWakeWord{}, failed: map[string]string{}}
	sweepUploads(dir)
	l.Reload()
	return l
}

// Dir is where models are kept.
func (l *Library) Dir() string { return l.dir }

// Ours is what the device has.
func (l *Library) Ours() []Model {
	l.muOurs.Lock()
	defer l.muOurs.Unlock()
	return l.ours
}

// Reload re-reads the directory, and is called wherever what is on disk changes.
func (l *Library) Reload() {
	models, err := Installed(l.dir)
	if err != nil {
		slog.Error("listing wake words failed", "dir", l.dir, "err", err)
		return
	}

	l.muOurs.Lock()
	l.ours = models
	l.muOurs.Unlock()
	l.Changed.Emit(struct{}{})
}

// Offered records what Home Assistant is hosting, replacing what it offered before.
func (l *Library) Offered(offers []esphome.ExternalWakeWord) {
	next := make(map[string]esphome.ExternalWakeWord, len(offers))
	for _, o := range offers {
		next[o.ID] = o
	}

	l.muOffers.Lock()
	defer l.muOffers.Unlock()
	l.offers = next
}

// Advertise is everything the device can offer: what it has, plus what Home Assistant is hosting. It
// also reports how many offers were left out.
//
// A phrase appears once. Home Assistant keys its wake word selects by phrase rather than by id, so two
// entries saying "Glados" collapse to whichever came last, and if the survivor is not the one that is
// active the select shows nothing. Ours are laid down first, so a model already on disk keeps the phrase
// and the offer that repeats it is the one dropped — the device prefers what it can load without a
// download, whichever engine that turns out to be.
//
// Being left out only affects what Home Assistant displays. Every offer keeps its URL in the map above,
// because Home Assistant selects by id and can ask for one it was never shown.
func (l *Library) Advertise() ([]esphome.WakeWord, int) {
	ours := l.Ours()

	words := make([]esphome.WakeWord, 0, len(ours))
	taken := make(map[string]bool, len(ours))
	for _, m := range ours {
		words = append(words, esphome.WakeWord{ID: m.ID, Phrase: m.Phrase, TrainedLanguages: m.Languages})
		taken[m.Phrase] = true
	}

	var shadowed int
	for _, o := range l.sorted() {
		if taken[o.Phrase] {
			shadowed++
			continue
		}
		taken[o.Phrase] = true
		words = append(words, esphome.WakeWord{ID: o.ID, Phrase: o.Phrase, TrainedLanguages: o.TrainedLanguages})
	}
	return words, shadowed
}

// sorted is what Home Assistant offered, in id order, so which of two entries sharing a phrase gets
// advertised does not change from one request to the next.
func (l *Library) sorted() []esphome.ExternalWakeWord {
	l.muOffers.Lock()
	defer l.muOffers.Unlock()

	out := make([]esphome.ExternalWakeWord, 0, len(l.offers))
	for _, o := range l.offers {
		out = append(out, o)
	}
	slices.SortFunc(out, func(a, b esphome.ExternalWakeWord) int { return strings.Compare(a.ID, b.ID) })
	return out
}

// Purge deletes the models no slot is listening for and reports how many went and what that freed.
func (l *Library) Purge(inUse []string) (int, int64) {
	gone, freed := Purge(l.dir, inUse)
	if gone > 0 {
		l.Reload()
	}
	return gone, freed
}

// Ensure downloads any of these that are offered and not already here, and reports everything the
// device has afterwards, which is what a selection is resolved against.
func (l *Library) Ensure(ids []string) []Model {
	// Said before anything is resolved: a removal from the setup page after this sees the selection,
	// and one before it has already gone from what the selection finds.
	l.muOwn.Lock()
	l.selecting = slices.Clone(ids)
	l.muOwn.Unlock()

	var fetched bool
	for _, id := range ids {
		if id == "" {
			continue
		}
		o := l.offer(id)
		if o.URL == "" || Have(l.dir, o) || l.uploaded(id) {
			continue
		}
		l.fetch(o)
		fetched = true
	}

	if fetched {
		l.Reload()
	}
	return l.Ours()
}

// Stale is the ids a slot is running from an old download: Home Assistant now offers a different model
// under the same id. A selection is the only thing that downloads, and Home Assistant sends none when
// its files change, so without this a new model sat unused until somebody chose another word and back.
func (l *Library) Stale() []string {
	l.muOwn.Lock()
	running := slices.Clone(l.selecting)
	l.muOwn.Unlock()

	var stale []string
	for _, id := range running {
		o := l.offer(id)
		if id == "" || o.URL == "" {
			continue
		}
		// One that is not here yet is the selection's to fetch, one sent from the setup page stays, and
		// one that already failed as this hash waits for a new one.
		if m, ok := Find(l.Ours(), id); !ok || m.Uploaded || Have(l.dir, o) || l.failedAs(id) == o.Hash {
			continue
		}
		stale = append(stale, id)
	}
	return stale
}

// Refetch downloads these again and reports the ones that now match what is offered. A download that
// fails leaves the old model where it was.
func (l *Library) Refetch(ids []string) []string {
	var fresh []string
	for _, id := range ids {
		o := l.offer(id)
		if o.URL == "" {
			continue
		}
		l.fetch(o)
		if Have(l.dir, o) {
			fresh = append(fresh, id)
		}
	}
	if len(fresh) > 0 {
		l.Reload()
	}
	return fresh
}

func (l *Library) failedAs(id string) string {
	l.muOffers.Lock()
	defer l.muOffers.Unlock()
	return l.failed[id]
}

// Loaded is the selection over: ids is what the engine took of what Ensure was asked for. A model it
// refused is in no slot, so it is free to be removed or replaced from the setup page again; it used to
// count as in use until the next selection.
func (l *Library) Loaded(ids []string) {
	l.muOwn.Lock()
	l.selecting = slices.Clone(ids)
	l.muOwn.Unlock()
}

// uploaded is whether the model with this id was sent from the setup page. Home Assistant offering one
// under the same id does not get to replace it: the owner sent it, and there is no getting it back.
func (l *Library) uploaded(id string) bool {
	m, ok := Find(l.Ours(), id)
	return ok && m.Uploaded
}

func (l *Library) offer(id string) esphome.ExternalWakeWord {
	l.muOffers.Lock()
	defer l.muOffers.Unlock()
	return l.offers[id]
}

func (l *Library) fetch(o esphome.ExternalWakeWord) {
	ctx, cancel := context.WithTimeout(context.Background(), adoptTimeout)
	defer cancel()

	m, err := Adopt(ctx, l.dir, o)
	l.muOffers.Lock()
	if err != nil {
		l.failed[o.ID] = o.Hash
	} else {
		delete(l.failed, o.ID)
	}
	l.muOffers.Unlock()
	if err != nil {
		slog.Error("adopting a wake word failed", "id", o.ID, "err", err)
		return
	}
	slog.Info("wake word adopted", "id", m.ID, "phrase", m.Phrase, "engine", m.Kind)
}
