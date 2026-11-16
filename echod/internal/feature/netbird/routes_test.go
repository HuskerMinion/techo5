//go:build !dot

package netbird

import (
	"bytes"
	"encoding/json"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestParseRoutes(t *testing.T) {
	was := localNets
	t.Cleanup(func() { localNets = was })
	localNets = func() []netip.Prefix {
		return []netip.Prefix{netip.MustParsePrefix("192.168.50.0/24"), netip.MustParsePrefix("100.64.0.0/16")}
	}
	for _, c := range []struct {
		in   string
		want []string
		err  string
	}{
		{"", nil, ""},
		{"  \n ", nil, ""},
		{"192.168.1.0/24", []string{"192.168.1.0/24"}, ""},
		{"192.168.1.0/24\r\n10.20.0.0/16, 192.168.1.0/24;172.16.5.4/32", []string{"192.168.1.0/24", "10.20.0.0/16", "172.16.5.4/32"}, ""},
		{"192.168.1.0", nil, "not a network"},
		{"home", nil, "not a network"},
		{"fd00::/64", nil, "only IPv4"},
		{"192.168.1.7/24", nil, "give the network"},
		{"0.0.0.0/0", nil, "too wide"},
		{"0.0.0.0/1", nil, "too wide"},
		{"10.0.0.0/7", nil, "too wide"},
		{"127.0.0.0/8", nil, "not a network a route"},
		{"169.254.0.0/16", nil, "not a network a route"},
		{"224.0.0.0/8", nil, "not a network a route"},
		{"250.0.0.0/8", nil, "not a network a route"},
		{"192.168.50.0/24", nil, "own network 192.168.50.0/24"},
		{"192.168.0.0/16", nil, "own network 192.168.50.0/24"},
		{"192.168.50.128/25", nil, "own network"},
		{"100.64.3.0/24", nil, "own network 100.64.0.0/16"},
		{strings.Repeat("10.1.0.0/16 ", 20), []string{"10.1.0.0/16"}, ""}, // the same twice is once
	} {
		got, err := ParseRoutes(c.in)
		if c.err != "" {
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("%q: %v, want %q", c.in, err, c.err)
			}
			continue
		}
		if err != nil || !slices.Equal(got, c.want) {
			t.Errorf("%q: %q %v, want %q", c.in, got, err, c.want)
		}
	}
	var many []string
	for i := range maxRoutes + 1 {
		many = append(many, netip.PrefixFrom(netip.AddrFrom4([4]byte{10, byte(i), 0, 0}), 16).String())
	}
	if _, err := ParseRoutes(strings.Join(many, "\n")); err == nil || !strings.Contains(err.Error(), "at the most") {
		t.Errorf("%d networks: %v", len(many), err)
	}
}

// What `netbird networks list` says (client/cmd/networks.go, v0.81.4).
const networksList = `Available Networks:

  - ID: home-lan
    Network: 192.168.1.0/24
    Status: Selected

  - ID: office
    Network: 10.20.0.0/16
    Status: Not Selected

  - ID: names
    Domains: *.example.com
    Status: Selected
    Resolved IPs: -

  - ID: printer
    Network: 192.168.1.40/32
    Status: Selected

  - ID: exit
    Network: 0.0.0.0/0, ::/0
    Status: Not Selected
`

func TestParseNetworks(t *testing.T) {
	got := parseNetworks([]byte(networksList))
	want := []network{
		{"home-lan", "192.168.1.0/24", true},
		{"office", "10.20.0.0/16", false},
		{"names", "", true},
		{"printer", "192.168.1.40/32", true},
		{"exit", "0.0.0.0/0, ::/0", false},
	}
	if !slices.Equal(got, want) {
		t.Errorf("got %+v", got)
	}
	if got := parseNetworks([]byte("No networks available.\n")); len(got) != 0 {
		t.Errorf("none: %+v", got)
	}
}

