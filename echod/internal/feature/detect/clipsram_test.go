package detect

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/lib/rootonly"
)

// ramRig is a sendRig whose recorder has a folder in RAM, as on a Linux device with sending on.
func ramRig(t *testing.T, url string) *sendRig {
	r := newSendRig(t, config.SendTater, url)
	r.c.ram = t.TempDir()
	r.c.ramCap = ramCapFor(1 << 30)
	r.c.waits = func(kind string) bool { return r.cfg.On() && r.cfg.Sends(kind) }
	return r
}

// whole writes a clip of kind as long as a real one, n seconds after the rig's start.
func (r *sendRig) whole(kind string, n int) {
	r.t.Helper()
	r.c.write(clip{kind: kind, at: r.base.Add(time.Duration(n) * time.Second), model: "Hey_Jarvis_v2",
		peak: 0.79, cutoff: 0.9, pcm: make([]int16, clipRate*clipSeconds)})
}

func wavs(t *testing.T, dir, kind string) int {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join(dir, kind, "*.wav"))
	return len(files)
}

// An unreachable trainer: nothing listens on port 1.
const nowhere = "http://127.0.0.1:1"

// A clip of a kind that is sent waits in RAM, is counted and listed there, and is let go of once the
// trainer has it: it is never written to the device's folder.
func TestASentClipIsNeverWrittenToTheDevicesFolder(t *testing.T) {
	// The server counts in its own goroutine, so the count is atomic for the race detector.
	var got atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		got.Add(1)
		fmt.Fprint(w, `{"ok": true}`)
	}))
	defer srv.Close()
	r := ramRig(t, srv.URL)

	r.whole("nearmiss", 1)
	if wavs(t, r.c.dir, "nearmiss") != 0 || wavs(t, r.c.ram, "nearmiss") != 1 {
		t.Fatalf("on the device %d, in RAM %d; want 0 and 1", wavs(t, r.c.dir, "nearmiss"), wavs(t, r.c.ram, "nearmiss"))
	}
	if r.c.counts["nearmiss"] != 1 {
		t.Fatalf("a clip in RAM is not counted: %v", r.c.counts)
	}
	if l, err := r.c.list("", 0); err != nil || l.Total != 1 {
		t.Fatalf("a clip in RAM is not listed: %+v %v", l, err)
	}
	if l, _ := r.c.list("", 0); len(l.Clips) == 1 {
		if _, err := r.c.audio(l.Clips[0].ID, 0); err != nil {
			t.Fatalf("a clip in RAM cannot be fetched: %v", err)
		}
	}
	if q := r.s.queue(r.cfg); len(q) != 1 {
		t.Fatalf("unsent: %d, want the clip in RAM", len(q))
	}

	r.drain()
	if got.Load() != 1 {
		t.Fatalf("the trainer got %d clips", got.Load())
	}
	if wavs(t, r.c.dir, "nearmiss") != 0 || wavs(t, r.c.ram, "nearmiss") != 0 {
		t.Fatalf("after it was sent: on the device %d, in RAM %d; want none", wavs(t, r.c.dir, "nearmiss"), wavs(t, r.c.ram, "nearmiss"))
	}
	if r.c.counts["nearmiss"] != 0 {
		t.Fatalf("a clip sent is still counted: %v", r.c.counts)
	}
	if n, _ := r.s.Unsent(); n != 0 {
		t.Fatalf("unsent %d", n)
	}
}

// A kind the owner chose not to send is kept on the device, as it always was: nothing else takes it off.
func TestAKindNotSentIsStillKeptOnTheDevice(t *testing.T) {
	r := ramRig(t, nowhere)
	r.cfg.Skip = []string{"wake"}
	r.whole("wake", 1)
	if wavs(t, r.c.dir, "wake") != 1 || wavs(t, r.c.ram, "wake") != 0 {
		t.Fatalf("on the device %d, in RAM %d; want 1 and 0", wavs(t, r.c.dir, "wake"), wavs(t, r.c.ram, "wake"))
	}

	// And with sending off, every kind.
	r.cfg.Target = config.SendOff
	r.whole("nearmiss", 2)
	if wavs(t, r.c.dir, "nearmiss") != 1 {
		t.Fatal("with sending off a clip did not go to the device")
	}
}

