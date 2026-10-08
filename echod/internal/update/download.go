package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// A download survives a slow or broken link rather than racing a clock. Two Dots that had been up
// for two days fetched from the internet at about 20 kB/s while their LAN still ran at 480 kB/s, and
// a single ten-minute deadline turned every update into a failure. So the bytes already fetched are
// kept, a dropped or stalled connection picks up where it left off, and what gives up is a download
// that has stopped moving, not one that is merely slow.
var (
	// stallTimeout is how long a download may go without a single byte arriving before the
	// connection is dropped and tried again from where it got to.
	stallTimeout = 2 * time.Minute

	// downloadCap bounds the whole download, every try included, so a device is not held in an
	// installing state forever by a link that trickles.
	downloadCap = 2 * time.Hour

	// tries is how many tries in a row may fail without the file growing before the download gives
	// up. A try that fetched something starts the count again; downloadCap still bounds the lot.
	tries = 5

	// firstBackoff is the wait before the second try, tripling up to mostBackoff.
	firstBackoff = 10 * time.Second
	mostBackoff  = 2 * time.Minute

	// slowWindow and slowRate are what counts as a slow link: less than slowRate bytes a second over
	// a whole slowWindow. A release asset comes from a CDN that serves megabytes a second, so a
	// download that crawls below this is the link, not the server.
	slowWindow       = 2 * time.Minute
	slowRate   int64 = 50_000
)

// errStalled is a connection dropped because nothing arrived on it for stallTimeout.
var errStalled = errors.New("nothing arrived")

// errBadResume is a server that would not carry on from where the file got to: a 416, or a 206 for
// some other part of it. The kept bytes are dropped for it.
var errBadResume = errors.New("the server would not resume")

// badResumes is how many of those in a row are taken before the download is given one plain fetch
// from the beginning, and then no more tries. Without it, a server that cuts every connection and
// will not resume would have the file fetched from nothing, cut, and refused, until downloadCap.
const badResumes = 2

var (
	slowMu   sync.Mutex
	slowHook func(bytesPerSecond int64)
)

// OnSlowDownload names who to tell when a download is crawling: the Wi-Fi watcher, which can
// reassociate a link that has degraded while staying up. bytesPerSecond is 0 for a stall. It is
// called on the download's own goroutine, so it must return at once.
func OnSlowDownload(f func(bytesPerSecond int64)) {
	slowMu.Lock()
	slowHook = f
	slowMu.Unlock()
}

func reportSlow(bytesPerSecond int64) {
	slowMu.Lock()
	f := slowHook
	slowMu.Unlock()
	if f != nil {
		f(bytesPerSecond)
	}
}

// finalError is a failure another try would only repeat: the server says the file is not there, or
// what arrived is not what was offered.
type finalError struct{ error }

func (e finalError) Unwrap() error { return e.error }

func final(err error) error { return finalError{err} }

// partPath is where a download collects before it is proved. The hash is in the name, so bytes kept
// from one release are never taken for the start of another.
func partPath(to string, b Binary) string {
	id := b.SHA256
	if len(id) > 16 {
		id = id[:16]
	}
	return to + "." + id + ".part"
}

// partial is how much of b a previous try left at to, which is that much less room the rest needs.
func partial(to string, b Binary) int64 {
	st, err := os.Stat(partPath(to, b))
	if err != nil || st.Size() > b.Size {
		return 0
	}
	return st.Size()
}

// dropParts removes the partial downloads matching pattern, except keep.
func dropParts(pattern, keep string) {
	old, _ := filepath.Glob(pattern)
	for _, p := range old {
		if p != keep {
			os.Remove(p)
		}
	}
}

// sweepRootfs clears the rootfs directory of what earlier installs left, before its room is measured.
// What an earlier try fetched of this release is kept so the download can carry on from it; what was
// fetched of any other release is only taking up room. A whole tarball is left behind when the device
// goes down during slotctl: this release's is checked again as though it were a partial download,
// and any other is removed.
func sweepRootfs(to string, b Binary) {
	dir, part := filepath.Dir(to), partPath(to, b)
	dropParts(filepath.Join(dir, "techo5-rootfs-*.part"), part)
	dropParts(filepath.Join(dir, "techo5-rootfs-*.tar.gz"), to)
	if _, err := os.Stat(to); err == nil {
		os.Remove(part)
		if err := os.Rename(to, part); err != nil {
			os.Remove(to)
		}
	}
}

