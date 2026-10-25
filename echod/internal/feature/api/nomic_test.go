package api

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	esphome "github.com/ygelfand/go-esphome-device"
)

// On a device that cannot hear, the listening entities go and everything else stays, in order.
func TestListeningEntitiesAreDropped(t *testing.T) {
	sw := func(id string) esphome.Entity { return &esphome.Switch{Base: esphome.Base{ObjectID: id, Name: id}} }
	in := []esphome.Entity{
		sw("microphone_mixing"), sw("screen"), sw("wake_threshold_1"), sw("phone_answer"),
		sw("alarm_sounding"), sw("keep_recordings_2"), sw("room_level"), sw("night_mode"),
	}
	var got []string
	for _, e := range dropListening(in) {
		got = append(got, e.Object())
	}
	want := []string{"screen", "alarm_sounding", "night_mode"}
	if len(got) != len(want) {
		t.Fatalf("kept %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("kept %v, want %v", got, want)
		}
	}
	if len(in) != 8 || in[0].Object() != "microphone_mixing" {
		t.Error("dropListening changed the list it was given")
	}
	// Prefixes stop at their families: a name that only starts with a listed word stays.
	for _, keep := range []string{"wake_light", "phone_numbers_page", "reply"} {
		if listening(keep) && keep != "phone" {
			t.Errorf("%q dropped", keep)
		}
	}
}

// Every listed object id is one a feature really makes: a renamed entity would otherwise come back on
// a device without microphones, its old name still listed here and matching nothing.
func TestListedListeningEntitiesExist(t *testing.T) {
	var src strings.Builder
	err := filepath.WalkDir("..", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") || strings.Contains(p, string(filepath.Separator)+"api"+string(filepath.Separator)) {
			return nil
		}
		b, err := os.ReadFile(p)
		src.Write(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	all := src.String()
	for id := range listeningObjects {
		if !strings.Contains(all, `"`+id+`"`) {
			t.Errorf("no feature makes an entity %q", id)
		}
	}
	for name := range listeningActions {
		if !strings.Contains(all, `Name: "`+name+`"`) {
			t.Errorf("no feature makes an action %q", name)
		}
	}
	for _, p := range listeningPrefixes {
		if !strings.Contains(all, `"`+p+`%d"`) {
			t.Errorf("no feature makes entities %q%%d", p)
		}
	}
}

// On a device that cannot hear, the phone's actions go (signing in, calling) and the rest stay.
func TestCallActionsAreDropped(t *testing.T) {
	var in []*esphome.Action
	for _, n := range []string{"phone_account", "announce", "phone_call", "intercom_call", "alarm_set", "phone_hangup"} {
		in = append(in, &esphome.Action{Name: n})
	}
	var got []string
	for _, a := range dropCallActions(in) {
		got = append(got, a.Name)
	}
	if strings.Join(got, ",") != "announce,alarm_set" {
		t.Fatalf("kept %v, want [announce alarm_set]", got)
	}
	if len(in) != 6 {
		t.Error("dropCallActions changed the list it was given")
	}
}
