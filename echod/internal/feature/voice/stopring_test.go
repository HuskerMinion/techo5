package voice

import (
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/ring"
)

func TestWhatStopsARing(t *testing.T) {
	for _, s := range []string{"Stop.", "stop the alarm", "Stop alarm", "turn off the timer", "Turn it off, please.", "stop ringing", "OK stop", "Stop time.", "turn the time off", "stop the timers"} {
		if !stopsRing(s) {
			t.Errorf("%q should stop a ring", s)
		}
	}
	for _, s := range []string{"", "stop the music", "what time is it", "turn off the lights", "the alarm", "snooze"} {
		if stopsRing(s) {
			t.Errorf("%q should not stop a ring", s)
		}
	}
}

// The same in the other languages (#741): a stop, with nothing but the alarm, please or now around it,
// stops; a sentence with anything else in it, or with no word that asks, is left to Home Assistant.
// Accents, cedillas and scripts other than Latin are words as plain letters are.
func TestWhatStopsARingInOtherLanguages(t *testing.T) {
	for _, c := range []struct {
		lang    string
		stops   []string
		doesNot []string
	}{
		{"German",
			[]string{"Stopp.", "Stopp, bitte!", "Halt!", "Timer aus.", "Alarm aus", "Mach den Wecker aus, bitte.", "Wecker ausschalten", "Stop", "Jetzt stoppen"},
			[]string{"Stopp die Musik", "Wecker", "aus dem Radio", "halt mal", "bitte"}},
		{"Spanish",
			[]string{"Para.", "Para, por favor.", "¡Detén la alarma!", "Basta ya.", "Apaga el temporizador", "Apágala", "Pare"},
			[]string{"para mañana", "para qué sirve", "pon la alarma", "por favor", "la alarma"}},
		{"French",
			[]string{"Arrête.", "Arrête l'alarme", "Arrête l’alarme !", "Éteins le minuteur, s'il te plaît.", "Stop", "Arrêtez la sonnerie", "Arrête-la tout de suite"},
			[]string{"arrête la musique", "le minuteur", "quelle heure est-il"}},
		{"Italian",
			[]string{"Ferma.", "Spegni la sveglia", "Basta, per favore.", "Fermalo subito", "Stop"},
			[]string{"spegni la luce", "la sveglia", "basta così poco"}},
		{"Dutch",
			[]string{"Stop.", "Zet de wekker uit", "Stop maar", "Timer uit, alsjeblieft", "Stoppen"},
			[]string{"uit de keuken", "zet de timer", "de wekker"}},
		{"Portuguese",
			[]string{"Para.", "Pare o despertador, por favor.", "Desliga o alarme", "Desligue isso agora"},
			[]string{"para amanhã", "o alarme", "desliga a luz"}},
		{"Polish",
			[]string{"Stop.", "Wyłącz budzik", "Zatrzymaj to, proszę.", "Przestań", "Wylacz alarm"},
			[]string{"wyłącz światło", "budzik", "zatrzymaj muzykę"}},
		{"Catalan",
			[]string{"Para.", "Atura l'alarma", "Apaga-la, si us plau.", "Prou!", "Atura't"},
			[]string{"apaga el llum", "l'alarma", "si us plau"}},
		{"Galician",
			[]string{"Para.", "Apaga a alarma", "Detén o temporizador, por favor."},
			[]string{"apaga a luz", "a alarma"}},
		{"Romanian",
			[]string{"Oprește.", "Oprește alarma", "Opreşte alarma, te rog", "Oprește-l acum", "Opreste timerul"},
			[]string{"oprește muzica", "alarma", "te rog"}},
		{"Turkish",
			[]string{"Dur.", "Alarmı durdur", "Kapat lütfen", "ALARMI KAPAT", "Zamanlayıcıyı durdur şimdi"},
			[]string{"ışığı kapat", "alarm", "lütfen"}},
		{"Russian",
			[]string{"Стоп.", "Выключи будильник", "Хватит, пожалуйста!", "Останови таймер", "СТОП"},
			[]string{"выключи свет", "будильник", "пожалуйста"}},
		{"Hebrew",
			[]string{"עצור.", "די!", "כבה את השעון המעורר", "תפסיק בבקשה"},
			[]string{"כבה את האור", "השעון המעורר"}},
		{"Japanese",
			[]string{"ストップ。", "止めて", "アラームを止めて。", "止めてください"},
			[]string{"アラーム", "電気を止めて"}},
	} {
		for _, s := range c.stops {
			if !stopsRing(s) {
				t.Errorf("%s: %q should stop a ring", c.lang, s)
			}
		}
		for _, s := range c.doesNot {
			if stopsRing(s) {
				t.Errorf("%s: %q should not stop a ring", c.lang, s)
			}
		}
	}
}

// Words of two languages do not make a stop between them: each sentence is read in one language.
func TestStopWordsDoNotMixLanguages(t *testing.T) {
	for _, s := range []string{"es para", "turn aus", "the alarma off por favor", "bitte para"} {
		if stopsRing(s) {
			t.Errorf("%q should not stop a ring", s)
		}
	}
}

// A wake word said at a ring opens a turn, and a stop in it is the whole of the answer: the turn ends
// here, and Home Assistant's reply to "Stop" (a stop for media, "Sorry, no device is playing") is not
// taken up. That holds when the stop word ended the ring before the transcript came (#754). A sentence
// that asks for more, or a "stop" in a turn opened with nothing ringing, goes on to Home Assistant.
func TestAStopInATurnOpenedOverARingIsAnsweredHere(t *testing.T) {
	for _, c := range []struct {
		name     string
		ringing  bool // a ring sounds when the turn opens
		endsSoon bool // the stop word ends it before the transcript comes
		heard    string
		answered bool
	}{
		{"the stop word ended the ring first", true, true, " Stop", true},
		{"the ring still sounding", true, false, "stop the alarm", true},
		{"in another language", true, true, "Stopp, bitte.", true},
		{"a sentence that asks for more", true, true, "stop the music", false},
		{"nothing ringing", false, false, " Stop", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := newWatcher(t, config.OnHA{})
			var done func()
			if c.ringing {
				ringing.Store(true)
				done = ring.Sounding()
				ring.Silence()
				t.Cleanup(func() {
					ringing.Store(false)
					if done != nil {
						done()
					}
				})
			}
			w.handle(event{kind: evStart, slot: 0, woke: true, at: w.at})
			if w.phase != phaseListening {
				t.Fatalf("the turn did not open: phase %s", w.phase)
			}
			if c.endsSoon {
				// The stop word, heard while the turn listens, ends the ring (Voice.Stop).
				ring.End()
				done()
				done = nil
			}
			w.handle(event{kind: evHeard, text: c.heard})

			if !c.answered {
				if w.phase == phaseIdle {
					t.Fatalf("%q was not left to Home Assistant", c.heard)
				}
				return
			}
			if w.phase != phaseIdle {
				t.Fatalf("after %q the turn is %s, want ended here", c.heard, w.phase)
			}
			if ringing.Load() {
				t.Error("the ring was not stopped")
			}
			if !w.draining {
				t.Error("the rest of Home Assistant's run would be taken up")
			}
			w.handle(event{kind: evReplyText, text: "Sorry, no device is playing"})
			if w.shown.Reply != "" {
				t.Errorf("the ended turn took up the reply %q", w.shown.Reply)
			}
		})
	}
}
