package detect

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
)

// sendRig is a recorder with a sender pointed at a test server on this machine, which is let through
// the home network check, and a wait that records instead of sleeping.
type sendRig struct {
	t      *testing.T
	c      *clips
	s      *sender
	cfg    config.WakeSend
	base   time.Time
	waits  []time.Duration
	stopAt int // the wait that says to stop, 0 for never
}

func newSendRig(t *testing.T, target, url string) *sendRig {
	t.Helper()
	c := newClips(t.TempDir())
	c.on.Store(true)
	c.shortest = 0
	r := &sendRig{t: t, c: c, base: time.Now().UTC().Truncate(time.Second).Add(time.Minute)}
	r.cfg = config.WakeSend{Target: target, URL: url, Since: r.base.Add(-time.Hour).Format(time.RFC3339)}
	r.s = newSender(c)
	r.s.cfg = func() config.WakeSend { return r.cfg }
	c.wantsTakes = func() bool { return r.cfg.Target == config.SendWakesmith && r.cfg.On() }
	r.s.device = func() string { return "Kitchen" }
	r.s.allow = func(ip net.IP) bool { return ip.IsLoopback() || homeAddress(ip) }
	r.s.wait = func(ctx context.Context, d time.Duration, _ <-chan struct{}) bool {
		r.waits = append(r.waits, d)
		return r.stopAt == 0 || len(r.waits) < r.stopAt
	}
	return r
}

// clip writes a clip of kind, kept n seconds after the rig's start, and says where.
func (r *sendRig) clip(kind string, n int, model string) string {
	r.t.Helper()
	r.c.write(clip{kind: kind, at: r.base.Add(time.Duration(n) * time.Second), slot: 0, model: model,
		peak: 0.79, cutoff: 0.9, pcm: make([]int16, 1600)})
	at := r.base.Add(time.Duration(n) * time.Second).UTC().Format("20060102-150405")
	files, _ := filepath.Glob(filepath.Join(r.c.dir, kind, at+"-*.wav"))
	if len(files) == 0 {
		r.t.Fatal("no clip was written")
	}
	last := files[0]
	return last
}

func (r *sendRig) meta(wav string) clipMeta {
	r.t.Helper()
	blob, err := os.ReadFile(sidecar(wav))
	if err != nil {
		r.t.Fatal(err)
	}
	var m clipMeta
	if err := json.Unmarshal(blob, &m); err != nil {
		r.t.Fatal(err)
	}
	return m
}

func (r *sendRig) drain() { r.s.drain(context.Background()) }

func TestATaterUploadHasTheHeadersAndTheClipAsTheBody(t *testing.T) {
	var got http.Header
	var body []byte
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		got, path = q.Header.Clone(), q.URL.Path
		body, _ = io.ReadAll(q.Body)
		fmt.Fprint(w, `{"ok": true}`)
	}))
	defer srv.Close()
	r := newSendRig(t, config.SendTater, srv.URL)
	wav := r.clip("nearmiss", 1, "Hey_Jarvis_v2")
	r.drain()

	if path != "/api/upload_captured_audio_raw" {
		t.Fatalf("posted to %q", path)
	}
	disk, _ := os.ReadFile(wav)
	if string(body) != string(disk) {
		t.Fatalf("the body is not the clip: %d bytes, want %d", len(body), len(disk))
	}
	name := strings.TrimSuffix(filepath.Base(wav), ".wav")
	want := map[string]string{
		"Content-Type": "audio/wav", "X-Audio-Format": "wav", "X-Original-Name": name + ".wav",
		"X-Source-Device": "Kitchen", "X-Wake-Word": "hey_jarvis", "X-Event-Type": "close_miss",
		"X-Captured-At": r.base.Add(time.Second).Format(time.RFC3339), "X-Max-Probability": "201",
		"X-Probability-Cutoff": "230", "X-Notes": "techo5 nearmiss, score 0.790, cutoff 0.900",
	}
	for k, v := range want {
		if got.Get(k) != v {
			t.Errorf("%s = %q, want %q", k, got.Get(k), v)
		}
	}
	if m := r.meta(wav); m.SentAt == "" {
		t.Errorf("a clip that went through is not marked sent: %+v", m)
	}
}