func TestChoose(t *testing.T) {
	nets := parseNetworks([]byte(networksList))
	for _, c := range []struct {
		wanted      []string
		take, leave []string
	}{
		{[]string{"192.168.1.0/24"}, []string{"home-lan", "printer"}, []string{"office", "names", "exit"}},
		{[]string{"192.168.1.40/32"}, []string{"printer"}, []string{"home-lan", "office", "names", "exit"}},
		{[]string{"10.0.0.0/8", "192.168.1.0/24"}, []string{"home-lan", "office", "printer"}, []string{"names", "exit"}},
		{[]string{"172.16.0.0/12"}, nil, []string{"home-lan", "office", "names", "printer", "exit"}},
	} {
		var wanted []netip.Prefix
		for _, w := range c.wanted {
			wanted = append(wanted, netip.MustParsePrefix(w))
		}
		take, leave := choose(nets, wanted)
		if !slices.Equal(take, c.take) || !slices.Equal(leave, c.leave) {
			t.Errorf("%v: take %v leave %v", c.wanted, take, leave)
		}
	}
}

func TestUnlisted(t *testing.T) {
	wanted := []netip.Prefix{netip.MustParsePrefix("192.168.1.0/24")}
	for _, c := range []struct {
		inUse []string
		want  bool
	}{
		{nil, false},
		{[]string{"192.168.1.0/24"}, false},
		{[]string{"192.168.1.40/32", "192.168.1.0/24"}, false},
		{[]string{"192.168.1.0/24", "10.20.0.0/16"}, true},
		{[]string{"192.168.0.0/16"}, true}, // wider than listed
		{[]string{"*.example.com"}, true},
	} {
		if got := unlisted(c.inUse, wanted); got != c.want {
			t.Errorf("%v: %v", c.inUse, got)
		}
	}
}

// The routes in use are the peers' (status --json, v0.81.4): the status's own "networks" are the routes
// the device itself offers.
func TestRoutesInUse(t *testing.T) {
	st, err := parseStatus([]byte(`{"daemonStatus":"Connected","netbirdIp":"100.64.0.20/16","networks":null,
		"peers":{"total":3,"details":[
			{"fqdn":"router.netbird.cloud","networks":["192.168.1.0/24","10.20.0.0/16"]},
			{"fqdn":"laptop.netbird.cloud","networks":null},
			{"fqdn":"backup.netbird.cloud","networks":["192.168.1.0/24"]}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := st.routesInUse(); !slices.Equal(got, []string{"10.20.0.0/16", "192.168.1.0/24"}) {
		t.Errorf("in use: %q", got)
	}
	st, _ = parseStatus([]byte(`{"daemonStatus":"Connected","networks":["192.168.1.0/24"],"peers":{"details":[{"networks":null}]}}`))
	if got := st.routesInUse(); len(got) != 0 {
		t.Errorf("the device's own routes taken for routes in use: %q", got)
	}
}

func TestClientRoutes(t *testing.T) {
	profile := filepath.Join(t.TempDir(), "default.json")
	read := func() map[string]any {
		var c map[string]any
		b, _ := os.ReadFile(profile)
		if err := json.Unmarshal(b, &c); err != nil {
			t.Fatal(err)
		}
		return c
	}

	// No profile yet: nothing to do (joining says it).
	if err := clientRoutes(profile, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(profile); !os.IsNotExist(err) {
		t.Fatalf("a profile was made: %v", err)
	}

	// Joined with routes off: turned on, the rest kept.
	os.WriteFile(profile, []byte(`{"DisableClientRoutes":true,"DisableServerRoutes":true,"DisableFirewall":false}`), 0o600)
	if err := clientRoutes(profile, true); err != nil {
		t.Fatal(err)
	}
	if c := read(); c["DisableClientRoutes"] != false || c["DisableServerRoutes"] != true || c["DisableFirewall"] != false {
		t.Errorf("on: %v", c)
	}

	// Set right already: left as it is, byte for byte.
	b, _ := os.ReadFile(profile)
	if err := clientRoutes(profile, true); err != nil {
		t.Fatal(err)
	}
	if b2, _ := os.ReadFile(profile); !bytes.Equal(b, b2) {
		t.Errorf("profile rewritten: %s", b2)
	}

	// And off again; server routes are never touched.
	if err := clientRoutes(profile, false); err != nil {
		t.Fatal(err)
	}
	if c := read(); c["DisableClientRoutes"] != true || c["DisableServerRoutes"] != true {
		t.Errorf("off: %v", c)
	}
	if fi, _ := os.Stat(profile); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v", fi.Mode().Perm())
	}

	os.WriteFile(profile, []byte("{"), 0o600)
	if err := clientRoutes(profile, true); err == nil {
		t.Error("no error for a broken profile")
	}
}
