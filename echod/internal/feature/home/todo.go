package home

import (
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/hastate"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// To-do and shopping lists: the lists this device shows, read from Home Assistant (docs/setup.md). Kept
// the way the calendar's months are: a screen asking while it draws gets what is kept, a read in the
// background replaces it, and Changed fires when something newer arrives.

const (
	// todoEvery is how long a list is kept before it is read again when somebody looks at it. Home
	// Assistant also tells the device when a list changes (todoChanged), so this is the net under that.
	todoEvery = time.Minute

	// todoNamesEvery is how often the lists' names are read again.
	todoNamesEvery = 10 * time.Minute

	// todoItemsKept is the most items kept of one list, and todoTextRunes the most of an item's words.
	// Whoever can write to a list can fill it, and every item is measured on each frame that draws it.
	todoItemsKept = 200
	todoTextRunes = 200
)

var todo struct {
	sync.Mutex
	lists     map[string]*todoList
	names     map[string]string
	namesAt   time.Time
	namesBusy bool
}

// todoList is one list's open items as last read.
type todoList struct {
	items []hass.TodoItem
	at    time.Time
	busy  bool
	read  bool
}

// TodoLists is the lists this device shows, in the order chosen; none is no To-do page.
func (f *Feature) TodoLists() []string { return config.Get().Todo.Lists }

// SetTodoLists chooses the lists this device shows.
func (f *Feature) SetTodoLists(ids []string) error {
	var clean []string
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || slices.Contains(clean, id) {
			continue
		}
		if !strings.HasPrefix(id, "todo.") {
			return errors.New("todo: " + id + " is not a to-do list entity")
		}
		clean = append(clean, id)
	}
	if err := config.Set().Todo().Lists(clean); err != nil {
		return err
	}
	todo.Lock()
	todo.lists = nil // read for the old choice
	todo.Unlock()
	slog.Info("todo: lists set", "count", len(clean))
	f.rewire() // Home Assistant is told the lists to follow at the next connection
	return nil
}

// todoAction is todo_lists: the lists this device shows, as entity ids separated by commas; empty shows
// none.
func (f *Feature) todoAction() *esphome.Action {
	return &esphome.Action{
		Name: "todo_lists",
		Args: []esphome.Arg{{Name: "lists", Type: esphome.ArgString}},
		Run: func(c esphome.Call) (any, error) {
			return nil, f.SetTodoLists(strings.Split(c.String("lists"), ","))
		},
	}
}

// todoChanged is a value arriving for a list followed: its open items changed, so it is read again the
// next time it is looked at, and the screen draws.
func (f *Feature) todoChanged(u hastate.Update) {
	if u.First || u.Attribute != "" || !slices.Contains(config.Get().Todo.Lists, u.Entity) {
		return
	}
	todo.Lock()
	if l := todo.lists[u.Entity]; l != nil {
		l.at = time.Time{}
	}
	todo.Unlock()
	f.Changed.Emit(struct{}{})
}

// TodoItems is a list's items still to do as last read, and whether it has been read. It starts a read
// when the list is missing or stale; it never waits.
func (f *Feature) TodoItems(entity string) ([]hass.TodoItem, bool) {
	todo.Lock()
	defer todo.Unlock()
	if todo.lists == nil {
		todo.lists = map[string]*todoList{}
	}
	l := todo.lists[entity]
	if l == nil {
		l = &todoList{}
		todo.lists[entity] = l
	}
	if !l.busy && hass.Get().Ready() && time.Since(l.at) > todoEvery {
		l.busy = true
		go f.readTodo(entity)
	}
	return slices.Clone(l.items), l.read
}

func (f *Feature) readTodo(entity string) {
	items, err := hass.Get().TodoItems(entity)
	if len(items) > todoItemsKept {
		items = items[:todoItemsKept]
	}
	for i := range items {
		items[i].Summary = capRunes(items[i].Summary, todoTextRunes)
	}
	todo.Lock()
	l := todo.lists[entity]
	if l == nil { // forgotten while it was read
		todo.Unlock()
		return
	}
	l.busy = false
	if err != nil {
		slog.Warn("todo: reading a list failed", "list", entity, "err", err)
		l.at = time.Now().Add(todoEvery/2 - todoEvery) // try again in half a minute
		todo.Unlock()
		return
	}
	l.items, l.at, l.read = items, time.Now(), true
	todo.Unlock()
	f.Changed.Emit(struct{}{})
}

// CompleteTodo marks an item done in Home Assistant and takes it off the screen at once; the list is
// read again after, so what Home Assistant did is what shows.
func (f *Feature) CompleteTodo(entity, uid string) error {
	if !slices.Contains(config.Get().Todo.Lists, entity) {
		return errors.New("todo: " + entity + " is not one of this device's lists")
	}
	if err := hass.Get().CompleteTodo(entity, uid); err != nil {
		return err
	}
	todo.Lock()
	if l := todo.lists[entity]; l != nil {
		l.items = slices.DeleteFunc(l.items, func(i hass.TodoItem) bool { return i.UID == uid })
		l.at = time.Time{}
	}
	todo.Unlock()
	f.Changed.Emit(struct{}{})
	return nil
}

