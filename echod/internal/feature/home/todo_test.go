package home

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// fakeTodo is Home Assistant with a shopping list and a chores list, and the service calls it was sent.
type fakeTodo struct {
	sync.Mutex
	calls  []string // "service entity item"
	bodies []map[string]any
}

func (f *fakeTodo) seen() []string {
	f.Lock()
	defer f.Unlock()
	return append([]string(nil), f.calls...)
}

func newFakeTodo(t *testing.T) *fakeTodo {
	t.Helper()
	f := &fakeTodo{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/states":
			_, _ = w.Write([]byte(`[{"entity_id":"todo.shopping","attributes":{"friendly_name":"Shopping List"}},
				{"entity_id":"todo.chores","attributes":{"friendly_name":"Chores"}},
				{"entity_id":"light.hall","attributes":{}}]`))
		case r.URL.Path == "/api/services/todo/get_items":
			var body map[string]any
			b, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(b, &body)
			e, _ := body["entity_id"].(string)
			items := `[{"uid":"u1","summary":"Milk","status":"needs_action"},{"uid":"u2","summary":"Eggs","status":"needs_action"}]`
			if e == "todo.chores" {
				items = `[]`
			}
			_, _ = w.Write([]byte(`{"service_response":{"` + e + `":{"items":` + items + `}}}`))
		case strings.HasPrefix(r.URL.Path, "/api/services/todo/"):
			var body map[string]any
			b, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(b, &body)
			item, _ := body["item"].(string)
			e, _ := body["entity_id"].(string)
			f.Lock()
			f.calls = append(f.calls, strings.TrimPrefix(r.URL.Path, "/api/services/todo/")+" "+e+" "+item)
			f.bodies = append(f.bodies, body)
			f.Unlock()
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
	todo.Lock()
	todo.lists, todo.names, todo.namesAt = nil, nil, time.Time{}
	todo.Unlock()
	return f
}

// Lists are chosen by their entities; nothing else is a list, and none is the default.
func TestTodoListsAreChosenByEntity(t *testing.T) {
	newFakeTodo(t)
	f := Get()
	if len(f.TodoLists()) != 0 {
		t.Fatal("a list is chosen before anybody chose one")
	}
	if err := f.SetTodoLists([]string{"light.hall"}); err == nil {
		t.Error("a light was taken for a list")
	}
	if err := f.SetTodoLists([]string{" todo.shopping ", "", "todo.chores", "todo.shopping"}); err != nil {
		t.Fatal(err)
	}
	if got := f.TodoLists(); len(got) != 2 || got[0] != "todo.shopping" || got[1] != "todo.chores" {
		t.Errorf("lists %v", got)
	}
	if err := f.SetTodoLists([]string{""}); err != nil || len(f.TodoLists()) != 0 {
		t.Errorf("empty did not clear the lists: %v, %v", f.TodoLists(), err)
	}
}

// A list is read in the background and kept; the first ask answers "not read yet".
func TestTodoItemsAreReadInTheBackground(t *testing.T) {
	newFakeTodo(t)
	f := Get()
	_ = f.SetTodoLists([]string{"todo.shopping"})
	if _, read := f.TodoItems("todo.shopping"); read {
		t.Fatal("read before it could be")
	}
	eventually(t, "the list to be read", func() bool { _, read := f.TodoItems("todo.shopping"); return read })
	items, _ := f.TodoItems("todo.shopping")
	if len(items) != 2 || items[0].Summary != "Milk" || items[0].UID != "u1" {
		t.Errorf("items %+v", items)
	}
}

// Completing an item calls todo.update_item for that item and list, and takes it off at once; a list
// the device does not show is refused.
func TestCompletingAnItemCallsHomeAssistant(t *testing.T) {
	ha := newFakeTodo(t)
	f := Get()
	_ = f.SetTodoLists([]string{"todo.shopping"})
	eventually(t, "the list to be read", func() bool { _, read := f.TodoItems("todo.shopping"); return read })
	if err := f.CompleteTodo("todo.chores", "u1"); err == nil {
		t.Error("a list this device does not show was changed")
	}
	if err := f.CompleteTodo("todo.shopping", "u1"); err != nil {
		t.Fatal(err)
	}
	if got := ha.seen(); len(got) != 1 || got[0] != "update_item todo.shopping u1" {
		t.Fatalf("calls %v", got)
	}
	if ha.bodies[0]["status"] != "completed" {
		t.Errorf("body %v", ha.bodies[0])
	}
	todo.Lock()
	left := todo.lists["todo.shopping"].items
	todo.Unlock()
	if len(left) != 1 || left[0].UID != "u2" {
		t.Errorf("the item stayed on the list: %+v", left)
	}
}

// By voice: the list is found by the words in its name, and the only list is the one meant when none
// was named.
func TestTheListAskedForIsPicked(t *testing.T) {
	name := func(id string) string {
		return map[string]string{"todo.shopping": "Shopping List", "todo.chores": "Chores"}[id]
	}
	both := []string{"todo.shopping", "todo.chores"}
	for _, c := range []struct{ asked, want string }{
		{"shopping", "todo.shopping"}, {"the shopping list", "todo.shopping"}, {"my chores list", "todo.chores"}, {"CHORES", "todo.chores"},
	} {
		if got, err := pickTodo(c.asked, both, name); err != nil || got != c.want {
			t.Errorf("%q: %q, %v", c.asked, got, err)
		}
	}
	if got, err := pickTodo("", both[:1], name); err != nil || got != "todo.shopping" {
		t.Errorf("the only list: %q, %v", got, err)
	}
	if _, err := pickTodo("", both, name); err == nil || !strings.Contains(err.Error(), "Shopping List, Chores") {
		t.Errorf("no list named among two: %v", err)
	}
	if _, err := pickTodo("garage", both, name); err == nil || !strings.Contains(err.Error(), "garage") {
		t.Errorf("a list that is not there: %v", err)
	}
	if _, err := pickTodo("shopping", nil, name); err == nil {
		t.Error("a list picked from none")
	}
}

// Adding and reading by name use Home Assistant's names for the lists.
func TestAddingAndReadingByListName(t *testing.T) {
	ha := newFakeTodo(t)
	f := Get()
	if _, err := f.AddTodoItem("shopping", "Butter"); err == nil {
		t.Error("added to a list nobody chose")
	}
	_ = f.SetTodoLists([]string{"todo.shopping", "todo.chores"})
	name, err := f.AddTodoItem("the shopping list", "  Butter ")
	if err != nil || name != "Shopping List" {
		t.Fatalf("%q, %v", name, err)
	}
	if got := ha.seen(); len(got) != 1 || got[0] != "add_item todo.shopping Butter" {
		t.Errorf("calls %v", got)
	}
	if _, err := f.AddTodoItem("shopping", " "); err == nil {
		t.Error("an empty item was added")
	}
	name, items, err := f.ReadTodoList("shopping")
	if err != nil || name != "Shopping List" || len(items) != 2 || items[1] != "Eggs" {
		t.Errorf("%q %v %v", name, items, err)
	}
	if _, items, err := f.ReadTodoList("chores"); err != nil || len(items) != 0 {
		t.Errorf("an empty list: %v, %v", items, err)
	}
}

// todo_lists is offered where there is a screen.
func TestTheTodoListsAction(t *testing.T) {
	if !hasScreen {
		t.Skip("no screen here")
	}
	found := false
	for _, a := range Get().Actions() {
		found = found || a.Name == "todo_lists"
	}
	if !found {
		t.Error("todo_lists is not offered")
	}
}

func TestAListNameFromItsEntity(t *testing.T) {
	if got := todoNameFrom("todo.shopping_list"); got != "Shopping list" {
		t.Errorf("got %q", got)
	}
}