func TestTaterIsToldWhatHappenedInItsOwnWords(t *testing.T) {
	for kind, want := range map[string]string{"wake": "wake", "nearmiss": "close_miss", "mistake": "false_trigger", "noise": "false_trigger"} {
		if got := taterEvent(kind); got != want {
			t.Errorf("%s is %q to Tater, want %q", kind, got, want)
		}
	}
	for model, want := range map[string]string{
		"hey_jarvis": "hey_jarvis", "Hey_Jarvis_v2": "hey_jarvis", "okay_nabu-v1.2": "okay_nabu", "stop": "stop",
		"hey tater.tflite": "hey_tater", "computer_2": "computer", "": "", "My Word!": "my_word",
	} {
		if got := taterWord(model); got != want {
			t.Errorf("taterWord(%q) = %q, want %q", model, got, want)
		}
	}
}

func TestACustomUploadIsAMultipartWithTheAudioAndTheMeta(t *testing.T) {
	for _, target := range []string{config.SendWakesmith, config.SendCustom} {
		var auth, path string
		var meta sendMeta
		var audio []byte
		var filename, audioType string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
			auth, path = q.Header.Get("Authorization"), q.URL.Path
			_, params, _ := mime.ParseMediaType(q.Header.Get("Content-Type"))
			mr := multipart.NewReader(q.Body, params["boundary"])
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
					audio, filename, audioType = blob, p.FileName(), p.Header.Get("Content-Type")
				}
			}
			fmt.Fprint(w, `{"duplicate": true}`)
		}))
		r := newSendRig(t, target, srv.URL)
		r.cfg.Token = "walnut-token"
		wav := r.clip("mistake", 1, "okay_nabu")
		r.drain()
		srv.Close()

		if path != "/api/clips" || auth != "Bearer walnut-token" {
			t.Errorf("%s: posted to %q with %q", target, path, auth)
		}
		name := strings.TrimSuffix(filepath.Base(wav), ".wav")
		if filename != name+".wav" || audioType != "audio/wav" {
			t.Errorf("%s: the audio part is %q as %q", target, filename, audioType)
		}
		disk, _ := os.ReadFile(wav)
		if string(audio) != string(disk) {
			t.Errorf("%s: the audio part is not the clip", target)
		}
		want := sendMeta{Name: name, Device: "Kitchen", Kind: "mistake", At: r.base.Add(time.Second).Format(time.RFC3339),
			Slot: 1, Model: "okay_nabu", Score: 0.79, Cutoff: 0.9}
		if meta != want {
			t.Errorf("%s: meta %+v, want %+v", target, meta, want)
		}
		if r.meta(wav).SentAt == "" {
			t.Errorf("%s: a duplicate is sent, and was not marked", target)
		}
	}
}

func TestNoTokenMeansNoAuthorizationHeader(t *testing.T) {
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		auth = q.Header.Get("Authorization")
		io.Copy(io.Discard, q.Body)
	}))
	defer srv.Close()
	r := newSendRig(t, config.SendCustom, srv.URL)
	r.clip("wake", 1, "m")
	r.drain()
	if auth != "" {
		t.Fatalf("sent %q with no token set", auth)
	}
}

// One clip the trainer refuses for good is marked and the rest go on.
func TestARefusedClipIsMarkedAndTheNextOneGoes(t *testing.T) {
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		io.Copy(io.Discard, q.Body)
		n++
		if n == 1 {
			http.Error(w, "bad wav", http.StatusBadRequest)
			return
		}
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer srv.Close()
	r := newSendRig(t, config.SendTater, srv.URL)
	first := r.clip("wake", 1, "m")
	second := r.clip("wake", 2, "m")
	r.drain()
	if m := r.meta(first); m.SendFailed != 400 || m.SentAt != "" {
		t.Errorf("the refused clip: %+v", m)
	}
	if m := r.meta(second); m.SentAt == "" {
		t.Errorf("the clip after it was not sent: %+v", m)
	}
	if n != 2 {
		t.Errorf("%d requests, want 2: a failed clip is not sent again", n)
	}
	if got, _ := r.s.Unsent(); got != 0 {
		t.Errorf("%d unsent", got)
	}
}

