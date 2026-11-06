package setup

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/phone"
)

func saveWC(v url.Values) string {
	r := httptest.NewRequest(http.MethodPost, "/setup/save", strings.NewReader(v.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return saves["wakecall"](r)
}

type wakeCallSet struct {
	slot    int
	contact string
}

func standInWakeCall(t *testing.T) *[]wakeCallSet {
	t.Helper()
	oldBook, oldSet := wakeCallBook, setWakeCall
	t.Cleanup(func() { wakeCallBook, setWakeCall = oldBook, oldSet })
	wakeCallBook = func() []phone.Contact {
		return []phone.Contact{{Name: "Chat service", Number: "100"}, {Name: "Front <Desk>", Number: "200"}}
	}
	var got []wakeCallSet
	setWakeCall = func(slot int, contact string) error {
		got = append(got, wakeCallSet{slot, contact})
		if contact == "refused" {
			return errors.New("wake call: choose a contact in the phone book")
		}
		return config.Set().Wake(slot - 1).CallContact(contact)
	}
	return &got
}

func TestWakeCallForm(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	_ = config.Set().Wake(0).ID("test_word")
	got := standInWakeCall(t)

	w := httptest.NewRecorder()
	wakeCallSection(w, "token")
	body := w.Body.String()
	if !strings.Contains(body, "the room is heard in the call") {
		t.Error("the page does not say the room is heard")
	}
	if !strings.Contains(body, `<option value="Chat service">Chat service</option>`) {
		t.Error("a contact is not offered")
	}
	if strings.Contains(body, "<Desk>") || strings.Contains(body, "100") {
		t.Error("a name is not escaped, or a number is shown")
	}

	// Unchanged: nothing is set.
	if p := saveWC(url.Values{"call0": {""}, "was_call0": {""}}); p != "" || len(*got) != 0 {
		t.Fatalf("%q %v", p, *got)
	}
	if p := saveWC(url.Values{"call0": {"Chat service"}, "was_call0": {""}}); p != "" {
		t.Fatal(p)
	}
	if len(*got) != 1 || (*got)[0] != (wakeCallSet{1, "Chat service"}) {
		t.Fatalf("%v", *got)
	}
	if config.Get().Wake.Slot(0).CallContact != "Chat service" {
		t.Fatal("not saved")
	}
	if p := saveWC(url.Values{"call0": {"refused"}, "was_call0": {"Chat service"}}); !strings.Contains(p, "phone book") {
		t.Fatalf("refusal not shown: %q", p)
	}

	// A contact gone from the book stays shown while it is chosen.
	wakeCallBook = func() []phone.Contact { return nil }
	w = httptest.NewRecorder()
	wakeCallSection(w, "token")
	if !strings.Contains(w.Body.String(), "Chat service (not in the phone book)") {
		t.Error("a chosen contact gone from the book is not shown")
	}
}

func TestWakeCallFormLeavesRealtimeSlotsAlone(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	_ = config.Set().Wake(0).ID("test_word")
	_ = config.Set().Wake(0).Realtime(true)
	got := standInWakeCall(t)
	w := httptest.NewRecorder()
	wakeCallSection(w, "token")
	if !strings.Contains(w.Body.String(), `name="call0" disabled`) {
		t.Error("a realtime slot can be chosen")
	}
	// A disabled select sends nothing.
	if p := saveWC(url.Values{"was_call0": {""}}); p != "" || len(*got) != 0 {
		t.Fatalf("%q %v", p, *got)
	}
}
