package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The text size is kept in state.json across a restart, unset on a new device so the device's own
// size is used, and held to what can be drawn.
func TestTextSizeIsKept(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state.json")
	st, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if v := st.Get().Screen.TextSize; v != 0 {
		t.Fatalf("a new device has text size %d, want 0 (the device's own)", v)
	}
	if err := st.Set().Screen().TextSize(125); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"text_size": 125`) {
		t.Errorf("state.json does not keep the text size:\n%s", b)
	}
	again, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if v := again.Get().Screen.TextSize; v != 125 {
		t.Errorf("after a restart the text size is %d, want 125", v)
	}

	for in, want := range map[int]int{80: 80, 10: TextSizeLeast, 900: TextSizeMost, 0: 0, -5: 0} {
		if err := st.Set().Screen().TextSize(in); err != nil {
			t.Fatal(err)
		}
		if v := st.Get().Screen.TextSize; v != want {
			t.Errorf("TextSize(%d) saved %d, want %d", in, v, want)
		}
	}
}