func TestATokenThatIsRefusedStopsSendingUntilTheSettingsChange(t *testing.T) {
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		io.Copy(io.Discard, q.Body)
		n++
		if q.Header.Get("Authorization") != "Bearer good" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		fmt.Fprint(w, `{}`)
	}))
	defer srv.Close()
	r := newSendRig(t, config.SendCustom, srv.URL)
	r.cfg.Token = "bad"
	wav := r.clip("wake", 1, "m")
	r.clip("wake", 2, "m")
	r.drain()
	if n != 1 {
		t.Fatalf("%d requests after a 401: sending should stop", n)
	}
	if _, status := r.s.Unsent(); status != statusRefused {
		t.Errorf("status %q", status)
	}
	if m := r.meta(wav); m.SentAt != "" || m.SendFailed != 0 {
		t.Errorf("a refused token marked the clip: %+v", m)
	}
	r.drain() // nothing changed: nothing is asked
	if n != 1 {
		t.Fatalf("asked again with the same token: %d requests", n)
	}
	r.cfg.Token = "good"
	r.drain()
	if n != 3 {
		t.Fatalf("%d requests after the token changed, want both clips sent", n)
	}
	if got, status := r.s.Unsent(); got != 0 || status != "" {
		t.Errorf("unsent %d, status %q", got, status)
	}
}

func TestAnUnreachableOrFailingTrainerIsBackedOffFromAndTheClipKept(t *testing.T) {
	for _, code := range []int{http.StatusInternalServerError, http.StatusServiceUnavailable, http.StatusRequestTimeout, http.StatusTooManyRequests} {
		n := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
			io.Copy(io.Discard, q.Body)
			n++
			if n <= 3 {
				w.WriteHeader(code)
				return
			}
			fmt.Fprint(w, `{"ok":true}`)
		}))
		r := newSendRig(t, config.SendTater, srv.URL)
		wav := r.clip("wake", 1, "m")
		r.drain()
		srv.Close()
		want := []time.Duration{5 * time.Second, 10 * time.Second, 20 * time.Second, sendEvery}
		if fmt.Sprint(r.waits) != fmt.Sprint(want) {
			t.Errorf("%d: waited %v, want %v", code, r.waits, want)
		}
		if r.meta(wav).SentAt == "" || r.meta(wav).SendFailed != 0 {
			t.Errorf("%d: a clip that was tried again was not sent in the end: %+v", code, r.meta(wav))
		}
	}
}

