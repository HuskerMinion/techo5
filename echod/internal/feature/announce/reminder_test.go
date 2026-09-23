package announce

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

func TestAReminderKeepsItsKindAndIdOnTheTrip(t *testing.T) {
	body, headers := encode(Message{From: "Kitchen", Text: "Pasta — now", Kind: KindReminder, ID: "kitchen-1"})
	r := httptest.NewRequest(http.MethodPost, "/announce", bytes.NewReader(body))
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	got, err := decode(r)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != KindReminder || got.ID != "kitchen-1" || got.Text != "Pasta — now" {
		t.Errorf("arrived as %+v", got)
	}

	// An announcement carries no kind, so a device that never heard of one reads it as before.
	_, headers = encode(Message{Text: "dinner"})
	if _, ok := headers[kindHeader]; ok {
		t.Error("an announcement grew a kind header")
	}
}

func TestPickingDevicesByName(t *testing.T) {
	house := []Peer{{Name: "Kitchen"}, {Name: "Office"}, {Name: "Guest Room"}}

	if got := pick(house, nil); len(got) != 3 {
		t.Errorf("nil picked %v, want the whole house", got)
	}
	got := pick(house, []string{"office", " guest room ", "Garage", "Office"})
	if len(got) != 2 || got[0].Name != "Office" || got[1].Name != "Guest Room" {
		t.Errorf("picked %v", got)
	}
	if got := pick(house, []string{}); len(got) != 0 {
		t.Errorf("no names picked %v", got)
	}
}

// A reminder that arrives goes to Reminded, and is not put up as an announcement: it is not one, and
// quiet hours must not hold it back.
func TestAReminderArrivingIsNotAnAnnouncement(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if err := config.Set().Home().HouseWord("costilla"); err != nil {
		t.Fatal(err)
	}
	f := &Feature{}
	var reminded, stopped []Message
	f.Reminded.Listen(func(m Message) { reminded = append(reminded, m) })
	f.ReminderStopped.Listen(func(m Message) { stopped = append(stopped, m) })

	post := func(m Message) int {
		body, headers := encode(m)
		r := httptest.NewRequest(http.MethodPost, "/announce", bytes.NewReader(body))
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		r.Header.Set(header, "costilla")
		w := httptest.NewRecorder()
		f.receive(w, r)
		return w.Code
	}

	if code := post(Message{From: "Kitchen", Text: "Pasta", Kind: KindReminder, ID: "kitchen-1"}); code != http.StatusNoContent {
		t.Fatalf("reminder answered %d", code)
	}
	if code := post(Message{From: "Kitchen", Kind: KindReminderStopped, ID: "kitchen-1"}); code != http.StatusNoContent {
		t.Fatalf("stop answered %d", code)
	}
	if code := post(Message{From: "Kitchen", Kind: KindReminder, ID: "kitchen-2"}); code != http.StatusBadRequest {
		t.Errorf("a reminder with nothing to say answered %d", code)
	}

	if len(reminded) != 1 || reminded[0].ID != "kitchen-1" || reminded[0].Text != "Pasta" {
		t.Errorf("reminded %+v", reminded)
	}
	if len(stopped) != 1 || stopped[0].ID != "kitchen-1" {
		t.Errorf("stopped %+v", stopped)
	}
	if _, showing := f.Showing(); showing {
		t.Error("a reminder was put up as an announcement")
	}
}
