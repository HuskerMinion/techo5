package config

import (
	"fmt"
	"net/url"
	"strings"
)

// Brain is where a turn's speech and answer come from when they do not come from Home Assistant: the
// direct pipeline (feature/voice/direct.go). Speech to text and text to speech are Wyoming servers
// (faster-whisper and Piper, as Home Assistant itself uses), and the answer is a chat model behind an
// OpenAI-style endpoint (llama.cpp's server, Ollama, and so on), which acts through the device's own
// abilities as tools. Empty Mode is Home Assistant's Assist pipeline, as always.
type Brain struct {
	Mode BrainMode `json:"mode,omitempty"`

	// STT and TTS are the Wyoming servers, host:port.
	STT string `json:"stt,omitempty"`
	TTS string `json:"tts,omitempty"`
	// Voice is the Piper voice, like en_US-lessac-medium; empty is the server's default.
	Voice string `json:"voice,omitempty"`
	// Language is what the speech is in, for the recognizer; empty is English.
	Language string `json:"language,omitempty"`

	// LLM is the chat endpoint's base, like http://192.168.1.20:8080/v1, and Model the model to ask
	// for (a server with one model ignores it). Key is sent as a bearer token when set; it is a
	// secret, never shown again once saved.
	LLM   string `json:"llm,omitempty"`
	Model string `json:"model,omitempty"`
	Key   string `json:"key,omitempty"`

	// Search is a SearXNG server's address, like http://192.168.1.20:8888, with its JSON format on: the
	// model looks things up there and reads the pages it finds. Empty is no looking anything up.
	Search string `json:"search,omitempty"`

	// Reader is a page reader's address (tools/page-reader), like http://192.168.1.20:8891: a browser
	// that loads a page the model wants to read when the page has no text until its scripts run, or
	// turns away a plain request. Empty is reading pages as they come.
	Reader string `json:"reader,omitempty"`

	// News is the household's own news updates, one a line: a name, then the address of its podcast
	// feed, like "City Radio https://example.com/news.xml". The first is the one played when no source
	// is named. Empty is only the built-in ones (assistant/news.go), and asking which.
	News string `json:"news,omitempty"`

	// Prompt is added to the device's own instructions to the model: a name, a tone, what the
	// household wants it to know.
	Prompt string `json:"prompt,omitempty"`

	// ReplyPlayer is a Home Assistant media_player entity id that the assistant's spoken replies play
	// on instead of this device's speaker. Empty is this device's own speaker.
	ReplyPlayer string `json:"reply_player,omitempty"`
}

// ValidReplyPlayer cleans up an entity id typed for Brain.ReplyPlayer and says whether it is one: empty
// (this device's speaker) or media_player. followed by lowercase letters, digits and underscores.
func ValidReplyPlayer(s string) (string, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return "", true
	}
	name, ok := strings.CutPrefix(s, "media_player.")
	if !ok || name == "" {
		return s, false
	}
	for _, r := range name {
		if r != '_' && (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return s, false
		}
	}
	return s, true
}

type BrainMode string

const (
	BrainHomeAssistant BrainMode = ""
	BrainDirect        BrainMode = "direct"
)

// Direct is whether turns go to the direct pipeline, and it is set up enough to run one.
func (b Brain) Direct() bool {
	return b.Mode == BrainDirect && b.STT != "" && b.TTS != "" && b.LLM != ""
}

type BrainWriter struct{ st *Store }

// Set replaces everything but the key, which only SetKey changes: a form that shows the key as
// "set" and posts nothing for it must not clear it.
func (w BrainWriter) Set(b Brain) error {
	return w.st.Update(func(c *Config) {
		key, player := c.Brain.Key, c.Brain.ReplyPlayer
		c.Brain = b
		c.Brain.Key = key
		c.Brain.ReplyPlayer = player
	})
}

// SetVoice changes the speaking voice alone ("" for the server's default), so a choice on the screen
// cannot undo a setup page save made at the same moment.
func (w BrainWriter) SetVoice(voice string) error {
	return w.st.Update(func(c *Config) { c.Brain.Voice = voice })
}

// SetReplyPlayer chooses where replies play: "" for this device, or a media_player entity id. An id
// that is not one is refused.
func (w BrainWriter) SetReplyPlayer(id string) error {
	id, ok := ValidReplyPlayer(id)
	if !ok {
		return fmt.Errorf("not a media_player entity id, like media_player.kitchen_dot")
	}
	return w.st.Update(func(c *Config) { c.Brain.ReplyPlayer = id })
}

func (w BrainWriter) SetKey(key string) error {
	return w.st.Update(func(c *Config) { c.Brain.Key = key })
}

// NewsSource is one news update the assistant can play: a name, and its podcast feed.
type NewsSource struct{ Name, Feed string }

// NewsSources reads News: one source a line, a name and then the feed's address. Blank lines are
// skipped; a line that is not a name and an address is an error, saying which.
func (b Brain) NewsSources() ([]NewsSource, error) {
	var out []NewsSource
	for i, line := range strings.Split(b.News, "\n") {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		feed := f[len(f)-1]
		u, err := url.Parse(feed)
		if len(f) < 2 || err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return nil, fmt.Errorf("line %d is not a name and then a feed's address, like City Radio https://example.com/news.xml", i+1)
		}
		out = append(out, NewsSource{Name: strings.Join(f[:len(f)-1], " "), Feed: feed})
	}
	return out, nil
}
