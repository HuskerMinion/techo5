package addons

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/feature/addons/catalog"
	"github.com/HuskerMinion/techo5/echod/internal/update"
)

func (r *rig) askedFor() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	asked := r.asked
	r.asked = nil
	return asked
}

// The format 2 catalog is asked for first and taken when it is there, the format 1 one beside it not
// even fetched.
func TestTheFormat2CatalogIsPreferred(t *testing.T) {
	r := newRig(t)
	plain := r.version("techo5-plex", "0.1.0", nil)
	files := r.withFiles("techo5-plex", r.version("techo5-plex", "0.2.0", nil), map[string]string{"a.bin": "a"})
	r.serve(catalog.Catalog{Format: 1, Serial: 3, Addons: []catalog.Addon{entry("techo5-plex", plain)}})
	r.serve(catalog.Catalog{Format: 2, Serial: 3, Addons: []catalog.Addon{entry("techo5-plex", plain, files)}})
	if _, err := r.m.cat.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if asked := r.askedFor(); !slices.Equal(asked, []string{catalogV2URL}) {
		t.Errorf("asked for %v", asked)
	}
	c, _ := r.m.cat.get()
	if a, _ := c.Find("techo5-plex"); c.Format != 2 || len(a.Versions) != 2 {
		t.Errorf("believed format %d with %+v", c.Format, a.Versions)
	}
	if _, err := os.Stat(filepath.Join(r.m.root, "catalog-v2.json.sig")); err != nil {
		t.Errorf("the format 2 catalog is not kept: %v", err)
	}
}

// Until the format 2 catalog is published, the format 1 one is taken, and that is no failure for the
// card; anything else wrong with the format 2 one is, and the format 1 one is not taken in its place.
func TestTheFormat1CatalogIsTakenUntilFormat2IsPublished(t *testing.T) {
	r := newRig(t)
	v := r.version("techo5-plex", "0.1.0", nil)
	r.serve(catalog.Catalog{Format: 1, Serial: 3, Addons: []catalog.Addon{entry("techo5-plex", v)}})
	ctx := context.Background()
	if _, err := r.m.cat.refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if asked := r.askedFor(); !slices.Equal(asked, []string{catalogV2URL, catalogURL}) {
		t.Errorf("asked for %v", asked)
	}
	if c, ok := r.m.cat.get(); !ok || c.Format != 1 || c.Serial != 3 {
		t.Errorf("believed %+v", c)
	}
	if _, problem, _, _ := r.m.CatalogState(); problem != "" {
		t.Errorf("the card is told %q", problem)
	}

	// A format 2 catalog with a bad signature is a failure, and the format 1 one is not fetched for it.
	r.serve(catalog.Catalog{Format: 2, Serial: 4, Addons: []catalog.Addon{entry("techo5-plex", v)}})
	r.mu.Lock()
	r.sigV2 = r.sig
	r.mu.Unlock()
	if _, err := r.m.cat.refresh(ctx); err == nil {
		t.Error("a format 2 catalog signed as format 1 was taken")
	}
	if asked := r.askedFor(); !slices.Equal(asked, []string{catalogV2URL}) {
		t.Errorf("asked for %v after a bad format 2 catalog", asked)
	}
	if _, problem, _, _ := r.m.CatalogState(); problem == "" {
		t.Error("the card is not told the format 2 catalog failed")
	}
}

// One serial runs across both formats: once a format 1 catalog is believed, a format 2 one below it is
// older, and one at it is taken; and the kept copy is the format last taken, checked under its own
// domain when read back.
func TestTheSerialHoldsAcrossFormats(t *testing.T) {
	r := newRig(t)
	v := r.version("techo5-plex", "0.1.0", nil)
	ctx := context.Background()
	r.serve(catalog.Catalog{Format: 1, Serial: 5, Addons: []catalog.Addon{entry("techo5-plex", v)}})
	if _, err := r.m.cat.refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(r.m.root, "catalog.json")); err != nil {
		t.Fatalf("the format 1 catalog is not kept: %v", err)
	}

	r.serve(catalog.Catalog{Format: 2, Serial: 4, Addons: []catalog.Addon{entry("techo5-plex", v)}})
	if _, err := r.m.cat.refresh(ctx); !errors.Is(err, errOlder) {
		t.Errorf("an older format 2 catalog: %v", err)
	}
	if c, _ := r.m.cat.get(); c.Format != 1 || c.Serial != 5 {
		t.Errorf("believed format %d serial %d", c.Format, c.Serial)
	}

	r.serve(catalog.Catalog{Format: 2, Serial: 5, Addons: []catalog.Addon{entry("techo5-plex", v)}})
	if _, err := r.m.cat.refresh(ctx); err != nil {
		t.Fatalf("a format 2 catalog of the same serial: %v", err)
	}
	if c, _ := r.m.cat.get(); c.Format != 2 || c.Serial != 5 {
		t.Errorf("believed format %d serial %d", c.Format, c.Serial)
	}
	if _, err := os.Stat(filepath.Join(r.m.root, "catalog.json")); !os.IsNotExist(err) {
		t.Errorf("the format 1 copy kept before is still there: %v", err)
	}
	fresh := &catalogStore{dir: r.m.root, clock: r.clock}
	fresh.load()
	if c, ok := fresh.get(); !ok || c.Format != 2 || c.Serial != 5 {
		t.Errorf("read back as format %d serial %d (%v)", c.Format, c.Serial, ok)
	}

	// The format 2 file and its signature put in the format 1 copy's place do not verify there, each
	// being checked under its own domain; and with no format 2 copy, after one was believed, a format 1
	// copy is not believed at all.
	r.mu.Lock()
	v2Body, v2Sig, v1Body, v1Sig := r.servedV2, r.sigV2, r.served, r.sig
	r.mu.Unlock()
	_ = os.Remove(filepath.Join(r.m.root, "catalog-v2.json"))
	_ = os.WriteFile(filepath.Join(r.m.root, "catalog.json"), v2Body, 0o644)
	_ = os.WriteFile(filepath.Join(r.m.root, "catalog.json.sig"), []byte(v2Sig), 0o644)
	swapped := &catalogStore{dir: r.m.root, clock: r.clock}
	swapped.load()
	if _, ok := swapped.get(); ok {
		t.Error("a format 2 catalog kept as format 1 was believed")
	}
	_ = os.WriteFile(filepath.Join(r.m.root, "catalog.json"), v1Body, 0o644)
	_ = os.WriteFile(filepath.Join(r.m.root, "catalog.json.sig"), []byte(v1Sig), 0o644)
	stale := &catalogStore{dir: r.m.root, clock: r.clock}
	stale.load()
	if _, ok := stale.get(); ok {
		t.Error("a kept format 1 catalog was believed after a format 2 one had been")
	}
}

