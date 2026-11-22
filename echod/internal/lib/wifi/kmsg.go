package wifi

import (
	"errors"
	"os"
	"strings"
	"sync"
	"syscall"

	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
)

// The kernel's lines about the link are followed as they are logged, one record at a time from
// /dev/kmsg, rather than read with dmesg when they are wanted (#690). dmesg reads the whole ring
// buffer at once, and on the Dot's 3.18 kernel that is done with interrupts off on that CPU for long
// enough that the microphones' SPI reader misses frames: about 16 ms of audio on every reassociation.
// A read of /dev/kmsg takes the lock for one record only.

// kmsgPath is the kernel log device. A variable for the tests.
var kmsgPath = "/dev/kmsg"

// kernelKept is how many of the kernel's link lines are kept, the most Evidence reports.
const kernelKept = 8

var kernelTail struct {
	sync.Mutex
	lines []string
	once  sync.Once
}

// FollowKernel starts keeping the kernel's lines about the link, once; Evidence starts it too. Started
// early, the first evidence has the lines from before it.
func FollowKernel() {
	kernelTail.once.Do(func() {
		f, err := os.Open(kmsgPath)
		if err != nil {
			return
		}
		safe.Go("wifi kernel log", func() {
			defer f.Close()
			followKmsg(f)
		})
	})
}

// followKmsg reads records until the device fails. A record overwritten before it was read is an
// EPIPE, and the next read goes on from the oldest one left.
func followKmsg(f interface{ Read([]byte) (int, error) }) {
	buf := make([]byte, 8192)
	for {
		n, err := f.Read(buf)
		if errors.Is(err, syscall.EPIPE) {
			continue
		}
		if err != nil {
			return
		}
		keepKernelLine(kmsgMessage(string(buf[:n])))
	}
}

// kmsgMessage is a /dev/kmsg record's text: what follows "prefix;" up to the end of its first line
// (the lines after it are key=value pairs).
func kmsgMessage(rec string) string {
	_, msg, ok := strings.Cut(rec, ";")
	if !ok {
		return ""
	}
	msg, _, _ = strings.Cut(msg, "\n")
	return msg
}

func keepKernelLine(line string) {
	kept := kernelEvidence(line, 1)
	if len(kept) == 0 {
		return
	}
	kernelTail.Lock()
	defer kernelTail.Unlock()
	kernelTail.lines = append(kernelTail.lines, kept[0])
	if len(kernelTail.lines) > kernelKept {
		kernelTail.lines = append([]string(nil), kernelTail.lines[len(kernelTail.lines)-kernelKept:]...)
	}
}

// kernelLines is the link lines kept so far, oldest first.
func kernelLines() []string {
	kernelTail.Lock()
	defer kernelTail.Unlock()
	return append([]string(nil), kernelTail.lines...)
}
