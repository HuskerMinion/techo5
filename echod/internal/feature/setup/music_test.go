package setup

import (
	"net/url"
	"path/filepath"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// The form as the page sends it carries the page's own token too, named "token": the Music Assistant
// token is its own field, and the page's is never taken for it.
func TestMusicAssistantSavesItsOwnToken(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	post := func(v url.Values) string {
		v.Set("token", "the-pages-own-token")
		v.Set("what", "music")
		return saveMusic(form(v))
	}
	if p := post(url.Values{"url": {"http://192.168.1.20:8095/"}, "matoken": {"ma-secret"}}); p != "" {
		t.Fatal(p)
	}
	if m := config.Get().MusicAssistant; m.URL != "http://192.168.1.20:8095" || m.Token != "ma-secret" {
		t.Errorf("saved %+v", m)
	}
	// Saved again with the token field left empty: the saved token stays.
	if p := post(url.Values{"url": {"http://192.168.1.20:8095"}}); p != "" {
		t.Fatal(p)
	}
	if got := config.Get().MusicAssistant.Token; got != "ma-secret" {
		t.Errorf("an empty field changed the token to %q", got)
	}
	if p := post(url.Values{"url": {"http://192.168.1.20:8095"}, "notoken": {"yes"}}); p != "" || config.Get().MusicAssistant.Token != "" {
		t.Errorf("remove left %q (%s)", config.Get().MusicAssistant.Token, p)
	}
	for _, bad := range []url.Values{
		{"url": {"http://user:pw@192.168.1.20:8095"}},
		{"url": {"ftp://192.168.1.20"}},
		{"url": {"http://192.168.1.20:8095"}, "matoken": {"two\twords"}},
	} {
		if post(bad) == "" {
			t.Errorf("%v was saved", bad)
		}
	}
}

// The Music source is saved from a form that showed it, checked for the shape of a Music Assistant id,
// and left alone by one that did not show it.
func TestMusicSourceSaved(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	post := func(v url.Values) string {
		v.Set("token", "the-pages-own-token")
		v.Set("what", "music")
		v.Set("url", "http://192.168.1.20:8095")
		return saveMusic(form(v))
	}
	if p := post(url.Values{"source": {"ytmusic--a1b2"}}); p != "" || config.Get().MusicAssistant.Source != "ytmusic--a1b2" {
		t.Fatalf("saved %q (%s)", config.Get().MusicAssistant.Source, p)
	}
	if p := post(url.Values{}); p != "" || config.Get().MusicAssistant.Source != "ytmusic--a1b2" {
		t.Errorf("a form without the choice changed it to %q (%s)", config.Get().MusicAssistant.Source, p)
	}
	if p := post(url.Values{"source": {""}}); p != "" || config.Get().MusicAssistant.Source != "" {
		t.Errorf("Any left %q (%s)", config.Get().MusicAssistant.Source, p)
	}
	for _, bad := range []string{"<script>", "a b", "yt/music", string(make([]byte, 65))} {
		if post(url.Values{"source": {bad}}) == "" {
			t.Errorf("%q was saved", bad)
		}
	}
	// A refused source saves nothing else from the form either.
	before := config.Get().MusicAssistant.URL
	if post(url.Values{"url": {"http://192.168.1.20:8096"}, "source": {"a b"}}) == "" || config.Get().MusicAssistant.URL != before {
		t.Errorf("a refused form changed the address to %q", config.Get().MusicAssistant.URL)
	}
}

// The playback delay is saved from the form that shows it, refused outside 0 to the maximum before
// anything is saved, and left alone by a form that does not carry it.
func TestSendspinDelaySaved(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	post := func(v url.Values) string {
		v.Set("token", "the-pages-own-token")
		return saveMusic(form(v))
	}
	if p := post(url.Values{"sendspin_delay": {"250"}}); p != "" || config.Get().Sendspin.DelayMs != 250 {
		t.Errorf("saved %d (%s), want 250", config.Get().Sendspin.DelayMs, p)
	}
	if p := post(url.Values{"url": {"http://192.168.1.20:8095"}}); p != "" || config.Get().Sendspin.DelayMs != 250 {
		t.Errorf("a form without it changed the delay to %d (%s)", config.Get().Sendspin.DelayMs, p)
	}
	for _, bad := range []string{"-1", "1001", "slow", "1.5"} {
		if post(url.Values{"sendspin_delay": {bad}}) == "" || config.Get().Sendspin.DelayMs != 250 {
			t.Errorf("%q was saved, delay %d", bad, config.Get().Sendspin.DelayMs)
		}
	}
	if p := post(url.Values{"sendspin_delay": {"0"}}); p != "" || config.Get().Sendspin.DelayMs != 0 {
		t.Errorf("0 saved %d (%s)", config.Get().Sendspin.DelayMs, p)
	}
}
