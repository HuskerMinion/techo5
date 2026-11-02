package hass

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Home Assistant's get_items answer is keyed by the list's entity: the items with a uid, a summary and a
// status. An item with no words is left out, and one with no uid is named by its summary.
func TestParseTodoItems(t *testing.T) {
	out := map[string]any{"todo.shopping": map[string]any{"items": []any{
		map[string]any{"uid": "a1", "summary": "Milk", "status": "needs_action"},
		map[string]any{"uid": "b2", "summary": "Eggs", "status": "completed"},
		map[string]any{"uid": "c3", "summary": ""},
		map[string]any{"summary": "Bread"},
	}}}
	items, err := parseTodoItems("todo.shopping", out)
	if err != nil {
		t.Fatal(err)
	}
	want := []TodoItem{{"a1", "Milk", false}, {"b2", "Eggs", true}, {"Bread", "Bread", false}}
	if len(items) != len(want) {
		t.Fatalf("got %+v", items)
	}
	for i := range want {
		if items[i] != want[i] {
			t.Errorf("item %d = %+v, want %+v", i, items[i], want[i])
		}
	}
	if items, err := parseTodoItems("todo.other", out); err != nil || len(items) != 0 {
		t.Errorf("a list the answer does not hold: %+v, %v", items, err)
	}
}

// The three calls go to the right services with the right data.
func TestTodoCallsGoToTheTodoServices(t *testing.T) {
	type call struct {
		path string
		body map[string]any
	}
	var calls []call
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(b, &body)
		calls = append(calls, call{r.URL.RequestURI(), body})
		if r.URL.Path == "/api/services/todo/get_items" {
			_, _ = w.Write([]byte(`{"changed_states":[],"service_response":{"todo.shopping":{"items":[{"uid":"a1","summary":"Milk","status":"needs_action"}]}}}`))
			return
		}
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()
	c := NewTestClient(srv.URL, "token", srv.Client())

	items, err := c.TodoItems("todo.shopping")
	if err != nil || len(items) != 1 || items[0].UID != "a1" {
		t.Fatalf("items %+v, %v", items, err)
	}
	if err := c.CompleteTodo("todo.shopping", "a1"); err != nil {
		t.Fatal(err)
	}
	if err := c.AddTodo("todo.shopping", "Butter"); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 3 {
		t.Fatalf("calls %+v", calls)
	}
	if calls[0].path != "/api/services/todo/get_items?return_response" || calls[0].body["status"] != "needs_action" {
		t.Errorf("get_items: %+v", calls[0])
	}
	if calls[1].path != "/api/services/todo/update_item" || calls[1].body["item"] != "a1" ||
		calls[1].body["status"] != "completed" || calls[1].body["entity_id"] != "todo.shopping" {
		t.Errorf("update_item: %+v", calls[1])
	}
	if calls[2].path != "/api/services/todo/add_item" || calls[2].body["item"] != "Butter" {
		t.Errorf("add_item: %+v", calls[2])
	}
}
