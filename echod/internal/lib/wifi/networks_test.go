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

// The key written for a passphrase is the one WPA makes of it. These two pairs are the worked
// examples in IEEE 802.11i's Annex H, so they say the derivation is the standard one and not
// something that merely round-trips through this package.
func TestThePassphraseBecomesTheKeyWPADerives(t *testing.T) {
	for _, c := range []struct {
		ssid, passphrase, key string
	}{
		{"IEEE", "password", "f42c6fc52df0ebef9ebb4b90b38a5f902e83fe1b135a70e23aed762e9710a12e"},
		{"ThisIsASSID", "ThisIsAPassword", "0dc0d6eb90555ed6419756b9a15ec3e3209b63df707dd508d14581f8982721af"},
	} {
		if got := psk(c.ssid, c.passphrase); got != c.key {
			t.Errorf("psk(%q, %q) = %s, want %s", c.ssid, c.passphrase, got, c.key)
		}
	}
}

// A quote or a backslash in a name or a passphrase is what the old escaping got wrong: it wrote \"
// and \\, which wpa_supplicant does not undo, so the radio was handed a passphrase with a backslash
// in it that nobody had typed and the person was told their password was wrong. Nothing in the file
// is quoted now, and the values read back byte for byte.
func TestAwkwardValuesAreWrittenSoTheSupplicantReadsThemBack(t *testing.T) {
	for _, c := range []struct{ name, ssid, passphrase string }{
		{"a quote in the passphrase", "Home", `pass"word`},
		{"a backslash in the passphrase", "Home", `pass\word`},
		{"both in the name", `Bob"s \ Wi-Fi`, "hunter2hunter"},
		{"a plain one", "Home", "hunter2hunter"},
		{"an open network", "Cafe", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			body := conf([]string{block(c.ssid, c.passphrase)})
			if strings.ContainsAny(body, "\"\\") {
				t.Errorf("a quote or a backslash reached the configuration:\n%s", body)
			}
			got := blocks(body)
			if len(got) != 1 {
				t.Fatalf("read %d networks out of\n%s", len(got), body)
			}
			if name := ssidOf(got[0]); name != c.ssid {
				t.Errorf("the name read back as %q, want %q", name, c.ssid)
			}
			// What wpa_supplicant would make of the key line, read the way it reads it.
			want := "key_mgmt=NONE"
			if c.passphrase != "" {
				want = "psk=" + psk(c.ssid, c.passphrase)
			}
			if !strings.Contains(body, "\t"+want+"\n") {
				t.Errorf("the key line is not %q:\n%s", want, body)
			}
		})
	}
}

// A plain network is written the way the boot script writes it, and nothing about that changed:
// the header, the name in hex, the key, one setting to a line.
func TestAPlainNetworkIsWrittenAsTheBootScriptWritesIt(t *testing.T) {
	want := "ctrl_interface=" + ctrlDir + "\nupdate_config=0\n" +
		"network={\n\tssid=486f6d65\n\tpsk=" + psk("Home", "hunter2hunter") + "\n}\n"
	if got := conf([]string{block("Home", "hunter2hunter")}); got != want {
		t.Errorf("wrote\n%s\nwant\n%s", got, want)
	}
}

// A file written before this package used hex still reads, so an update does not lose the network
// the device is on. A quoted value is what the supplicant makes of it: verbatim to the last quote.
func TestAQuotedConfigurationStillReads(t *testing.T) {
	body := "ctrl_interface=/run/wpa\nupdate_config=0\n" +
		"network={\n\tssid=\"Home\"\n\tpsk=\"hunter2hunter\"\n}\n" +
		"network={\n\tbssid=00:11:22:33:44:55\n\tssid=\"Cafe\"\n\tkey_mgmt=NONE\n}\n"
	withConf(t, body)
	got := Saved()
	want := []string{"Home", "Cafe"}
	if len(got) != len(want) {
		t.Fatalf("saved %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("saved %v, want %v (bssid= is not ssid=)", got, want)
		}
	}
}

// wpa_cli exits 0 on a refusal, so the reply is where a refusal has to be read.
func TestAFailReplyIsARefusal(t *testing.T) {
	for _, c := range []struct{ out, want string }{
		{"OK\n", ""},
		{"FAIL\n", "FAIL"},
		{"FAIL-BUSY\n", "FAIL-BUSY"},
		{"bssid / frequency / signal level / flags / ssid\n00:11:22:33:44:55\t2412\t-40\t[WPA2-PSK-CCMP]\tFAIL\n", ""},
	} {
		if got := refusal(c.out); got != c.want {
			t.Errorf("refusal(%q) = %q, want %q", c.out, got, c.want)
		}
	}
}

// The supplicant prints a name with the awkward characters escaped, so the name it reports has to be
// brought back to plain text before it is compared with the one that was asked for.
func TestANameFromTheSupplicantIsReadBackPlain(t *testing.T) {
	for _, c := range []struct{ out, want string }{
		{"Home", "Home"},
		{`Bob\"s \\ Wi-Fi`, `Bob"s \ Wi-Fi`},
		{`Caf\xc3\xa9`, "Café"},
		{`one\ttwo`, "one\ttwo"},
	} {
		if got := decodeName(c.out); got != c.want {
			t.Errorf("decodeName(%q) = %q, want %q", c.out, got, c.want)
		}
	}
}
