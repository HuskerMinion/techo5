// Package activity is what a turn leaves behind in Home Assistant's logbook.
//
// The phases are already there: Home Assistant's own assist_satellite entity moves through idle,
// listening, processing and responding, and those state changes show in the feed. What it cannot
// show is content or which assistant answered, because it does not know either. These do.
package activity

import (
	"log/slog"
	"sync"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
)

func init() {
	component.Register(component.Device, Get())
}

// Log is the three sensors, and the switch that keeps the words off them. It has no hardware and no
// loop: something else has a turn, and tells it.
type Log struct {
	word  *esphome.TextSensor
	heard *esphome.TextSensor
	reply *esphome.TextSensor

	// send is Send voice text: off, what was heard and the reply stay on the device. What people say
	// goes into Home Assistant's history as plain text, for as long as the recorder keeps it, and not
	// everyone in a house chose that.
	send *esphome.Switch
}

var (
	once   sync.Once
	shared *Log
)

// Get is the log.
func Get() *Log {
	once.Do(func() {
		shared = &Log{
			word: &esphome.TextSensor{
				Base: esphome.Base{ObjectID: "last_wake_word", Name: "Last wake word", Icon: "mdi:account-voice"},
			},
			heard: &esphome.TextSensor{
				Base: esphome.Base{ObjectID: "last_heard", Name: "Last heard", Icon: "mdi:ear-hearing"},
			},
			reply: &esphome.TextSensor{
				Base: esphome.Base{ObjectID: "last_reply", Name: "Last reply", Icon: "mdi:message-text"},
			},
			send: &esphome.Switch{
				Base: esphome.Base{ObjectID: "send_voice_text", Name: "Send voice text", Icon: "mdi:message-lock",
					Category: esphome.CategoryConfig},
			},
		}
		shared.send.OnCommand = shared.sendText
	})
	return shared
}

// Restore shows the saved choice.
func (l *Log) Restore(c config.Config) { l.send.Set(!c.Security.WordsKept) }

// sendText is Send voice text turned on or off. Off empties the two sensors at once, so the last
// request's words do not sit there afterwards.
func (l *Log) sendText(on bool) {
	if err := config.Set().Security().WordsKept(!on); err != nil {
		slog.Error("saving send voice text failed", "err", err)
		return
	}
	l.send.Set(on)
	if !on {
		l.heard.Set("")
		l.reply.Set("")
	}
	slog.Info("send voice text", "on", on)
}

// wordsKept is whether what was heard and the reply stay on the device (Send voice text off).
func wordsKept() bool { return config.Get().Security.WordsKept }

// Said is text somebody said, or the reply to it, as the daemon's log may carry it: left out while Send
// voice text is off, since the log goes into a diagnostics bundle that may be posted.
func Said(text string) string {
	if wordsKept() && text != "" {
		return "(not logged: Send voice text is off)"
	}
	return text
}

func (l *Log) Name() string { return "activity" }

func (l *Log) Entities() []esphome.Entity {
	return []esphome.Entity{l.word, l.heard, l.reply, l.send}
}

// Woke records which wake word started a turn, so the transcript that follows can be attributed.
func (l *Log) Woke(phrase string) {
	if phrase == "" {
		return
	}
	l.word.Set(component.Fit(phrase))
}

func (l *Log) Heard(text string) {
	if text == "" || wordsKept() {
		return
	}
	l.heard.Set(component.Fit(text))
}

func (l *Log) Replied(text string) {
	if text == "" || wordsKept() {
		return
	}
	l.reply.Set(component.Fit(text))
}
