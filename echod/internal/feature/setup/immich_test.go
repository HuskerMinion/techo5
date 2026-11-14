package setup

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/lib/netguard"
)

const (
	pageKey   = "made-up-key-for-the-page-test-42"
	pageAlbum = "22222222-2222-2222-2222-222222222222"
)

// pageImmich is a fake Immich with one album, on the loopback, let through netguard for the test.
func pageImmich(t *testing.T) string {
	t.Helper()
	was := netguard.Refused
	netguard.Refused = func(ip net.IP) bool { return !ip.IsLoopback() && was(ip) }
	t.Cleanup(func() { netguard.Refused = was })
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != pageKey {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		fmt.Fprintf(w, `[{"id":%q,"albumName":"Garden <b>","assetCount":7}]`, pageAlbum)
	}))
	t.Cleanup(srv.Close)
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	return srv.URL
}

func immichForm(v url.Values) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/setup/save", strings.NewReader(v.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	_ = r.ParseForm()
	return r
}

// A server is kept only once Immich has answered with its key; a wrong key is said as such; a kept key
// goes only to the server it was given for.
func TestSavingAnImmichServer(t *testing.T) {
	addr := pageImmich(t)
	if got := saveImmich(immichForm(url.Values{"server": {addr}, "key": {"wrong"}})); !strings.Contains(got, "refused") {
		t.Errorf("a wrong key: %q", got)
	}
	if config.Get().Home.Immich.Set() {
		t.Fatal("kept after a wrong key")
	}
	if got := saveImmich(immichForm(url.Values{"server": {addr + "/"}, "key": {pageKey}})); got != "" {
		t.Fatalf("refused: %q", got)
	}
	if i := config.Get().Home.Immich; i.Server != addr || i.Key != pageKey {
		t.Errorf("kept %+v", i)
	}
	// Saved again with the key left empty, the kept one stays; for another server it is not sent.
	if got := saveImmich(immichForm(url.Values{"server": {addr}})); got != "" {
		t.Errorf("an empty key with the same server: %q", got)
	}
	if got := saveImmich(immichForm(url.Values{"server": {"http://192.168.1.99:2283"}})); got == "" {
		t.Error("the kept key went to another server")
	}
	if got := saveImmich(immichForm(url.Values{"server": {"ftp://x"}, "key": {pageKey}})); got == "" {
		t.Error("an ftp address was taken")
	}
}

// The section says a key is kept and never shows it; the album names are escaped; a choice that is not
// Immich's is refused; forgetting drops the key and the photos with it.
func TestTheImmichSection(t *testing.T) {
	addr := pageImmich(t)
	if err := config.Set().Home().Immich(config.Immich{Server: addr, Key: pageKey}); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	immichSection(w, "tok")
	page := w.Body.String()
	if strings.Contains(page, pageKey) {
		t.Fatal("the page shows the key")
	}
	for _, want := range []string{secretSaved, "The whole library", home.ImmichAlbumPrefix + pageAlbum, "Garden &lt;b&gt; (7 photos)", "Forget this server"} {
		if !strings.Contains(page, want) {
			t.Errorf("the section lacks %q", want)
		}
	}
	for _, bad := range []string{"immich:asset/x", "media-source://media_source", home.ImmichAlbumPrefix + "../x"} {
		if got := chooseImmichAlbum(immichForm(url.Values{"source": {bad}})); got == "" {
			t.Errorf("%q was taken as the photos", bad)
		}
	}
	if got := chooseImmichAlbum(immichForm(url.Values{"source": {home.ImmichAlbumPrefix + pageAlbum}})); got != "" {
		t.Fatalf("the album was refused: %q", got)
	}
	if source, _, _ := home.Get().SlideshowSettings(); source != home.ImmichAlbumPrefix+pageAlbum {
		t.Errorf("source = %q", source)
	}
	if got := forgetImmich(); got != "" {
		t.Fatal(got)
	}
	if source, _, _ := home.Get().SlideshowSettings(); source != "" || config.Get().Home.Immich.Key != "" {
		t.Errorf("after forgetting: source %q, key kept %v", source, config.Get().Home.Immich.Key != "")
	}
}

// Saving the same server again keeps the album's name with its id, so the album is still named after
// a restart.
func TestSavingAgainKeepsTheAlbumsName(t *testing.T) {
	addr := pageImmich(t)
	if err := config.Set().Home().Immich(config.Immich{Server: addr, Key: pageKey, Album: "Garden", AlbumID: pageAlbum}); err != nil {
		t.Fatal(err)
	}
	if got := saveImmich(immichForm(url.Values{"server": {addr}})); got != "" {
		t.Fatalf("refused: %q", got)
	}
	if i := config.Get().Home.Immich; i.Album != "Garden" || i.AlbumID != pageAlbum {
		t.Errorf("kept %+v", i)
	}
}