// A trainer that cannot be reached for ramWait has the clips moved to the device, still unsent, to wait
// there as before; one reached in time does not.
func TestClipsWaitingTooLongAreMovedToTheDevice(t *testing.T) {
	r := ramRig(t, nowhere)
	r.stopAt = 1
	r.whole("nearmiss", 1)
	r.s.now = func() time.Time { return r.base.Add(10 * time.Minute) }
	r.drain()
	if wavs(t, r.c.ram, "nearmiss") != 1 {
		t.Fatal("moved after ten minutes")
	}

	r.waits = nil
	r.s.now = func() time.Time { return r.base.Add(time.Second + ramWait) }
	r.drain()
	if wavs(t, r.c.dir, "nearmiss") != 1 || wavs(t, r.c.ram, "nearmiss") != 0 {
		t.Fatalf("after ramWait: on the device %d, in RAM %d; want 1 and 0", wavs(t, r.c.dir, "nearmiss"), wavs(t, r.c.ram, "nearmiss"))
	}
	files, _ := filepath.Glob(filepath.Join(r.c.dir, "nearmiss", "*.wav"))
	if m := r.meta(files[0]); m.SentAt != "" || m.SendFailed != 0 || m.Score != 0.79 {
		t.Fatalf("moved with its sidecar changed: %+v", m)
	}
	if q := r.s.queue(r.cfg); len(q) != 1 {
		t.Fatalf("a moved clip is not still to be sent: %d", len(q))
	}
	if r.c.counts["nearmiss"] != 1 {
		t.Fatalf("counts %v", r.c.counts)
	}
}

// A full folder in RAM sends the next clip to the device, and the sender moves the rest there too.
func TestAFullFolderInRAMSpillsToTheDevice(t *testing.T) {
	r := ramRig(t, nowhere)
	r.stopAt = 1
	r.c.ramCap = 2 * clipBytes
	for i := 1; i <= 3; i++ {
		r.whole("noise", i)
	}
	if wavs(t, r.c.ram, "noise") != 2 || wavs(t, r.c.dir, "noise") != 1 {
		t.Fatalf("in RAM %d, on the device %d; want 2 and 1", wavs(t, r.c.ram, "noise"), wavs(t, r.c.dir, "noise"))
	}
	if r.c.counts["noise"] != 3 {
		t.Fatalf("counts %v", r.c.counts)
	}
	r.drain()
	if wavs(t, r.c.ram, "noise") != 0 || wavs(t, r.c.dir, "noise") != 3 {
		t.Fatalf("after the sender looked: in RAM %d, on the device %d; want 0 and 3", wavs(t, r.c.ram, "noise"), wavs(t, r.c.dir, "noise"))
	}
}

// Turning sending off, or leaving a kind out, keeps what was waiting: it goes to the device.
func TestSendingTurnedOffKeepsWhatWasWaiting(t *testing.T) {
	r := ramRig(t, nowhere)
	r.whole("mistake", 1)
	r.cfg.Target = config.SendOff
	r.drain()
	if wavs(t, r.c.ram, "mistake") != 0 || wavs(t, r.c.dir, "mistake") != 1 {
		t.Fatalf("in RAM %d, on the device %d; want 0 and 1", wavs(t, r.c.ram, "mistake"), wavs(t, r.c.dir, "mistake"))
	}
}

// Turning Keep wake clips off deletes what waits in RAM too, and the start counts it.
func TestKeepOffClearsRAMAndRecountSeesIt(t *testing.T) {
	r := ramRig(t, nowhere)
	r.whole("wake", 1)
	r.whole("nearmiss", 2)
	r.c.counts = map[string]int{}
	r.c.recount()
	if r.c.counts["wake"] != 1 || r.c.counts["nearmiss"] != 1 {
		t.Fatalf("recount missed RAM: %v", r.c.counts)
	}
	r.c.enable(false)
	for _, k := range clipKinds {
		if n := wavs(t, r.c.ram, k); n != 0 {
			t.Fatalf("%s: %d left in RAM", k, n)
		}
		if r.c.counts[k] != 0 {
			t.Fatalf("counts %v", r.c.counts)
		}
	}
	// And a stopping sender, which moves what waits, makes nothing again on the device.
	r.c.spill(func(string, time.Time) bool { return true })
	if _, err := os.Stat(r.c.dir); err == nil && wavs(t, r.c.dir, "wake") != 0 {
		t.Fatal("a clip came back after Keep went off")
	}
}

