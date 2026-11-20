package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// A file written by a newer build keeps what this build does not know when this build saves, at the
// top and inside objects it does know; and what this build clears stays cleared.
func TestSettingsANewerBuildWroteAreKept(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	newer := `{
	  "brain": {"mode": "direct", "stt": "host:1", "key": "secret"},
	  "home": {"weather": "weather.x", "garden_sensor": "sensor.soil"},
	  "from_the_future": {"level": 3},
	  "speaker": {"volume": 5}
	}`
	if err := os.WriteFile(path, []byte(newer), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	// A change of this build's own, and a setting it clears.
	if err := st.Set().Speaker().Volume(7); err != nil {
		t.Fatal(err)
	}
	if err := st.Set().Brain().SetKey(""); err != nil {
		t.Fatal(err)
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if f, ok := got["from_the_future"].(map[string]any); !ok || f["level"] != 3.0 {
		t.Errorf("a setting this build has no field for was dropped: %v", got["from_the_future"])
	}
	home, _ := got["home"].(map[string]any)
	if home["garden_sensor"] != "sensor.soil" || home["weather"] != "weather.x" {
		t.Errorf("home = %v", home)
	}
	brain, _ := got["brain"].(map[string]any)
	if _, kept := brain["key"]; kept {
		t.Error("a key this build cleared came back from the old file")
	}
	if brain["stt"] != "host:1" {
		t.Errorf("brain = %v", brain)
	}
	if sp, _ := got["speaker"].(map[string]any); sp["volume"] != 7.0 {
		t.Errorf("this build's own change was lost: %v", got["speaker"])
	}
}

// The file of a device that had the built-in NetBird on loads, and keeps its NetBird section for the
// techo5-netbird add-on, which takes the peer's management server from it (#580). Turning the old switch
// off leaves the rest of the section as it was.
func TestAFileWithTheBuiltInNetbirdLoads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	old := `{
	  "netbird": {"on": true, "management": "https://netbird.example.com", "joined": true},
	  "tailscale": {"on": true},
	  "speaker": {"volume": 5}
	}`
	if err := os.WriteFile(path, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c := st.Get(); !c.Netbird.On || !c.Tailscale.On || c.Speaker.Volume != 5 {
		t.Fatalf("loaded %+v %+v %+v", c.Netbird, c.Tailscale, c.Speaker)
	}
	if err := st.Set().Netbird().Off(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	nb, _ := got["netbird"].(map[string]any)
	if _, on := nb["on"]; on {
		t.Errorf("the old switch is still on: %v", nb)
	}
	if nb["management"] != "https://netbird.example.com" || nb["joined"] != true {
		t.Errorf("the section the add-on reads lost its management server: %v", nb)
	}
	if ts, _ := got["tailscale"].(map[string]any); ts["on"] != true {
		t.Errorf("tailscale = %v", got["tailscale"])
	}
}
