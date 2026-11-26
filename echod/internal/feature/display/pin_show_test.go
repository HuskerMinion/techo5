//go:build !dot && !spot

package display

import (
	"image"
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/security"
)

// A finger on a locked row or a locked list's choice reaches the PIN pad, not the setting: the gate is
// wired into the Show's taps, not only into gate itself.
func TestTheShowsTapsGoThroughTheLock(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if err := security.Get().SetPIN("2468"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pinPress("cancel")
		_ = security.Get().SetPIN("")
	})
	d := &Display{r: testRenderer(), poke: make(chan struct{}, 1), sheet: true}
	d.r.zones = []zone{{r: image.Rect(0, 0, 100, 100), kind: zoneRow, id: "ssh", label: "SSH"}}
	d.nextTap(50, 50)
	if p := pinNow(time.Now()); !p.open || p.what != "SSH" || config.Get().Security.SSH {
		t.Fatalf("a tap on SSH: pad up %v for %q, SSH %v; want the pad for SSH and no change", p.open, p.what, config.Get().Security.SSH)
	}
	pinPress("cancel")

	d.picker = "screenlang"
	d.r.zones = []zone{{r: image.Rect(0, 0, 100, 100), kind: zoneOption, opt: 1, label: "Screen language"}}
	before := config.Get().Screen.Language
	d.nextTap(50, 50)
	if p := pinNow(time.Now()); !p.open || p.what != "Screen language" || config.Get().Screen.Language != before {
		t.Errorf("a choice in the locked Screen language list: pad up %v for %q, language %q (was %q)", p.open, p.what, config.Get().Screen.Language, before)
	}
}

// A long message or title on the pad (the German "Konnte nicht gespeichert werden" on a Show 5) stops
// short of the keys instead of running under them.
func TestThePadsWordsStayLeftOfTheKeys(t *testing.T) {
	long := "Konnte nicht gespeichert werden, Konnte nicht gespeichert werden"
	frame := func(p pinView) *image.RGBA {
		r := testRenderer()
		r.pinPage(p)
		return r.dst
	}
	plain := frame(pinView{open: true})
	r := testRenderer()
	edge := r.pinKeyRects()[0].Min.X - r.s(24)
	for _, p := range []pinView{{open: true, msg: long}, {open: true, title: long}} {
		got := frame(p)
		for y := 0; y < got.Bounds().Dy(); y++ {
			for x := edge; x < got.Bounds().Dx(); x++ {
				if got.RGBAAt(x, y) != plain.RGBAAt(x, y) {
					t.Fatalf("msg %q, title %q: drawn at (%d,%d), right of %d where the keys begin", p.msg, p.title, x, y, edge)
				}
			}
		}
	}
}
