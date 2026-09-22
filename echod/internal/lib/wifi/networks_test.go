package wifi

import (
	"context"
	"os"
	"strings"
	"testing"
)

func withConf(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	old := Conf
	Conf = dir + "/wpa_supplicant.conf"
	t.Cleanup(func() { Conf = old })
	if body != "" {
		if err := os.WriteFile(Conf, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// Joining a network keeps the ones already saved: a device set up here still joins the network it is
// taken to, and the one it came from is still there to come back to.
func TestJoiningKeepsTheNetworksAlreadySaved(t *testing.T) {
	withConf(t, conf([]string{block("Home", "hunter2hunter"), block("Cafe", "")}))

	kept := []string{block("Theirs", "another-one")}
	for _, b := range blocks(readConf(t)) {
		if ssidOf(b) != "Theirs" {
			kept = append(kept, b)
		}
	}
	if err := os.WriteFile(Conf, []byte(conf(kept)), 0o600); err != nil {
		t.Fatal(err)
	}

	got := Saved()
	want := []string{"Theirs", "Home", "Cafe"}
	if len(got) != len(want) {
		t.Fatalf("saved %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("saved %v, want %v: the one just asked for comes first", got, want)
		}
	}
}

// A name with a quote in it survives being written and read back.
func TestAnAwkwardNameSurvivesTheRoundTrip(t *testing.T) {
	withConf(t, conf([]string{block(`Bob"s "Wi-Fi`, "passphrase1")}))
	if got := Saved(); len(got) != 1 || got[0] != `Bob"s "Wi-Fi` {
		t.Errorf("saved %q, want the name it was given back", got)
	}
}

// Forgetting takes one out and leaves the rest alone, header and all.
func TestForgettingLeavesTheRestAlone(t *testing.T) {
	withConf(t, conf([]string{block("Home", "hunter2hunter"), block("Cafe", "")}))
	var kept []string
	for _, b := range blocks(readConf(t)) {
		if ssidOf(b) != "Home" {
			kept = append(kept, b)
		}
	}
	if err := os.WriteFile(Conf, []byte(conf(kept)), 0o600); err != nil {
		t.Fatal(err)
	}
	body := readConf(t)
	if !strings.Contains(body, "ctrl_interface=") || !strings.Contains(body, "update_config=0") {
		t.Error("the header the boot scripts expect did not survive")
	}
	if got := Saved(); len(got) != 1 || got[0] != "Cafe" {
		t.Errorf("saved %v, want Cafe alone", got)
	}
}

// A stanza this code did not write — one with settings of its own — is kept as it was.
func TestAStanzaWeDidNotWriteIsKept(t *testing.T) {
	hand := "network={\n\tssid=\"Office\"\n\tkey_mgmt=WPA-EAP\n\tidentity=\"someone\"\n}\n"
	withConf(t, conf([]string{hand, block("Home", "hunter2hunter")}))
	var kept []string
	for _, b := range blocks(readConf(t)) {
		if ssidOf(b) != "Home" {
			kept = append(kept, b)
		}
	}
	if err := os.WriteFile(Conf, []byte(conf(kept)), 0o600); err != nil {
		t.Fatal(err)
	}
	if body := readConf(t); !strings.Contains(body, "key_mgmt=WPA-EAP") || !strings.Contains(body, `identity="someone"`) {
		t.Errorf("a hand-written network lost its settings:\n%s", body)
	}
}

func readConf(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(Conf)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// A name and a passphrase are written into wpa_supplicant.conf as quoted values, and that file is
// read a line at a time: a newline inside one ends its line and everything after it is read as
// configuration nobody asked for. The setup page takes whatever somebody types into the "other
// network" box, so the value is refused here before anything is written.
func TestJoinRefusesALineEndingInAValue(t *testing.T) {
	withConf(t, "")

	injected := "Home\nnetwork={\n\tssid=\"Theirs\"\n\tkey_mgmt=NONE\n}"
	if err := Join(context.Background(), injected, "hunter2hunter"); err == nil {
		t.Error("a network name carrying a line ending was written to the configuration")
	}
	if err := Join(context.Background(), "Home", "hunter2\rhunter"); err == nil {
		t.Error("a passphrase carrying a carriage return was written to the configuration")
	}

	if _, err := os.Stat(Conf); !os.IsNotExist(err) {
		t.Errorf("the configuration was written for a value that was refused: %v", err)
	}
}
