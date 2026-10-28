//go:build !dot && !spot

package display

import (
	"fmt"
	"image"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/feature/phone"
	"github.com/HuskerMinion/techo5/echod/internal/feature/talkback"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

func TestDrawerTranslationsKeepNamesAndActions(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	t.Cleanup(func() { _ = config.Set().Screen().Language("") })
	for _, lang := range []string{"en", "ja", "es", "pt", "en", "ja"} {
		_ = config.Set().Screen().Language(lang)
		wantTabs := []string{"Cameras", "Radio", "Announce", "Call"}
		if lang == "ja" {
			wantTabs = []string{"カメラ", "ラジオ", "アナウンス", "通話"}
		}
		for i, label := range drawerTabLabels() {
			if label != wantTabs[i] {
				t.Fatalf("%s: tab %d = %q", lang, i, label)
			}
		}
		if !reflect.DeepEqual(drawerTabs, []string{"Cameras", "Radio", "Announce", "Call"}) {
			t.Fatal("canonical tab names changed")
		}
		s := scene{drawerTab: drawerRadio, radio: home.Radio{Configured: true, Source: config.RadioLocal, Sources: 2,
			Playing: true, Grouped: true, Now: "Radio", Stations: []string{"Radio", "Cameras"}}}
		before := radioList(s.radio)
		rows, _ := drawerRows(s)
		wantStop, wantPlaying, wantGroup := "Stop the radio", "Playing", "all rooms in the group"
		if lang == "ja" {
			wantStop, wantPlaying, wantGroup = "ラジオを停止", "再生中", "グループ内のすべての部屋"
		}
		if rows[0].id != "radiosource" || rows[1].id != "st:0" || rows[1].label != wantStop || rows[1].sub != wantGroup || rows[2].label != "Radio" || rows[2].button != wantPlaying || rows[3].label != "Cameras" {
			t.Fatalf("%s: radio rows %+v", lang, rows)
		}
		if !reflect.DeepEqual(before, radioList(s.radio)) || before[0] != "■ Stop" {
			t.Fatal("translation changed radio commands or station order")
		}
		s.drawerTab = drawerCameras
		s.cameras = []config.Camera{{Entity: "camera.one", Name: "Radio"}, {Entity: "camera.two", Name: "Front door"}}
		rows, _ = drawerRows(s)
		if rows[0].id != "grid:0" || rows[1].id != "cam:0" || rows[1].label != "Radio" || rows[2].label != "Front door" {
			t.Fatal("camera identity or order changed", rows)
		}
		if lang == "ja" && (rows[0].label != "すべてのカメラ" || rows[0].button != "表示" || rows[1].sub != "「radioを見せて」と話しかけても表示") {
			t.Fatal(rows)
		}
		s.drawerTab = drawerCall
		s.callees = []phone.Callee{{Name: "Call", Device: true}, {Name: "Announce"}}
		rows, _ = drawerRows(s)
		if rows[0].id != "call:0" || rows[0].label != "Call" || rows[1].label != "Announce" {
			t.Fatal("callee names or actions changed", rows)
		}
		if lang == "ja" && (rows[0].button != "通話" || rows[0].sub != "家の中の端末間通話" || rows[1].sub != "電話") {
			t.Fatal(rows)
		}
	}
}

func TestJapaneseDrawerEmptyAndProgressStates(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	t.Cleanup(func() { _ = config.Set().Screen().Language("") })
	_ = config.Set().Screen().Language("ja")
	for _, tab := range []int{drawerCameras, drawerRadio, drawerAnnounce, drawerCall} {
		rows, note := drawerRows(scene{drawerTab: tab})
		if len(rows) != 0 || note == "" || !strings.ContainsAny(note, "ありません合言葉") {
			t.Fatalf("tab %d: untranslated empty state %q", tab, note)
		}
	}
	for _, tc := range []struct {
		radio home.Radio
		want  string
	}{
		{home.Radio{Configured: true, Loading: true}, "Home Assistantから放送局を取得中…"},
		{home.Radio{Configured: true, Problem: "server detail"}, "放送局を取得できませんでした"},
		{home.Radio{Configured: true, Source: config.RadioLocal}, "100km以内に放送局がありません"},
		{home.Radio{Configured: true}, "この一覧には放送局がありません"},
	} {
		rows, _ := drawerRows(scene{drawerTab: drawerRadio, radio: tc.radio})
		if len(rows) != 2 || rows[1].label != tc.want {
			t.Fatalf("%+v: %+v", tc.radio, rows)
		}
		if tc.radio.Problem != "" && rows[1].sub != tc.radio.Problem {
			t.Fatal("server diagnostics were rewritten")
		}
	}
	for _, n := range []int{0, 1, 3} {
		rows, _ := announceRows(scene{announceReady: true, announcePeers: n})
		want := "ほかの端末が見つかりません"
		if n > 0 {
			want = fmt.Sprintf("ほかの%d台に届きます", n)
		}
		if rows[0].id != "announce:0" || rows[0].label != "ほかの部屋に伝える" || rows[0].sub != want || rows[0].button != "話す" {
			t.Fatal(rows)
		}
	}
	rows, _ := announceRows(scene{announceReady: true, announceRecording: true})
	if rows[0].label != "お話しください" || rows[0].value != "録音中" || rows[0].rowTap {
		t.Fatal(rows)
	}
	for _, source := range []string{config.RadioFavorites, config.RadioLocal, config.RadioPopular, config.RadioOwn} {
		if radioSourceText(source) == home.SourceLabel(source) {
			t.Fatalf("source %q not translated", source)
		}
	}
	if radioSourceText("Owner's list") != "Owner's list" {
		t.Fatal("unknown source renamed")
	}
}

func TestDrawerCameraControlsAndAnnouncementCounts(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	t.Cleanup(func() { _ = config.Set().Screen().Language("") })
	for _, lang := range []string{"en", "ja", "es", "pt"} {
		_ = config.Set().Screen().Language(lang)
		want := []string{"Talk", "Connecting…", "End talk  1:05", "Try again", "Mute", "Unmute", "1 other device", "3 other devices"}
		if lang == "ja" {
			want = []string{"話す", "接続中…", "会話を終了  1:05", "再試行", "消音", "消音解除", "ほかの1台", "ほかの3台"}
		}
		r := testRenderer()
		got := []string{
			talkLabel(talkback.State{}, "camera.one"),
			talkLabel(talkback.State{Entity: "camera.one", Phase: talkback.Opening}, "camera.one"),
			talkLabel(talkback.State{Entity: "camera.one", Phase: talkback.Talking, Left: 65 * time.Second}, "camera.one"),
			talkLabel(talkback.State{Entity: "camera.one", Error: "server detail"}, "camera.one"),
			r.cameraSoundLabel(scene{cameraSoundLive: true}), r.cameraSoundLabel(scene{}),
			announcementDevices(1), announcementDevices(3),
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: controls %v, want %v", lang, got, want)
		}
	}
}

// Model Japanese advances independently of fonts installed on the test host.
type drawerJapaneseFace struct {
	font.Face
	pixels int
}

func (f drawerJapaneseFace) GlyphAdvance(r rune) (fixed.Int26_6, bool) {
	if r < 128 {
		return fixed.I(f.pixels / 2), true
	}
	return fixed.I(f.pixels), true
}
func (drawerJapaneseFace) Kern(rune, rune) fixed.Int26_6 { return 0 }

func TestJapaneseDrawerLayoutFitsShow5(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	t.Cleanup(func() { _ = config.Set().Screen().Language("") })
	_ = config.Set().Screen().Language("ja")
	r := testRenderer()
	fc := r.faces()
	fc.nav, fc.navBold = drawerJapaneseFace{basicfont.Face7x13, 24}, drawerJapaneseFace{basicfont.Face7x13, 24}
	fc.label, fc.sub = drawerJapaneseFace{basicfont.Face7x13, 29}, drawerJapaneseFace{basicfont.Face7x13, 21}
	fc.button, fc.value = drawerJapaneseFace{basicfont.Face7x13, 23}, drawerJapaneseFace{basicfont.Face7x13, 25}
	r.fc = &fc
	for _, label := range drawerTabLabels() {
		if r.width(fc.navBold, label) > r.drawerSeg()/4-r.s(8) {
			t.Fatalf("tab %q overlaps", label)
		}
	}
	for _, tab := range []int{drawerCameras, drawerRadio, drawerAnnounce, drawerCall} {
		s := scene{drawerTab: tab, announceReady: true, announcePeers: 3,
			cameras: []config.Camera{{Entity: "camera.one", Name: "玄関"}, {Entity: "camera.two", Name: "庭"}},
			callees: []phone.Callee{{Name: "寝室", Device: true}},
			radio:   home.Radio{Configured: true, Playing: true, Now: "Station", Stations: []string{"Station"}}}
		r.drawer(s)
		panel := image.Rect(r.w-r.cardIn()-r.drawerW(), r.cardIn(), r.w-r.cardIn(), r.h-r.cardIn())
		rows, _ := drawerRows(s)
		for _, row := range rows {
			label, sub, end := r.rowWords(panel, row, fc.label)
			if label != row.label || sub != row.sub {
				t.Fatalf("tab %d: %s text is truncated: %q / %q", tab, row.id, label, sub)
			}
			if end+r.s(rowGap)+r.controlWidth(row) > panel.Max.X-r.s(26) {
				t.Fatalf("tab %d: %s text overlaps its control", tab, row.id)
			}
		}
		for _, z := range r.zones {
			if !z.r.In(r.dst.Rect) {
				t.Fatalf("tab %d offscreen target: %v", tab, z.r)
			}
		}
		// Long empty-state guidance must wrap within the drawer rather than run offscreen.
		_, note := drawerRows(scene{drawerTab: tab})
		lines := r.wrapLines(fc.value, note, r.drawerW()-2*r.rowIn())
		if r.cardIn()+r.headerH()+r.s(50)+r.s(36)*(len(lines)-1) > r.h-r.cardIn() {
			t.Fatalf("tab %d: guidance exceeds drawer height", tab)
		}
		for _, line := range lines {
			if r.width(fc.value, line) > r.drawerW()-2*r.rowIn() {
				t.Fatal("empty state exceeds drawer")
			}
		}
	}
}
