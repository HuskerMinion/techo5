package musicassistant

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestResumeAsksForTheQueueThenPlay(t *testing.T) {
	for _, hasResume := range []bool{true, false} {
		var mu sync.Mutex
		var asked []string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				Command string         `json:"command"`
				Args    map[string]any `json:"args"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			mu.Lock()
			asked = append(asked, body.Command+" "+firstArg(body.Args))
			mu.Unlock()
			if body.Command == "player_queues/resume" && !hasResume {
				http.Error(w, "no such command", http.StatusBadRequest)
				return
			}
			w.Write([]byte("null"))
		}))
		c := Client{URL: srv.URL, Token: "t"}
		if err := c.Resume(context.Background(), "up1234"); err != nil {
			t.Errorf("resume (server has resume: %v): %v", hasResume, err)
		}
		srv.Close()
		want := []string{"player_queues/resume up1234"}
		if !hasResume {
			want = append(want, "players/cmd/play up1234")
		}
		if len(asked) != len(want) || asked[0] != want[0] || (len(want) == 2 && asked[1] != want[1]) {
			t.Errorf("server has resume: %v: asked %v, want %v", hasResume, asked, want)
		}
	}
}

func firstArg(a map[string]any) string {
	for _, k := range []string{"queue_id", "player_id"} {
		if v, ok := a[k].(string); ok {
			return v
		}
	}
	return ""
}
