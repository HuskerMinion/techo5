package config

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestWakeCallConsentNeverTravelsWithASettingsFile(t *testing.T) {
	here := Defaults()
	here.Wake.Words = []WakeWord{DefaultWakeWord()}
	here.Wake.Words[0].CallContact = "Local service"
	from := Defaults()
	from.Wake.Words = []WakeWord{DefaultWakeWord()}
	from.Wake.Words[0].CallContact = "Foreign service"
	b, err := Export(from, "", "v0")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "Foreign service") {
		t.Fatal("wake-call consent exported")
	}
	// Even a hand-written file must not choose the device's call target.
	var file File
	if err := json.Unmarshal(b, &file); err != nil {
		t.Fatal(err)
	}
	file.Settings, _ = json.Marshal(from)
	b, _ = json.Marshal(file)
	got, err := Restored(here, b, "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Config.Wake.Words[0].CallContact != "Local service" {
		t.Fatal("local choice overwritten")
	}
	empty := Defaults()
	got, err = Restored(empty, b, "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Config.Wake.Words[0].CallContact != "" {
		t.Fatal("file enabled call mode")
	}
	// Like realtime voice, the choice is the slot's: a file with another word in it keeps it.
	from.Wake.Words[0].ID = "another_word"
	file.Settings, _ = json.Marshal(from)
	b, _ = json.Marshal(file)
	got, err = Restored(here, b, "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Config.Wake.Words[0].CallContact != "Local service" {
		t.Fatal("a file with another word changed the slot's choice")
	}
}

func TestWakeCallModesAreMutuallyExclusive(t *testing.T) {
	st, err := Load(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	w := st.Set().Wake(0)
	if err := w.CallContact("Chat service"); err != nil {
		t.Fatal(err)
	}
	if err := w.Realtime(true); err == nil {
		t.Fatal("realtime enabled over a wake call")
	}
	if st.Get().Wake.Slot(0).Realtime {
		t.Fatal("failed choice changed the active mode")
	}
	if err := w.CallContact(""); err != nil {
		t.Fatal(err)
	}
	if err := w.Realtime(true); err != nil {
		t.Fatal(err)
	}
	if err := w.CallContact("Chat service"); err == nil {
		t.Fatal("wake call enabled over realtime")
	}
	if st.Get().Wake.Slot(0).CallContact != "" {
		t.Fatal("failed choice changed the contact")
	}
}
