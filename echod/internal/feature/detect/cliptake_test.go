package detect

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/takes"
)

// takesFollowTheRig has the rig's clips ask the rig's settings whether takes still go to wakesmith.
func (r *sendRig) takesFollowTheRig() *sendRig {
	r.c.wantsTakes = func() bool { return r.cfg.Target == config.SendWakesmith && r.cfg.On() }
	return r
}

// takesLeft is how many takes are on the device.
func (r *sendRig) takesLeft() int {
	files, _ := filepath.Glob(filepath.Join(r.c.dir, takeKind, "*.wav"))
	return len(files)
}

// take keeps take n of a run, n seconds after the rig's start, and says where.
func (r *sendRig) take(n int) string {
	r.t.Helper()
	r.c.keepTake(takes.Capture{Run: "a1b2c3d4e5f6", Index: n, Word: "hey_jarvis_v2", Front: "zserge", Frame: "heard",
		At: r.base.Add(time.Duration(n)*time.Second + 250*time.Millisecond), PCM: make([]int16, 9600)})
	files, _ := filepath.Glob(filepath.Join(r.c.dir, takeKind, fmt.Sprintf("*-take%02d-*.wav", n)))
	if len(files) != 1 {
		r.t.Fatalf("take %d: %d files", n, len(files))
	}
	return files[0]
}

// trainer is a wakesmith that keeps the meta of each upload, raw, and answers status.
type trainer struct {
	mu     sync.Mutex
	metas  []map[string]any
	audio  [][]byte
	status int
	hold   chan struct{} // when set, each upload waits for it
}

