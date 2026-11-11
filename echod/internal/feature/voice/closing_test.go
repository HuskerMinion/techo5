package voice

import (
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

func TestClosingPhrase(t *testing.T) {
	for text, want := range map[string]bool{
		"Thank you.": true, " thanks!": true, "That's all.": true, "That’s it": true, "Never mind.": true,
		"Goodbye!": true, "I'm good.": true, "Danke schön.": true, "C'est tout.": true, "Спасибо.": true,
		"以上です。": true,
		"":      false, "Thanks, and what's the weather?": false, "Thank you, turn off the lights.": false,
		"What time is it?": false, "Good": false,
	} {
		if got := closingPhrase(text); got != want {
			t.Errorf("closingPhrase(%q) = %v, want %v", text, got, want)
		}
	}
}

// followUpHeard is a follow-up turn on a slot set to listen again, that has heard heard and been
// answered, with Home Assistant asking a question back when asks is set.
func followUpHeard(t *testing.T, heard string, asks bool) *conversation {
	t.Helper()
	c := thinkingTurn(t)
	if err := config.Set().Wake(0).FollowUp(5); err != nil {
		t.Fatal(err)
	}
	c.followUp = true
	c.handle(event{kind: evHeard, text: heard})
	c.handle(event{kind: evReplyText, text: "You're welcome."})
	if asks {
		c.handle(event{kind: evContinue})
	}
	return c
}

// "Thank you" in a follow-up is answered, and then the device stops listening (#510).
func TestAClosingPhraseEndsTheConversation(t *testing.T) {
	c := followUpHeard(t, "Thank you.", false)
	if c.followsReply(0) {
		t.Error("listening again after \"Thank you.\"")
	}
}

// Even a question Home Assistant asked back is not listened for after "never mind".
func TestAClosingPhraseEndsAQuestionToo(t *testing.T) {
	c := followUpHeard(t, "Never mind.", true)
	if c.pending == nil {
		t.Fatal("Home Assistant's question was not recorded")
	}
	if c.followsReply(0) || c.pending != nil {
		t.Errorf("after \"never mind\" to a question: pending %+v", c.pending)
	}
}

// Anything else is followed up as before, and Home Assistant's question still opens its answer.
func TestAnythingElseIsStillFollowedUp(t *testing.T) {
	if c := followUpHeard(t, "Thanks, and what time is it?", false); !c.followsReply(0) {
		t.Error("an ordinary follow-up was not followed up")
	}
	if c := followUpHeard(t, "The kitchen.", true); c.pending == nil {
		t.Error("an answer to Home Assistant's question dropped the next question")
	}
}

// A closing phrase belongs to the turn it was said in: the turn ending forgets it.
func TestAnEndedTurnForgetsTheClosing(t *testing.T) {
	c := followUpHeard(t, "Thank you.", false)
	c.handle(event{kind: evCancel})
	if c.closing {
		t.Error("an ended turn still carries its closing phrase")
	}
}
