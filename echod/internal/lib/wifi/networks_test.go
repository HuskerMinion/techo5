package wifi

import (
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