func TestTheBackoffIsCappedAndStartsOverAfterAClipGoes(t *testing.T) {
	down := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		io.Copy(io.Discard, q.Body)
		if down {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer srv.Close()
	r := newSendRig(t, config.SendTater, srv.URL)
	r.clip("wake", 1, "m")
	r.stopAt = 12
	r.drain()
	if len(r.waits) != 12 || r.waits[0] != sendBackoffMin || r.waits[11] != sendBackoffMax {
		t.Fatalf("waits %v", r.waits)
	}
	for i := 1; i < len(r.waits); i++ {
		if r.waits[i] < r.waits[i-1] || r.waits[i] > sendBackoffMax {
			t.Fatalf("waits are not growing to the cap: %v", r.waits)
		}
	}
	// Back up: the clip goes, and the next failure starts from the first wait again.
	down = false
	r.waits, r.stopAt = nil, 0
	r.drain()
	r.clip("wake", 2, "m")
	down = true
	r.stopAt = 1
	r.drain()
	if len(r.waits) < 2 || r.waits[len(r.waits)-1] != sendBackoffMin {
		t.Fatalf("after a success the wait was %v", r.waits)
	}
}

func TestClipsAreSentOneAtATimeOldestFirstAndPaced(t *testing.T) {
	var mu sync.Mutex
	var order []string
	inFlight, most := 0, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		mu.Lock()
		inFlight++
		most = max(most, inFlight)
		order = append(order, q.Header.Get("X-Original-Name"))
		mu.Unlock()
		io.Copy(io.Discard, q.Body)
		mu.Lock()
		inFlight--
		mu.Unlock()
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer srv.Close()
	r := newSendRig(t, config.SendTater, srv.URL)
	var want []string
	// Written newest first, and across kinds.
	for _, c := range []struct {
		kind string
		n    int
	}{{"noise", 5}, {"wake", 3}, {"mistake", 1}, {"nearmiss", 4}, {"wake", 2}} {
		want = append(want, filepath.Base(r.clip(c.kind, c.n, "m")))
	}
	r.drain()
	if len(order) != 5 || most != 1 {
		t.Fatalf("%d sent, %d at once", len(order), most)
	}
	// Oldest first by when each was kept: 1, 2, 3, 4, 5.
	for i, idx := range []int{2, 4, 1, 3, 0} {
		if order[i] != want[idx] {
			t.Fatalf("sent %v, want the oldest first", order)
		}
	}
	paced := 0
	for _, d := range r.waits {
		if d == sendEvery {
			paced++
		} else {
			t.Errorf("an unexpected wait %v", d)
		}
	}
	if paced != 5 {
		t.Errorf("%d paced waits for 5 clips: %v", paced, r.waits)
	}
}

func TestOnlyClipsKeptAfterTheTargetWasSetAreSent(t *testing.T) {
	var names []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		io.Copy(io.Discard, q.Body)
		names = append(names, q.Header.Get("X-Original-Name"))
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer srv.Close()
	r := newSendRig(t, config.SendTater, srv.URL)
	old := r.clip("wake", -7200, "m")
	before := r.clip("wake", -10, "m")
	after := r.clip("wake", 10, "m")
	r.cfg.Since = r.base.Format(time.RFC3339)
	if _, n := r.s.next(r.cfg); n != 1 {
		t.Fatalf("%d clips to send, want the one kept after", n)
	}
	r.drain()
	if len(names) != 1 || names[0] != filepath.Base(after) {
		t.Fatalf("sent %v", names)
	}
	for _, f := range []string{old, before} {
		if m := r.meta(f); m.SentAt != "" || m.SendFailed != 0 {
			t.Errorf("an earlier clip was touched: %+v", m)
		}
	}
	// No moment set: nothing is old enough to send.
	r.cfg.Since = ""
	if _, n := r.s.next(r.cfg); n != 0 {
		t.Errorf("%d to send with no moment set", n)
	}
}

func TestKindsLeftOutAreNotSent(t *testing.T) {
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		io.Copy(io.Discard, q.Body)
		n++
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer srv.Close()
	r := newSendRig(t, config.SendTater, srv.URL)
	r.cfg.Skip = []string{"noise", "mistake"}
	for i, k := range clipKinds {
		r.clip(k, i+1, "m")
	}
	r.drain()
	if n != 2 {
		t.Fatalf("%d sent, want the wake and the near miss", n)
	}
}

func TestNothingIsSentWhileOffOrWhileKeepIsOff(t *testing.T) {
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) { n++ }))
	defer srv.Close()
	r := newSendRig(t, config.SendOff, srv.URL)
	r.clip("wake", 1, "m")
	r.drain()
	r.cfg.Target = config.SendTater
	r.c.on.Store(false)
	r.drain()
	if n != 0 {
		t.Fatalf("%d requests while off", n)
	}
	if _, status := r.s.Unsent(); status != statusKeepOff {
		t.Errorf("status %q", status)
	}
}

// Turning Keep wake clips off deletes the clips, unsent ones with them, and says so to the sender.
func TestPurgingTheClipsWakesTheSenderAndLeavesNothingToSend(t *testing.T) {
	r := newSendRig(t, config.SendTater, "http://127.0.0.1:1")
	r.c.changed = r.s.wake
	r.clip("wake", 1, "m")
	<-r.s.nudge // the write woke it
	r.c.enable(false)
	select {
	case <-r.s.nudge:
	default:
		t.Fatal("the purge did not wake the sender")
	}
	if _, n := r.s.next(r.cfg); n != 0 {
		t.Fatalf("%d clips left to send after the purge", n)
	}
}

func TestWritingAClipWakesTheSender(t *testing.T) {
	r := newSendRig(t, config.SendTater, "http://127.0.0.1:1")
	r.c.changed = r.s.wake
	r.clip("wake", 1, "m")
	select {
	case <-r.s.nudge:
	default:
		t.Fatal("a clip was written and the sender was not woken")
	}
}

func TestOnlyAddressesOnTheHomeNetworkAreSentTo(t *testing.T) {
	for ip, want := range map[string]bool{
		"192.168.1.20": true, "192.168.255.1": true, "10.0.0.5": true, "172.16.0.1": true, "172.31.255.254": true,
		"169.254.10.10": true, "fd00::1": true, "fc00::5": true, "fe80::1": true, "::ffff:192.168.1.5": true,
		"172.15.0.1": false, "172.32.0.1": false, "8.8.8.8": false, "1.1.1.1": false, "127.0.0.1": false,
		"::1": false, "2001:4860:4860::8888": false, "100.64.0.1": true, "100.127.255.255": true, "100.63.255.255": false, "100.128.0.1": false, "fd7a:115c:a1e0::1": true, "0.0.0.0": false,
	} {
		if got := homeAddress(net.ParseIP(ip)); got != want {
			t.Errorf("%s is home: %v, want %v", ip, got, want)
		}
	}
}