// download fetches the binary and proves it before it is allowed near /system. The hash is taken as the
// bytes go past rather than by reading the file back, so nothing has to hold sixteen megabytes in memory
// on a device with half a gigabyte. Bytes from an earlier try are read back once, to start the hash.
//
// The file appears at to only once it is whole and its hash is right. Until then it is kept beside it,
// so the next try, or the next install, carries on from where this one stopped.
func download(ctx context.Context, b Binary, to string, progress func(float32)) error {
	ctx, cancel := context.WithTimeout(ctx, downloadCap)
	defer cancel()

	part := partPath(to, b)
	dropParts(to+".*.part", part)

	wait := firstBackoff
	refused, lastTry := 0, false
	for failed := 1; ; failed++ {
		before := partial(to, b)
		err := fetch(ctx, b, part, progress)
		if err == nil {
			return os.Rename(part, to)
		}
		var f finalError
		if errors.As(err, &f) || lastTry {
			os.Remove(part)
			return err
		}
		switch {
		case errors.Is(err, errBadResume):
			if refused++; refused >= badResumes {
				slog.Warn("update: the server will not resume; one more try from the beginning", "err", err)
				os.Remove(part)
				lastTry = true
			}
		case before > 0:
			refused = 0 // it resumed
		}
		if ctx.Err() != nil {
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return fmt.Errorf("update: gave up after %v with %d of %d bytes: %w", downloadCap, partial(to, b), b.Size, err)
			}
			return err
		}
		have := partial(to, b)
		if have > before {
			failed, wait = 0, firstBackoff // it moved: this is a link that drops, not one that is gone
		}
		if failed >= tries {
			return err
		}
		slog.Warn("update: download interrupted; trying again from where it got to",
			"have", have, "size", b.Size, "in", wait, "err", err)
		select {
		case <-ctx.Done():
			return err
		case <-time.After(wait):
		}
		wait = min(wait*3, mostBackoff)
	}
}

// fetch is one try: it carries on from whatever is in part and checks the whole file at the end.
func fetch(ctx context.Context, b Binary, part string, progress func(float32)) error {
	f, err := os.OpenFile(part, os.O_CREATE|os.O_RDWR, 0o755)
	if err != nil {
		return final(err)
	}
	defer f.Close()

	sum := sha256.New()
	have, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		return final(err)
	}
	if have > b.Size {
		have = 0
	}
	if have > 0 {
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return final(err)
		}
		if _, err := io.CopyN(sum, f, have); err != nil {
			sum.Reset()
			have = 0
		}
	}
	if err := f.Truncate(have); err != nil {
		return final(err)
	}

	start := have
	if have < b.Size {
		if start, have, err = receive(ctx, b, f, sum, have, progress); err != nil {
			return err
		}
	} else if progress != nil {
		progress(1)
	}
	if err := f.Sync(); err != nil {
		return final(fmt.Errorf("update: writing %s: %w", part, err))
	}

	// More than offered is the wrong file. Less, with the connection closed cleanly, is a body that
	// ended early (a short 206, or a proxy that closes without a length), so the next try carries on.
	if have > b.Size {
		return final(fmt.Errorf("update: %d bytes, offered as %d", have, b.Size))
	}
	if have < b.Size {
		return fmt.Errorf("update: the connection closed at %d of %d bytes", have, b.Size)
	}
	if got := hex.EncodeToString(sum.Sum(nil)); got != b.SHA256 {
		err := fmt.Errorf("update: hash %s, offered as %s", got, b.SHA256)
		if start > 0 {
			// The kept bytes may be what is wrong. Once more from nothing settles it.
			f.Truncate(0)
			return fmt.Errorf("%w; starting over from the beginning", err)
		}
		return final(err)
	}
	return nil
}

