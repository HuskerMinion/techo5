package api

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/ygelfand/go-esphome-device/api"

	"github.com/HuskerMinion/techo5/echod/internal/component"
)

func TestVersionAtLeast(t *testing.T) {
	for _, c := range []struct {
		info, min string
		want      bool
	}{
		{"Home Assistant 2026.12.0b0", "2026.12", true},
		{"Home Assistant 2026.12.0", "2026.12", true},
		{"Home Assistant 2027.1.0", "2026.12", true},
		{"Home Assistant 2026.11.3", "2026.12", false},
		{"Home Assistant 2025.12.4", "2026.12", false},
		{"aioesphomeapi", "2026.12", false},
		{"Home Assistant dev", "2026.12", false},
		{"aioesphomeapi", "", true},
	} {
		if got := versionAtLeast(c.info, c.min); got != c.want {
			t.Errorf("versionAtLeast(%q, %q) = %v, want %v", c.info, c.min, got, c.want)
		}
	}
}

func TestUnwrapResponse(t *testing.T) {
	got, err := unwrapResponse(&api.HomeassistantActionResponse{Success: true, ResponseData: []byte(`{"response": "01HA"}`)})
	if err != nil || string(got) != `"01HA"` {
		t.Errorf("got %s, %v; want \"01HA\"", got, err)
	}
	if _, err := unwrapResponse(&api.HomeassistantActionResponse{ErrorMessage: "no such entity"}); err == nil {
		t.Error("a failed action read as an answer")
	}
	if got, err := unwrapResponse(&api.HomeassistantActionResponse{Success: true}); err != nil || got != nil {
		t.Errorf("an empty answer read as %s, %v", got, err)
	}
}

// A question with nobody new enough to ask is answered with why, and not left waiting.
func TestAskingWithNobodyToAsk(t *testing.T) {
	r := newReplies()
	got := make(chan error, 1)
	r.ask(component.Call{Service: "timer_list.create_timer", MinVersion: "2026.12", Reply: func(_ json.RawMessage, err error) { got <- err }},
		&api.HomeassistantActionRequest{Service: "timer_list.create_timer"})
	select {
	case err := <-got:
		if !errors.Is(err, errNoHomeAssistant) {
			t.Errorf("got %v, want errNoHomeAssistant", err)
		}
	case <-time.After(time.Second):
		t.Fatal("no reply")
	}
}

// An answer goes to the question it answers, once, and an answer to nothing asked is passed over.
func TestAnAnswerGoesToItsQuestion(t *testing.T) {
	r := newReplies()
	got := make(chan string, 2)
	r.waiting[7] = func(resp json.RawMessage, err error) { got <- string(resp) }

	if err := r.Handle(t.Context(), nil, &api.HomeassistantActionResponse{CallId: 9, Success: true, ResponseData: []byte(`{"response": 1}`)}); err != nil {
		t.Fatal(err)
	}
	if err := r.Handle(t.Context(), nil, &api.HomeassistantActionResponse{CallId: 7, Success: true, ResponseData: []byte(`{"response": "01HA"}`)}); err != nil {
		t.Fatal(err)
	}
	select {
	case resp := <-got:
		if resp != `"01HA"` {
			t.Errorf("answered %s", resp)
		}
	case <-time.After(time.Second):
		t.Fatal("the question was not answered")
	}
	if _, waiting := r.waiting[7]; waiting {
		t.Error("an answered question is still waiting")
	}
}

// An action that returns nothing is asked only whether it worked: asked for a response, Home Assistant
// refuses it.
func TestOnlyATemplateAsksForAResponse(t *testing.T) {
	req := &api.HomeassistantActionRequest{Service: "timer_list.pause_timer"}
	numbered(req, 3, "")
	if req.GetCallId() != 3 || req.GetWantsResponse() {
		t.Errorf("pause_timer asked with call %d, wants response %v; want call 3 and no response", req.GetCallId(), req.GetWantsResponse())
	}
	req = &api.HomeassistantActionRequest{Service: "timer_list.create_timer"}
	numbered(req, 4, "{{ x }}")
	if !req.GetWantsResponse() || req.GetResponseTemplate() != "{{ x }}" {
		t.Errorf("create_timer asked without its response: %+v", req)
	}
}
