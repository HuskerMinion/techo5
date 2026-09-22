package main

import (
	"syscall"
	"testing"
	"time"
)

// The read the driver refuses has to be one it would accept. A Dot's driver complained about every
// read the bridge made because the bridge asked for 64 KiB of a buffer that holds a kilobyte or two.
func TestTheReadFitsTheDriversBuffer(t *testing.T) {
	// One kibibyte is at or under BT_BUFFER_SIZE in every vendor build of stp_chrdev_bt.c, and the
	// framer puts packets back together across reads, so nothing needs a bigger one.
	if readSize > 1024 {
		t.Errorf("reading %d bytes at a time: the MTK driver refuses anything over its own buffer", readSize)
	}
}

// A refused read used to be asked again the same instant, which is what filled the kernel log and
// woke the CPUs. Each failure has to wait longer than the last, and the log has to hear about the
// run once rather than once per attempt.
func TestAFailedReadWaitsAndIsSaidOnce(t *testing.T) {
	var b backoff

	wait, report, giveUp := b.fail(syscall.EINVAL)
	if !report {
		t.Error("the first failure of a run was not worth printing")
	}
	if giveUp || wait <= 0 {
		t.Errorf("the first failure waited %v, gave up %v: want a short wait and another try", wait, giveUp)
	}

	last := wait
	for i := 1; i < 20; i++ {
		wait, report, giveUp = b.fail(syscall.EINVAL)
		if report {
			t.Fatalf("failure %d printed a line of its own", i+1)
		}
		if giveUp {
			t.Fatalf("gave up after %d failures, want it to keep waiting a while", i+1)
		}
		if wait < last {
			t.Errorf("failure %d waited %v, less than the %v before it", i+1, wait, last)
		}
		last = wait
	}
	if last > backoffMax {
		t.Errorf("waiting %v, want no longer than %v", last, backoffMax)
	}

	// A different complaint is worth saying, even in the middle of a run.
	if _, report, _ = b.fail(syscall.ENODEV); !report {
		t.Error("a different error was swallowed by the run before it")
	}
}

// A driver that has gone away is not fixed by waiting: the run ends the bridge, and init opens the
// device again.
func TestAFailureThatNeverStopsGivesUp(t *testing.T) {
	var b backoff
	var giveUp bool
	for i := 0; i < giveUpAfter; i++ {
		_, _, giveUp = b.fail(syscall.EINVAL)
	}
	if !giveUp {
		t.Errorf("still waiting after %d failures in a row", giveUpAfter)
	}
}

// A read that works ends the run, so the next bad patch is reported and waits from the start again.
func TestAReadThatWorksEndsTheRun(t *testing.T) {
	var b backoff
	b.fail(syscall.EINVAL)
	b.fail(syscall.EINVAL)
	if refused := b.ok(); refused != 2 {
		t.Errorf("the run counted %d refused reads, want 2", refused)
	}
	if refused := b.ok(); refused != 0 {
		t.Errorf("a second reading says %d were refused, want nothing left to report", refused)
	}

	wait, report, _ := b.fail(syscall.EINVAL)
	if !report {
		t.Error("the first failure after a good read was not printed")
	}
	if wait != backoffFirst {
		t.Errorf("it waited %v, want the first wait of %v again", wait, backoffFirst)
	}
}

// The waits are short enough to keep a busy controller served and long enough to be a wait.
func TestTheWaitsAreSane(t *testing.T) {
	if backoffFirst <= 0 || backoffFirst > 50*time.Millisecond {
		t.Errorf("the first wait is %v", backoffFirst)
	}
	if backoffMax < backoffFirst {
		t.Errorf("the longest wait %v is shorter than the first %v", backoffMax, backoffFirst)
	}
}
