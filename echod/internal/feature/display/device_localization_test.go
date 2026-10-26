//go:build !dot

package display

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

func TestDeviceReportedJapaneseLabelsAndOptions(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	t.Cleanup(func() { _ = config.Set().Screen().Language("") })
	for _, lang := range []string{"en", "ja", "de"} {
		if err := config.Set().Screen().Language(lang); err != nil {
			t.Fatal(err)
		}
		font, style := fontRow(), clockStyleRow()
		wantFont, wantStyle := "Font", "Clock style"
		wantPositions := []string{"Center", "Bottom left", "Bottom center", "Bottom right"}
		if len(clockPositions) == 2 {
			wantPositions = []string{"Center", "Bottom"}
		}
		if lang == "ja" {
			wantFont, wantStyle = "フォント", "時計スタイル"
			wantPositions = []string{"中央", "左下", "下部中央", "右下"}
			if len(clockPositions) == 2 {
				wantPositions = []string{"中央", "下部"}
			}
		}
		if font.label != wantFont || style.label != wantStyle {
			t.Errorf("%s: labels %q %q", lang, font.label, style.label)
		}
		picker, ok := pickerFor("clockpos", sheetView{})
		if !ok || !reflect.DeepEqual(picker.opts, wantPositions) {
			t.Errorf("%s: positions %v", lang, picker.opts)
		}
		// The canonical values published to Home Assistant must not be translated.
		if opts := clockPositionOptions(); opts[0] != "Center" {
			t.Fatalf("%s: canonical position changed: %v", lang, opts)
		}
		for _, name := range []string{"Dashboard", "Classic", "Noto Sans JP", "My speaker"} {
			if got := optionLabel(name); got != name {
				t.Errorf("design/owner name %q became %q", name, got)
			}
		}
		// A user-supplied name that happens to equal a generic option stays untouched.
		if builtinOptionRow("follow") || builtinOptionRow("font") {
			t.Fatal("user names must not be translated as generic options")
		}
		p, _ := pickerFor("camfit", sheetView{})
		if lang == "ja" && p.opts[0] != "映像全体" {
			t.Error(p.opts)
		}
		if lang == "ja" {
			for input, want := range map[string]string{"15 minutes": "15分", "20 seconds": "20秒", "15 minutes before": "15分前", "Until tapped": "タップするまで", "Strip after 10 s": "10秒後に小さく表示", "12-hour": "12時間表示"} {
				if got := optionLabel(input); got != want {
					t.Errorf("%q: %q, want %q", input, got, want)
				}
			}
			photos, found := pickerFor("photoevery", sheetView{})
			if !found || len(photos.opts) == 0 || photos.opts[0] != "15秒" {
				t.Fatalf("photo duration options were not translated: %v", photos.opts)
			}
			if everyOptions[0] != "15 seconds" {
				t.Fatal("canonical photo timing labels changed")
			}
			if font.sub != "画面上の文字に適用" || style.sub != "日中の時計のデザイン" {
				t.Errorf("untranslated descriptions: %q / %q", font.sub, style.sub)
			}
			if tr("presence_label") != "人感検知" || tr("presence_description") != "カメラで人の接近を検知。映像は保存しません" {
				t.Fatal("presence messages were left in English")
			}
		}
	}
}

func TestJapaneseSunriseDescriptionsAndTimingChoices(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	t.Cleanup(func() { _ = config.Set().Screen().Language("") })
	wasProgress, wasHold, wasGlow := sunriseProgress, sunriseHold, sunriseGlow
	t.Cleanup(func() { sunriseProgress, sunriseHold, sunriseGlow = wasProgress, wasHold, wasGlow })
	sunriseProgress = func(time.Time) float64 { return 0 }
	sunriseHold = func(time.Time) (time.Time, bool, bool) { return time.Time{}, false, false }
	sunriseGlow = func(time.Time) (time.Time, bool) { return time.Time{}, false }
	for _, lang := range []string{"en", "ja", "de"} {
		_ = config.Set().Screen().Language(lang)
		wantSub, wantTime := "Before alarms that don't choose their own", "5 minutes before"
		if lang == "ja" {
			wantSub, wantTime = "個別設定のないアラームの前に点灯", "5分前"
		}
		if got := sunriseSub(); got != wantSub {
			t.Errorf("%s: %q", lang, got)
		}
		if got := sunriseLabels()[1]; got != wantTime {
			t.Errorf("%s: %q", lang, got)
		}
		if lang == "ja" {
			if got := awayLabel(5); got != "5分" {
				t.Fatal(got)
			}
		}
	}
}
