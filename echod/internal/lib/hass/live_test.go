package hass

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// Home Assistant refuses a command whose id is not higher than every one it has seen ("id_reuse"), so
// commands sent at once must reach it in the order their ids were given: a slider's volume and a tap
// sent together went out the wrong way round, and the second was refused.
func TestLiveCommandsGoOutInTheOrderOfTheirIDs(t *testing.T) {
	var mu sync.Mutex
	var seen []int
	up := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		_ = c.WriteJSON(map[string]string{"type": "auth_required"})
		var auth map[string]any
		if c.ReadJSON(&auth) != nil {
			return
		}
		_ = c.WriteJSON(map[string]string{"type": "auth_ok"})
		last := 0
		for {
			var cmd map[string]any
			if c.ReadJSON(&cmd) != nil {
				return
			}
			id := int(cmd["id"].(float64))
			mu.Lock()
			seen = append(seen, id)
			mu.Unlock()
			if id <= last {
				_ = c.WriteJSON(map[string]any{"id": id, "type": "result", "success": false,
					"error": map[string]string{"code": "id_reuse", "message": "Identifier values have to increase."}})
				continue
			}
			last = id
			_ = c.WriteJSON(map[string]any{"id": id, "type": "result", "success": true, "result": nil})
		}
	}))
	defer srv.Close()

	c := &Client{acc: access{URL: srv.URL, Token: "secret"}}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	l, err := c.OpenLive(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	const n = 200
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := l.Call(ctx, map[string]any{"type": "ping"}); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("a command sent alongside others was refused: %v", err)
		break
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != n {
		t.Fatalf("Home Assistant got %d commands, want %d", len(seen), n)
	}
	for i := 1; i < len(seen); i++ {
		if seen[i] <= seen[i-1] {
			t.Fatalf("id %d came after %d", seen[i], seen[i-1])
		}
	}
}
