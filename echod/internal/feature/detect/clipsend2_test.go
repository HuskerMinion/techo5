package detect

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// testCA makes a certificate authority, and a certificate it signs for the given names and addresses.
func testCA(t *testing.T) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test root"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	ca, _ := x509.ParseCertificate(der)
	return ca, key
}

func testLeaf(t *testing.T, ca *x509.Certificate, caKey *ecdsa.PrivateKey, dns []string, ips []net.IP) tls.Certificate {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "leaf"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames: dns, IPAddresses: ips,
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, ca, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

func tlsServerWith(t *testing.T, cert tls.Certificate) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) { fmt.Fprint(w, `{"ok":true,"safe_word":"x"}`) }))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv
}

// An https address that is an IP has no server name in the handshake: the certificate is still checked
// for that very address, so one that a trusted authority signed for another name is refused.
func TestACertificateIsCheckedForTheAddressItWasReachedAt(t *testing.T) {
	ca, caKey := testCA(t)
	pool := x509.NewCertPool()
	pool.AddCert(ca)
	ctx := context.Background()

	other := tlsServerWith(t, testLeaf(t, ca, caKey, []string{"other.example"}, nil))
	r := newSendRig(t, config.SendWakesmith, other.URL)
	r.s.roots = pool
	_, err := r.s.Ping(ctx, r.cfg)
	var ce *CertError
	if !errors.As(err, &ce) {
		t.Fatalf("a certificate signed for another name was accepted at an IP address: %v", err)
	}
	wav := r.clip("wake", 1, "m")
	r.stopAt = 1
	r.drain()
	if r.meta(wav).SentAt != "" {
		t.Fatal("a clip was sent to it")
	}

	right := tlsServerWith(t, testLeaf(t, ca, caKey, nil, []net.IP{net.ParseIP("127.0.0.1")}))
	r2 := newSendRig(t, config.SendWakesmith, right.URL)
	r2.s.roots = pool
	if _, err := r2.s.Ping(ctx, r2.cfg); err != nil {
		t.Fatalf("a certificate with the IP address in it: %v", err)
	}
	wav = r2.clip("wake", 1, "m")
	r2.drain()
	if r2.meta(wav).SentAt == "" {
		t.Fatal("not sent to a trainer with a certificate for its address")
	}
}

// Anything that says the address or the settings are wrong stops sending, with the clips kept.
func TestAnswersAboutTheAddressStopSendingAndMarkNothing(t *testing.T) {
	for _, code := range []int{302, 307, 403, 404, 405, 413, 409} {
		n := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
			io.Copy(io.Discard, q.Body)
			n++
			if code/100 == 3 {
				w.Header().Set("Location", "http://192.168.1.1/")
			}
			w.WriteHeader(code)
		}))
		r := newSendRig(t, config.SendWakesmith, srv.URL)
		a := r.clip("wake", 1, "m")
		b := r.clip("wake", 2, "m")
		r.drain()
		r.drain()
		srv.Close()
		if n != 1 {
			t.Errorf("%d: %d requests: sending should stop at the first, and not start again", code, n)
		}
		if got, status := r.s.Unsent(); got != 2 || !strings.Contains(status, fmt.Sprint(code)) {
			t.Errorf("%d: unsent %d, status %q", code, got, status)
		}
		for _, f := range []string{a, b} {
			if m := r.meta(f); m.SendFailed != 0 || m.SentAt != "" {
				t.Errorf("%d: a clip was marked: %+v", code, m)
			}
		}
	}
}

func TestOnlyAClipTheTrainerWillNeverTakeIsMarkedFailed(t *testing.T) {
	for _, code := range []int{400, 415, 422} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
			io.Copy(io.Discard, q.Body)
			w.WriteHeader(code)
		}))
		r := newSendRig(t, config.SendWakesmith, srv.URL)
		wav := r.clip("wake", 1, "m")
		r.drain()
		srv.Close()
		if m := r.meta(wav); m.SendFailed != code {
			t.Errorf("%d: %+v", code, m)
		}
	}
}

