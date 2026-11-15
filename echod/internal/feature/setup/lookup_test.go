package setup

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/lib/netguard"
)

// waitLess makes a draw wait only a moment for an answer it does not have.
func waitLess(t *testing.T) {
	t.Helper()
	was := lookupWait
	lookupWait = 50 * time.Millisecond
	t.Cleanup(func() { lookupWait = was })
}

// An answer is asked for once and kept; a draw that comes before it does not wait for it; a server
// asked of another way is asked again; and forget drops what is kept.
func TestALookupAsksOnceAndDoesNotWait(t *testing.T) {
	waitLess(t)
	l := &lookup[int]{ask: 5 * time.Second}
	var asked atomic.Int32
	release := make(chan struct{})
	slow := func(ctx context.Context) int {
		asked.Add(1)
		select {
		case <-release:
		case <-ctx.Done():
		}
		return 7
	}
	key := lookupKey("http://server", "secret")
	start := time.Now()
	if _, ok := l.get(context.Background(), key, slow); ok {
		t.Fatal("an answer before the server gave one")
	}
	if _, ok := l.get(context.Background(), key, slow); ok {
		t.Fatal("an answer before the server gave one")
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("two draws waited %s for a server that did not answer", took)
	}
	close(release)
	deadline := time.Now().Add(3 * time.Second)
	for {
		if v, ok := l.get(context.Background(), key, slow); ok {
			if v != 7 {
				t.Errorf("kept %d", v)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the answer was never kept")
		}
	}
	if n := asked.Load(); n != 1 {
		t.Errorf("the server was asked %d times", n)
	}
	// Another server, or another secret, is another question.
	quick := func(context.Context) int { asked.Add(1); return 9 }
	if v, ok := l.get(context.Background(), lookupKey("http://server", "other"), quick); !ok || v != 9 {
		t.Errorf("another key got %d, %v", v, ok)
	}
	l.forget()
	if v, ok := l.get(context.Background(), lookupKey("http://server", "other"), quick); !ok || v != 9 || asked.Load() != 3 {
		t.Errorf("after forget: %d, %v, asked %d times", v, ok, asked.Load())
	}
	// The key kept is not the secret.
	if strings.Contains(string(key[:]), "secret") {
		t.Error("the key holds the secret")
	}
}

// The Immich section is drawn without waiting for an Immich that is slow to list its albums, and has
// them on a later draw.
func TestTheImmichSectionDoesNotWaitForImmich(t *testing.T) {
	waitLess(t)
	was := netguard.Refused
	netguard.Refused = func(ip net.IP) bool { return !ip.IsLoopback() && was(ip) }
	t.Cleanup(func() { netguard.Refused = was })
	release := make(chan struct{})
	var asked atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked.Add(1)
		<-release
		fmt.Fprintf(w, `[{"id":%q,"albumName":"Orchard","assetCount":3}]`, pageAlbum)
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if err := config.Set().Home().Immich(config.Immich{Server: srv.URL, Key: pageKey}); err != nil {
		t.Fatal(err)
	}
	draw := func() string {
		w := httptest.NewRecorder()
		immichSection(w, "tok")
		return w.Body.String()
	}
	start := time.Now()
	if page := draw(); !strings.Contains(page, "Asking Immich") || strings.Contains(page, "Orchard") {
		t.Errorf("a slow Immich drew: %s", first(page))
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("the section waited %s for Immich", took)
	}
	close(release)
	deadline := time.Now().Add(3 * time.Second)
	for !strings.Contains(draw(), "Orchard (3 photos)") {
		if time.Now().After(deadline) {
			t.Fatal("the albums never came")
		}
	}
	if n := asked.Load(); n != 1 {
		t.Errorf("Immich was asked %d times", n)
	}
}
