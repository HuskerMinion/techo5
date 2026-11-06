package voice

import (
	"path/filepath"
	"slices"
	"testing"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/phone"
)

// wakeCallSelectVoice is a Voice with its selects built over the stand-ins, the phone's Changed left
// out: a test calls showWakeCalls itself.
func wakeCallSelectVoice(t *testing.T) (*Voice, []*esphome.Select) {
	t.Helper()
	old := wakeCallBookChanged
	t.Cleanup(func() { wakeCallBookChanged = old })
	wakeCallBookChanged = func(func()) {}
	v := wakeCallVoice()
	return v, v.wakeCallSelects()
}

func TestWakeCallOptionsAreOffTheBookAndTheChoice(t *testing.T) {
	book := []phone.Contact{
		{Name: "Front Desk", Number: "100", Also: []string{"Desk"}},
		{Name: "off", Number: "101"},
		{Name: "Gate", Number: "102"},
	}
	if got, want := wakeCallOptions(book, ""), []string{"Off", "Front Desk", "Gate"}; !slices.Equal(got, want) {
		t.Fatalf("options %v, want %v", got, want)
	}
	if got, want := wakeCallOptions(book, "Gone"), []string{"Off", "Front Desk", "Gate", "Gone"}; !slices.Equal(got, want) {
		t.Fatalf("a chosen contact gone from the book: options %v, want %v", got, want)
	}
	if wakeCallOption("") != "Off" || wakeCallOption("Gate") != "Gate" {
		t.Fatal("the chosen option is not what is saved")
	}
}

func TestWakeCallSelectsSitOnTheirAssistants(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	standIn(t)
	v, sels := wakeCallSelectVoice(t)
	if len(sels) != 2 || len(v.Entities()) != 2 {
		t.Fatalf("%d selects, want one for each wake word", len(sels))
	}
	for n, sel := range sels {
		if sel.DeviceID != component.AssistantDevice(n) || sel.Category != esphome.CategoryConfig {
			t.Fatalf("slot %d select is not a setting of its assistant", n+1)
		}
		if sel.Get() != wakeCallOff || !slices.Equal(sel.Options, []string{wakeCallOff, "Chat service"}) {
			t.Fatalf("slot %d select shows %q of %v", n+1, sel.Get(), sel.Options)
		}
	}
}

func TestWakeCallSelectChoosesAsTheSetupPageDoes(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	_ = config.Set().Wake(0).ID("test_word")
	standIn(t)
	_, sels := wakeCallSelectVoice(t)

	sels[0].OnCommand("Chat service")
	if config.Get().Wake.Slot(0).CallContact != "Chat service" || sels[0].Get() != "Chat service" {
		t.Fatalf("chosen in the select: saved %q, shown %q", config.Get().Wake.Slot(0).CallContact, sels[0].Get())
	}

	// A slot with no wake word is refused, and the select goes back to what is saved.
	sels[1].OnCommand("Chat service")
	if config.Get().Wake.Slot(1).CallContact != "" || sels[1].Get() != wakeCallOff {
		t.Fatalf("a slot with no word took a call: shown %q", sels[1].Get())
	}

	// So is a slot set for realtime voice.
	_ = config.Set().Wake(1).ID("other_word")
	_ = config.Set().Wake(1).Realtime(true)
	sels[1].OnCommand("Chat service")
	if config.Get().Wake.Slot(1).CallContact != "" || sels[1].Get() != wakeCallOff {
		t.Fatalf("a realtime slot took a call: shown %q", sels[1].Get())
	}

	sels[0].OnCommand(wakeCallOff)
	if config.Get().Wake.Slot(0).CallContact != "" || sels[0].Get() != wakeCallOff {
		t.Fatal("Off did not clear the call")
	}
}

func TestWakeCallSelectShowsAChoiceMadeElsewhere(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	_ = config.Set().Wake(0).ID("test_word")
	standIn(t)
	_, sels := wakeCallSelectVoice(t)
	if err := SetWakeCall(1, "assistant"); err != nil {
		t.Fatal(err)
	}
	if sels[0].Get() != "Chat service" {
		t.Fatalf("the setup page's choice is not shown: %q", sels[0].Get())
	}
}

func TestWakeCallSelectFollowsThePhoneBook(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	_ = config.Set().Wake(0).ID("test_word")
	s := standIn(t)
	v, sels := wakeCallSelectVoice(t)
	sels[0].OnCommand("Chat service")

	reconnects := 0
	cancel := component.Reconnect.Listen(func(struct{}) { reconnects++ })
	t.Cleanup(cancel)

	// Nothing new: no new connection.
	v.showWakeCalls()
	if reconnects != 0 {
		t.Fatal("an unchanged phone book asked for a new connection")
	}

	// The chosen contact gone, another added: it stays offered while chosen.
	s.book = []phone.Contact{{Name: "Gate", Number: "102"}}
	v.showWakeCalls()
	if got, want := sels[0].Options, []string{wakeCallOff, "Gate", "Chat service"}; !slices.Equal(got, want) {
		t.Fatalf("slot 1 options %v, want %v", got, want)
	}
	if got, want := sels[1].Options, []string{wakeCallOff, "Gate"}; !slices.Equal(got, want) {
		t.Fatalf("slot 2 options %v, want %v", got, want)
	}
	if sels[0].Get() != "Chat service" || reconnects != 1 {
		t.Fatalf("shown %q, %d new connections", sels[0].Get(), reconnects)
	}
}