// TodoName is a list's name as Home Assistant shows it, or one made from its entity id until that has
// been read.
func (f *Feature) TodoName(entity string) string {
	if n := f.todoNames(false)[entity]; n != "" {
		return n
	}
	return todoNameFrom(entity)
}

// todoNameFrom is "Shopping list" for "todo.shopping_list".
func todoNameFrom(entity string) string {
	n := strings.ReplaceAll(strings.TrimPrefix(entity, "todo."), "_", " ")
	if n == "" {
		return entity
	}
	return strings.ToUpper(n[:1]) + n[1:]
}

// todoNames are the lists' names by entity. A read in the background when they are stale, or inline
// when wait is set, for a voice request that needs them now.
func (f *Feature) todoNames(wait bool) map[string]string {
	todo.Lock()
	stale := time.Since(todo.namesAt) > todoNamesEvery && !todo.namesBusy && hass.Get().Ready()
	if stale {
		todo.namesBusy = true
	}
	todo.Unlock()
	if stale {
		if wait {
			f.readTodoNames()
		} else {
			go f.readTodoNames()
		}
	}
	todo.Lock()
	defer todo.Unlock()
	return todo.names
}

func (f *Feature) readTodoNames() {
	list, err := hass.Get().Entities("todo")
	todo.Lock()
	todo.namesBusy = false
	if err != nil {
		slog.Warn("todo: reading Home Assistant's lists failed", "err", err)
		todo.namesAt = time.Now().Add(time.Minute - todoNamesEvery) // try again in a minute
	} else {
		todo.names = map[string]string{}
		for _, e := range list {
			todo.names[e.ID] = e.Name
		}
		todo.namesAt = time.Now()
	}
	todo.Unlock()
	if err == nil {
		f.Changed.Emit(struct{}{})
	}
}

// pickTodo is the list asked for by name ("shopping", "the shopping list"): the one whose name holds
// the words, or the only list there is when none was named.
func pickTodo(asked string, lists []string, name func(string) string) (string, error) {
	if len(lists) == 0 {
		return "", errors.New("this device shows no to-do lists")
	}
	words := strings.Fields(strings.ToLower(strings.NewReplacer("list", "", "the ", "", "my ", "").Replace(asked)))
	if len(words) == 0 {
		if len(lists) == 1 {
			return lists[0], nil
		}
		return "", fmt.Errorf("no list was named: ask which of these: %s", todoNameList(lists, name))
	}
	for _, l := range lists {
		have := strings.ToLower(name(l))
		ok := true
		for _, w := range words {
			ok = ok && strings.Contains(have, w)
		}
		if ok {
			return l, nil
		}
	}
	return "", fmt.Errorf("there is no list called %q; the lists are: %s", asked, todoNameList(lists, name))
}

func todoNameList(lists []string, name func(string) string) string {
	names := make([]string, len(lists))
	for i, l := range lists {
		names[i] = name(l)
	}
	return strings.Join(names, ", ")
}

// AddTodoItem adds an item to the list asked for among this device's lists, and says which list it
// went to.
func (f *Feature) AddTodoItem(list, item string) (string, error) {
	item = capRunes(strings.TrimSpace(item), todoTextRunes)
	if item == "" {
		return "", errors.New("no item was given")
	}
	entity, err := f.pickList(list)
	if err != nil {
		return "", err
	}
	if err := hass.Get().AddTodo(entity, item); err != nil {
		return "", err
	}
	todo.Lock()
	if l := todo.lists[entity]; l != nil {
		l.at = time.Time{}
	}
	todo.Unlock()
	f.Changed.Emit(struct{}{})
	return f.TodoName(entity), nil
}

// ReadTodoList is the open items on the list asked for, and its name.
func (f *Feature) ReadTodoList(list string) (string, []string, error) {
	entity, err := f.pickList(list)
	if err != nil {
		return "", nil, err
	}
	items, err := hass.Get().TodoItems(entity)
	if err != nil {
		return "", nil, err
	}
	var out []string
	for _, i := range items[:min(len(items), todoItemsKept)] {
		out = append(out, capRunes(i.Summary, todoTextRunes))
	}
	return f.TodoName(entity), out, nil
}

func (f *Feature) pickList(asked string) (string, error) {
	lists := f.TodoLists()
	if len(lists) == 0 {
		return "", errors.New("this device shows no to-do lists")
	}
	if !hass.Get().Ready() {
		return "", errors.New("Home Assistant is not connected")
	}
	f.todoNames(true)
	return pickTodo(asked, lists, f.TodoName)
}
