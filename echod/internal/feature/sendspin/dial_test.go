package sendspin

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

func TestMusicAssistantSendspinAddress(t *testing.T) {
	for api, want := range map[string]string{
		"http://100.64.0.10:8095":     "ws://100.64.0.10:8927/sendspin",
		"https://ma.example.com/":     "wss://ma.example.com:8927/sendspin",
		"http://[fd7a::1]:8095":       "ws://[fd7a::1]:8927/sendspin",
		"":                            "",
		"ftp://192.168.1.20":          "",
		"not a url at all, really ::": "",
	} {
		if got := maSendspinURL(api); got != want {
			t.Errorf("%q: %q, want %q", api, got, want)
		}
	}
}

// fakeMA is a Music Assistant Sendspin server that counts the devices that dial it.
func fakeMA(t *testing.T) (port int, dialed *atomic.Int32) {
	dialed = &atomic.Int32{}
	up := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != path {
			http.NotFound(w, r)
			return
		}
		// Counted as the request arrives: counted after the upgrade, the device can see its session
		// start before the count does (a flake seen in CI).
		dialed.Add(1)
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		c.ReadMessage() // held until the device hangs up
		c.Close()
	}))
	t.Cleanup(srv.Close)
	_, p, _ := net.SplitHostPort(srv.Listener.Addr().String())
	port, _ = strconv.Atoi(p)
	return port, dialed
}

// fakeAdvertise stands in for mDNS: it reports whether the device is advertised, and fails the test
// if the device dials while it is.
func fakeAdvertise(t *testing.T) (advertised *atomic.Bool) {
	advertised = &atomic.Bool{}
	was, wasFor, wasDial, wasLook := advertiseFn, advertiseFor, dialFor, lookEvery
	advertiseFn = func(ctx context.Context, _ string, _ int) {
		advertised.Store(true)
		<-ctx.Done()
		advertised.Store(false)
	}
	advertiseFor, dialFor, lookEvery = 300*time.Millisecond, 2*time.Second, 20*time.Millisecond
	t.Cleanup(func() { advertiseFn, advertiseFor, dialFor, lookEvery = was, wasFor, wasDial, wasLook })
	return advertised
}

// connectFor runs connect until the test ends, and is done with it before the test's variables are put
// back.
func connectFor(t *testing.T, l *listener) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { l.connect(ctx, "Test", Port); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
}

// The device is advertised, and dials Music Assistant only once it is set up and no server has dialed
// in for a while, and never while it is advertised.
func TestTheDeviceDialsMusicAssistant(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	advertised := fakeAdvertise(t)
	port, dialed := fakeMA(t)
	wasPort, wasRun := maPort, runSession
	maPort = port
	ran := make(chan struct{}, 4)
	runSession = func(ctx context.Context, l *listener, conn *websocket.Conn, name string) error {
		if advertised.Load() {
			t.Error("dialed while advertised")
		}
		ran <- struct{}{}
		<-ctx.Done()
		return nil
	}
	t.Cleanup(func() { maPort, runSession = wasPort, wasRun })

	l := newListener(nil, nil, func(string) {})

	// Not set up: advertised, and nothing is dialed.
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { l.connect(ctx, "Test", Port); close(done) }()
	time.Sleep(time.Second)
	if !advertised.Load() || dialed.Load() != 0 {
		t.Fatalf("not set up: advertised %v, dialed %d", advertised.Load(), dialed.Load())
	}
	cancel()
	<-done
	if advertised.Load() {
		t.Fatal("still advertised after the switch went off")
	}

	// Set up, with a server in the room: nothing is dialed over it, and nothing advertised.
	tok := "t"
	if err := config.Set().MusicAssistant().Server("http://127.0.0.1:8095", &tok); err != nil {
		t.Fatal(err)
	}
	l.take()
	ctx, cancel = context.WithCancel(context.Background())
	done = make(chan struct{})
	go func() { l.connect(ctx, "Test", Port); close(done) }()
	time.Sleep(time.Second)
	if advertised.Load() || dialed.Load() != 0 {
		t.Fatalf("held: advertised %v, dialed %d", advertised.Load(), dialed.Load())
	}
	cancel()
	<-done
	l.give()

	// Set up and free: Music Assistant is dialed first, at once, and the session runs.
	start := time.Now()
	connectFor(t, l)
	select {
	case <-ran:
	case <-time.After(5 * time.Second):
		t.Fatal("the device never dialed Music Assistant")
	}
	if waited := time.Since(start); waited > time.Second {
		t.Errorf("dialed after %v, not at once", waited)
	}
	if dialed.Load() != 1 {
		t.Errorf("dialed %d times", dialed.Load())
	}
	if l.take() {
		t.Error("the room was free while the dialed session ran")
	}
}

// A Music Assistant that cannot be reached is dialed for a while, then the device is advertised, and
// with no server dialing in it dials again.
func TestTheDeviceAdvertisesAgainWhenMusicAssistantIsAway(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	advertised := fakeAdvertise(t)
	wasPort := maPort
	maPort = 1 // nothing listens there
	t.Cleanup(func() { maPort = wasPort })
	tok := "t"
	if err := config.Set().MusicAssistant().Server("http://127.0.0.1:8095", &tok); err != nil {
		t.Fatal(err)
	}

	connectFor(t, newListener(nil, nil, func(string) {}))

	saw := func(want bool, within time.Duration) {
		t.Helper()
		for end := time.Now().Add(within); time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
			if advertised.Load() == want {
				return
			}
		}
		t.Fatalf("advertised never became %v", want)
	}
	time.Sleep(200 * time.Millisecond)
	if advertised.Load() {
		t.Fatal("advertised before Music Assistant was dialed")
	}
	saw(true, 15*time.Second) // gave up on Music Assistant
	saw(false, 2*time.Second) // gave up on servers dialing in, dialing again
}

// Music Assistant set up while the device advertises is dialed at once, not after advertiseFor.
func TestMusicAssistantSetUpLaterIsDialedAtOnce(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	advertised := fakeAdvertise(t)
	advertiseFor = time.Hour // only the new setting can end the advertising
	port, dialed := fakeMA(t)
	wasPort, wasRun := maPort, runSession
	maPort = port
	runSession = func(ctx context.Context, l *listener, conn *websocket.Conn, name string) error {
		<-ctx.Done()
		return nil
	}
	t.Cleanup(func() { maPort, runSession = wasPort, wasRun })

	connectFor(t, newListener(nil, nil, func(string) {}))
	for end := time.Now().Add(time.Second); time.Now().Before(end) && !advertised.Load(); time.Sleep(10 * time.Millisecond) {
	}
	if !advertised.Load() {
		t.Fatal("not advertised before Music Assistant was set up")
	}
	tok := "t"
	if err := config.Set().MusicAssistant().Server("http://127.0.0.1:8095", &tok); err != nil {
		t.Fatal(err)
	}
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
		if dialed.Load() > 0 {
			return
		}
	}
	t.Fatal("Music Assistant set up later was not dialed")
}
