//go:build linux

package wifi

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// A pid file is only a DHCP client while its process is there: one left behind by a client that is
// gone, or none at all, is no one to ask for a lease, and renewLease starts one instead.
func TestDHCPClientIsOnlyARunningOne(t *testing.T) {
	was := udhcpcPID
	t.Cleanup(func() { udhcpcPID = was })
	udhcpcPID = filepath.Join(t.TempDir(), "udhcpc.pid")

	if dhcpClient() != nil {
		t.Fatal("a client with no pid file")
	}
	if err := os.WriteFile(udhcpcPID, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if dhcpClient() == nil {
		t.Fatal("no client for a running process")
	}
	// Past the kernel's largest pid: nothing runs there.
	if err := os.WriteFile(udhcpcPID, []byte("4194305\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if dhcpClient() != nil {
		t.Fatal("a client for a process that is gone")
	}
	if err := os.WriteFile(udhcpcPID, []byte("junk"), 0o644); err != nil {
		t.Fatal(err)
	}
	if dhcpClient() != nil {
		t.Fatal("a client for a pid file that holds no pid")
	}
}

// With no client running, a join starts udhcpc alone, with techo5-net's hook (written when techo5-net
// never got as far), and not a second one once it runs.
func TestStartDHCPStartsUdhcpcOnly(t *testing.T) {
	dir := t.TempDir()
	wasPID, wasScript, wasLog := udhcpcPID, udhcpcScript, udhcpcLog
	t.Cleanup(func() { udhcpcPID, udhcpcScript, udhcpcLog = wasPID, wasScript, wasLog })
	udhcpcPID = filepath.Join(dir, "udhcpc.pid")
	udhcpcScript = filepath.Join(dir, "udhcpc.sh")

	bin := filepath.Join(dir, "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	calls := filepath.Join(dir, "calls")
	// The stub writes the test's own pid as the client's: a process that is there, as -b leaves one.
	// It also leaves a child running on its stdout and stderr, as -b does: startDHCP must not wait for
	// that one.
	stub := "#!/bin/sh\necho \"$*\" >> " + calls + "\necho " + strconv.Itoa(os.Getpid()) + " > " + udhcpcPID +
		"\nsleep 30 &\n"
	if err := os.WriteFile(filepath.Join(bin, "udhcpc"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))

	udhcpcLog = filepath.Join(dir, "udhcpc.log")
	start := time.Now()
	startDHCP()
	startDHCP()
	if d := time.Since(start); d > 10*time.Second {
		t.Fatalf("waited %v on the client left running", d)
	}

	b, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	want := "-i wlan0 -s " + udhcpcScript + " -b -t 8 -p " + udhcpcPID + "\n"
	if string(b) != want {
		t.Fatalf("udhcpc called as %q, want it once as %q", b, want)
	}
	hook, err := os.ReadFile(udhcpcScript)
	if err != nil || string(hook) != udhcpcHook {
		t.Fatalf("hook not written: %v", err)
	}
}
