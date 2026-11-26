package wifi

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/lib/rootonly"
)

// The tests run as somebody other than root: what they make passes as root's would.
func init() { rootonly.Owner = os.Getuid() }

// A script or a pid file that root did not leave as root would (another user's, one others can write
// to, a link) is not taken: a script there would run as root.
func TestAPlantedScriptIsNotRun(t *testing.T) {
	dir := t.TempDir()
	wp, ws, wo, wst, wi := dhcpPid, dhcpScripts, dhcpOwn, startDHC, iface
	t.Cleanup(func() { dhcpPid, dhcpScripts, dhcpOwn, startDHC, iface = wp, ws, wo, wst, wi })
	planted := filepath.Join(dir, "planted.sh")
	_ = os.WriteFile(planted, []byte("#!/bin/sh\nid > /tmp/owned\n"), 0o777)
	_ = os.Chmod(planted, 0o777)
	dhcpPid = filepath.Join(dir, "udhcpc.pid")
	dhcpScripts = []string{planted}
	dhcpOwn = filepath.Join(dir, "own", "udhcpc.sh")
	iface = func() string { return "wlan0" }
	var ran []string
	startDHC = func(args ...string) error { ran = args; return nil }
	// A pid file others could write, naming a live process, does not keep the client from starting.
	_ = os.WriteFile(dhcpPid, []byte(strconv.Itoa(os.Getpid())), 0o666)
	_ = os.Chmod(dhcpPid, 0o666)
	if !startDHCP() {
		t.Fatal("a planted pid file kept the client from starting")
	}
	if got := ran[len(ran)-1]; got != dhcpOwn {
		t.Errorf("ran the script %s, want its own", got)
	}
}
