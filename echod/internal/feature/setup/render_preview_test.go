package setup

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/alarm"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/feature/timer"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/buttons"
)

// With SETUP_PREVIEW set to a file, writes the page as a browser receives it, for looking at: the
// default tab at that name, and every tab beside it as name-<tab>.html. SETUP_THEME picks the
// screen's theme to wear.
func TestRenderSetupPage(t *testing.T) {
	out := os.Getenv("SETUP_PREVIEW")
	if out == "" {
		t.Skip("set SETUP_PREVIEW to a file to write the page")
	}
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if th := os.Getenv("SETUP_THEME"); th != "" {
		if err := config.Set().Screen().Theme(th); err != nil {
			t.Fatal(err)
		}
	}
	if err := home.SetOwnStations([]config.Station{
		{Name: "KXYZ 101.1", URL: "https://stream.example.org/kxyz"},
		{Name: "The Mountain", URL: "https://stream.example.org/mountain"},
	}); err != nil {
		t.Fatal(err)
	}
	a := alarm.Get()
	wake, _ := a.Set(6, 45, config.DaysWeekdays, "Wake up")
	wake.Sunrise = 20
	_ = a.Put(wake)
	_, _ = a.Set(9, 0, config.DaysWeekends, "")
	_, _ = a.SetReminder(14, 30, config.DaysOnce, "Take the medication", []string{"Kitchen", "Office"})
	timer.Get().Start("Pasta", 272*time.Second)

	f := build()
	f.Open()
	c := ask(t, f)
	f.button(buttons.Event{Name: buttons.Action, Kind: buttons.Tap})

	write := func(path, to string) {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.AddCookie(c)
		w := httptest.NewRecorder()
		f.serve(w, r)
		if err := os.WriteFile(to, w.Body.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("/setup?saved=1", out)
	base := strings.TrimSuffix(out, filepath.Ext(out))
	for _, tb := range tabs {
		write("/setup?tab="+tb.id, base+"-"+tb.id+".html")
	}
}