// Once a format 2 catalog has been believed, it is never given up for the format 1 one: not when
// catalog-v2.json goes missing, nor after a restart. The format 1 file lists less (no version that
// requires anything, nor its withdrawal), and at the same serial it would replace what was shown.
func TestNoFallBackToFormat1AfterFormat2(t *testing.T) {
	r := newRig(t)
	plain := r.version("techo5-plex", "0.1.0", nil)
	files := r.withFiles("techo5-plex", r.version("techo5-plex", "0.2.0", nil), map[string]string{"a.bin": "a"})
	r.serve(catalog.Catalog{Format: 1, Serial: 3, Addons: []catalog.Addon{entry("techo5-plex", plain)}})
	r.serve(catalog.Catalog{Format: 2, Serial: 3, Addons: []catalog.Addon{entry("techo5-plex", plain, files)}})
	ctx := context.Background()
	if _, err := r.m.cat.refresh(ctx); err != nil {
		t.Fatal(err)
	}
	r.unpublishV2()
	r.askedFor()
	for _, s := range []*catalogStore{r.m.cat, func() *catalogStore {
		fresh := &catalogStore{dir: r.m.root, clock: r.clock}
		fresh.load()
		return fresh
	}()} {
		if _, err := s.refresh(ctx); err == nil || errors.Is(err, update.ErrNotFound) {
			t.Errorf("a missing format 2 catalog after one was believed: %v", err)
		}
		if asked := r.askedFor(); !slices.Equal(asked, []string{catalogV2URL}) {
			t.Errorf("asked for %v", asked)
		}
		if c, ok := s.get(); !ok || c.Format != 2 {
			t.Errorf("believed format %d (%v)", c.Format, ok)
		}
		if _, err := os.Stat(filepath.Join(r.m.root, "catalog-v2.json")); err != nil {
			t.Errorf("the kept format 2 copy is gone: %v", err)
		}
	}
}

// An add-on whose every version needs a newer TECHO5 is listed, grayed, as needing one; one with a
// version this build reads offers that, the newer one left out.
func TestAnAddonForANewerTECHO5IsListedAsNeedingOne(t *testing.T) {
	r := newRig(t)
	future := r.version("techo5-next", "1.0.0", nil)
	future.Requires = []string{"sandbox"}
	plain := r.version("techo5-plex", "0.1.0", nil)
	newer := r.version("techo5-plex", "0.2.0", nil)
	newer.Requires = []string{"sandbox"}
	r.serve(catalog.Catalog{Format: 2, Serial: 1, Addons: []catalog.Addon{entry("techo5-next", future), entry("techo5-plex", plain, newer)}})
	if _, err := r.m.cat.refresh(context.Background()); err != nil {
		t.Fatalf("a catalog with versions for a newer TECHO5 was refused: %v", err)
	}
	var next, plex Listing
	for _, l := range r.m.Available() {
		switch l.Addon.ID {
		case "techo5-next":
			next = l
		case "techo5-plex":
			plex = l
		}
	}
	if !next.NeedsNewer || next.Runs || next.Why != "needs a newer TECHO5" || next.Version.Version != "1.0.0" {
		t.Errorf("the add-on for a newer TECHO5: %+v", next)
	}
	if plex.NeedsNewer || !plex.Runs || plex.Version.Version != "0.1.0" {
		t.Errorf("the add-on with a version read: runs %v, version %s, needs newer %v", plex.Runs, plex.Version.Version, plex.NeedsNewer)
	}
}
