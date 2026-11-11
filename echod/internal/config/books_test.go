package config

import (
	"path/filepath"
	"testing"
	"time"
)

// A book's place is kept, the latest first, saved again over its old place, let go when asked, and
// written to the file so it outlasts a restart.
func TestBookSpotsAreKeptLatestFirstAndSurviveARestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	st, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Unix(1_700_000_000, 0)
	w := st.Set().BookPlaces()
	if err := w.Save("library://audiobook/1", 600, at); err != nil {
		t.Fatal(err)
	}
	if err := w.Save("library://audiobook/2", 90, at.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := w.Save("library://audiobook/1", 1200, at.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}

	again, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	got := again.Get().BookPlaces
	if len(got.List) != 2 || got.List[0].URI != "library://audiobook/1" {
		t.Fatalf("places = %+v; want book 1 latest, then book 2", got.List)
	}
	if s, ok := got.Spot("library://audiobook/1"); !ok || s != 1200 {
		t.Errorf("book 1 at %d (%v); want 1200", s, ok)
	}

	if err := again.Set().BookPlaces().Forget("library://audiobook/1"); err != nil {
		t.Fatal(err)
	}
	if _, ok := again.Get().BookPlaces.Spot("library://audiobook/1"); ok {
		t.Errorf("book 1 still has a place after it was let go")
	}
}

// Past MaxBookSpots the place left longest ago is the one let go.
func TestBookSpotsKeepOnlyTheLatest(t *testing.T) {
	st, err := Load(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	at := time.Unix(1_700_000_000, 0)
	for i := range MaxBookSpots + 3 {
		uri := "library://audiobook/" + string(rune('a'+i%26)) + string(rune('a'+i/26))
		if err := st.Set().BookPlaces().Save(uri, 100+i, at.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	got := st.Get().BookPlaces.List
	if len(got) != MaxBookSpots {
		t.Fatalf("kept %d places; want %d", len(got), MaxBookSpots)
	}
	if got[0].Seconds != 100+MaxBookSpots+2 {
		t.Errorf("latest kept is %+v; want the last saved first", got[0])
	}
}
