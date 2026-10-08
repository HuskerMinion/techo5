package wifi

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An address is read as people type it, with or without its prefix, and refused when it could not work
// on a network, so that what reaches techo5-net is only ever a usable address.
func TestAnAddressIsReadAsTypedAndRefusedWhenItCouldNotWork(t *testing.T) {
	for _, c := range []struct {
		address, gateway, dns string
		want                  string // Address.String(), or "error"
		dnsList               string
	}{
		{"", "", "", "automatic (DHCP)", ""},
		{"  ", "192.168.1.1", "", "automatic (DHCP)", ""},
		{"192.168.1.50", "192.168.1.1", "", "192.168.1.50/24 via 192.168.1.1", "192.168.1.1"},
		{"192.168.1.50/24", " 192.168.1.1 ", "1.1.1.1, 9.9.9.9", "192.168.1.50/24 via 192.168.1.1", "1.1.1.1, 9.9.9.9"},
		{"10.0.5.20/16", "10.0.0.1", "10.0.0.1 1.1.1.1", "10.0.5.20/16 via 10.0.0.1", "10.0.0.1, 1.1.1.1"},
		{"192.168.1.50/24", "192.168.2.1", "", "error", ""},  // gateway outside the network
		{"192.168.1.0/24", "192.168.1.1", "", "error", ""},   // the network's own address
		{"192.168.1.255/24", "192.168.1.1", "", "error", ""}, // its broadcast address
		{"192.168.1.1/24", "192.168.1.1", "", "error", ""},   // the gateway itself
		{"192.168.1.50/31", "192.168.1.51", "", "error", ""}, // a prefix with no room
		{"192.168.1.50/x", "192.168.1.1", "", "error", ""},   // not a prefix
		{"192.168.1.500", "192.168.1.1", "", "error", ""},    // not an address
		{"fd00::5/64", "fd00::1", "", "error", ""},           // not IPv4
		{"192.168.1.50", "", "", "error", ""},                // no gateway
		{"192.168.1.50", "192.168.1.1", "nameserver", "error", ""},
		{"192.168.1.50", "192.168.1.1", "1.1.1.1,1.0.0.1,9.9.9.9,8.8.8.8", "error", ""},
		{"192.168.1.50; reboot", "192.168.1.1", "", "error", ""}, // nothing but an address goes through
	} {
		a, err := ParseAddress(c.address, c.gateway, c.dns)
		if c.want == "error" {
			if err == nil {
				t.Errorf("%q via %q (dns %q) was taken as %s", c.address, c.gateway, c.dns, a)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q via %q (dns %q): %v", c.address, c.gateway, c.dns, err)
			continue
		}
		if a.String() != c.want || a.DNSList() != c.dnsList {
			t.Errorf("%q via %q (dns %q) read as %s with name servers %q, want %s and %q",
				c.address, c.gateway, c.dns, a, a.DNSList(), c.want, c.dnsList)
		}
	}
}

// The setting is kept in a file techo5-net reads as shell variables, and read back the same; a missing
// or unreadable file is DHCP, as is one switched back to it.
func TestTheAddressSettingIsKeptForTechoNet(t *testing.T) {
	dir := t.TempDir()
	prev := addressFile
	addressFile = filepath.Join(dir, "network.conf")
	t.Cleanup(func() { addressFile = prev })

	if a := LoadAddress(); a.Fixed {
		t.Fatalf("no file read as %s", a)
	}
	a, err := ParseAddress("192.168.1.50/24", "192.168.1.1", "1.1.1.1 9.9.9.9")
	if err != nil {
		t.Fatal(err)
	}
	if err := SaveAddress(a); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(addressFile)
	for _, line := range []string{"MODE=static", "ADDRESS=192.168.1.50", "PREFIX=24", "GATEWAY=192.168.1.1", `DNS="1.1.1.1 9.9.9.9"`} {
		if !strings.Contains(string(b), line+"\n") {
			t.Errorf("the file has no %s:\n%s", line, b)
		}
	}
	if got := LoadAddress(); got.String() != a.String() || got.DNSList() != a.DNSList() {
		t.Errorf("read back as %s (%s), want %s (%s)", got, got.DNSList(), a, a.DNSList())
	}
	// The setting replaced is kept for techo5-net to go back to: DHCP before the first one.
	if prev, _ := os.ReadFile(addressFile + ".prev"); string(prev) != "MODE=dhcp\n" {
		t.Errorf("before the first setting, the one to go back to is %q", prev)
	}
	if err := SaveAddress(Address{}); err != nil {
		t.Fatal(err)
	}
	if prev, _ := os.ReadFile(addressFile + ".prev"); !strings.Contains(string(prev), "ADDRESS=192.168.1.50\n") {
		t.Errorf("after the fixed one, the one to go back to is %q", prev)
	}
	if got := LoadAddress(); got.Fixed {
		t.Errorf("switched back to DHCP, read as %s", got)
	}
	// A file edited by hand into something that is not an address is DHCP, not a guess.
	_ = os.WriteFile(addressFile, []byte("MODE=static\nADDRESS=192.168.1.50\nPREFIX=24\nGATEWAY=elsewhere\n"), 0o644)
	if got := LoadAddress(); got.Fixed {
		t.Errorf("a broken file read as %s", got)
	}
}

// Why a fixed address is not in use is what techo5-net wrote, and nothing while there is no reason.
func TestTheFallbackSaysWhy(t *testing.T) {
	dir := t.TempDir()
	prev := fallbackFile
	fallbackFile = filepath.Join(dir, "network-fallback")
	t.Cleanup(func() { fallbackFile = prev })
	if got := AddressFallback(); got != "" {
		t.Errorf("no file says %q", got)
	}
	_ = os.WriteFile(fallbackFile, []byte("the gateway 192.168.1.1 did not answer from 192.168.1.50/24\n"), 0o644)
	if got := AddressFallback(); got != "the gateway 192.168.1.1 did not answer from 192.168.1.50/24" {
		t.Errorf("says %q", got)
	}
}
