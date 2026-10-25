package config

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

	// Prompt is added to the device's own instructions to the model: a name, a tone, what the
	// household wants it to know.
	Prompt string `json:"prompt,omitempty"`
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
		key := c.Brain.Key
		c.Brain = b
		c.Brain.Key = key
	})
}

// SetVoice changes the speaking voice alone ("" for the server's default), so a choice on the screen
// cannot undo a setup page save made at the same moment.
func (w BrainWriter) SetVoice(voice string) error {
	return w.st.Update(func(c *Config) { c.Brain.Voice = voice })
}

func (w BrainWriter) SetKey(key string) error {
	return w.st.Update(func(c *Config) { c.Brain.Key = key })
}