func (tr *trainer) serve(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		_, params, _ := mime.ParseMediaType(q.Header.Get("Content-Type"))
		mr := multipart.NewReader(q.Body, params["boundary"])
		var meta map[string]any
		var audio []byte
		for {
			p, err := mr.NextPart()
			if err != nil {
				break
			}
			blob, _ := io.ReadAll(p)
			switch p.FormName() {
			case "meta":
				json.Unmarshal(blob, &meta)
			case "audio":
				audio = blob
			}
		}
		if tr.hold != nil {
			<-tr.hold
		}
		tr.mu.Lock()
		tr.metas, tr.audio = append(tr.metas, meta), append(tr.audio, audio)
		status := tr.status
		tr.mu.Unlock()
		if status != 0 {
			w.WriteHeader(status)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (tr *trainer) got() []map[string]any {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	return append([]map[string]any(nil), tr.metas...)
}

// drainBy drains, failing the test if that does not come back: a sender that spins on a take it may not
// send never returns.
func (r *sendRig) drainBy(d time.Duration) {
	r.t.Helper()
	done := make(chan struct{})
	go func() { r.drain(); close(done) }()
	select {
	case <-done:
	case <-time.After(d):
		r.t.Fatal("the sender never came back: it is spinning")
	}
}

// A take goes to wakesmith with the fields wakesmith 0.4.12 reads, the word where a clip has the model,
// and none of a clip's scores; once it is there it is gone from the device.
func TestATakeGoesToWakesmithWithItsOwnMeta(t *testing.T) {
	var tr trainer
	r := newSendRig(t, config.SendWakesmith, tr.serve(t).URL)
	r.cfg.Token = "walnut-token"
	wav := r.take(7)
	disk, _ := os.ReadFile(wav)
	r.drainBy(5 * time.Second)

	got := tr.got()
	if len(got) != 1 {
		t.Fatalf("%d uploads, want 1", len(got))
	}
	m := got[0]
	name := strings.TrimSuffix(filepath.Base(wav), ".wav")
	want := map[string]any{
		"kind": "take", "word": "hey_jarvis_v2", "index": 7.0, "run": "a1b2c3d4e5f6", "frame": "heard", "front": "zserge",
		"device": "Kitchen", "name": name, "at": r.base.Add(7 * time.Second).Format(time.RFC3339),
	}
	for k, v := range want {
		if m[k] != v {
			t.Errorf("meta %q = %#v, want %#v", k, m[k], v)
		}
	}
	for _, k := range []string{"model", "slot", "score", "cutoff"} {
		if _, ok := m[k]; ok {
			t.Errorf("a take's meta has %q", k)
		}
	}
	if len(m) != len(want) {
		t.Errorf("meta has %d fields, want %d: %v", len(m), len(want), m)
	}
	if string(tr.audio[0]) != string(disk) || len(disk) != 44+2*9600 {
		t.Error("the audio part is not the take as a 16 kHz mono WAV")
	}
	if _, err := os.Stat(wav); err == nil {
		t.Error("a take that reached wakesmith is still on the device")
	}
	if _, err := os.Stat(sidecar(wav)); err == nil {
		t.Error("a sent take's sidecar is still on the device")
	}
}

// at is exact UTC to the second, as the wake clips send it, and the name is plain: digits, letters and
// dashes, starting with a digit.
func TestATakesNameAndTimeAreWhatWakesmithTakes(t *testing.T) {
	r := newSendRig(t, config.SendWakesmith, "http://127.0.0.1:1")
	r.c.keepTake(takes.Capture{Run: "../X.y", Index: 3, Word: "w", At: time.Date(2026, 11, 17, 4, 5, 6, 789e6, time.FixedZone("x", -7*3600)), PCM: []int16{1}})
	files, _ := filepath.Glob(filepath.Join(r.c.dir, takeKind, "*.wav"))
	if len(files) != 1 {
		t.Fatalf("%d takes", len(files))
	}
	name := strings.TrimSuffix(filepath.Base(files[0]), ".wav")
	if !regexp.MustCompile(`^[0-9][0-9a-z-]*$`).MatchString(name) || name != "20261117-110506-789-take03-" {
		t.Errorf("name %q", name)
	}
	m := r.meta(files[0])
	if m.At != "2026-11-17T11:05:06Z" || m.Kind != takeKind || m.Index != 3 || m.Run != "" {
		t.Errorf("sidecar %+v", m)
	}
}

// Nothing empty is ever kept to be sent.
func TestAnEmptyTakeIsNotKept(t *testing.T) {
	r := newSendRig(t, config.SendWakesmith, "http://127.0.0.1:1")
	r.c.keepTake(takes.Capture{Run: "ab", Index: 1, Word: "w", At: r.base})
	if _, n := r.s.next(r.cfg); n != 0 {
		t.Errorf("%d to send", n)
	}
}

// With Keep wake clips off, takes still go, one at a time and paced as clips are, and when the last is
// gone the sender stops and says Keep is off: it neither spins on them nor stops short of them.
func TestTakesAreSentWithKeepWakeClipsOff(t *testing.T) {
	var tr trainer
	r := newSendRig(t, config.SendWakesmith, tr.serve(t).URL)
	r.c.on.Store(false)
	for n := 1; n <= 3; n++ {
		r.take(n)
	}
	r.drainBy(5 * time.Second)
	got := tr.got()
	if len(got) != 3 {
		t.Fatalf("%d takes sent with Keep off, want 3", len(got))
	}
	for i, m := range got {
		if m["index"] != float64(i+1) {
			t.Errorf("upload %d is take %v: not oldest first", i, m["index"])
		}
	}
	if len(r.waits) != 3 || r.waits[0] != sendEvery {
		t.Errorf("waited %v, want a pace after each of the three", r.waits)
	}
	if n, status := r.s.Unsent(); n != 0 || status != statusKeepOff {
		t.Errorf("unsent %d, status %q", n, status)
	}
	// Again with nothing to send: straight back, no request, no wait.
	r.drainBy(5 * time.Second)
	if len(tr.got()) != 3 || len(r.waits) != 3 {
		t.Error("a drain with nothing to send did something")
	}
}

// A take the trainer cannot be reached for with Keep off is backed off from, not tried in a loop, and a
// take it refuses for good is marked and not tried again.
func TestATakeThatCannotGoIsBackedOffFromWithKeepOff(t *testing.T) {
	tr := trainer{status: http.StatusServiceUnavailable}
	r := newSendRig(t, config.SendWakesmith, tr.serve(t).URL)
	r.c.on.Store(false)
	r.stopAt = 3
	wav := r.take(1)
	r.drainBy(5 * time.Second)
	if len(tr.got()) != 3 || len(r.waits) != 3 || r.waits[0] != sendBackoffMin || r.waits[1] != 2*sendBackoffMin {
		t.Errorf("%d tries, waits %v: want three tries backed off", len(tr.got()), r.waits)
	}
	if _, err := os.Stat(wav); err != nil {
		t.Error("a take that did not go was let go of")
	}

	tr.mu.Lock()
	tr.status = http.StatusUnprocessableEntity // a refusal for good: wakesmith's for a silent take
	tr.mu.Unlock()
	r.stopAt, r.waits = 0, nil
	r.drainBy(5 * time.Second)
	if _, err := os.Stat(wav); err == nil {
		t.Error("a take wakesmith refused for good is still on the device")
	}
	if _, err := os.Stat(sidecar(wav)); err == nil {
		t.Error("a refused take's sidecar is still on the device")
	}
	n := len(tr.got())
	r.drainBy(5 * time.Second)
	if len(tr.got()) != n {
		t.Error("a take refused for good was sent again")
	}
}

// Takes go to wakesmith alone: not to Tater Trainer nor a custom receiver, Keep on or off.
func TestTakesGoOnlyToWakesmith(t *testing.T) {
	for _, target := range []string{config.SendTater, config.SendCustom} {
		var tr trainer
		r := newSendRig(t, config.SendWakesmith, tr.serve(t).URL)
		r.take(1) // kept while it was wakesmith; then the target moved, as by hand in the settings file
		r.cfg.Target = target
		for _, keep := range []bool{true, false} {
			r.c.on.Store(keep)
			r.drainBy(5 * time.Second)
			if _, n := r.s.next(r.cfg); n != 0 {
				t.Errorf("%s, keep %v: %d to send", target, keep, n)
			}
		}
		if len(tr.got()) != 0 {
			t.Errorf("%s was sent a take", target)
		}
	}
}

// Turning Keep wake clips off deletes the clips and, while wakesmith is where takes go, leaves the takes
// waiting, which are then sent.
func TestTurningKeepOffLeavesTheTakes(t *testing.T) {
	var tr trainer
	r := newSendRig(t, config.SendWakesmith, tr.serve(t).URL).takesFollowTheRig()
	clipWav := r.clip("wake", 1, "m")
	takeWav := r.take(2)
	if _, n := r.s.next(r.cfg); n != 2 {
		t.Fatalf("%d to send, want the clip and the take", n)
	}
	r.c.enable(false)
	if _, err := os.Stat(clipWav); err == nil {
		t.Error("the clip was not purged")
	}
	if _, err := os.Stat(takeWav); err != nil {
		t.Fatal("the purge took the take waiting to be sent")
	}
	if it, n := r.s.next(r.cfg); n != 1 || it.meta.Kind != takeKind {
		t.Fatalf("%d to send after the purge", n)
	}
	r.drainBy(5 * time.Second)
	if got := tr.got(); len(got) != 1 || got[0]["kind"] != "take" {
		t.Errorf("sent %v", got)
	}
}

// With takes going nowhere (the target is not wakesmith), turning Keep wake clips off deletes them too.
func TestTurningKeepOffWithNowhereForTakesDeletesThem(t *testing.T) {
	for _, target := range []string{config.SendOff, config.SendTater, config.SendCustom} {
		r := newSendRig(t, config.SendWakesmith, "http://127.0.0.1:1").takesFollowTheRig()
		r.clip("wake", 1, "m")
		r.take(2)
		r.cfg.Target = target
		r.c.enable(false)
		if n := r.takesLeft(); n != 0 {
			t.Errorf("%q: %d takes left after Keep went off", target, n)
		}
		if _, err := os.Stat(r.c.dir); err == nil {
			t.Errorf("%q: folders left behind", target)
		}
	}
}

// Takes don't linger once they have nowhere to go: the target moving off wakesmith, or its address
// changing, deletes them; saving wakesmith again at the same address does not.
func TestTakesGoWhenTheTargetOrItsAddressChanges(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		name string
		next config.WakeSend
		left int
	}{
		{"saved again", config.WakeSend{Target: config.SendWakesmith, URL: "http://trainer.local:8765"}, 1},
		{"another address", config.WakeSend{Target: config.SendWakesmith, URL: "http://other.local:8765"}, 0},
		{"Tater", config.WakeSend{Target: config.SendTater, URL: "http://trainer.local:8789"}, 0},
		{"Custom", config.WakeSend{Target: config.SendCustom, URL: "http://trainer.local:8765"}, 0},
		{"Off", config.WakeSend{}, 0},
	} {
		d := setupSend(t)
		if err := d.SetSend(ctx, config.WakeSend{Target: config.SendWakesmith, URL: "http://trainer.local:8765"}); err != nil {
			t.Fatal(err)
		}
		d.sender.c.keepTake(takes.Capture{Run: "ab", Index: 1, Word: "w", At: time.Now(), PCM: []int16{1}})
		if err := d.SetSend(ctx, c.next); err != nil {
			t.Fatal(err)
		}
		files, _ := filepath.Glob(filepath.Join(d.sender.c.dir, takeKind, "*.wav"))
		if len(files) != c.left {
			t.Errorf("%s: %d takes left, want %d", c.name, len(files), c.left)
		}
	}
}

// Record takes of a word going from on to off deletes the takes waiting; it staying off, or coming on,
// does not.
func TestTheTakesSwitchGoingOffDeletesTheTakes(t *testing.T) {
	r := newSendRig(t, config.SendWakesmith, "http://127.0.0.1:1")
	r.take(1)
	r.c.takesSwitched(false) // it was off: nothing changed
	r.c.takesSwitched(true)
	if n := r.takesLeft(); n != 1 {
		t.Fatalf("%d takes left with the switch coming on", n)
	}
	r.c.takesSwitched(false)
	if n := r.takesLeft(); n != 0 {
		t.Errorf("%d takes left after the switch went off", n)
	}
}

// Letting go of the takes cuts short a take's upload, never a clip's.
func TestDroppingTheTakesCutsOnlyATakesUpload(t *testing.T) {
	r := newSendRig(t, config.SendWakesmith, "http://127.0.0.1:1")
	cut := ""
	r.s.mu.Lock()
	r.s.abort, r.s.abortKind = func() { cut = "wake" }, "wake"
	r.s.mu.Unlock()
	r.c.abortSend = r.s.abortSend
	r.c.dropTakes("test")
	if cut != "" {
		t.Error("dropping the takes cut a clip's upload")
	}
	r.s.mu.Lock()
	r.s.abort, r.s.abortKind = func() { cut = takeKind }, takeKind
	r.s.mu.Unlock()
	r.c.dropTakes("test")
	if cut != takeKind {
		t.Error("dropping the takes left a take's upload going")
	}
}

// A clip let into the queue while Keep was on and refused at the send once it went off is skipped, and
// the sender goes on to the takes rather than coming back to it.
func TestKeepGoingOffBetweenTheQueueAndTheSendSkipsOnlyTheClip(t *testing.T) {
	r := newSendRig(t, config.SendWakesmith, "http://127.0.0.1:1")
	r.clip("wake", 1, "m")
	r.take(2)
	it, _ := r.s.next(r.cfg)
	if it.meta.Kind != "wake" {
		t.Fatalf("first is %s", it.meta.Kind)
	}
	r.c.on.Store(false)
	if out, _ := r.s.send(context.Background(), r.cfg, it); out != outSkip {
		t.Fatalf("the clip: %v", out)
	}
	if it, n := r.s.next(r.cfg); n != 1 || it.meta.Kind != takeKind {
		t.Fatalf("after the skip: %d to send", n)
	}
}

// Keep wake clips going off cuts short a clip's upload, never a take's.
func TestAPurgeDoesNotCutATakesUploadShort(t *testing.T) {
	tr := trainer{hold: make(chan struct{})}
	r := newSendRig(t, config.SendWakesmith, tr.serve(t).URL).takesFollowTheRig()
	r.c.abortSend = r.s.abortSend
	r.take(1)
	done := make(chan struct{})
	go func() { r.drain(); close(done) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		r.s.mu.Lock()
		up := r.s.abort != nil
		r.s.mu.Unlock()
		if up {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the upload never started")
		}
		time.Sleep(time.Millisecond)
	}
	r.c.enable(false)
	close(tr.hold)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the sender never came back")
	}
	if len(tr.got()) != 1 {
		t.Fatal("the take did not arrive")
	}
	if _, n := r.s.next(r.cfg); n != 0 {
		t.Errorf("%d left: the take was cut short and is waiting again", n)
	}
}

