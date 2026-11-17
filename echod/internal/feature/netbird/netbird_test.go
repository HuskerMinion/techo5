//go:build !dot

package netbird

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/update"
)

func TestParseStatus(t *testing.T) {
	st, err := parseStatus([]byte(`{"daemonStatus":"Connected","netbirdIp":"100.64.0.20/16","fqdn":"kitchen.netbird.cloud",
		"management":{"url":"https://api.netbird.io:443","connected":true,"error":""},"peers":{"total":2}}`))
	if err != nil || st.DaemonStatus != connected || st.IP != "100.64.0.20" || st.FQDN != "kitchen.netbird.cloud" || !st.Management.Connected {
		t.Fatalf("%+v %v", st, err)
	}
	if _, err := parseStatus([]byte("Daemon status: NeedsLogin")); err == nil {
		t.Error("text taken for JSON")
	}
}

func TestStatusText(t *testing.T) {
	for _, c := range []struct {
		s    Status
		want string
	}{
		{Status{}, "Off"},
		{Status{On: true}, "Starting"},
		{Status{On: true, Downloading: true, Got: 0.42}, "Downloading NetBird (42%)"},
		{Status{On: true, Running: true, State: needsLogin, Problem: "needs a setup key"}, "Needs a setup key"},
		{Status{On: true, Running: true, State: "Connecting", Problem: "the management server: refused"}, "Trouble: the management server: refused"},
		{Status{On: true, Running: true, State: connected, Name: "kitchen", IP: "100.64.0.20"}, "Connected as kitchen (100.64.0.20)"},
		{Status{On: true, Running: true, State: "Connecting"}, "Connecting"},
	} {
		if got := statusText(c.s); got != c.want {
			t.Errorf("%+v: %q, want %q", c.s, got, c.want)
		}
	}
}

func TestManagementURL(t *testing.T) {
	for in, want := range map[string]string{
		"":                               DefaultManagement,
		"  https://nb.example.com  ":     "https://nb.example.com",
		"https://nb.example.com:33073":   "https://nb.example.com:33073",
		"http://192.0.2.4:80":            "http://192.0.2.4:80",
		"nb.example.com":                 "",
		"ftp://nb.example.com":           "",
		"https://user:pw@nb.example.com": "",
		"https://":                       "",
	} {
		got, err := managementURL(in)
		if want == "" {
			if err == nil {
				t.Errorf("%q taken as %q", in, got)
			}
			continue
		}
		if err != nil || got != want {
			t.Errorf("%q: %q %v, want %q", in, got, err, want)
		}
	}
	if mgmtSaved(DefaultManagement) != "" || mgmtSaved("https://nb.example.com") != "https://nb.example.com" {
		t.Error("mgmtSaved")
	}
}

// archive is a client archive as build-netbird.sh makes one.
func archive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		tw.Write([]byte(body))
	}
	tw.Close()
	gz.Close()
	return b.Bytes()
}

