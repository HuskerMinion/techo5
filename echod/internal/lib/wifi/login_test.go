package wifi

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestALoginIsWrittenInHex(t *testing.T) {
	ca := filepath.Join(t.TempDir(), "ca.crt")
	if err := os.WriteFile(ca, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := caBundle
	caBundle = ca
	t.Cleanup(func() { caBundle = old })

	l := Login{Identity: "student@example.edu", Password: `pa"ss\word`, Anonymous: "anonymous@example.edu", Server: "example.edu"}
	b, err := loginBlock("eduroam", l)
	if err != nil {
		t.Fatal(err)
	}
	h := func(s string) string { return hex.EncodeToString([]byte(s)) }
	for _, want := range []string{
		"key_mgmt=WPA-EAP", "eap=PEAP", `phase2="auth=MSCHAPV2"`,
		"identity=" + h(l.Identity), "anonymous_identity=" + h(l.Anonymous), "password=" + h(l.Password),
		`ca_cert="` + ca + `"`, "domain_suffix_match=" + h("example.edu"),
	} {
		if setting(b, strings.SplitN(want, "=", 2)[0]) != strings.SplitN(want, "=", 2)[1] {
			t.Errorf("missing %s in\n%s", want, b)
		}
	}
	if strings.Contains(b, "student") || strings.Contains(b, "pa\"ss") {
		t.Errorf("a typed value went in as text:\n%s", b)
	}
	if got := ssidOf(b); got != "eduroam" {
		t.Errorf("ssidOf = %q", got)
	}
	if n := len(blocks(conf([]string{b}))); n != 1 {
		t.Errorf("read back as %d stanzas", n)
	}
}

func TestALoginWithoutAServerCheckHasToBeAskedFor(t *testing.T) {
	l := Login{Method: "ttls-pap", Identity: "u", Password: "p"}
	if _, err := loginBlock("School", l); err == nil {
		t.Fatal("a login with no server name and no NoCheck was written")
	}
	l.NoCheck = true
	b, err := loginBlock("School", l)
	if err != nil {
		t.Fatal(err)
	}
	if setting(b, "eap") != "TTLS" || setting(b, "phase2") != `"auth=PAP"` {
		t.Errorf("ttls-pap written as\n%s", b)
	}
	if strings.Contains(b, "ca_cert") || strings.Contains(b, "domain_suffix_match") {
		t.Errorf("an unchecked login names a server check:\n%s", b)
	}
}

func TestALoginIsRefusedWhenIncomplete(t *testing.T) {
	for name, l := range map[string]Login{
		"no username":   {Password: "p", NoCheck: true},
		"no password":   {Identity: "u", NoCheck: true},
		"line ending":   {Identity: "u\nx", Password: "p", NoCheck: true},
		"unknown":       {Method: "leap", Identity: "u", Password: "p", NoCheck: true},
		"no CA to read": {Identity: "u", Password: "p", Server: "example.edu"},
	} {
		if name == "no CA to read" {
			old := caBundle
			caBundle = filepath.Join(t.TempDir(), "none")
			_, err := loginBlock("School", l)
			caBundle = old
			if err == nil {
				t.Errorf("%s: written", name)
			}
			continue
		}
		if _, err := loginBlock("School", l); err == nil {
			t.Errorf("%s: written", name)
		}
	}
}