// A take kept before the target was set, as one recorded for another target and then left, is not sent,
// like a clip.
func TestATakeFromBeforeTheTargetIsNotSent(t *testing.T) {
	r := newSendRig(t, config.SendWakesmith, "http://127.0.0.1:1")
	r.take(1)
	r.cfg.Since = r.base.Add(time.Hour).Format(time.RFC3339)
	if _, n := r.s.next(r.cfg); n != 0 {
		t.Errorf("%d to send", n)
	}
}

// Writing a take wakes the sender, Keep on or off.
func TestWritingATakeWakesTheSender(t *testing.T) {
	r := newSendRig(t, config.SendWakesmith, "http://127.0.0.1:1")
	r.c.on.Store(false)
	r.c.changed = r.s.wake
	r.take(1)
	select {
	case <-r.s.nudge:
	default:
		t.Fatal("the take did not wake the sender")
	}
}

// A take is not kept when takes have nowhere to go: Send wake clips to is not wakesmith.
func TestATakeIsNotKeptWhenTakesHaveNowhereToGo(t *testing.T) {
	for _, target := range []string{config.SendOff, config.SendTater, config.SendCustom} {
		r := newSendRig(t, target, "http://127.0.0.1:1")
		r.c.keepTake(takes.Capture{Run: "ab", Index: 1, Word: "w", At: r.base, PCM: []int16{1}})
		if n := r.takesLeft(); n != 0 {
			t.Errorf("%q: a take was kept with nowhere to go", target)
		}
	}
}

