package main

import "time"

// How the bridge reads from the vendor driver, and what it does when a read keeps failing.
//
// MediaTek's stp_chrdev_bt.c refuses a read whose count is larger than its own BT_BUFFER_SIZE and
// prints "BT_read: count > BT_BUFFER_SIZE" for every one of them. The bridge asked for 64 KiB and
// then asked again the instant it was refused, so on a Dot the kernel log was nothing but that line
// and the CPU governor was bringing cores up and down under a spin that did no work at all.
//
// Two things keep that from coming back: a read no larger than the driver will take, and a failure
// that waits instead of returning at once.
const (
	// readSize is how much is asked for in one read. The driver's own header is not in this tree, so
	// this is the size that is under every BT_BUFFER_SIZE the vendor sources use (one or two KiB)
	// rather than the exact limit. Asking for less than a whole packet costs nothing: what a read
	// returns is framed into packets across reads anyway (h4.go), and the driver hands over whatever
	// of its ring buffer fits.
	readSize = 1024

	// A read that fails waits, and waits longer while it goes on failing, so that something the
	// bridge cannot fix by asking again is not asked again thousands of times a second.
	backoffFirst = 5 * time.Millisecond
	backoffMax   = time.Second

	// giveUpAfter is how many failures in a row end the bridge. Waiting is right for a driver that is
	// busy or briefly unhappy; a run this long is a driver that is not coming back, and starting over
	// with a fresh open of the device is the better answer.
	giveUpAfter = 100
)

// backoff is a run of failed reads: how long to wait before the next one, and whether this one is
// worth a line on stderr. A driver that refuses every read refuses thousands, and a log that says so
// thousands of times hides everything else — so a run is announced when it starts and when it ends,
// and nothing is printed in between.
type backoff struct {
	n   int
	err string
}

// fail records a read that failed. It reports how long to wait before trying again, whether this
// failure is the start of a run and so worth printing, and whether the run has gone on long enough
// that the bridge should give up and let init start it again.
func (b *backoff) fail(err error) (wait time.Duration, report, giveUp bool) {
	if s := err.Error(); s != b.err {
		// A different complaint is a different run: the first of each is printed.
		b.err, b.n, report = s, 0, true
	}
	b.n++

	wait = backoffFirst
	for i := 1; i < b.n && wait < backoffMax; i++ {
		wait *= 2
	}
	if wait > backoffMax {
		wait = backoffMax
	}
	return wait, report, b.n >= giveUpAfter
}

// ok ends a run and reports how many reads it refused, so that a caller which said a run had begun
// can say it is over. Zero means there was nothing to say.
func (b *backoff) ok() int {
	n := b.n
	b.n, b.err = 0, ""
	return n
}
