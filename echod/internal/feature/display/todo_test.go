//go:build !dot && !spot

package display

import (
	"encoding/json"
	"image"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/voice"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/touch"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// todoHA is a Home Assistant that records the to-do services it is asked to run.
func todoHA(t *testing.T) func() []map[string]any {
	t.Helper()
	var mu sync.Mutex
	var got []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/services/todo/") {
			var body map[string]any
			b, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(b, &body)
			body["service"] = strings.TrimPrefix(r.URL.Path, "/api/services/todo/")
			mu.Lock()
			got = append(got, body)
			mu.Unlock()
		}
		_, _ = w.Write([]byte(`{"service_response":{}}`))
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
	return func() []map[string]any {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(got)
	}
}

func todoScene() scene {
	return scene{now: time.Date(2026, 11, 2, 9, 0, 0, 0, time.Local), phase: "idle", showDrawer: true, drawerTab: drawerTodo,
		todo: []todoSection{
			{id: "todo.shopping", name: "Shopping List", read: true, items: []hass.TodoItem{{UID: "u1", Summary: "Milk"}, {UID: "u2", Summary: "Eggs"}}},
			{id: "todo.chores", name: "Chores", read: true},
			{id: "todo.garage", name: "Garage"},
		}}
}

// The tab lays out each list as a heading with its items under it: an empty list says so, and one not
// read yet says it is loading rather than empty.
func TestTodoRows(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	rows, note := todoRows(todoScene())
	if note != "" {
		t.Fatalf("note %q", note)
	}
	var got []string
	for _, r := range rows {
		got = append(got, r.label+"|"+r.id)
	}
	want := []string{"Shopping List|", "Milk|todo:0:u1", "Eggs|todo:0:u2", "Chores|", "Nothing to do|", "Garage|", "Loading…|"}
	if !slices.Equal(got, want) {
		t.Errorf("rows\n got %q\nwant %q", got, want)
	}
	if !rows[0].bold || rows[1].kind != ctlButton || !rows[1].rowTap {
		t.Errorf("a heading is bold and an item is a tappable button: %+v %+v", rows[0], rows[1])
	}
	if rows, note := todoRows(scene{}); len(rows) != 0 || note == "" {
		t.Errorf("no list chosen: %v %q", rows, note)
	}
	s := todoScene()
	s.demo = true
	rows, _ = todoRows(s)
	if rows[0].label != "List 1" || rows[1].label != "Item 1" {
		t.Errorf("screenshots show names: %q %q", rows[0].label, rows[1].label)
	}
}

// The tab is offered once a list is chosen, and not before.
func TestTodoTabNeedsAList(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if slices.Contains(shownTabIDs(), drawerTodo) || len(shownTabs()) != len(shownTabIDs()) {
		t.Fatal("a To-do tab with no list")
	}
	_ = config.Set().Todo().Lists([]string{"todo.shopping"})
	ids := shownTabIDs()
	if ids[len(ids)-1] != drawerTodo || shownTabs()[len(ids)-1] != "To-do" || tabLabel(drawerTodo) != "To-do" {
		t.Errorf("tabs %v %v", ids, shownTabs())
	}
	d := &Display{poke: make(chan struct{}, 1)}
	if !d.OpenTodo() || !d.drawer || d.drawerTab != drawerTodo {
		t.Error("To-do did not open")
	}
	_ = config.Set().Todo().Lists(nil)
	if d.OpenTodo() {
		t.Error("To-do opened with no list")
	}
}

// The drawn tab, with five tabs on its strip, lays the items out as rows a finger can find, and nothing
// panics; a tap on an item calls todo.update_item for that item and list.
func TestTodoTabDrawsAndATapMarksTheItemDone(t *testing.T) {
	seen := todoHA(t)
	_ = config.Set().Todo().Lists([]string{"todo.shopping", "todo.chores", "todo.garage"})
	d := &Display{r: newRenderer(image.NewRGBA(image.Rect(0, 0, 960, 480))), poke: make(chan struct{}, 1),
		view: voice.State{Phase: "idle"}}
	d.r.draw(todoScene())

	var item image.Point
	d.r.zmu.Lock()
	for _, z := range d.r.zones {
		if z.kind == zoneRow && z.id == "todo:0:u2" {
			item = image.Pt((z.r.Min.X+z.r.Max.X)/2, (z.r.Min.Y+z.r.Max.Y)/2)
		}
	}
	d.r.zmu.Unlock()
	if item == (image.Point{}) {
		t.Fatal("the second item has no row to tap")
	}
	d.drawerGesture(touch.Gesture{Kind: touch.Tap, X: item.X, Y: item.Y})
	deadline := time.Now().Add(3 * time.Second)
	for len(seen()) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	got := seen()
	if len(got) != 1 || got[0]["service"] != "update_item" || got[0]["entity_id"] != "todo.shopping" ||
		got[0]["item"] != "u2" || got[0]["status"] != "completed" {
		t.Fatalf("calls %v", got)
	}
}

// A tap that names a list this device does not show, or no item, calls nothing.
func TestTodoTapIgnoresWhatIsNotShown(t *testing.T) {
	seen := todoHA(t)
	_ = config.Set().Todo().Lists([]string{"todo.shopping"})
	d := &Display{poke: make(chan struct{}, 1)}
	for _, id := range []string{"todo:5:u1", "todo:x:u1", "todo:0:", "todo:0", "todo:-1:u1"} {
		d.todoTap(id)
	}
	time.Sleep(100 * time.Millisecond)
	if got := seen(); len(got) != 0 {
		t.Errorf("calls %v", got)
	}
}
