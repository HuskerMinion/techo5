package detect

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// The select and the setup page's form both save the sender's settings: neither may put back what the
// other just saved.
func TestSelectAndPageSavesDoNotRevertEachOther(t *testing.T) {
	d := setupSend(t)
	ctx := context.Background()
	if err := d.SetSend(ctx, config.WakeSend{Target: config.SendCustom, URL: "http://trainer.local:80", Token: "one", Skip: []string{"nearmiss"}}); err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	inside, release := make(chan struct{}), make(chan struct{})
	inner := d.sender.resolve
	d.sender.resolve = func(ctx context.Context, host string) ([]net.IP, error) {
		once.Do(func() { close(inside); <-release })
		return inner(ctx, host)
	}
	done := make(chan error, 1)
	go func() { done <- d.SetSendTarget(ctx, config.SendWakesmith) }()
	<-inside
	saved := make(chan error, 1)
	go func() {
		saved <- d.SetSend(ctx, config.WakeSend{Target: config.SendCustom, URL: "http://trainer.local:80", Token: "two"})
	}()
	time.Sleep(50 * time.Millisecond) // the page's save is waiting for the select's
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := <-saved; err != nil {
		t.Fatal(err)
	}
	if got := config.Get().Wake.Send; got.Token != "two" || got.Target != config.SendCustom || len(got.Skip) != 0 {
		t.Fatalf("the later save was reverted: %+v", got)
	}
}

func TestChangingTheAddressForgetsTheRefusedCertificate(t *testing.T) {
	d := setupSend(t)
	ctx := context.Background()
	if err := d.SetSend(ctx, config.WakeSend{Target: config.SendCustom, URL: "http://192.168.1.2:80"}); err != nil {
		t.Fatal(err)
	}
	d.sender.mu.Lock()
	d.sender.cert = "aa"
	d.sender.mu.Unlock()
	if err := d.SetSend(ctx, config.WakeSend{Target: config.SendCustom, URL: "http://192.168.1.2:80", Token: "t"}); err != nil {
		t.Fatal(err)
	}
	if d.sender.Untrusted() != "aa" {
		t.Error("the same address lost the certificate")
	}
	if err := d.SetSend(ctx, config.WakeSend{Target: config.SendCustom, URL: "http://192.168.1.3:80"}); err != nil {
		t.Fatal(err)
	}
	if got := d.sender.Untrusted(); got != "" {
		t.Errorf("an old host's fingerprint is still shown: %q", got)
	}
}

// Turning Keep wake clips off while a clip is on its way cuts the request short, and the clip is not
// counted as the trainer's refusal.
func TestPurgeMidSendCancelsTheRequest(t *testing.T) {
	started := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		close(started)
		select {
		case <-q.Context().Done():
		case <-time.After(10 * time.Second):
		}
	}))
	defer srv.Close()
	r := newSendRig(t, config.SendTater, srv.URL)
	r.c.abortSend = r.s.abortSend
	r.clip("wake", 1, "m")
	done := make(chan struct{})
	go func() { r.drain(); close(done) }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the upload never started")
	}
	r.c.enable(false)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the upload went on after the clips were purged")
	}
	if len(r.waits) != 0 {
		t.Errorf("waited %v: it was counted as a failure or a retry", r.waits)
	}
}

func TestAClipGoneBeforeItIsOpenedIsSkippedNotRefused(t *testing.T) {
	r := newSendRig(t, config.SendTater, "http://127.0.0.1:1")
	wav := r.clip("wake", 1, "m")
	it, _ := r.s.next(r.cfg)
	os.Remove(wav)
	out, why := r.s.send(context.Background(), r.cfg, it)
	if out != outSkip || why != "" {
		t.Fatalf("%v %q", out, why)
	}
	if m := r.meta(wav); m.SendFailed != 0 {
		t.Errorf("marked failed: %+v", m)
	}
}

func TestACommandedOffAfterOpenSendsNothing(t *testing.T) {
	r := newSendRig(t, config.SendTater, "http://127.0.0.1:1")
	r.clip("wake", 1, "m")
	it, _ := r.s.next(r.cfg)
	r.c.on.Store(false)
	if out, _ := r.s.send(context.Background(), r.cfg, it); out != outSkip {
		t.Fatalf("%v", out)
	}
}

func TestTheTrainersAddressIsNotInTheErrors(t *testing.T) {
	refused := &url.Error{Op: "Post", URL: "http://192.168.1.50:8765/x", Err: &net.OpError{
		Op: "dial", Net: "tcp", Addr: &net.TCPAddr{IP: net.ParseIP("192.168.1.50"), Port: 8765},
		Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)}}
	if got := cleanErr(refused); got != "can't connect to the trainer: connection refused" {
		t.Errorf("%q", got)
	}
	reset := &url.Error{Op: "Post", URL: "http://192.168.1.50:8765/x", Err: &net.OpError{
		Op: "read", Net: "tcp", Addr: &net.TCPAddr{IP: net.ParseIP("192.168.1.50"), Port: 8765},
		Err: os.NewSyscallError("read", syscall.ECONNRESET)}}
	if got := cleanErr(reset); strings.Contains(got, "192.168") || !strings.Contains(got, "reset") {
		t.Errorf("%q", got)
	}
	if got := cleanErr(errors.New("Post http://x: dial tcp 192.168.1.50:8765: boom")); strings.Contains(got, "192.168") {
		t.Errorf("%q", got)
	}
	// Through the sender, to a port nothing listens on.
	r := newSendRig(t, config.SendTater, "http://127.0.0.1:1")
	r.clip("wake", 1, "m")
	r.stopAt = 1
	r.drain()
	if _, status := r.s.Unsent(); strings.Contains(status, "127.0.0.1") || !strings.Contains(status, "can't connect to the trainer") {
		t.Errorf("status %q", status)
	}
}
