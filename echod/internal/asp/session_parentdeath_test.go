//go:build linux

package asp

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

// This test is executed only in a separate Go test process. The outer test
// SIGKILLs this parent, never its own runner or another device process.
func TestIsolatedHelperParent(t *testing.T) {
	if os.Getenv("ASP_ISOLATED_PARENT") != "1" {
		t.Skip("only run as a dedicated child test process")
	}
	root, _, env := fixture(t, selfExecutable(t))
	defer root.Close()
	s, err := startFixture(t, context.Background(), root, supported(),
		append(env, "ASP_TEST_BEHAVIOR=sleep"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	fmt.Printf("HELPER_PID=%d\n", s.cmd.Process.Pid)
	for {
		time.Sleep(time.Second) // test owner keeps running until the outer process kills it
	}
}

func runningProcess(pid int) bool {
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return false
	}
	// /proc/PID/stat wraps comm in parentheses (which may contain spaces).
	at := bytes.LastIndexByte(stat, ')')
	if at < 0 || len(stat) <= at+2 {
		return false
	}
	return stat[at+2] != 'Z' && stat[at+2] != 'X'
}

func TestHelperDiesWhenDaemonIsSIGKILLed(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	parent := exec.CommandContext(ctx, selfExecutable(t), "-test.run=^TestIsolatedHelperParent$", "-test.timeout=15s")
	parent.Env = append(os.Environ(), "ASP_ISOLATED_PARENT=1", "TMPDIR="+t.TempDir())
	stdout, err := parent.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	parent.Stderr = &stderr
	if err := parent.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if parent.ProcessState == nil {
			_ = parent.Process.Kill()
			_ = parent.Wait()
		}
	})
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		t.Fatalf("isolated daemon did not start a helper: %v (%s)", err, stderr.String())
	}
	var childPID int
	if _, err := fmt.Sscanf(strings.TrimSpace(line), "HELPER_PID=%d", &childPID); err != nil || childPID <= 1 {
		t.Fatalf("invalid isolated helper PID %q: %v (%s)", line, err, stderr.String())
	}
	if !runningProcess(childPID) {
		t.Fatalf("helper %d died before parent was killed", childPID)
	}
	// In particular, the locked spawning thread must survive scheduler/GC
	// activity while the daemon itself is healthy.
	time.Sleep(250 * time.Millisecond)
	if !runningProcess(childPID) {
		t.Fatalf("helper %d died while parent was alive", childPID)
	}
	if err := parent.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = parent.Wait() // expected SIGKILL, not a normal exit
	limit := time.Now().Add(3 * time.Second)
	for runningProcess(childPID) && time.Now().Before(limit) {
		time.Sleep(25 * time.Millisecond)
	}
	if runningProcess(childPID) {
		t.Fatalf("orphaned helper %d is still alive after parent SIGKILL", childPID)
	}
}

func TestParentDeathSignalDoesNotKillHealthyHelper(t *testing.T) {
	root, _, env := fixture(t, selfExecutable(t))
	defer root.Close()
	s, err := startFixture(t, context.Background(), root, supported(), env)
	if err != nil {
		t.Fatal(err)
	}
	if s.cmd.SysProcAttr == nil || s.cmd.SysProcAttr.Pdeathsig != syscall.SIGKILL {
		t.Fatal("helper is missing Linux parent-death SIGKILL")
	}
	pid := s.cmd.Process.Pid
	runtime.GC()
	for i := 0; i < 20; i++ {
		runtime.Gosched()
	}
	if got, err := s.Exchange(context.Background(), pcm()); err != nil || len(got) != OutputBytes {
		t.Fatalf("helper died despite healthy daemon: %v", err)
	}
	if err := s.Close(); err != nil && !strings.Contains(err.Error(), "signal: killed") {
		t.Fatalf("shutdown failed: %v", err)
	}
	if _, err := os.Stat(fmt.Sprintf("/proc/%d", pid)); !os.IsNotExist(err) {
		t.Fatalf("Wait did not reap child %d: %v", pid, err)
	}
}
