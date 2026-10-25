package voice

import (
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/phone"
)

var testBook = []phone.Contact{
	{Name: "Mom", Number: "15551234567", Also: []string{"Mother"}},
	{Name: "Alex", Number: "15557654321"},
	{Name: "Sam Smith", Number: "15550001111", Also: []string{"Sammy"}},
	{Name: "The Office", Number: "106"},
	{Name: "O'Brien", Number: "15552223333"},
}

func TestWhatAsksForACall(t *testing.T) {
	for text, want := range map[string]string{
		"Call Mom.":                          "Mom",
		"call mom":                           "Mom",
		"CALL MOM!":                          "Mom",
		"Call Mother.":                       "Mom",
		"Phone Alex.":                        "Alex",
		"Ring Alex":                          "Alex",
		"Dial Alex.":                         "Alex",
		"Call up Alex.":                      "Alex",
		"Call to Alex":                       "Alex",
		"Please call Mom.":                   "Mom",
		"Hey, call Mom.":                     "Mom",
		"Can you call Mom, please?":          "Mom",
		"Could you please call Mom?":         "Mom",
		"Would you call Mom for me?":         "Mom",
		"Call Mom now.":                      "Mom",
		"Call Mom on the phone.":             "Mom",
		"Call Mom now, please.":              "Mom",
		"Call Sam Smith.":                    "Sam Smith",
		"Call Sammy":                         "Sam Smith",
		"Call the Office.":                   "The Office",
		"Call Office":                        "",
		"Call O'Brien.":                      "O'Brien",
		"Give Mom a call.":                   "Mom",
		"Give Alex a call, please.":          "Alex",
		"Can you give Sam Smith a call?":     "Sam Smith",
		"Call the Mom":                       "Mom",
		"":                                   "",
		"Call":                               "",
		"Call Tom.":                          "",
		"Call Mo.":                           "",
		"Call Mommy":                         "",
		"Call mom's cell.":                   "",
		"Call Mom's phone":                   "",
		"Call Mom and Alex":                  "",
		"Call Mom later":                     "",
		"Call Sam":                           "",
		"Call Smith":                         "",
		"Call the the Office":                "",
		"Call the kitchen.":                  "",
		"Stop the alarm.":                    "",
		"Turn off the call.":                 "",
		"Mom":                                "",
		"What did Mom call about?":           "",
		"Don't call Mom.":                    "",
		"Call me Mom":                        "",
		"Call Mom a taxi":                    "",
		"Give Mom a ring":                    "",
		"Give Mom":                           "",
		"Hang up":                            "",
		"Call Mom please and turn the light": "",
	} {
		got, ok := callAsked(text, testBook)
		if ok != (want != "") || ok && got.Name != want {
			t.Errorf("%q: got %q %v, want %q", text, got.Name, ok, want)
		}
	}
}

// The number goes with the name it was found by.
func TestACallAskedHasItsNumber(t *testing.T) {
	got, ok := callAsked("Call Mother", testBook)
	if !ok || got.Number != "15551234567" {
		t.Errorf("got %+v %v", got, ok)
	}
	if _, ok := callAsked("Call Mom", nil); ok {
		t.Error("a call was found in an empty phone book")
	}
}

// standInPhone puts a phone book and a phone in place of the real ones for the rest of the test, and
// collects the calls placed.
func standInPhone(t *testing.T, configured, ready bool) chan string {
	t.Helper()
	placed := make(chan string, 4)
	wasNow, wasPlace := phoneNow, placeCall
	phoneNow = func() (bool, bool, []phone.Contact) { return configured, ready, testBook }
	placeCall = func(who phone.Contact, words string, ready bool) {
		if ready {
			placed <- words + " " + who.Number
		} else {
			placed <- words
		}
	}
	t.Cleanup(func() { phoneNow, placeCall = wasNow, wasPlace })
	return placed
}

// "Call Mom" ends the turn here, as a wake by mistake does, so Home Assistant's own reply never plays,
// and the call is placed to Mom's number after saying so.
func TestACallRequestIsPlacedHere(t *testing.T) {
	c := thinkingTurn(t)
	c.woke = true
	placed := standInPhone(t, true, true)

	c.handle(event{kind: evHeard, text: "Call Mother, please."})
	if c.phase != phaseIdle {
		t.Fatalf("after a call request the turn is %s, want idle", c.phase)
	}
	if !c.draining {
		t.Error("Home Assistant's run is not being drained")
	}
	select {
	case got := <-placed:
		if got != "Calling Mom. 15551234567" {
			t.Errorf("placed %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("no call placed")
	}
	c.handle(event{kind: evReplyText, text: "Sorry, I couldn't understand that."})
	if c.shown.Reply != "" {
		t.Errorf("Home Assistant's reply was taken up: %q", c.shown.Reply)
	}
}

// A phone with an account that is not signed in is said to be so, here: Home Assistant could not place
// the call either.
func TestACallRequestWithThePhoneSignedOut(t *testing.T) {
	c := thinkingTurn(t)
	placed := standInPhone(t, true, false)

	c.handle(event{kind: evHeard, text: "Call Mom"})
	if c.phase != phaseIdle {
		t.Fatalf("the turn is %s, want idle", c.phase)
	}
	select {
	case got := <-placed:
		if got != notSignedIn {
			t.Errorf("said %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("nothing said")
	}
}

// What is left to Home Assistant: a phone with no account, a sentence that is not a call to a contact,
// and a turn whose words answer something rather than ask.
func TestCallRequestsLeftToHomeAssistant(t *testing.T) {
	for _, c := range []struct {
		name       string
		configured bool
		text       string
		followUp   bool
		asked      bool
	}{
		{"no phone account", false, "Call Mom", false, false},
		{"not a contact", true, "Call the kitchen", false, false},
		{"stop the alarm", true, "Stop the alarm", false, false},
		{"a follow-up", true, "Call Mom", true, false},
		{"a turn Home Assistant opened", true, "Call Mom", false, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			conv := thinkingTurn(t)
			conv.followUp, conv.asked = c.followUp, c.asked
			placed := standInPhone(t, c.configured, true)
			conv.handle(event{kind: evHeard, text: c.text})
			if conv.phase == phaseIdle {
				t.Error("the turn was ended")
			}
			select {
			case got := <-placed:
				t.Errorf("placed %q", got)
			case <-time.After(50 * time.Millisecond):
			}
		})
	}
}
