package voice

import (
	"strings"
	"unicode"
)

// closings are what somebody says to end a conversation (#510). Said as the whole of a turn, the reply
// to it is still spoken, and then the device stops listening rather than opening another follow-up.
// Only the whole utterance counts: "thanks, and what's the weather" goes on as any other turn.
var closings = map[string]bool{
	"thank you": true, "thank you very much": true, "thanks": true, "thanks a lot": true,
	"thank you so much": true, "no thanks": true, "no thank you": true,
	"that's all": true, "that is all": true, "that's it": true, "that's all thanks": true,
	"that's all thank you": true, "never mind": true, "nevermind": true, "goodbye": true, "bye": true,
	"bye bye": true, "i'm good": true, "i'm done": true, "we're done": true, "nothing else": true,
	// The same in the other languages the screen offers.
	"danke": true, "danke schön": true, "danke dir": true, "das war's": true, "das wär's": true,
	"tschüss": true, "egal": true, "vergiss es": true,
	"gracias": true, "muchas gracias": true, "eso es todo": true, "adiós": true, "nada más": true,
	"merci": true, "merci beaucoup": true, "c'est tout": true, "au revoir": true, "laisse tomber": true,
	"grazie": true, "grazie mille": true, "è tutto": true, "basta così": true, "ciao": true,
	"arrivederci": true, "lascia stare": true,
	"obrigado": true, "obrigada": true, "muito obrigado": true, "muito obrigada": true,
	"é só isso": true, "só isso": true, "tchau": true, "deixa pra lá": true,
	"спасибо": true, "большое спасибо": true, "это всё": true, "это все": true, "пока": true,
	"до свидания": true, "неважно": true,
	"ありがとう": true, "ありがとうございます": true, "以上": true, "以上です": true, "もういい": true,
	"大丈夫": true, "さようなら": true,
}

// closingPhrase is whether text is nothing but a way of ending the conversation: case, spacing and
// punctuation aside, one of the phrases above.
func closingPhrase(text string) bool {
	text = strings.ReplaceAll(strings.ToLower(text), "’", "'")
	words := strings.FieldsFunc(text, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '\''
	})
	if len(words) == 0 {
		return false
	}
	return closings[strings.Join(words, " ")]
}
