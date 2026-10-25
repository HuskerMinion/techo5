// Package llm asks a chat model behind an OpenAI-style endpoint (/v1/chat/completions), with tools:
// llama.cpp's server, Ollama, vLLM and the rest all speak it. Only what a voice turn needs is here.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Message is one entry in the conversation.
type Message struct {
	Role       string     `json:"role"` // system, user, assistant, tool
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Name       string     `json:"name,omitempty"`
}

// ToolCall is the model asking for one of the tools.
type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"` // JSON, as the model wrote it
	} `json:"function"`
}

// Tool is something the model may call: a name, what it is for, and its arguments as JSON Schema.
type Tool struct {
	Name        string
	Description string
	Parameters  map[string]any
}

// Client is one endpoint.
type Client struct {
	Base  string // like http://192.168.1.20:8080/v1
	Key   string
	Model string
	HTTP  *http.Client

	// Reasoning asks a thinking model to think harder than its server's setting, like "medium", for a
	// turn where it pays: picking a page and reading it. Empty leaves the server's setting. A server
	// that refuses the asking is not asked again (noReasoning).
	Reasoning string
}

// noReasoning is the endpoints that refused a reasoning setting: OpenAI's own refuses one for a model
// that does not reason, where llama.cpp's server and Ollama take or ignore it.
var noReasoning sync.Map

// timeout is how long one answer may take: a model on a slow machine thinks for a while, but a voice
// turn nobody answers for this long is over.
const timeout = 60 * time.Second

// maxAnswer bounds what is read back.
const maxAnswer = 1 << 20

type toolJSON struct {
	Type     string `json:"type"`
	Function struct {
		Name        string         `json:"name"`
		Description string         `json:"description"`
		Parameters  map[string]any `json:"parameters"`
	} `json:"function"`
}

// Chat sends the conversation so far and returns the model's next message, which is either words or
// tool calls.
func (c *Client) Chat(ctx context.Context, msgs []Message, tools []Tool) (Message, error) {
	// Low: a voice turn wants the same tool for the same request every time, not variety.
	req := map[string]any{"messages": msgs, "temperature": 0.2}
	if c.Model != "" {
		req["model"] = c.Model
	}
	if len(tools) > 0 {
		ts := make([]toolJSON, len(tools))
		for i, t := range tools {
			ts[i].Type = "function"
			ts[i].Function.Name, ts[i].Function.Description, ts[i].Function.Parameters = t.Name, t.Description, t.Parameters
		}
		req["tools"] = ts
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if _, refused := noReasoning.Load(endpoint(c.Base)); c.Reasoning != "" && !refused {
		// reasoning_effort is OpenAI's own name; llama.cpp's server hands chat_template_kwargs to the
		// model's chat template, which is where gpt-oss reads it.
		withR := map[string]any{"reasoning_effort": c.Reasoning, "chat_template_kwargs": map[string]any{"reasoning_effort": c.Reasoning}}
		for k, v := range req {
			withR[k] = v
		}
		m, status, err := c.post(ctx, withR)
		switch {
		case status == http.StatusBadRequest && refusedReasoning(err):
			noReasoning.Store(endpoint(c.Base), true)
		case status >= 500:
			// llama.cpp's server sometimes cannot parse what gpt-oss wrote after thinking longer
			// ("does not match the expected ... format"): asked again as the server is set, it answers.
		default:
			return m, err
		}
	}
	m, _, err := c.post(ctx, req)
	return m, err
}

// refusedReasoning is whether a server's refusal is of the reasoning setting, not of the conversation
// (too long for its context, say), which asking without it would not mend.
func refusedReasoning(err error) bool {
	if err == nil {
		return false
	}
	e := strings.ToLower(err.Error())
	for _, w := range []string{"reasoning", "chat_template_kwargs", "unrecognized", "unknown", "unsupported", "not supported"} {
		if strings.Contains(e, w) {
			return true
		}
	}
	return false
}

// post sends one request, returning the answer's status with it.
func (c *Client) post(ctx context.Context, req map[string]any) (Message, int, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return Message{}, 0, err
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint(c.Base), bytes.NewReader(body))
	if err != nil {
		return Message{}, 0, err
	}
	r.Header.Set("Content-Type", "application/json")
	if c.Key != "" {
		r.Header.Set("Authorization", "Bearer "+c.Key)
	}
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(r)
	if err != nil {
		return Message{}, 0, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxAnswer))
	if err != nil {
		return Message{}, resp.StatusCode, err
	}
	if resp.StatusCode != http.StatusOK {
		// The body says why, but never the key: it is not in anything the server sends back.
		return Message{}, resp.StatusCode, fmt.Errorf("llm: %s: %.200s", resp.Status, strings.TrimSpace(string(b)))
	}
	var out struct {
		Choices []struct {
			Message Message `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return Message{}, resp.StatusCode, fmt.Errorf("llm: not an answer: %w", err)
	}
	if len(out.Choices) == 0 {
		return Message{}, resp.StatusCode, errors.New("llm: no answer")
	}
	m := out.Choices[0].Message
	m.Role = "assistant"
	return m, resp.StatusCode, nil
}

// endpoint is the chat address for a base. A base given as just the server (http://host:8080), as
// people often type it, means the usual /v1 under it.
func endpoint(base string) string {
	base = strings.TrimRight(base, "/")
	if u, err := url.Parse(base); err == nil && u.Path == "" {
		base += "/v1"
	}
	return base + "/chat/completions"
}
