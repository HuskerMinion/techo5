package config

import (
	"path/filepath"
	"testing"
)

func TestNowPlayingLayoutPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	st, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Get().Screen.ModernNowPlaying {
		t.Fatal("a new device should use Classic")
	}
	for _, modern := range []bool{true, false} {
		if err := st.Set().Screen().ModernNowPlaying(modern); err != nil {
			t.Fatal(err)
		}
		st, err = Load(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := st.Get().Screen.ModernNowPlaying; got != modern {
			t.Errorf("reloaded Modern = %t, want %t", got, modern)
		}
	}
}