func TestANameIsHomeOnlyIfEverythingItResolvesToIs(t *testing.T) {
	c := newClips(t.TempDir())
	s := newSender(c)
	names := map[string][]string{
		"trainer.local": {"192.168.1.9"}, "nas": {"10.1.1.1", "fd00::2"}, "public.example": {"8.8.8.8"},
		"mixed.example": {"192.168.1.9", "93.184.216.34"}, "none.example": {},
	}
	s.resolve = func(_ context.Context, host string) ([]net.IP, error) {
		v, ok := names[host]
		if !ok {
			return nil, fmt.Errorf("no such host")
		}
		var out []net.IP
		for _, a := range v {
			out = append(out, net.ParseIP(a))
		}
		return out, nil
	}
	ctx := context.Background()
	for raw, ok := range map[string]bool{
		"http://trainer.local:8789": true, "https://nas/x": true, "http://192.168.1.20:8789": true,
		"http://[fd00::1]:80": true, "http://public.example": false, "http://mixed.example": false,
		"http://8.8.8.8": false, "http://[2001:db8::1]": false, "http://none.example": false, "http://unknown": false,
	} {
		err := s.CheckHome(ctx, raw)
		if (err == nil) != ok {
			t.Errorf("%s: %v, want ok=%v", raw, err, ok)
		}
	}
	if err := s.CheckHome(ctx, "http://public.example"); err == nil || !strings.Contains(err.Error(), "home network") {
		t.Errorf("the refusal does not say why: %v", err)
	}
}

// The address is looked at again before each send: a name that came to point somewhere else gets nothing.
func TestAnAddressThatMovedOffTheHomeNetworkIsNotSentToAndNothingIsMarked(t *testing.T) {
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) { n++ }))
	defer srv.Close()
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	r := newSendRig(t, config.SendTater, "http://trainer.example:"+port)
	r.s.allow = homeAddress
	r.s.resolve = func(context.Context, string) ([]net.IP, error) { return []net.IP{net.ParseIP("203.0.113.9")}, nil }
	wav := r.clip("wake", 1, "m")
	r.stopAt = 2
	r.drain()
	if n != 0 {
		t.Fatalf("%d requests to an address off the home network", n)
	}
	if _, status := r.s.Unsent(); !strings.Contains(status, "home network") {
		t.Errorf("status %q", status)
	}
	if r.meta(wav).SentAt != "" {
		t.Error("marked sent")
	}
}

// The connection itself is made only to a home address, whatever the name said when it was checked.
func TestTheConnectionIsRefusedToAPublicAddress(t *testing.T) {
	r := newSendRig(t, config.SendTater, "")
	r.s.allow = homeAddress
	r.s.resolve = func(context.Context, string) ([]net.IP, error) { return []net.IP{net.ParseIP("203.0.113.9")}, nil }
	if _, err := r.s.dial(context.Background(), "tcp", "trainer.example:80"); err != errNotHome {
		t.Fatalf("dialed: %v", err)
	}
}

func TestRedirectsAreNotFollowed(t *testing.T) {
	hit := 0
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) { hit++ }))
	defer other.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		http.Redirect(w, q, other.URL, http.StatusTemporaryRedirect)
	}))
	defer srv.Close()
	r := newSendRig(t, config.SendTater, srv.URL)
	wav := r.clip("wake", 1, "m")
	r.drain()
	if hit != 0 {
		t.Fatal("a redirect was followed")
	}
	if m := r.meta(wav); m.SendFailed != 0 || m.SentAt != "" {
		t.Errorf("a redirect marked the clip: %+v", m)
	}
	if _, status := r.s.Unsent(); !strings.Contains(status, "307") {
		t.Errorf("status %q", status)
	}
}

// A clip is streamed from its file: sending one much bigger than any a device keeps allocates nowhere
// near its size, for either format.
func TestAClipIsStreamedNotLoaded(t *testing.T) {
	for _, target := range []string{config.SendTater, config.SendCustom} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
			io.Copy(io.Discard, q.Body)
			fmt.Fprint(w, `{"ok":true}`)
		}))
		r := newSendRig(t, target, srv.URL)
		wav := r.clip("wake", 1, "m")
		const big = 48 << 20
		if err := os.WriteFile(wav, make([]byte, big), 0o600); err != nil {
			t.Fatal(err)
		}
		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		r.drain()
		runtime.ReadMemStats(&after)
		srv.Close()
		if r.meta(wav).SentAt == "" {
			t.Fatalf("%s: not sent", target)
		}
		if used := after.TotalAlloc - before.TotalAlloc; used > big/4 {
			t.Errorf("%s: sending a %d MB clip allocated %d MB", target, big>>20, used>>20)
		}
	}
}