// Each kind keeps its newest clipsKept wherever its clips are.
func TestThePruneCountsBothFolders(t *testing.T) {
	r := ramRig(t, nowhere)
	r.c.keep = 2
	r.cfg.Skip = []string{"wake"}
	r.whole("wake", 1) // on the device
	r.cfg.Skip = nil
	r.whole("wake", 2) // in RAM
	r.whole("wake", 3)
	if n := wavs(t, r.c.dir, "wake") + wavs(t, r.c.ram, "wake"); n != 2 || wavs(t, r.c.dir, "wake") != 0 {
		t.Fatalf("kept %d (on the device %d); want the newest two, both in RAM", n, wavs(t, r.c.dir, "wake"))
	}
}

// A clip sent from RAM whose sidecar cannot be removed is not sent again on every pass.
func TestASentClipThatCannotBeRemovedIsNotSentAgain(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root removes from a read-only folder")
	}
	// The server counts in its own goroutine, so the count is atomic for the race detector.
	var got atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		got.Add(1)
		fmt.Fprint(w, `{"ok": true}`)
	}))
	defer srv.Close()
	r := ramRig(t, srv.URL)
	r.whole("nearmiss", 1)
	dir := filepath.Join(r.c.ram, "nearmiss")
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o700)

	r.drain()
	r.drain()
	if got.Load() != 1 {
		t.Fatalf("the trainer got it %d times", got.Load())
	}
	if q := r.s.queue(r.cfg); len(q) != 0 {
		t.Fatalf("still queued: %d", len(q))
	}
}

func TestTheRAMCapFollowsTheBoard(t *testing.T) {
	for _, c := range []struct{ total, want int64 }{
		{0, 4 << 20}, {256 << 20, 4 << 20}, {512 << 20, 8 << 20}, {1 << 30, 16 << 20}, {2 << 30, 16 << 20},
	} {
		if got := ramCapFor(c.total); got != c.want {
			t.Errorf("ramCapFor(%d) = %d, want %d", c.total, got, c.want)
		}
	}
}

// The folder in RAM is used only if it is root's own: made now, or there already as root would have
// made it. One another user made first, one anybody can write to, or a link is not where the house's
// voices wait, and clips are then kept on the device as they are without a tmpfs.
func TestTheFolderInRAMIsOnlyOneRootWouldHaveMade(t *testing.T) {
	var st unix.Statfs_t
	if err := unix.Statfs("/dev/shm", &st); err != nil || int64(st.Type) != tmpfsMagic {
		t.Skip("no tmpfs at /dev/shm to make the folder in")
	}
	base, err := os.MkdirTemp("/dev/shm", "techo5-ramdir-")
	if err != nil {
		t.Skip("no folder could be made in /dev/shm:", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(base) })

	old := rootonly.Owner
	t.Cleanup(func() { rootonly.Owner = old })
	rootonly.Owner = os.Getuid()

	clips := filepath.Join(base, "clips")
	if got := ramDirFor(clips); got != clips {
		t.Errorf("a new folder in RAM was %q, want %q", got, clips)
	}
	if got := ramDirFor(clips); got != clips {
		t.Errorf("the folder made before was %q the second time, want %q", got, clips)
	}

	open := filepath.Join(base, "open")
	_ = os.Mkdir(open, 0o777)
	_ = os.Chmod(open, 0o777)
	if got := ramDirFor(open); got != "" {
		t.Errorf("a folder anybody can write to was used: %q", got)
	}

	link := filepath.Join(base, "link")
	_ = os.Symlink(clips, link)
	if got := ramDirFor(link); got != "" {
		t.Errorf("a link was used: %q", got)
	}

	rootonly.Owner = os.Getuid() + 1
	if got := ramDirFor(clips); got != "" {
		t.Errorf("another user's folder was used: %q", got)
	}
}
