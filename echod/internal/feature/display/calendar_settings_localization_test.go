//go:build !dot && !spot

package display

import (
	"bytes"
	"image"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

func TestDisplayCalendarSettingsUseJapanesePresentationOnly(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	t.Cleanup(func() { _ = config.Set().Screen().Language("") })
	wasWide := panelWide.Swap(false)
	t.Cleanup(func() { panelWide.Store(wasWide) })
	if len(calendarLookRows()) != 0 {
		t.Fatal("calendar look rows appeared without any calendars")
	}
	if err := config.Set().Calendar().Sources([]string{"calendar.family"}); err != nil {
		t.Fatal(err)
	}
	wantJapanese := map[string][2]string{
		"opens_on":          {"最初に開く表示", "日付をタップしたときの表示"},
		"day_timeline":      {"日の時間軸表示", "その日の予定を時刻に沿って表示"},
		"coming_up_refined": {"予定を1行ずつ表示", "各日の天気も表示"},
		"month_details":     {"月表示の詳細", "時刻・アイコン・天気を表示"},
		"event_icons":       {"予定のアイコン", "誕生日・ごみ出し・旅行などの目印"},
		"countdowns":        {"開始までの時間", "今日の予定に「あと25分」などを表示"},
		"week_view":         {"週表示", "7日分の予定を時刻に沿って表示"},
		"next_up":           {"時計に次の予定", "時計の下部に今日の次の予定を表示"},
		"weather":           {"カレンダーの天気", "各日の天気予報を表示"},
	}
	for _, lang := range []string{"en", "ja", "de"} {
		if err := config.Set().Screen().Language(lang); err != nil {
			t.Fatal(err)
		}
		before := config.Get().Calendar
		rows := calendarLookRows()
		if len(rows) != len(lookSwitches)+1 {
			t.Fatal("look settings were lost")
		}
		heading := "Calendar"
		if lang == "ja" {
			heading = "カレンダー"
		}
		if rows[0].label != heading {
			t.Fatalf("%s: heading %q", lang, rows[0].label)
		}
		for i, s := range lookSwitches {
			row := rows[i+1]
			want := [2]string{s.label, s.sub}
			if lang == "ja" {
				var ok bool
				want, ok = wantJapanese[s.id]
				if !ok {
					t.Fatalf("new calendar setting %q needs Japanese text and a regression case", s.id)
				}
			}
			if row.id != lookRowPrefix+s.id || row.label != want[0] || row.sub != want[1] {
				t.Errorf("%s/%s: %+v", lang, s.id, row)
			}
		}
		for _, tc := range []struct {
			id            string
			raw, japanese []string
		}{
			{"cal_opens_on", []string{"Month", "Week", "Day", "Coming up"}, []string{"月表示", "週表示", "日表示", "今後の予定"}},
			{"cal_month_details", []string{"Full", "Simple"}, []string{"詳細", "簡易"}},
		} {
			p, ok := pickerFor(tc.id, sheetView{})
			want := tc.raw
			if lang == "ja" {
				want = tc.japanese
			}
			if !ok || !reflect.DeepEqual(p.opts, want) {
				t.Errorf("%s/%s: choices %v", lang, tc.id, p.opts)
			}
			rowID := tc.id
			for _, row := range rows {
				if row.id == rowID && row.value != p.opts[p.cur] {
					t.Errorf("row and picker disagree: %q / %q", row.value, p.opts[p.cur])
				}
			}
		}
		if !reflect.DeepEqual(before, config.Get().Calendar) {
			t.Fatal("drawing settings mutated the calendar configuration")
		}
		// The same metadata feeds HA entities and the web setup API. Keep both canonical.
		d := &Display{poke: make(chan struct{}, 1)}
		d.buildLookEntities()
		setupChoices := d.lookChoices()
		for i, s := range lookSwitches {
			if setupChoices[i].Label != s.label || setupChoices[i].Note != s.sub {
				t.Fatal("screen translation changed setup metadata")
			}
			switch entity := d.lookEnts[i].(type) {
			case *esphome.Select:
				if entity.ObjectID != "calendar_"+s.id || entity.Name != s.label || !reflect.DeepEqual(entity.Options, s.opts) {
					t.Fatal("screen translation changed HA select metadata")
				}
			case *esphome.Switch:
				if entity.ObjectID != "calendar_"+s.id || entity.Name != s.label {
					t.Fatal("screen translation changed HA switch metadata")
				}
			}
		}
	}
	if !reflect.DeepEqual(opensOpts, []string{"Month", "Week", "Day", "Coming up"}) || !reflect.DeepEqual(monthDetailOpts, []string{"Full", "Simple"}) {
		t.Fatal("canonical option tables were modified")
	}
	_ = config.Set().Screen().Language("ja")
	d := &Display{poke: make(chan struct{}, 1)}
	for i, value := range opensVals {
		if !d.lookChoose("cal_opens_on", i) || config.Get().Calendar.Opens != value {
			t.Fatalf("choice %d no longer saves %q", i, value)
		}
		p, _ := lookPicker("cal_opens_on")
		if p.cur != i {
			t.Fatal("selected index changed with translation")
		}
	}
}

// Full-width advances model Japanese at the actual settings font sizes without
// relying on a Japanese font being installed on the machine running the tests.
type calendarSettingFace struct {
	font.Face
	pixels int
}

func (f calendarSettingFace) GlyphAdvance(rune) (fixed.Int26_6, bool) { return fixed.I(f.pixels), true }
func (calendarSettingFace) Kern(rune, rune) fixed.Int26_6             { return 0 }

func TestDisplayCalendarJapaneseTextFitsShow5(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	t.Cleanup(func() { _ = config.Set().Screen().Language("") })
	_ = config.Set().Screen().Language("ja")
	_ = config.Set().Calendar().Sources([]string{"calendar.family"})
	r := testRenderer()
	fc := r.faces()
	fc.label = calendarSettingFace{basicfont.Face7x13, 29}
	fc.sub = calendarSettingFace{basicfont.Face7x13, 21}
	fc.value = calendarSettingFace{basicfont.Face7x13, 25}
	fc.header = calendarSettingFace{basicfont.Face7x13, 36}
	fc.button = calendarSettingFace{basicfont.Face7x13, 23}
	r.fc = &fc
	card := r.nextCard(960, 480)
	for _, row := range calendarLookRows() {
		if row.kind == ctlHeading {
			continue
		}
		label, sub, end := r.rowWords(card, row, fc.label)
		if label != row.label || sub != row.sub {
			t.Errorf("%s would be truncated: %q / %q", row.id, label, sub)
		}
		if end+r.s(rowGap)+r.controlWidth(row) > card.Max.X-r.s(26) {
			t.Errorf("%s overlaps its control", row.id)
		}
	}
	for _, id := range []string{"cal_opens_on", "cal_month_details"} {
		p, ok := lookPicker(id)
		if !ok {
			t.Fatal(id)
		}
		r.pending = nil
		r.picker(p, 0)
		for _, z := range r.pending {
			if !z.r.In(r.dst.Rect) {
				t.Errorf("%s has an offscreen choice: %v", id, z.r)
			}
		}
	}
}

func TestCameraTestLocalizesOnlyItsSettingsLabel(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	t.Cleanup(func() { _ = config.Set().Screen().Language("") })
	wasAvailable := camTestAvailable
	camTestAvailable = func() bool { return true }
	t.Cleanup(func() { camTestAvailable = wasAvailable })
	var englishPage []byte
	for _, lang := range []string{"en", "ja", "de"} {
		_ = config.Set().Screen().Language(lang)
		rows := cameraTestRows()
		want := "Camera test"
		if lang == "ja" {
			want = "カメラテスト"
		}
		if len(rows) != 1 || rows[0].id != "camtest" || rows[0].label != want || rows[0].sub != "Frame rate, picture and how the board makes it" || rows[0].button != "Open" {
			t.Fatalf("%s: camera test row %+v", lang, rows)
		}
		r := testRenderer()
		v := &camTestView{left: 90 * time.Second, started: true, sensorFPS: 29.8, shownFPS: 3.1, w: 640, h: 480,
			preview: image.NewRGBA(image.Rect(0, 0, 640, 480)), board: "Echo Show 5 2nd gen (cronos)",
			path: []string{"Sensor → ISP → JPEG", "Pages: authenticated RTSP"}, user: "frontdoor", address: "192.0.2.50", network: true}
		r.cameraTestPage(scene{now: time.Date(2026, 10, 27, 9, 41, 0, 0, time.UTC), camtest: v})
		if lang == "en" {
			englishPage = bytes.Clone(r.dst.Pix)
		} else if !bytes.Equal(englishPage, r.dst.Pix) {
			t.Fatalf("%s changed the English camera test page", lang)
		}
	}
}
