package setup

import (
	"fmt"
	"html"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/wakeword"
)

// listeningSection is how long the device listens again after an answer, and how many times in a row:
// Home Assistant's two settings for the first wake word, here for a device that has none.
func listeningSection(w http.ResponseWriter, token string) {
	fmt.Fprint(w, `<fieldset><legend>Listening after an answer</legend><form method="post" action="/setup/save">`)
	hidden(w, token, "listening", "sound")
	fmt.Fprintf(w, `<label for="fu">Keep listening for (seconds, 0 for only when asked a question)</label>
	 <input id="fu" name="followup" type="number" min="0" max="30" value="%d">
	 <label for="fus">Times in a row (0 for no limit)</label>
	 <input id="fus" name="followups" type="number" min="0" max="10" value="%d">
	 <p class="note">After an answer the device listens again without the wake word, for this long and this
	  many times. A question the assistant asks is always listened for.</p>
	 <p><button type="submit">Save</button></p></form></fieldset>`,
		int(wakeword.FollowUp(0).Seconds()), wakeword.FollowUps(0))
}

func saveListening(r *http.Request) string {
	fu, err1 := strconv.Atoi(strings.TrimSpace(r.PostFormValue("followup")))
	fus, err2 := strconv.Atoi(strings.TrimSpace(r.PostFormValue("followups")))
	if err1 != nil || err2 != nil || fu < 0 || fu > 30 || fus < 0 || fus > 10 {
		return "listening is 0 to 30 seconds, and 0 to 10 times in a row"
	}
	wakeword.Get().SetFollowUp(0, fu)
	wakeword.Get().SetFollowUps(0, fus)
	return ""
}

// brainSection is where the voice answers come from: Home Assistant's Assist pipeline, or speech and a
// chat model reached directly (config.Brain). The key is never shown: it is written, or left alone.
func brainSection(w http.ResponseWriter, token string) {
	b := config.Get().Brain
	fmt.Fprint(w, `<fieldset><legend>Voice assistant</legend><form method="post" action="/setup/save">`)
	hidden(w, token, "brain", "sound")
	fmt.Fprintf(w, `<label for="mode">Answered by</label>
	 <select id="mode" name="mode">
	  <option value=""%s>Home Assistant (its Assist pipeline)</option>
	  <option value="direct"%s>Speech and a chat model, directly</option>
	 </select>
	 <p class="note">Directly, the device needs no Home Assistant: what is said goes to a speech-to-text
	  server, the words to a chat model that can set timers and alarms, play the radio and call other
	  devices, and the answer to a text-to-speech server. The servers are Wyoming ones, as Home Assistant
	  uses (faster-whisper, Piper), and any chat model with an OpenAI-style endpoint (llama.cpp, Ollama).</p>
	 <label for="stt">Speech to text (host:port)</label>
	 <input id="stt" name="stt" value="%s" placeholder="192.168.1.20:10300" autocomplete="off">
	 <label for="tts">Text to speech (host:port)</label>
	 <input id="tts" name="tts" value="%s" placeholder="192.168.1.20:10200" autocomplete="off">
	 <label for="voice">Voice</label>
	 <input id="voice" name="voice" value="%s" placeholder="en_US-lessac-medium" autocomplete="off">
	 <label for="language">Language</label>
	 <input id="language" name="language" value="%s" placeholder="en" maxlength="8" autocomplete="off">
	 <label for="llm">Chat model endpoint</label>
	 <input id="llm" name="llm" value="%s" placeholder="http://192.168.1.20:8080/v1" autocomplete="off">
	 <label for="model">Model</label>
	 <input id="model" name="model" value="%s" placeholder="only if the server has several" autocomplete="off">
	 <label for="key">Key</label>
	 <input id="key" name="key" type="password" value="" placeholder="%s" autocomplete="off">
	 <p><label><input type="checkbox" name="nokey" value="yes" style="width:auto"> Remove the key</label></p>
	 <label for="search">Web search (a SearXNG server)</label>
	 <input id="search" name="search" value="%s" placeholder="http://192.168.1.20:8888" autocomplete="off">
	 <p class="note">To look up what the chat model cannot know: games, news, opening hours. SearXNG needs
	  its JSON format turned on (search.formats in its settings.yml). Empty: no looking things up.</p>
	 <label for="reader">Page reader</label>
	 <input id="reader" name="reader" value="%s" placeholder="http://192.168.1.20:8891" autocomplete="off">
	 <p class="note">Optional: a browser that reads pages that fill themselves in with scripts, like a
	  store's hours (TECHO5's tools/page-reader). Used only when a page has almost no text as it comes,
	  or the site turns the device away with an error.</p>
	 <label for="prompt">Anything the assistant should know</label>
	 <textarea id="prompt" name="prompt" rows="3" maxlength="2000">%s</textarea>
	 <p><button type="submit">Save</button></p></form></fieldset>`,
		selected(b.Mode == config.BrainHomeAssistant), selected(b.Mode == config.BrainDirect),
		html.EscapeString(b.STT), html.EscapeString(b.TTS), html.EscapeString(b.Voice), html.EscapeString(b.Language),
		html.EscapeString(b.LLM), html.EscapeString(b.Model), keyHint(b.Key != ""), html.EscapeString(b.Search),
		html.EscapeString(b.Reader), html.EscapeString(b.Prompt))
}

