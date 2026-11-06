package realtime

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// gemini is Google's Gemini Live API.
type gemini struct {
	// heard is what was heard of the question so far (hear); it is read on one goroutine at a time.
	heard *strings.Builder
	// called is whether the current turn segment made function calls and is yielding for toolResponse.
	called bool
}

const (
	geminiBase  = "wss://generativelanguage.googleapis.com/ws/google.ai.generativelanguage.v1beta.GenerativeService.BidiGenerateContent"
	geminiModel = "gemini-2.5-flash-native-audio-latest"
	geminiVoice = "Puck"
	geminiRate  = 24000
)

func (g *gemini) address(c Config) (string, http.Header, error) {
	addr, keyOK, err := endpoint(c, geminiBase)
	if err != nil {
		return "", nil, err
	}
	u, err := url.Parse(addr)
	if err != nil {
		return "", nil, err
	}
	h := http.Header{}
	switch {
	case c.Key != "" && keyOK:
		// In a header, not the address, so it is in no log line that quotes the address.
		h.Set("x-goog-api-key", c.Key)
	case c.Endpoint == "" && c.Key == "":
		return "", nil, ErrNoKey
	}
	return u.String(), h, nil
}

func (g *gemini) setup(c Config) []any {
	model := c.Model
	if model == "" {
		model = geminiModel
	}
	if !strings.HasPrefix(model, "models/") {
		model = "models/" + model
	}
	voice := c.Voice
	if voice == "" {
		voice = geminiVoice
	}
	var decls []map[string]any
	for _, f := range c.Functions {
		d := map[string]any{"name": f.Name, "description": f.Description}
		if f.Parameters != nil {
			// As JSON Schema, which Home Assistant's tools are written in: "parameters" takes only a part
			// of OpenAPI's.
			d["parametersJsonSchema"] = f.Parameters
		}
		decls = append(decls, d)
	}
	setup := map[string]any{
		"model": model,
		"generationConfig": map[string]any{
			"responseModalities": []string{"AUDIO"},
			"speechConfig": map[string]any{
				"voiceConfig": map[string]any{"prebuiltVoiceConfig": map[string]any{"voiceName": voice}},
			},
		},
		"systemInstruction": map[string]any{"parts": []any{map[string]any{"text": c.Instructions}}},
		// The device says when somebody has finished.
		"realtimeInputConfig":      map[string]any{"automaticActivityDetection": map[string]any{"disabled": true}},
		"inputAudioTranscription":  map[string]any{},
		"outputAudioTranscription": map[string]any{},
	}
	if len(decls) > 0 {
		setup["tools"] = []any{map[string]any{"functionDeclarations": decls}}
	}
	return []any{
		map[string]any{"setup": setup},
		map[string]any{"realtimeInput": map[string]any{"activityStart": map[string]any{}}},
	}
}

func (g *gemini) audio(pcm16k []int16) any {
	return map[string]any{"realtimeInput": map[string]any{
		"audio": map[string]any{"data": pcmBase64(pcm16k), "mimeType": "audio/pcm;rate=16000"},
	}}
}

func (g *gemini) endOfSpeech() []any {
	return []any{map[string]any{"realtimeInput": map[string]any{"activityEnd": map[string]any{}}}}
}

// goOn is nothing: Gemini goes on by itself once a call is answered.
func (g *gemini) goOn() []any { return nil }

// ready is the setupComplete that answers the setup: until then nothing else is sent.
func (g *gemini) ready(msg []byte) bool {
	var m struct {
		SetupComplete *struct{} `json:"setupComplete"`
	}
	return json.Unmarshal(msg, &m) == nil && m.SetupComplete != nil
}

func (g *gemini) readyFirst() bool { return false }

func (g *gemini) toolResult(callID, name, output string) []any {
	return []any{map[string]any{"toolResponse": map[string]any{"functionResponses": []any{
		map[string]any{"id": callID, "name": name, "response": map[string]any{"result": output}},
	}}}}
}

