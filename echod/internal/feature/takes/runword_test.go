package takes

import "testing"

// twoWords is a device with Hey Alfred in its first slot and Alexa in its second.
func twoWords() []SlotWord {
	return []SlotWord{
		{Slot: 0, ID: "hey_alfred", Front: "tflm", Phrase: "Hey Alfred"},
		{Slot: 1, ID: "alexa", Front: "zserge", Phrase: "Alexa"},
	}
}

// With two words loaded the first is the one a run is of until another is chosen, and the screen's
// stepper goes round them both ways.
func TestARunsWordIsTheFirstUntilAnotherIsChosen(t *testing.T) {
	_, f, _ := runDevice(t)
	words := twoWords()
	f.SetWakeWords(func() []SlotWord { return words }, func(Capture) {})
	if w, ok := f.RunWord(); !ok || w.ID != "hey_alfred" {
		t.Fatalf("the run is of %+v before a choice, want the first slot's", w)
	}
	f.StepRunWord(1)
	if w, _ := f.RunWord(); w.ID != "alexa" {
		t.Errorf("a step on gives %q", w.ID)
	}
	f.StepRunWord(1)
	if w, _ := f.RunWord(); w.ID != "hey_alfred" {
		t.Errorf("a step past the last gives %q, want round to the first", w.ID)
	}
	f.StepRunWord(-1)
	if w, _ := f.RunWord(); w.ID != "alexa" {
		t.Errorf("a step back from the first gives %q, want round to the last", w.ID)
	}
	if f.SetRunWord(5) {
		t.Error("a slot with no word was chosen")
	}
	if w, _ := f.RunWord(); w.ID != "alexa" {
		t.Errorf("an empty slot changed the choice to %q", w.ID)
	}
}

// A chosen word that goes is not held to: the run is of the first word left, and the choice comes
// back with the word.
func TestAChosenWordThatGoesFallsBackToTheFirst(t *testing.T) {
	_, f, _ := runDevice(t)
	words := twoWords()
	f.SetWakeWords(func() []SlotWord { return words }, func(Capture) {})
	f.SetRunWord(1)
	words = words[:1]
	f.WakeWordChanged()
	if w, _ := f.RunWord(); w.ID != "hey_alfred" {
		t.Errorf("with the chosen word gone the run is of %q", w.ID)
	}
	f.StepRunWord(1) // one word: nothing to step to
	if w, _ := f.RunWord(); w.ID != "hey_alfred" {
		t.Errorf("a step with one word gives %q", w.ID)
	}
	words = twoWords()
	f.WakeWordChanged()
	if w, _ := f.RunWord(); w.ID != "alexa" {
		t.Errorf("the chosen word came back and the run is of %q", w.ID)
	}
}

// A device whose only word is in its second slot offers that word, as the first loaded.
func TestAWordInTheSecondSlotAloneIsTheRunsWord(t *testing.T) {
	_, f, _ := runDevice(t)
	f.SetWakeWords(func() []SlotWord { return []SlotWord{{Slot: 1, ID: "alexa", Front: "zserge"}} }, func(Capture) {})
	if w, ok := f.RunWord(); !ok || w.ID != "alexa" || w.Name() != "alexa" {
		t.Errorf("the run is of %+v (%q)", w, w.Name())
	}
}

// The setup page's choice is the run's word, and a word the page showed that has gone since is said,
// not swapped for another.
func TestTheSetupPagesChoiceIsTheRunsWord(t *testing.T) {
	_, f, _ := runDevice(t)
	f.SetWakeWords(twoWords, func(Capture) {})
	if err := f.ArmRun(5, 2, 4, "browser"); err == nil || err.Error() != errWordGone {
		t.Errorf("a slot with no word was asked for: %v", err)
	}
	if f.Card().Up() {
		t.Error("a run of a word that has gone was asked for anyway")
	}
	if err := f.ArmRun(5, 2, 1, "browser"); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	r := f.run
	f.mu.Unlock()
	if r == nil || r.word != "alexa" || r.front != "zserge" {
		t.Errorf("the run asked for is %+v, want alexa on zserge", r)
	}
	f.DiscardPage("the test is over")
}
