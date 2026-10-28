//go:build !dot

package display

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

func TestInlineSettingsMessagesFollowScreenLanguage(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	t.Cleanup(func() { _ = config.Set().Screen().Language("") })
	for _, c := range []struct{ lang, ssh, lock, check string }{
		{"en", "On, but no key yet, so nothing listens", "A PIN before most settings change", "Checking…"},
		{"ja", "有効ですが、SSHキーがないため接続できません", "多くの設定変更にPINを要求", "確認中…"},
		{"es", "On, but no key yet, so nothing listens", "A PIN before most settings change", "Checking…"},
	} {
		if err := config.Set().Screen().Language(c.lang); err != nil {
			t.Fatal(err)
		}
		sv := sheetView{}
		sv.security.SSHAvailable, sv.security.SSH = true, true
		rows := securityRows(sv)
		for id, want := range map[string]string{"ssh": c.ssh, "settingslock": c.lock} {
			found := false
			for _, row := range rows {
				if row.id == id {
					found = true
					if row.sub != want {
						t.Errorf("%s/%s: %q, want %q", c.lang, id, row.sub, want)
					}
				}
			}
			if !found {
				t.Errorf("%s: missing %s row", c.lang, id)
			}
		}
		sv.st.checking = true
		for _, row := range generalRows(sv) {
			if row.id == "updates" && row.sub != c.check {
				t.Errorf("%s: update message %q", c.lang, row.sub)
			}
		}
		if cat, ok := catByName("Connections"); !ok || cat != catConnections {
			t.Fatal("translation changed the English category identifier")
		}
	}
}

func TestScreenLanguageDoesNotChangeHomeAssistantOptionValues(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	t.Cleanup(func() { _ = config.Set().Screen().Language("") })
	var english []string
	for _, lang := range []string{"en", "ja"} {
		if err := config.Set().Screen().Language(lang); err != nil {
			t.Fatal(err)
		}
		options := nightHoursSelect(&Display{}).Options
		if lang == "en" {
			english = options
		} else if !reflect.DeepEqual(options, english) {
			t.Fatalf("Home Assistant option values changed with the screen language: %v != %v", options, english)
		}
	}
}