func keyHint(set bool) string {
	if set {
		return "set; leave empty to keep it"
	}
	return "none"
}

// saveBrain keeps the form, refusing what could not work rather than keeping it to fail at the next
// wake word.
func saveBrain(r *http.Request) string {
	v := func(k string) string { return strings.TrimSpace(r.PostFormValue(k)) }
	b := config.Brain{
		Mode:     config.BrainMode(v("mode")),
		STT:      v("stt"),
		TTS:      v("tts"),
		Voice:    v("voice"),
		Language: v("language"),
		LLM:      strings.TrimRight(v("llm"), "/"),
		Model:    v("model"),
		Search:   strings.TrimRight(v("search"), "/"),
		Reader:   strings.TrimRight(v("reader"), "/"),
		Prompt:   strings.TrimSpace(r.PostFormValue("prompt")),
	}
	if b.Mode != config.BrainHomeAssistant && b.Mode != config.BrainDirect {
		return "that is not a way of answering this device knows"
	}
	for _, hp := range []struct{ what, v string }{{"speech to text", b.STT}, {"text to speech", b.TTS}} {
		if hp.v == "" {
			continue
		}
		if _, port, err := net.SplitHostPort(hp.v); err != nil || port == "" {
			return hp.what + " should be host:port, like 192.168.1.20:10300"
		}
	}
	if b.LLM != "" {
		u, err := url.Parse(b.LLM)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return "the chat model endpoint should be an address like http://192.168.1.20:8080/v1"
		}
	}
	if b.Search != "" {
		u, err := url.Parse(b.Search)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return "the search server should be an address like http://192.168.1.20:8888"
		}
	}
	if b.Reader != "" {
		u, err := url.Parse(b.Reader)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return "the page reader should be an address like http://192.168.1.20:8891"
		}
	}
	if b.Mode == config.BrainDirect && (b.STT == "" || b.TTS == "" || b.LLM == "") {
		return "answering directly needs all three: speech to text, text to speech and the chat model"
	}
	if strings.ContainsAny(b.Voice+b.Language+b.Model, "\r\n") {
		return "the voice, language and model are one line each"
	}
	if err := config.Set().Brain().Set(b); err != nil {
		return "could not save it: " + err.Error()
	}
	switch key := strings.TrimSpace(r.PostFormValue("key")); {
	case r.PostFormValue("nokey") == "yes":
		if err := config.Set().Brain().SetKey(""); err != nil {
			return "could not remove the key: " + err.Error()
		}
	case key != "":
		if strings.ContainsAny(key, "\r\n") {
			return "a key is one line"
		}
		if err := config.Set().Brain().SetKey(key); err != nil {
			return "could not save the key: " + err.Error()
		}
	}
	slog.Info("setup page: the voice assistant was set", "mode", b.Mode, "stt", b.STT, "tts", b.TTS, "llm", b.LLM)
	return ""
}
