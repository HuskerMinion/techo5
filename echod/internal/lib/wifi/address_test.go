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
		{"127.0.0.5/8", "127.0.0.1", "", "error", ""},            // loopback
		{"169.254.3.4/16", "169.254.0.1", "", "error", ""},       // link-local
		{"0.1.2.3/8", "0.1.2.1", "", "error", ""},                // "this network"
		{"224.0.0.5", "224.0.0.1", "", "error", ""},              // multicast
		{"240.0.0.5", "240.0.0.1", "", "error", ""},              // reserved
		{"192.168.1.50", "192.168.1.0", "", "error", ""},         // the gateway on the network's own address
		{"192.168.1.50", "192.168.1.255", "", "error", ""},       // or its broadcast address
		{"192.168.1.50", "192.168.1.1", "0.0.0.0", "error", ""},  // DNS servers nobody answers from
		{"192.168.1.50", "192.168.1.1", "255.255.255.255", "error", ""},
		{"192.168.1.50", "192.168.1.1", "224.0.0.251", "error", ""},
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

// A change is written as a candidate for techo5-net to try, never over the setting in use, which only
// techo5-net replaces once the candidate has worked: a restart mid-trial comes back on the last good one.
// The candidate reads back as it was written, with the network it was set on.
func TestAChangeIsACandidateUntilItHasWorked(t *testing.T) {
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
	if err := writeCandidate(a, "486f6d65"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(candidate())
	for _, line := range []string{"MODE=static", "ADDRESS=192.168.1.50", "PREFIX=24", "GATEWAY=192.168.1.1", `DNS="1.1.1.1 9.9.9.9"`, "SSID=486f6d65"} {
		if !strings.Contains(string(b), line+"\n") {
			t.Errorf("the candidate has no %s:\n%s", line, b)
		}
	}
	if _, err := os.Stat(addressFile); err == nil {
		t.Error("the setting in use was written before the candidate was tried")
	}
	if got := LoadAddress(); got.Fixed {
		t.Errorf("the untried candidate is in use: %s", got)
	}
	if got := readAddress(candidate()); !got.same(a) {
		t.Errorf("the candidate reads back as %s (%s), want %s (%s)", got, got.DNSList(), a, a.DNSList())
	}

	// techo5-net keeps it: it is the setting in use, and saving it again changes nothing.
	if err := os.Rename(candidate(), addressFile); err != nil {
		t.Fatal(err)
	}
	if !AddressUnchanged(a) {
		t.Error("the setting in use reads as a change")
	}
	if AddressUnchanged(Address{}) {
		t.Error("DHCP reads as unchanged while a fixed address is in use")
	}

	// A file edited by hand into something that is not an address, or missing a value, is DHCP, as
	// techo5-net reads it, not a guess.
	for _, bad := range []string{
		"MODE=static\nADDRESS=192.168.1.50\nPREFIX=24\nGATEWAY=elsewhere\n",
		"MODE=static\nADDRESS=192.168.1.50\nGATEWAY=192.168.1.1\n", // no prefix
	} {
		_ = os.WriteFile(addressFile, []byte(bad), 0o644)
		if got := LoadAddress(); got.Fixed {
			t.Errorf("%q read as %s", bad, got)
		}
	}
	// DHCP in use and DHCP asked for: nothing to try, so the device does not drop its lease for nothing.
	_ = os.WriteFile(addressFile, []byte("MODE=dhcp\n"), 0o644)
	if !AddressUnchanged(Address{}) {
		t.Error("DHCP asked for while on DHCP reads as a change")
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
