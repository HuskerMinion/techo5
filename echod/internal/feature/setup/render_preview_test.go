package setup

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/buttons"
)

// With SETUP_PREVIEW set to a file, writes the page as a browser receives it, for looking at.
func TestRenderSetupPage(t *testing.T) {
	out := os.Getenv("SETUP_PREVIEW")
	if out == "" {
		t.Skip("set SETUP_PREVIEW to a file to write the page")
	}
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if err := home.SetOwnStations([]config.Station{
		{Name: "KXYZ 101.1", URL: "https://stream.example.org/kxyz"},
		{Name: "The Mountain", URL: "https://stream.example.org/mountain"},
	}); err != nil {
		t.Fatal(err)
	}
	f := build()
	f.Open()
	c := ask(t, f)
	f.button(buttons.Event{Name: buttons.Action, Kind: buttons.Tap})

	r := httptest.NewRequest(http.MethodGet, "/setup", nil)
	r.AddCookie(c)
	w := httptest.NewRecorder()
	f.serve(w, r)
	if err := os.WriteFile(out, w.Body.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %d bytes", w.Body.Len())
}