func TestFiveClipsInARowRefusedStopsSending(t *testing.T) {
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		io.Copy(io.Discard, q.Body)
		n++
		w.WriteHeader(422)
	}))
	defer srv.Close()
	r := newSendRig(t, config.SendWakesmith, srv.URL)
	for i := 1; i <= 7; i++ {
		r.clip("wake", i, "m")
	}
	r.drain()
	if n != sendFailuresMax {
		t.Fatalf("%d requests, want %d", n, sendFailuresMax)
	}
	if got, status := r.s.Unsent(); got != 2 || !strings.Contains(status, "5 clips in a row") {
		t.Fatalf("unsent %d, status %q", got, status)
	}
	// Saving the settings again lets it try again.
	r.cfg.Token = "other"
	r.drain()
	if n != 7 {
		t.Fatalf("%d requests after the settings changed", n)
	}
}

func TestATaterThatAnswersWithoutOkStopsSending(t *testing.T) {
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		io.Copy(io.Discard, q.Body)
		n++
		fmt.Fprint(w, `<html>hello</html>`)
	}))
	defer srv.Close()
	r := newSendRig(t, config.SendTater, srv.URL)
	r.clip("wake", 1, "m")
	r.drain()
	r.drain()
	if _, status := r.s.Unsent(); n != 1 || !strings.Contains(status, "not as Tater Trainer") {
		t.Fatalf("%d requests, status %q", n, status)
	}
}

// A clip that went through but whose sidecar cannot be written is not sent again, and the status says so.
func TestAClipThatCannotBeMarkedIsNotSentAgain(t *testing.T) {
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		io.Copy(io.Discard, q.Body)
		n++
	}))
	defer srv.Close()
	r := newSendRig(t, config.SendWakesmith, srv.URL)
	wav := r.clip("wake", 1, "m")
	// A folder where the new sidecar is written first: it cannot be.
	if err := os.Mkdir(sidecar(wav)+".tmp", 0o700); err != nil {
		t.Fatal(err)
	}
	r.drain()
	r.drain()
	if n != 1 {
		t.Fatalf("sent %d times", n)
	}
	if _, status := r.s.Unsent(); !strings.Contains(status, "can't record what was sent") {
		t.Fatalf("status %q", status)
	}
	if got, _ := r.s.Unsent(); got != 0 {
		t.Fatalf("%d unsent", got)
	}
}

func TestTurningKeepOnWakesTheSender(t *testing.T) {
	r := newSendRig(t, config.SendTater, "http://127.0.0.1:1")
	r.c.changed = r.s.wake
	r.c.on.Store(false)
	r.c.enable(true)
	select {
	case <-r.s.nudge:
	default:
		t.Fatal("turning it on did not wake the sender")
	}
}

func TestAClipInTheSecondTheTargetWasSetCounts(t *testing.T) {
	r := newSendRig(t, config.SendTater, "http://127.0.0.1:1")
	r.clip("wake", 5, "m")
	r.cfg.Since = r.base.Add(5 * time.Second).Format(time.RFC3339)
	if _, n := r.s.next(r.cfg); n != 1 {
		t.Fatalf("%d", n)
	}
	r.cfg.Since = r.base.Add(6 * time.Second).Format(time.RFC3339)
	if _, n := r.s.next(r.cfg); n != 0 {
		t.Fatalf("%d", n)
	}
}

func TestAClockThatIsNotSetStampsNothing(t *testing.T) {
	d := setupSend(t)
	was := sendClock
	defer func() { sendClock = was }()
	sendClock = func() time.Time { return time.Date(1970, 1, 1, 0, 1, 0, 0, time.UTC) }
	err := d.SetSend(context.Background(), config.WakeSend{Target: config.SendTater, URL: "http://192.168.1.2:8789"})
	if err == nil || !strings.Contains(err.Error(), "clock isn't set") {
		t.Fatalf("%v", err)
	}
	if config.Get().Wake.Send.Target != "" {
		t.Fatal("saved anyway")
	}
	// Off needs no moment.
	if err := d.SetSend(context.Background(), config.WakeSend{URL: "http://192.168.1.2:8789"}); err != nil {
		t.Fatal(err)
	}
}

