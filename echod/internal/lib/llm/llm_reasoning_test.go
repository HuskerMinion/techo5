package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

const answer = `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`

func TestReasoningIsAskedFor(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&got)
		w.Write([]byte(answer))
	}))
	defer srv.Close()
	c := &Client{Base: srv.URL + "/v1", Reasoning: "medium"}
	if _, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil); err != nil {
		t.Fatal(err)
	}
	if got["reasoning_effort"] != "medium" {
		t.Errorf("reasoning_effort %v", got["reasoning_effort"])
	}
	if k, _ := got["chat_template_kwargs"].(map[string]any); k["reasoning_effort"] != "medium" {
		t.Errorf("chat_template_kwargs %v", got["chat_template_kwargs"])
	}

	c.Reasoning = ""
	got = nil
	if _, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil); err != nil {
		t.Fatal(err)
	}
	if _, ok := got["reasoning_effort"]; ok {
		t.Errorf("asked for reasoning without a setting: %v", got)
	}
}

// A server that refuses the setting is asked again without it, and not asked with it after.
func TestReasoningRefusedIsDropped(t *testing.T) {
	var with, without atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		json.NewDecoder(r.Body).Decode(&req)
		if _, ok := req["reasoning_effort"]; ok {
			with.Add(1)
			http.Error(w, `{"error":{"message":"Unrecognized request argument supplied: reasoning_effort"}}`, http.StatusBadRequest)
			return
		}
		without.Add(1)
		w.Write([]byte(answer))
	}))
	defer srv.Close()
	c := &Client{Base: srv.URL + "/v1", Reasoning: "medium"}
	for range 2 {
		m, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil)
		if err != nil || m.Content != "ok" {
			t.Fatalf("%v %q", err, m.Content)
		}
	}
	if with.Load() != 1 || without.Load() != 2 {
		t.Errorf("asked with it %d times, without %d", with.Load(), without.Load())
	}
}

// A server error with the setting is asked once more without it, and the setting is still asked for next time.
func TestReasoningServerErrorRetriesWithout(t *testing.T) {
	var with, without atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		json.NewDecoder(r.Body).Decode(&req)
		if _, ok := req["reasoning_effort"]; ok {
			with.Add(1)
			http.Error(w, `{"error":{"code":500,"message":"The model produced output that does not match the expected format"}}`, http.StatusInternalServerError)
			return
		}
		without.Add(1)
		w.Write([]byte(answer))
	}))
	defer srv.Close()
	c := &Client{Base: srv.URL + "/v1", Reasoning: "medium"}
	for range 2 {
		if m, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil); err != nil || m.Content != "ok" {
			t.Fatalf("%v %q", err, m.Content)
		}
	}
	if with.Load() != 2 || without.Load() != 2 {
		t.Errorf("asked with it %d times, without %d", with.Load(), without.Load())
	}
}

// Any other refusal is the conversation's, and stands.
func TestOtherRefusalsStand(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		http.Error(w, `{"error":{"message":"the request exceeds the available context size"}}`, http.StatusBadRequest)
	}))
	defer srv.Close()
	c := &Client{Base: srv.URL + "/v1", Reasoning: "medium"}
	if _, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil); err == nil {
		t.Fatal("no error")
	}
	if n.Load() != 1 {
		t.Errorf("asked %d times", n.Load())
	}
	if _, refused := noReasoning.Load(endpoint(c.Base)); refused {
		t.Error("took a context refusal for a reasoning one")
	}
}