// A take recorded before the takes were let go of, and handed on late from its own goroutine, is not
// written after them; one recorded after is.
func TestATakeArrivingAfterTheTakesWentIsNotKept(t *testing.T) {
	r := newSendRig(t, config.SendWakesmith, "http://127.0.0.1:1")
	clock := r.base.Add(time.Minute)
	r.c.now = func() time.Time { return clock }
	recorded := clock.Add(-time.Second)
	r.c.dropTakes("the target moved")
	r.c.keepTake(takes.Capture{Run: "ab", Index: 1, Word: "w", At: recorded, PCM: []int16{1}})
	if n := r.takesLeft(); n != 0 {
		t.Error("a take recorded before the drop was written after it")
	}
	r.c.keepTake(takes.Capture{Run: "ab", Index: 2, Word: "w", At: clock.Add(time.Second), PCM: []int16{1}})
	if n := r.takesLeft(); n != 1 {
		t.Errorf("%d takes after one recorded after the drop", n)
	}
	// The same for a purge that took the takes (Keep off with the target moved).
	clock = clock.Add(time.Minute)
	r.cfg.Target = config.SendTater
	r.c.enable(false)
	r.cfg.Target = config.SendWakesmith
	r.c.keepTake(takes.Capture{Run: "ab", Index: 3, Word: "w", At: clock.Add(-time.Second), PCM: []int16{1}})
	if n := r.takesLeft(); n != 0 {
		t.Error("a take recorded before the purge was written after it")
	}
}

