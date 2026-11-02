//go:build !dot && !spot

package display

import (
	"log/slog"
	"strconv"
	"strings"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// The To-do tab of the drawer: each list this device shows (config.Todo) as a heading with its open
// items under it. A tap on an item marks it done in Home Assistant, and it leaves the list; done items
// are not shown. The lists are read from Home Assistant in the background (feature/home todo.go).

// todoSection is one list as the tab draws it.
type todoSection struct {
	id, name string
	items    []hass.TodoItem
	read     bool // the list has been read; before that it has no items to show, which is not empty
}

// todoSections are the lists shown, as last read.
func todoSections() []todoSection {
	var out []todoSection
	for _, id := range home.Get().TodoLists() {
		items, read := home.Get().TodoItems(id)
		out = append(out, todoSection{id: id, name: home.Get().TodoName(id), items: items, read: read})
	}
	return out
}

// OpenTodo puts the drawer up on its To-do tab. It reports false, and does nothing, on a device that
// shows no list.
func (d *Display) OpenTodo() bool {
	if len(config.Get().Todo.Lists) == 0 {
		return false
	}
	d.openDrawer(drawerTodo)
	return true
}

// todoRows are the To-do tab's rows, and a note to show when it has none. An item's row id is the list's
// place among the lists shown and the item's uid, so a tap finds the item whatever has moved since the
// page was drawn.
func todoRows(s scene) ([]settingRow, string) {
	if len(s.todo) == 0 {
		return nil, tr("drawer_no_todo")
	}
	var rows []settingRow
	for n, sec := range s.todo {
		name := sec.name
		if s.demo {
			name = "List " + strconv.Itoa(n+1) // a list's name can say whose it is
		}
		rows = append(rows, settingRow{label: name, bold: true, kind: ctlValue})
		switch {
		case !sec.read:
			rows = append(rows, settingRow{label: tr("ui_loading"), kind: ctlValue})
		case len(sec.items) == 0:
			rows = append(rows, settingRow{label: tr("drawer_todo_empty"), kind: ctlValue})
		}
		for i, it := range sec.items {
			label := it.Summary
			if s.demo {
				label = "Item " + strconv.Itoa(i+1)
			}
			rows = append(rows, settingRow{id: "todo:" + strconv.Itoa(n) + ":" + it.UID, label: label,
				kind: ctlButton, button: tr("done"), rowTap: true})
		}
	}
	return rows, ""
}

// todoTap is a tap on an item's row, "todo:<list's place>:<uid>": it is marked done in Home Assistant.
func (d *Display) todoTap(id string) {
	parts := strings.SplitN(id, ":", 3)
	if len(parts) != 3 {
		return
	}
	lists := home.Get().TodoLists()
	n, err := strconv.Atoi(parts[1])
	if err != nil || n < 0 || n >= len(lists) || parts[2] == "" {
		return
	}
	go func(list, uid string) {
		if err := home.Get().CompleteTodo(list, uid); err != nil {
			slog.Warn("screen: marking a to-do item done failed", "list", list, "err", err)
		}
	}(lists[n], parts[2])
}