// hear keeps a piece of what was heard. Gemini sends the words as they are made out, a few letters at a
// time ("Ho", "w", " are"), and can go on after its answer has begun: they are said as one Heard at the
// end of the turn (flush), so nothing takes a piece for the whole question.
func (g *gemini) hear(s string) {
	if g.heard != nil {
		g.heard.WriteString(s)
	}
}

// flush says what was heard so far, if anything, as one Heard.
func (g *gemini) flush(emit func(Event)) {
	if g.heard == nil || g.heard.Len() == 0 {
		return
	}
	text := strings.TrimSpace(g.heard.String())
	g.heard.Reset()
	if text != "" {
		emit(Event{Kind: Heard, Text: text})
	}
}

func (g *gemini) read(msg []byte, emit func(Event)) {
	var m struct {
		ServerContent *struct {
			ModelTurn *struct {
				Parts []struct {
					InlineData *struct {
						MimeType string `json:"mimeType"`
						Data     string `json:"data"`
					} `json:"inlineData"`
				} `json:"parts"`
			} `json:"modelTurn"`
			TurnComplete        bool                   `json:"turnComplete"`
			Interrupted         bool                   `json:"interrupted"`
			InputTranscription  *struct{ Text string } `json:"inputTranscription"`
			OutputTranscription *struct{ Text string } `json:"outputTranscription"`
		} `json:"serverContent"`
		ToolCall *struct {
			FunctionCalls []struct {
				ID   string          `json:"id"`
				Name string          `json:"name"`
				Args json.RawMessage `json:"args"`
			} `json:"functionCalls"`
		} `json:"toolCall"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(msg, &m) != nil {
		return
	}
	if m.Error != nil {
		emit(Event{Kind: Failed, Err: errors.New("realtime: " + m.Error.Message)})
		return
	}
	if sc := m.ServerContent; sc != nil {
		if sc.InputTranscription != nil {
			g.hear(sc.InputTranscription.Text)
		}
		if sc.OutputTranscription != nil && sc.OutputTranscription.Text != "" {
			g.called = false
			emit(Event{Kind: Saying, Text: sc.OutputTranscription.Text})
		}
		if sc.ModelTurn != nil {
			for _, p := range sc.ModelTurn.Parts {
				if p.InlineData == nil || !strings.HasPrefix(p.InlineData.MimeType, "audio/pcm") {
					continue
				}
				if pcm := pcmFrom(p.InlineData.Data); len(pcm) > 0 {
					g.called = false
					emit(Event{Kind: Audio, PCM: pcm, Rate: rateOf(p.InlineData.MimeType)})
				}
			}
		}
	}
	if tc := m.ToolCall; tc != nil && len(tc.FunctionCalls) > 0 {
		g.called = true
		for _, f := range tc.FunctionCalls {
			args := string(f.Args)
			if args == "" || args == "null" {
				args = "{}"
			}
			emit(Event{Kind: Call, CallID: f.ID, Name: f.Name, Args: args})
		}
	}
	if sc := m.ServerContent; sc != nil {
		if sc.Interrupted {
			g.called = false
			g.flush(emit)
			emit(Event{Kind: Interrupt})
		}
		if sc.TurnComplete {
			// A turn segment that called functions is yielding for toolResponse, not ending the answer.
			if g.called {
				g.called = false
				emit(Event{Kind: Calls})
			} else {
				g.flush(emit)
				emit(Event{Kind: Done})
			}
		}
	}
}

// rateOf is the rate an audio type says, as in audio/pcm;rate=24000; Gemini's own when it says none.
func rateOf(mime string) int {
	if i := strings.Index(mime, "rate="); i >= 0 {
		if r, err := strconv.Atoi(mime[i+5:]); err == nil && r > 0 && r <= 48000 {
			return r
		}
	}
	return geminiRate
}