// Takes refused for good don't count toward stopping the sending: an older wakesmith refusing every
// take must not halt the wake clips behind them.
func TestRefusedTakesDoNotHaltTheClips(t *testing.T) {
	var refused atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		_, params, _ := mime.ParseMediaType(q.Header.Get("Content-Type"))
		mr := multipart.NewReader(q.Body, params["boundary"])
		kind := ""
		for {
			p, err := mr.NextPart()
			if err != nil {
				break
			}
			blob, _ := io.ReadAll(p)
			if p.FormName() == "meta" {
				var m map[string]any
				json.Unmarshal(blob, &m)
				kind, _ = m["kind"].(string)
			}
		}
		if kind == takeKind {
			refused.Add(1)
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer srv.Close()
	r := newSendRig(t, config.SendWakesmith, srv.URL)
	for n := 1; n <= sendFailuresMax+2; n++ {
		r.take(n)
	}
	wav := r.clip("wake", sendFailuresMax+5, "m")
	r.drainBy(10 * time.Second)
	if n := refused.Load(); n != sendFailuresMax+2 {
		t.Errorf("%d takes refused, want all %d tried", n, sendFailuresMax+2)
	}
	if r.meta(wav).SentAt == "" {
		t.Error("the wake clip behind the refused takes was not sent")
	}
	if why, halted := r.s.isHalted(r.cfg); halted {
		t.Errorf("sending stopped: %s", why)
	}
}

// Takes are kept to the same budget as each kind of clip.
func TestTakesArePrunedLikeClips(t *testing.T) {
	r := newSendRig(t, config.SendWakesmith, "http://127.0.0.1:1")
	r.c.keep = 3
	for n := 1; n <= 5; n++ {
		r.take(n)
	}
	if files, _ := filepath.Glob(filepath.Join(r.c.dir, takeKind, "*.wav")); len(files) != 3 {
		t.Errorf("%d takes kept, want the newest 3", len(files))
	}
}
