//go:build !dot

package dashboard

import (
	"context"
	"image"
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// pictureSource is a dashboard of nothing but pictures, a block each, named by their keys.
type pictureSource struct{ want []wanted }

func (p pictureSource) load(context.Context, *hass.Live) (needs, error) {
	return needs{pictures: p.want}, nil
}

func (p pictureSource) sections(l look) []Section {
	var s Section
	for _, w := range p.want {
		sh := l.pictures[w.key()]
		s.Blocks = append(s.Blocks, Block{Picture: &Picture{Name: w.key(), Image: sh.img}})
	}
	return []Section{s}
}

// drawnAt is a Feature showing the drawn dashboard at path, its sessions run by hand: nothing
// connects to a Home Assistant.
func drawnAt(t *testing.T, path string) *Feature {
	t.Helper()
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if err := config.Set().Dashboard().Path(path); err != nil {
		t.Fatal(err)
	}
	was := runDrawn
	runDrawn = func(*session) {}
	t.Cleanup(func() { runDrawn = was })
	f := pickerFeature()
	f.Drawn(screenWidth)
	return f
}

// loaded is the session up now as once leaves it once the dashboard is read: a page of the pictures
// in want, from what it has of them.
func loaded(f *Feature, want ...wanted) *session {
	f.mu.Lock()
	s := f.drawn
	f.mu.Unlock()
	s.mu.Lock()
	s.src = pictureSource{want}
	s.seed(want)
	s.mu.Unlock()
	s.publish()
	return s
}

// arrived is w's picture fetched, and the page rebuilt with it now rather than within publishEvery.
func arrived(s *session, w wanted) {
	s.got(w.key(), small)
	s.publish()
}

// showing is the name of the first picture on the page drawn now, and whether it is there yet.
func showing(f *Feature) (string, bool) {
	d := f.Drawn(screenWidth)
	if len(d.Sections) == 0 || len(d.Sections[0].Blocks) == 0 || d.Sections[0].Blocks[0].Picture == nil {
		return "", false
	}
	p := d.Sections[0].Blocks[0].Picture
	return p.Name, p.Image != nil
}

var (
	room  = wanted{url: "/local/room.png", wide: true}
	popup = wanted{url: "/local/popup.png", wide: true}
	small = shot{img: image.NewRGBA(image.Rect(0, 0, 4, 4)), size: image.Pt(4, 4)}
)

// A tap that goes to another view keeps the page it was tapped on up while the view is read and its
// picture fetched, and shows the view once the picture is there.
func TestANavigateKeepsThePageUntilItsPictureIsReady(t *testing.T) {
	f := drawnAt(t, "home/room")
	if name, _ := showing(f); name != "" {
		t.Fatalf("first open held a page: %q", name)
	}
	s := loaded(f, room)
	arrived(s, room)
	if name, there := showing(f); name != room.key() || !there {
		t.Fatalf("the room: %q, %v", name, there)
	}

	f.Tap(Action{View: "/home/popup"})
	if name, _ := showing(f); name != room.key() {
		t.Errorf("while the view is read: %q, want the room", name)
	}
	s = loaded(f, popup)
	if name, _ := showing(f); name != room.key() {
		t.Errorf("while its picture loads: %q, want the room", name)
	}
	arrived(s, popup)
	if name, there := showing(f); name != popup.key() || !there {
		t.Errorf("with its picture: %q, %v", name, there)
	}
}

// A held page is let go after holdFor whatever the view is doing, and at once when the view says
// what went wrong.
func TestAHeldPageIsLetGo(t *testing.T) {
	f := drawnAt(t, "home/room")
	s := loaded(f, room)
	arrived(s, room)

	f.Tap(Action{View: "home/popup"})
	f.Drawn(screenWidth)
	s = loaded(f, popup)
	s.mu.Lock()
	until := time.Until(s.holdUntil)
	s.mu.Unlock()
	if until <= 0 || until > holdFor {
		t.Errorf("held for %v, want up to %v", until, holdFor)
	}
	if name, _ := showing(f); name != room.key() {
		t.Fatalf("held: %q", name)
	}
	s.mu.Lock()
	s.holdUntil = time.Now().Add(-time.Millisecond)
	s.mu.Unlock()
	if name, there := showing(f); name != popup.key() || there {
		t.Errorf("past the limit: %q, %v, want the view, its picture loading", name, there)
	}

	f.Tap(Action{View: "home/room"})
	f.Drawn(screenWidth)
	f.mu.Lock()
	s = f.drawn
	f.mu.Unlock()
	s.problem("Can't reach Home Assistant.")
	if d := f.Drawn(screenWidth); d.Problem == "" {
		t.Errorf("a view that failed was held: %+v", d)
	}
}

// A view opened again draws its picture at once, from the pictures kept between views.
func TestARevisitedViewDrawsAtOnce(t *testing.T) {
	f := drawnAt(t, "home/room")
	arrived(loaded(f, room), room)
	f.Tap(Action{View: "home/popup"})
	f.Drawn(screenWidth)
	arrived(loaded(f, popup), popup)

	f.Tap(Action{View: "home/room"})
	f.Drawn(screenWidth)
	loaded(f, room)
	if name, there := showing(f); name != room.key() || !there {
		t.Errorf("back in the room: %q, %v, want its picture at once", name, there)
	}
}

// Another dashboard chosen, rather than a tap, holds nothing: no page, no pictures, not even one
// still on its way for the last dashboard.
func TestNothingIsHeldAcrossADashboard(t *testing.T) {
	f := drawnAt(t, "home/room")
	old := loaded(f, room)
	arrived(old, room)

	if err := config.Set().Dashboard().Path("other/view"); err != nil {
		t.Fatal(err)
	}
	if name, _ := showing(f); name != "" {
		t.Errorf("another dashboard held the last: %q", name)
	}
	arrived(old, room) // late, from the session being closed
	if _, ok := f.recent.get(room.key()); ok {
		t.Error("the last dashboard's picture is still kept")
	}
	loaded(f, room)
	if _, there := showing(f); there {
		t.Error("the new dashboard drew the last one's picture")
	}

	f.CloseDrawn()
	f.recent.put(f.recent.generation(), room.key(), small)
	f.CloseDrawn()
	if _, ok := f.recent.get(room.key()); ok {
		t.Error("a closed page kept its pictures")
	}
}

// The pictures kept between views are few: the newest recentMost, within recentBytes, and none that
// would not load.
func TestThePicturesKeptAreFew(t *testing.T) {
	var r recent
	page := shot{img: image.NewRGBA(image.Rect(0, 0, 1280, 800))} // a whole page, 4 MB
	for i := range 10 {
		r.put(0, string(rune('a'+i)), page)
	}
	if len(r.shots) != recentMost || r.bytes() > recentBytes {
		t.Errorf("kept %d, %d bytes", len(r.shots), r.bytes())
	}
	if _, ok := r.shots["a"]; ok {
		t.Error("the oldest is still kept")
	}
	if _, ok := r.shots["j"]; !ok {
		t.Error("the newest is not kept")
	}
	r.get("e") // used again: kept over the next oldest
	r.put(0, "k", page)
	if _, ok := r.shots["e"]; !ok {
		t.Error("one just used was let go")
	}

	r.put(0, "huge", shot{img: image.NewRGBA(image.Rect(0, 0, 1280, 5200))})
	if _, ok := r.shots["huge"]; ok || r.bytes() > recentBytes {
		t.Errorf("a picture larger than the whole allowance was kept: %d bytes", r.bytes())
	}
	r.put(0, "broken", shot{failed: true})
	if _, ok := r.shots["broken"]; ok {
		t.Error("a picture that would not load was kept")
	}
}

// A picture is counted at what its decoded image holds: a 16-bit one at 8 bytes a pixel.
func TestShotBytesByKind(t *testing.T) {
	r := image.Rect(0, 0, 10, 10)
	for _, c := range []struct {
		img  image.Image
		want int64
	}{{image.NewRGBA(r), 400}, {image.NewNRGBA64(r), 800}, {image.NewGray(r), 100}, {image.NewYCbCr(r, image.YCbCrSubsampleRatio420), 400}} {
		if got := shotBytes(shot{img: c.img}); got != c.want {
			t.Errorf("%T: %d bytes, want %d", c.img, got, c.want)
		}
	}
}