func TestSwitchingTargetKeepsTheTokenAndThePin(t *testing.T) {
	d := setupSend(t)
	ctx := context.Background()
	if err := d.SetSend(ctx, config.WakeSend{Target: config.SendWakesmith, URL: "http://192.168.1.2:8765", Token: "walnut"}); err != nil {
		t.Fatal(err)
	}
	config.Set().SendUpdate(func(w *config.WakeSend) { w.Pin = "ab" })
	for _, tg := range []string{config.SendTater, config.SendOff, config.SendCustom} {
		if err := d.SetSendTarget(ctx, tg); err != nil {
			t.Fatal(err)
		}
		if got := config.Get().Wake.Send; got.Token != "walnut" || got.Pin != "ab" || got.Target != tg {
			t.Fatalf("%s: %+v", tg, got)
		}
	}
	// And Tater is never sent it.
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		auth = q.Header.Get("Authorization")
		io.Copy(io.Discard, q.Body)
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer srv.Close()
	r := newSendRig(t, config.SendTater, srv.URL)
	r.cfg.Token = "walnut"
	r.clip("wake", 1, "m")
	r.drain()
	if auth != "" {
		t.Fatalf("Tater was sent %q", auth)
	}
}

func TestAddressesWithAZoneAreSaidToBeUnsupported(t *testing.T) {
	if _, err := CheckSendURL("http://[fe80::1%25eth0]:8765"); err == nil || !strings.Contains(err.Error(), "zone") {
		t.Fatalf("%v", err)
	}
}

func TestStaleHalfWrittenSidecarsAreSwept(t *testing.T) {
	r := newSendRig(t, config.SendTater, "http://127.0.0.1:1")
	wav := r.clip("wake", 1, "m")
	stale := sidecar(wav) + ".tmp"
	os.WriteFile(stale, []byte("{"), 0o600)
	r.c.recount()
	if _, err := os.Stat(stale); err == nil {
		t.Fatal("recount left it")
	}
	os.WriteFile(stale, []byte("{"), 0o600)
	prune(filepath.Dir(wav), clipsKept)
	if _, err := os.Stat(stale); err == nil {
		t.Fatal("prune left it")
	}
}

func TestKeepWakeClipsFromThePage(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	dir := t.TempDir()
	c := newClips(dir)
	c.shortest = 0
	d := &Detect{clips: c}
	d.keepClips = newKeepClipsEntity(d)
	var saved []bool
	was := saveKeepClips
	saveKeepClips = func(on bool) error { saved = append(saved, on); return nil }
	defer func() { saveKeepClips = was }()
	clipsOffPath = filepath.Join(t.TempDir(), "off")

	if err := d.SetKeepClips(true); err != nil || !c.on.Load() || !d.KeepOn() {
		t.Fatalf("on: %v", err)
	}
	c.write(clip{kind: "wake", at: time.Now(), pcm: make([]int16, 100)})
	if files, _ := filepath.Glob(filepath.Join(dir, "wake", "*.wav")); len(files) != 1 {
		t.Fatalf("%d clips kept", len(files))
	}
	if err := d.SetKeepClips(false); err != nil || c.on.Load() {
		t.Fatalf("off: %v", err)
	}
	if _, err := os.Stat(dir + "/wake"); err == nil {
		t.Fatal("turning it off did not delete the clips")
	}
	if fmt.Sprint(saved) != "[true false]" {
		t.Fatalf("saved %v", saved)
	}
	// An on that cannot be saved is refused, and the switch stays where it was.
	saveKeepClips = func(bool) error { return errors.New("flash is full") }
	if err := d.SetKeepClips(true); err == nil || c.on.Load() {
		t.Fatalf("an on that did not save: %v", err)
	}
}
