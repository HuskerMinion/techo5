package config

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

// A settings file may contain a forged ASP selection, but Restore must keep
// the local choice. Ordinary microphone processing settings still travel.
func TestASPSelectionIsLocalAcrossRestore(t *testing.T) {
	for _, tc := range []struct {
		name        string
		local, file ASPMode
	}{
		{"built-in cannot be started by a file", ASPBuiltin, ASPExternal},
		{"external cannot be stopped by a file", ASPExternal, ASPBuiltin},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "state.json")
			Use(path)
			if err := Set().Microphone().ASP(tc.local); err != nil {
				t.Fatal(err)
			}
			if err := Set().Microphone().Muted(true); err != nil {
				t.Fatal(err)
			}
			incoming := Defaults()
			incoming.Microphone.ASP = tc.file
			incoming.Microphone.Gain = 31
			incoming.Microphone.Leveling = false
			incoming.Microphone.Noise = true // local-only
			settings, err := json.Marshal(incoming)
			if err != nil {
				t.Fatal(err)
			}
			file, err := json.Marshal(File{
				Format: FileFormat, Version: FileVersion, Model: "m", Settings: settings,
			})
			if err != nil {
				t.Fatal(err)
			}
			// Apply through the same persistent Writer.Restore as the setup page,
			// not only through KeepOwn or the Restored preview.
			if _, err := Set().Restore(file, "m"); err != nil {
				t.Fatal(err)
			}
			got := Get().Microphone
			if got.ASP != tc.local {
				t.Errorf("ASP from file changed local choice: got %q, want %q", got.ASP, tc.local)
			}
			if got.Gain != 31 || got.Leveling || got.Noise || !got.Muted {
				t.Errorf("microphone restore broke transferable or local-only fields: %+v", got)
			}
			reloaded, err := Load(path)
			if err != nil || reloaded.Get().Microphone.ASP != tc.local {
				t.Fatal("restored ASP choice was not persisted", err)
			}
		})
	}
}

func TestASPSelectionIsClearedFromExport(t *testing.T) {
	c := Defaults()
	c.Microphone.ASP = ASPExternal
	file, err := Export(c, "m", "v0")
	if err != nil {
		t.Fatal(err)
	}
	var f File
	if err := json.Unmarshal(file, &f); err != nil {
		t.Fatal(err)
	}
	var exported Config
	if err := json.Unmarshal(f.Settings, &exported); err != nil {
		t.Fatal(err)
	}
	if exported.Microphone.ASP == ASPExternal {
		t.Fatal("exported settings carried the local External selection")
	}
}