// The first time on, the client is downloaded, unpacked with its license, and an older one goes; after
// that it is not downloaded again.
func TestFetchClient(t *testing.T) {
	was, wasDownload := stateDir, download
	t.Cleanup(func() { stateDir, download = was, wasDownload })
	stateDir = t.TempDir()
	os.WriteFile(filepath.Join(stateDir, "netbird-0.0.1"), []byte("old"), 0o755)
	tgz := archive(t, map[string]string{"netbird": "#!client", "LICENSE": "BSD"})
	fetched := 0
	download = func(_ context.Context, b update.Binary, to string, progress func(float32)) error {
		fetched++
		if b != client {
			t.Errorf("fetched %+v", b)
		}
		progress(1)
		return os.WriteFile(to, tgz, 0o600)
	}
	f := &Feature{sw: Get().sw, status: Get().status}
	for range 2 {
		if err := f.fetchClient(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if fetched != 1 {
		t.Errorf("downloaded %d times", fetched)
	}
	if b, _ := os.ReadFile(binPath()); string(b) != "#!client" {
		t.Errorf("client %q", b)
	}
	if info, _ := os.Stat(binPath()); info.Mode()&0o111 == 0 {
		t.Error("client not executable")
	}
	if b, _ := os.ReadFile(filepath.Join(stateDir, "LICENSE")); string(b) != "BSD" {
		t.Errorf("license %q", b)
	}
	left, _ := filepath.Glob(filepath.Join(stateDir, "*"))
	for _, l := range left {
		if b := filepath.Base(l); b != "LICENSE" && b != "netbird-"+Version {
			t.Errorf("left behind: %s", b)
		}
	}
	if f.State().Downloading {
		t.Error("still downloading")
	}
}

func TestUnpackWantsTheClient(t *testing.T) {
	was := stateDir
	t.Cleanup(func() { stateDir = was })
	stateDir = t.TempDir()
	a := filepath.Join(stateDir, "a.tar.gz")
	os.WriteFile(a, archive(t, map[string]string{"LICENSE": "BSD"}), 0o600)
	if err := unpack(a, filepath.Join(stateDir, "bin")); err == nil {
		t.Error("an archive with no client unpacked")
	}
}

// Joining needs a key that looks like one, a management server that is a URL, and an encrypted link.
func TestLoginRefuses(t *testing.T) {
	wasEnc := encrypted
	t.Cleanup(func() { encrypted = wasEnc })
	encrypted = func() bool { return false }
	f := &Feature{sw: Get().sw, status: Get().status}
	ctx := context.Background()
	for _, c := range []struct{ key, mgmt, want string }{
		{"", "", "not a setup key"},
		{"A B", "", "not a setup key"},
		{"A1B2C3D4-0000-0000-0000-000000000000", "nb.example.com", "is a URL"},
		{"A1B2C3D4-0000-0000-0000-000000000000", "", "encryption key"},
	} {
		if err := f.Login(ctx, c.key, c.mgmt); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q %q: %v", c.key, c.mgmt, err)
		}
	}
}

// The pinned client is a release's, by https, with a size and a full SHA-256.
func TestPinned(t *testing.T) {
	if !strings.HasPrefix(client.URL, "https://github.com/HuskerMinion/techo5/releases/download/") ||
		len(client.SHA256) != 64 || client.Size < 1<<20 {
		t.Errorf("%+v", client)
	}
}

func TestFirewallOn(t *testing.T) {
	dir := t.TempDir()
	profile := filepath.Join(dir, "default.json")

	// No profile yet: nothing to do.
	if err := firewallOn(profile); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(profile); !os.IsNotExist(err) {
		t.Fatalf("a profile was made: %v", err)
	}

	// Joined with --disable-firewall: turned on, the rest kept.
	os.WriteFile(profile, []byte(`{"ManagementURL":{"Host":"mesh.example"},"DisableFirewall": true,"DisableDNS":true}`), 0o600)
	if err := firewallOn(profile); err != nil {
		t.Fatal(err)
	}
	var c map[string]any
	b, _ := os.ReadFile(profile)
	if err := json.Unmarshal(b, &c); err != nil {
		t.Fatal(err)
	}
	if c["DisableFirewall"] != false || c["DisableDNS"] != true || c["ManagementURL"].(map[string]any)["Host"] != "mesh.example" {
		t.Errorf("profile = %s", b)
	}
	if fi, _ := os.Stat(profile); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v", fi.Mode().Perm())
	}

	// On already: left as it is, byte for byte.
	on := []byte(`{"DisableFirewall":false}`)
	os.WriteFile(profile, on, 0o600)
	if err := firewallOn(profile); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(profile); !bytes.Equal(b, on) {
		t.Errorf("profile rewritten: %s", b)
	}

	// Not a profile it can read: said, and left alone.
	os.WriteFile(profile, []byte("{"), 0o600)
	if err := firewallOn(profile); err == nil {
		t.Error("no error for a broken profile")
	}
}

// The client's own firewall must stay on: without it, a policy decides which peers see the device but
// not which ports they reach (#579).
func TestUpKeepsTheFirewallOn(t *testing.T) {
	args := upArgs("https://mesh.example:443", "kitchen")
	if args[0] != "up" {
		t.Fatalf("args = %q", args)
	}
	on := 0
	for _, a := range args {
		switch {
		case a == "--disable-firewall=false":
			on++
		case strings.HasPrefix(a, "--disable-firewall"):
			t.Errorf("%q in the up arguments", a)
		}
	}
	if on != 1 {
		t.Errorf("--disable-firewall=false %d times in %q", on, args)
	}
}