// receive asks for the file from byte have on, and appends what arrives to f and to sum. It returns
// where it really started (a server that ignores the range sends it all again) and how much f holds.
func receive(ctx context.Context, b Binary, f *os.File, sum hash.Hash, have int64, progress func(float32)) (int64, int64, error) {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	// The watchdog covers the whole try, the connection and the response headers included: a server
	// that never answers is as stalled as one that stops halfway.
	dog := time.AfterFunc(stallTimeout, func() { cancel(errStalled) })
	defer dog.Stop()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, b.URL, nil)
	if err != nil {
		return have, have, final(err)
	}
	if have > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", have))
	}
	resp, err := client.Do(req)
	if err != nil {
		return have, have, stalled(ctx, fmt.Errorf("update: fetching %s: %w", b.URL, err))
	}
	defer resp.Body.Close()

	restart := func() error {
		sum.Reset()
		have = 0
		return f.Truncate(0)
	}
	switch code := resp.StatusCode; {
	case code == http.StatusPartialContent && have > 0:
		from, total, ok := contentRange(resp.Header.Get("Content-Range"))
		if total >= 0 && total != b.Size {
			return have, have, final(fmt.Errorf("update: %s is %d bytes, offered as %d", b.URL, total, b.Size))
		}
		if !ok || from != have {
			restart()
			return have, have, fmt.Errorf("update: asked for %s from byte %d, sent %q: %w", b.URL, have, resp.Header.Get("Content-Range"), errBadResume)
		}
		slog.Info("update: resuming the download", "from", have, "size", b.Size)
	case code == http.StatusOK:
		if have > 0 {
			slog.Info("update: the server sent the whole file again; starting over", "had", have)
			if err := restart(); err != nil {
				return have, have, final(err)
			}
		}
	case code == http.StatusRequestedRangeNotSatisfiable:
		restart()
		return have, have, fmt.Errorf("update: fetching %s from byte %d: %s: %w", b.URL, have, resp.Status, errBadResume)
	case code == http.StatusRequestTimeout || code == http.StatusTooManyRequests || code >= 500:
		return have, have, fmt.Errorf("update: fetching %s: %s", b.URL, resp.Status)
	default:
		return have, have, final(fmt.Errorf("update: fetching %s: %s", b.URL, resp.Status))
	}
	if _, err := f.Seek(have, io.SeekStart); err != nil {
		return have, have, final(err)
	}
	start := have

	// One byte past what was offered is all that is read. The size in the manifest is what the room
	// check was made against, and a server that keeps sending after it — a mirror serving the wrong
	// file, or something aiming a stream of zeros at a device with a couple of gigabytes of flash —
	// would otherwise write until the partition was full. The extra byte is what makes the length
	// check say "more than offered" rather than silently accepting a truncation.
	c := newCounter(io.LimitReader(resp.Body, b.Size-have+1), have, b.Size, progress)
	c.moved = func() { dog.Reset(stallTimeout) }
	w := &disk{to: f}
	n, err := io.Copy(io.MultiWriter(w, sum), c)
	have += n
	if w.err != nil {
		// The device's own storage, full or failing: another try would only fill it again.
		return start, have, final(fmt.Errorf("update: writing %s: %w", f.Name(), w.err))
	}
	if err != nil {
		return start, have, stalled(ctx, fmt.Errorf("update: downloading %s: %w", b.URL, err))
	}
	return start, have, nil
}

// disk is the partial file, remembering a failed write so it can be told from a failed read.
type disk struct {
	to  io.Writer
	err error
}

func (d *disk) Write(p []byte) (int, error) {
	n, err := d.to.Write(p)
	if err != nil {
		d.err = err
	}
	return n, err
}

// stalled names a failure for what it was when the watchdog caused it.
func stalled(ctx context.Context, err error) error {
	if errors.Is(context.Cause(ctx), errStalled) {
		slog.Warn("update: the download stalled", "for", stallTimeout)
		reportSlow(0)
		return fmt.Errorf("update: %w for %v: %w", errStalled, stallTimeout, err)
	}
	return err
}

// contentRange reads "bytes 100-199/200": where the part starts, and the whole size, or -1 when the
// server gives it as "*".
func contentRange(h string) (from, total int64, ok bool) {
	total = -1
	rest, found := strings.CutPrefix(h, "bytes ")
	if !found {
		return 0, total, false
	}
	span, size, found := strings.Cut(rest, "/")
	if !found {
		return 0, total, false
	}
	if size != "*" {
		if t, err := strconv.ParseInt(size, 10, 64); err == nil {
			total = t
		}
	}
	first, _, found := strings.Cut(span, "-")
	if !found {
		return 0, total, false
	}
	from, err := strconv.ParseInt(first, 10, 64)
	return from, total, err == nil
}

// counter reports how far a download has got, as a fraction, and notices a crawl.
type counter struct {
	from   io.Reader
	size   int64
	report func(float32)
	moved  func()

	read int64
	last float32

	// The rate is measured over windows of slowWindow.
	windowAt   time.Time
	windowRead int64
}

func newCounter(from io.Reader, read, size int64, report func(float32)) *counter {
	c := &counter{from: from, size: size, report: report, read: read, windowAt: time.Now(), windowRead: read}
	if report != nil && size > 0 {
		c.last = float32(read) / float32(size)
		report(c.last)
	}
	return c
}

func (c *counter) Read(p []byte) (int, error) {
	n, err := c.from.Read(p)
	c.read += int64(n)
	if n > 0 && c.moved != nil {
		c.moved()
	}

	if now := time.Now(); now.Sub(c.windowAt) >= slowWindow {
		rate := int64(float64(c.read-c.windowRead) / now.Sub(c.windowAt).Seconds())
		if rate < slowRate {
			slog.Warn("update: the download is slow", "bytes_per_second", rate, "have", c.read, "size", c.size)
			reportSlow(rate)
		}
		c.windowAt, c.windowRead = now, c.read
	}

	if c.report == nil || c.size <= 0 {
		return n, err
	}

	// Only when it has moved a percent, since every update is a message to Home Assistant.
	if at := float32(c.read) / float32(c.size); at-c.last >= 0.01 {
		c.last = at
		c.report(at)
	}
	return n, err
}