// The trainer is unreachable while far more clips than are kept are written: the disk holds what it
// always did, and what waits is no more than the kept clips.
func TestABacklogNeverHoldsMoreThanTheKeptClips(t *testing.T) {
	r := newSendRig(t, config.SendTater, "http://127.0.0.1:1")
	r.c.keep = clipsKept
	for i := 0; i < 150; i++ {
		for _, k := range clipKinds {
			r.clip(k, i+1, "m")
		}
	}
	for _, k := range clipKinds {
		files, _ := filepath.Glob(filepath.Join(r.c.dir, k, "*.wav"))
		sides, _ := filepath.Glob(filepath.Join(r.c.dir, k, "*.json"))
		if len(files) != clipsKept || len(sides) != clipsKept {
			t.Errorf("%s: %d clips and %d sidecars on disk, want %d", k, len(files), len(sides), clipsKept)
		}
	}
	_, n := r.s.next(r.cfg)
	if n != 4*clipsKept {
		t.Fatalf("%d waiting, want %d", n, 4*clipsKept)
	}
	// And the sender, unable to reach it, reports them and keeps trying.
	r.stopAt = 3
	r.drain()
	if got, _ := r.s.Unsent(); got != 4*clipsKept {
		t.Errorf("reports %d unsent", got)
	}
}

func TestAClipPrunedWhileBeingSentIsNotMarked(t *testing.T) {
	r := newSendRig(t, config.SendTater, "http://127.0.0.1:1")
	wav := r.clip("wake", 1, "m")
	it, _ := r.s.next(r.cfg)
	os.Remove(wav)
	os.Remove(sidecar(wav))
	r.s.mark(it, func(m *clipMeta) { m.SentAt = "x" })
	if _, err := os.Stat(sidecar(wav)); err == nil {
		t.Fatal("a sidecar was made for a clip that is gone")
	}
}

