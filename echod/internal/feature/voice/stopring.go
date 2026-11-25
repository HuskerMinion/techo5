package voice

import (
	"strings"
	"unicode"
)

// stopVocab is one language's words for stopping a ring: asks are the words that ask for it ("stop",
// "off"), and fillers the words said around them ("the alarm", "please", "now"), which ask for nothing
// on their own.
type stopVocab struct {
	asks, fillers map[string]bool
}

// stopVocabs are the words a request to stop a ring is made of, and nothing else, one language at a
// time: "stop", "stop the alarm", "turn the timer off please", "Stopp, bitte", "apaga la alarma". A
// sentence with any other word in it is something else and is left to Home Assistant: "stop the music"
// is not about the alarm.
//
// One language at a time, so the words of two cannot make a sentence neither would say: the Spanish
// "es" ("is") and the German "es" ("it") are fillers of their own language only.
//
// Some of the words mean something else too, and are here only because a sentence has to be made of
// nothing but these words to count. The Spanish and Portuguese "para" is "for" as well as "stop", the
// Dutch "uit" is "out" and the German "aus" "from": said on their own, or with nothing but "the alarm"
// or "please" around them, over a ring that the turn was opened over, they can only be a stop. With
// any other word in the sentence ("para mañana", "aus dem Radio"), nothing matches and nothing stops.
// The words for "enough" ("basta", "хватит", "די", "prou") are here because, said on their own over
// a ring, they are how a stop is said. Nothing is here that asks for something else on its own: no
// German "halt mal" ("hold on"), no "gata" ("done", "ready"), no "chega" ("arrives" as well as
// "enough").
//
// The languages are those of the screen, and Portuguese, Russian and Hebrew, which people speak to it
// in (#741). "stop" asks in every one of them, and "ok" and "okay" go with any of them, as speech to
// text writes them so in most languages.
var stopVocabs = []stopVocab{
	// English. "time" is there because speech to text often writes "timer" so (#724): over a ring,
	// "stop time" can only mean the ring.
	vocab("stop off",
		"turn it that the alarm timer ringing please now shut up time timers alarms"),
	// German: "Stopp", "Timer aus", "mach den Wecker aus, bitte".
	vocab("stopp stoppen halt aus ausmachen ausschalten abschalten",
		"mach mache schalt schalte den die das der es ihn wecker timer alarm bitte jetzt sofort"),
	// Spanish: "para", "detén la alarma", "basta ya", "apaga el temporizador por favor".
	vocab("para pare parar detén deten detente detener basta apaga apágala apágalo apagala apagalo apagar",
		"la el lo alarma alarmas temporizador temporizadores ya ahora eso por favor"),
	// French: "arrête", "arrête l'alarme", "éteins le minuteur s'il te plaît". The apostrophe keeps
	// "l'alarme" and "s'il" one word each.
	vocab("arrête arrete arrêtez arretez arrêter arreter stoppe éteins eteins éteindre eteindre",
		"le la l'alarme alarme réveil reveil minuteur minuterie sonnerie ça ca s'il te vous plaît plait maintenant tout de suite"),
	// Italian: "ferma", "spegni la sveglia", "basta, per favore".
	vocab("ferma fermala fermalo fermati fermare basta spegni spegnila spegnilo spegnere stoppa",
		"la il lo l'allarme allarme sveglia timer per favore adesso ora subito"),
	// Dutch: "stop", "zet de wekker uit", "stop maar".
	vocab("stoppen uit uitzetten",
		"zet het de die dat wekker timer alarm nu alsjeblieft alstublieft graag even maar oké"),
	// Portuguese: "para", "desliga o alarme", "pare o despertador por favor".
	vocab("para pare parar desliga desligue desligar",
		"o a isso alarme alarmes despertador temporizador timer por favor agora já ja"),
	// Polish: "stop", "wyłącz budzik", "zatrzymaj to, proszę".
	vocab("zatrzymaj wyłącz wylacz przestań przestan",
		"to ten go budzik alarm minutnik timer proszę prosze teraz już juz"),
	// Catalan and Valencian: "para", "atura l'alarma", "apaga-la, si us plau". A hyphen parts "apaga-la"
	// into two words, which both count.
	vocab("para pare atura atura't aturar apaga apagar prou",
		"la el l'alarma alarma despertador temporitzador si us plau per favor ara ja això aixo"),
	// Galician: "para", "apaga a alarma", "detén o temporizador por favor".
	vocab("para pare parar detén deten apaga apagar basta",
		"a o alarma temporizador despertador por favor xa agora iso"),
	// Romanian: "oprește", "oprește alarma", "oprește-l, te rog". Written with ş or ș alike
	// (stopFold).
	vocab("oprește opreste",
		"l o te rog acum alarma alarmă alarmele timerul timer cronometrul deșteptătorul desteptatorul"),
	// Turkish: "dur", "alarmı durdur", "kapat lütfen".
	vocab("dur durdur kapat",
		"alarm alarmı alarmi zamanlayıcı zamanlayici zamanlayıcıyı zamanlayiciyi onu lütfen lutfen şimdi simdi artık artik tamam"),
	// Russian: "стоп", "выключи будильник", "хватит, пожалуйста".
	vocab("стоп хватит выключи выключить останови прекрати",
		"будильник таймер сигнал его это пожалуйста сейчас уже"),
	// Hebrew: "עצור", "די", "כבה את השעון המעורר".
	vocab("עצור עצרי די תפסיק תפסיקי כבה כבי",
		"את זה השעון המעורר הטיימר האזעקה בבקשה עכשיו"),
	// Japanese is written without spaces, so each way of saying it is one word here.
	vocab("ストップ 止めて とめて 止めてください とめてください アラームを止めて アラーム止めて タイマーを止めて タイマー止めて", ""),
}

// vocab makes a stopVocab of space-separated words, with "stop" asking in it and "ok" and "okay" said
// around it.
func vocab(asks, fillers string) stopVocab {
	v := stopVocab{asks: map[string]bool{"stop": true}, fillers: map[string]bool{"ok": true, "okay": true}}
	for _, w := range strings.Fields(stopFold.Replace(asks)) {
		v.asks[w] = true
	}
	for _, w := range strings.Fields(stopFold.Replace(fillers)) {
		v.fillers[w] = true
	}
	return v
}

// stopsRing reports whether a transcript asks for the ring to stop: only the words of one language
// above, and at least one of them a word that asks.
func stopsRing(text string) bool {
	words := stopSplit(text)
	if len(words) == 0 {
		return false
	}
	for _, v := range stopVocabs {
		if v.stops(words) {
			return true
		}
	}
	return false
}

func (v stopVocab) stops(words []string) bool {
	asks := false
	for _, w := range words {
		switch {
		case v.asks[w]:
			asks = true
		case !v.fillers[w]:
			return false
		}
	}
	return asks
}

// stopFold reads the typographic apostrophe as the plain one, and Romanian's s and t with a comma below
// as the ones with a cedilla, which many keyboards and some speech to text still write for them: the
// same word either way. That way round, and not the other, because the Turkish ş is the one with the
// cedilla, and folding it to a comma would make "şimdi" a word no list has.
var stopFold = strings.NewReplacer("’", "'", "ș", "ş", "ț", "ţ")

// stopSplit is text's words, lower case and folded (stopFold), with punctuation and hyphens between
// them. Letters of any script count, so "arrête", "выключи" and "עצור" are words as "stop" is.
func stopSplit(text string) []string {
	text = stopFold.Replace(strings.ToLower(text))
	return strings.FieldsFunc(text, func(r rune) bool {
		return !unicode.IsLetter(r) && r != '\''
	})
}
