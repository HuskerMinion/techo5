//go:build !dot

package display

import (
	"fmt"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/netbird"
	"github.com/HuskerMinion/techo5/echod/internal/feature/streaming"
)

func TestV168CameraSettingsTranslateWithoutChangingValues(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	t.Cleanup(func() { _ = config.Set().Screen().Language("") })
	_ = config.Set().Screen().CameraFit("native")
	_ = config.Set().Screen().CameraBands("fade")
	_ = config.Set().Screen().CameraShade("strong")
	for _, lang := range []string{"en", "ja", "es"} {
		_ = config.Set().Screen().Language(lang)
		for _, tc := range []struct {
			id, english, japanese string
			labels                []string
			translated            []string
			selected              int
		}{
			{"camfit", "Camera picture", "カメラ映像", []string{"Whole picture", "Fill the screen", "Camera's own size"}, []string{"映像全体", "画面いっぱい", "元のサイズ"}, 2},
			{"cambands", "Camera strips", "カメラの帯", []string{"Solid", "Fade"}, []string{"単色", "グラデーション"}, 1},
			{"camshade", "Camera strips shade", "カメラの帯の濃さ", []string{"Light", "Medium", "Strong"}, []string{"薄め", "標準", "濃いめ"}, 2},
		} {
			p, ok := pickerFor(tc.id, sheetView{})
			wantTitle, wantLabels := tc.english, tc.labels
			if lang == "ja" {
				wantTitle, wantLabels = tc.japanese, tc.translated
			}
			if !ok || p.title != wantTitle || !reflect.DeepEqual(p.opts, wantLabels) || p.cur != tc.selected {
				t.Errorf("%s/%s: %+v", lang, tc.id, p)
			}
		}
		if got := cameraLookLabels(cameraFits); !reflect.DeepEqual(got, []string{"Whole picture", "Fill the screen", "Camera's own size"}) {
			t.Fatal("canonical camera options changed", got)
		}
		if sc := config.Get().Screen; sc.CameraFit != "native" || sc.CameraBands != "fade" || sc.CameraShade != "strong" {
			t.Fatal("presentation changed saved camera settings")
		}
	}
}

func TestV168NetbirdAndStreamingMessages(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	t.Cleanup(func() { _ = config.Set().Screen().Language("") })
	for _, lang := range []string{"en", "ja", "es"} {
		_ = config.Set().Screen().Language(lang)
		for _, tc := range []struct {
			state             netbird.Status
			english, japanese string
		}{
			{netbird.Status{}, "Reach this device from away: off", "外部からのアクセス：オフ"},
			{netbird.Status{On: true, Downloading: true, Got: 0.37}, "Downloading NetBird, 37%", "NetBirdをダウンロード中：37%"},
			{netbird.Status{On: true, State: "Connected", Name: "My Show"}, "Connected as My Show", "My Showとして接続済み"},
			{netbird.Status{On: true, State: "NeedsLogin"}, "Needs a setup key: the setup page, Connections", "セットアップ画面のConnectionsでセットアップキーを設定"},
			{netbird.Status{On: true, Problem: "A server-specific problem"}, "A server-specific problem", "A server-specific problem"},
		} {
			want := tc.english
			if lang == "ja" {
				want = tc.japanese
			}
			if got := netbirdSub(tc.state); got != want {
				t.Errorf("%s: %q, want %q", lang, got, want)
			}
		}
		for _, row := range withStreaming(nil) {
			if row.id == "airplay2" {
				want := "Group it with other AirPlay 2 speakers"
				if lang == "ja" {
					want = "ほかのAirPlay 2スピーカーとグループ化"
				}
				if row.label != "AirPlay 2" || row.sub != want {
					t.Errorf("%s: AirPlay 2 row: %+v", lang, row)
				}
			}
		}
		// Hosts without the AirPlay 2 helper still check its translated description.
		if !streaming.Has2() && lang == "ja" && tr("ui_group_it_with_other_airplay_2_speakers") != "ほかのAirPlay 2スピーカーとグループ化" {
			t.Fatal("AirPlay 2 description is missing")
		}
	}
}

func TestV168WeekendHomeAssistantOptionsStayStable(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	t.Cleanup(func() { _ = config.Set().Screen().Language("") })
	_ = config.Set().Screen().Night("22-7")
	d := &Display{poke: make(chan struct{}, 1)}
	for _, lang := range []string{"en", "ja", "es"} {
		_ = config.Set().Screen().Language(lang)
		d.weekendStart, d.weekendEnd = weekendEndSelect(d, true), weekendEndSelect(d, false)
		if d.weekendStart.ObjectID != "screen_weekend_night_start" || d.weekendEnd.Name != "Weekend night ends" || d.weekendEnd.Options[0] != sameAsNights {
			t.Fatal("screen language changed Home Assistant identifiers or option values")
		}
		d.weekendEnd.OnCommand("09:30")
		if got := config.Get().Screen.NightWeekend; got != "22:00-09:30" {
			t.Fatal(fmt.Sprintf("%s: saved weekend window %q", lang, got))
		}
		d.weekendStart.OnCommand(sameAsNights)
		if got := config.Get().Screen.NightWeekend; got != "" {
			t.Fatal("resetting to normal nights failed")
		}
	}
}