func TestPingAsksEachKindOfTrainerInItsOwnWay(t *testing.T) {
	var path, auth string
	status := 200
	body := `{"ok":true,"safe_word":"hey_tater"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		path, auth = q.URL.Path, q.Header.Get("Authorization")
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}))
	defer srv.Close()
	r := newSendRig(t, config.SendTater, srv.URL)
	ctx := context.Background()
	if _, err := r.s.Ping(ctx, r.cfg); err != nil || path != "/api/session" || auth != "" {
		t.Fatalf("Tater: %v %q %q", err, path, auth)
	}
	body = `{"ok":true}`
	if _, err := r.s.Ping(ctx, r.cfg); err == nil || !strings.Contains(err.Error(), "not Tater") {
		t.Fatalf("something else answered as Tater: %v", err)
	}
	r.cfg.Target, r.cfg.Token = config.SendWakesmith, "walnut"
	if _, err := r.s.Ping(ctx, r.cfg); err != nil || path != "/api/clips/ping" || auth != "Bearer walnut" {
		t.Fatalf("wakesmith: %v %q %q", err, path, auth)
	}
	status = 401
	if _, err := r.s.Ping(ctx, r.cfg); err == nil || !strings.Contains(err.Error(), "refused the token") {
		t.Fatalf("a refused token: %v", err)
	}
	status = 404
	if _, err := r.s.Ping(ctx, r.cfg); err == nil {
		t.Fatal("a 404 is not a trainer")
	}
	// Off the home network, nothing is asked.
	r.s.allow = homeAddress
	r.cfg.URL = srv.URL
	if _, err := r.s.Ping(ctx, r.cfg); err != errNotHome {
		t.Fatalf("a loopback address: %v", err)
	}
}

func TestSendAddressesAreChecked(t *testing.T) {
	for raw, want := range map[string]string{
		"http://192.168.1.20:8789":                "http://192.168.1.20:8789",
		"https://trainer.local/":                  "https://trainer.local",
		" http://nas:8789/base/ ":                 "http://nas:8789/base",
		"ftp://192.168.1.2":                       "",
		"192.168.1.20":                            "",
		"http://":                                 "",
		"http://user:pw@10.0.0.1":                 "",
		"http://10.0.0.1/?a=b":                    "",
		"http://10.0.0.1/#x":                      "",
		"":                                        "",
		"https://192.168.1.20.:8765":              "https://192.168.1.20:8765",
		"https://192.168.1.20:8765/api/clips":     "https://192.168.1.20:8765",
		"https://192.168.1.20:8765/api/clips/":    "https://192.168.1.20:8765",
		"http://Trainer.LAN.:8765/api/clips/ping": "http://trainer.lan:8765",
		"http://nas/wake/api/clips":               "http://nas/wake",
	} {
		got, err := CheckSendURL(raw)
		if (err == nil) != (want != "") || got != want {
			t.Errorf("%q: %q, %v; want %q", raw, got, err, want)
		}
	}
}

func TestTheTokenIsNeverInTheSendersWords(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		http.Error(w, "echo "+q.Header.Get("Authorization"), http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	r := newSendRig(t, config.SendCustom, srv.URL)
	r.cfg.Token = "walnut-secret-token"
	r.clip("wake", 1, "m")
	r.stopAt = 2
	r.drain()
	if _, status := r.s.Unsent(); strings.Contains(status, "walnut") {
		t.Fatalf("the status says the token: %q", status)
	}
}

func tlsRig(t *testing.T, handler http.HandlerFunc) (*sendRig, *httptest.Server, string) {
	t.Helper()
	srv := httptest.NewTLSServer(handler)
	t.Cleanup(srv.Close)
	r := newSendRig(t, config.SendWakesmith, srv.URL)
	return r, srv, certFingerprint(srv.Certificate())
}

func TestASelfSignedCertificateIsRefusedUntilPinned(t *testing.T) {
	r, _, fp := tlsRig(t, func(w http.ResponseWriter, q *http.Request) { io.Copy(io.Discard, q.Body) })
	ctx := context.Background()
	_, err := r.s.Ping(ctx, r.cfg)
	var ce *CertError
	if !errors.As(err, &ce) || ce.Changed || ce.Fingerprint != fp {
		t.Fatalf("an unpinned self-signed certificate: %v", err)
	}
	if r.s.Untrusted() != fp {
		t.Fatalf("the refused fingerprint was not kept for the page: %q", r.s.Untrusted())
	}
	wav := r.clip("wake", 1, "m")
	r.stopAt = 1
	r.drain()
	if r.meta(wav).SentAt != "" {
		t.Fatal("a clip went to an unpinned self-signed trainer")
	}

	r.cfg.Pin = strings.ToUpper(fp) // the pin matches whatever case it was kept in
	if _, err := r.s.Ping(ctx, r.cfg); err != nil {
		t.Fatalf("a pinned certificate: %v", err)
	}
	r.waits, r.stopAt = nil, 0
	r.drain()
	if r.meta(wav).SentAt == "" {
		t.Fatal("a clip was not sent to a pinned trainer")
	}
}

func TestAPinnedCertificateThatChangedIsRefusedAndSaidSo(t *testing.T) {
	r, _, _ := tlsRig(t, func(w http.ResponseWriter, q *http.Request) { io.Copy(io.Discard, q.Body) })
	r.cfg.Pin = strings.Repeat("ab", 32)
	_, err := r.s.Ping(context.Background(), r.cfg)
	var ce *CertError
	if !errors.As(err, &ce) || !ce.Changed || !strings.Contains(err.Error(), "certificate changed") {
		t.Fatalf("%v", err)
	}
	wav := r.clip("wake", 1, "m")
	r.stopAt = 1
	r.drain()
	if _, status := r.s.Unsent(); !strings.Contains(status, "certificate changed") || r.meta(wav).SentAt != "" {
		t.Fatalf("status %q", status)
	}
}

func TestChangingTheAddressClearsThePin(t *testing.T) {
	d := setupSend(t)
	d.sender.allow = func(ip net.IP) bool { return ip.IsLoopback() || homeAddress(ip) }
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {}))
	defer srv.Close()
	ctx := context.Background()
	if err := d.SetSend(ctx, config.WakeSend{Target: config.SendWakesmith, URL: srv.URL}); err != nil {
		t.Fatal(err)
	}
	if err := d.Trust(ctx, strings.Repeat("0", 64)); err == nil {
		t.Fatal("trusted a certificate that is not the one shown")
	}
	if _, err := d.sender.Ping(ctx, config.Get().Wake.Send); err == nil {
		t.Fatal("no refusal to show")
	}
	if err := d.Trust(ctx, d.sender.Untrusted()); err != nil {
		t.Fatal(err)
	}
	if config.Get().Wake.Send.Pin != certFingerprint(srv.Certificate()) {
		t.Fatalf("pin %q", config.Get().Wake.Send.Pin)
	}
	// Saved again at the same address, the pin stays; at another, it goes.
	if err := d.SetSend(ctx, config.WakeSend{Target: config.SendWakesmith, URL: srv.URL}); err != nil || config.Get().Wake.Send.Pin == "" {
		t.Fatalf("same address: %v %+v", err, config.Get().Wake.Send)
	}
	if err := d.SetSend(ctx, config.WakeSend{Target: config.SendWakesmith, URL: "https://192.168.1.77:8765"}); err != nil || config.Get().Wake.Send.Pin != "" {
		t.Fatalf("new address: %v %+v", err, config.Get().Wake.Send)
	}
}

func TestEverySentMetaHasWhatWakesmithRequires(t *testing.T) {
	var raw map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		_, params, _ := mime.ParseMediaType(q.Header.Get("Content-Type"))
		mr := multipart.NewReader(q.Body, params["boundary"])
		for {
			p, err := mr.NextPart()
			if err != nil {
				break
			}
			blob, _ := io.ReadAll(p)
			if p.FormName() == "meta" {
				json.Unmarshal(blob, &raw)
			}
		}
	}))
	defer srv.Close()
	r := newSendRig(t, config.SendWakesmith, srv.URL)
	r.clip("wake", 1, "") // a clip with no model recorded
	r.drain()
	for _, k := range []string{"name", "device", "kind", "at", "model"} {
		if s, ok := raw[k].(string); !ok || s == "" {
			t.Errorf("meta %q is %v", k, raw[k])
		}
	}
	for _, k := range []string{"slot", "score", "cutoff"} {
		if _, ok := raw[k].(float64); !ok {
			t.Errorf("meta %q is %v, not a number", k, raw[k])
		}
	}
}

func TestAReceiversErrorTextIsSaidInTheStatusAndTheTest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		io.Copy(io.Discard, q.Body)
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprint(w, `{"ok": false, "error": "updating, back in a minute"}`)
	}))
	defer srv.Close()
	r := newSendRig(t, config.SendWakesmith, srv.URL)
	r.clip("wake", 1, "m")
	r.stopAt = 1
	r.drain()
	if _, status := r.s.Unsent(); !strings.Contains(status, "updating, back in a minute") {
		t.Errorf("status %q", status)
	}
	if _, err := r.s.Ping(context.Background(), r.cfg); err == nil || !strings.Contains(err.Error(), "updating, back in a minute") {
		t.Errorf("test: %v", err)
	}
}

func TestARefusedClipSaysWhyInTheLog(t *testing.T) {
	if got := answerText([]byte(`{"ok": false, "error": "meta.model is required"}`)); got != ": meta.model is required" {
		t.Errorf("%q", got)
	}
	if got := answerText([]byte(`<html>`)); got != "" {
		t.Errorf("%q", got)
	}
	if o, why := judge(config.SendWakesmith, 400, []byte(`{"error":"bad"}`)); o != outFailed || failedStatus(why) != 400 {
		t.Errorf("%v %q", o, why)
	}
}

// The device sent is the ESPHome node name, the <device> of esphome.<device>_wake_clips, so a trainer's
// folders and a puller's agree; and the clip's name is the file's, with no kind in it.
func TestTheDeviceSentIsTheNodeNameHomeAssistantUses(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	mac, _ := layout.FactoryMAC()
	name := config.Get().Device.Name
	got := deviceName()
	if got != layout.NodeID(name, mac) || strings.ContainsAny(got, "- ABCDEFGHIJKLMNOPQRSTUVWXYZ") {
		t.Fatalf("device %q", got)
	}
	if action := layout.ActionName(name, mac, "wake_clips"); action != "esphome."+got+"_wake_clips" {
		t.Fatalf("the action is %q", action)
	}
	if layout.NodeID("Living Room", "aa:bb:cc:dd:ee:ff") != "living_room" {
		t.Fatalf("%q", layout.NodeID("Living Room", "aa:bb:cc:dd:ee:ff"))
	}
	if s := newSender(newClips(t.TempDir())); s.device() != got {
		t.Fatalf("the sender sends %q", s.device())
	}
}
