package assistant

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// fakeLists is Home Assistant with a shopping list, recording what is added to it.
func fakeLists(t *testing.T) func() []string {
	t.Helper()
	var mu sync.Mutex
	var added []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/states":
			_, _ = w.Write([]byte(`[{"entity_id":"todo.shopping","attributes":{"friendly_name":"Shopping"}}]`))
		case "/api/services/todo/get_items":
			_, _ = w.Write([]byte(`{"service_response":{"todo.shopping":{"items":[{"uid":"1","summary":"Milk","status":"needs_action"},{"uid":"2","summary":"Eggs","status":"needs_action"}]}}}`))
		case "/api/services/todo/add_item":
			var body map[string]any
			b, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(b, &body)
			mu.Lock()
			added = append(added, body["entity_id"].(string)+" "+body["item"].(string))
			mu.Unlock()
			_, _ = w.Write([]byte(`[]`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	config.Use(filepath.Join(dir, "state.json"))
	was := hass.Path
	hass.Path = filepath.Join(dir, "hass.json")
	t.Cleanup(func() { hass.Path = was })
	if err := hass.Get().Set(srv.URL, "token"); err != nil {
		t.Fatal(err)
	}
	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), added...)
	}
}

func todoTool(t *testing.T, name string) tool {
	t.Helper()
	for _, tl := range todoTools() {
		if tl.Name == name {
			return tl
		}
	}
	t.Fatalf("no %s tool", name)
	return tool{}
}

// The tools are offered only to a device that was given lists.
func TestTodoToolsNeedLists(t *testing.T) {
	fakeLists(t)
	if len(todoTools()) != 0 {
		t.Fatal("to-do tools with no list chosen")
	}
	_ = config.Set().Todo().Lists([]string{"todo.shopping"})
	if len(todoTools()) != 2 {
		t.Fatalf("tools %d", len(todoTools()))
	}
}

// "Add milk to the shopping list" adds the item to that list, and "what's on the shopping list" reads it
// out.
func TestTodoToolsAddAndRead(t *testing.T) {
	added := fakeLists(t)
	_ = config.Set().Todo().Lists([]string{"todo.shopping"})

	got, err := todoTool(t, "add_todo").Run(map[string]any{"item": "oat milk", "list": "the shopping list"})
	if err != nil || got != "added to the Shopping list" {
		t.Fatalf("%q, %v", got, err)
	}
	if a := added(); len(a) != 1 || a[0] != "todo.shopping oat milk" {
		t.Errorf("added %v", a)
	}
	if _, err := todoTool(t, "add_todo").Run(map[string]any{"item": "x", "list": "garage"}); err == nil {
		t.Error("added to a list that is not there")
	}

	got, err = todoTool(t, "read_todo").Run(map[string]any{})
	if err != nil || !strings.Contains(got, "Milk; Eggs") {
		t.Errorf("%q, %v", got, err)
	}
}

// Reading a list brings words anyone with write access to it chose into the turn, as a calendar does, so
// the turn that has read one can only look things up; adding changes something and is refused then.
func TestTodoToolsAreSortedForAWebTurn(t *testing.T) {
	if !readsTheWeb["read_todo"] || !allowedAfterWeb["read_todo"] {
		t.Error("read_todo is not held to what a turn that read outside words may do")
	}
	if allowedAfterWeb["add_todo"] || allowedAfterStations["add_todo"] || readsTheWeb["add_todo"] {
		t.Error("add_todo changes a list, so a turn that has read the web must not run it")
	}
}
