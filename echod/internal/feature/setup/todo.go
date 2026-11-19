package setup

import (
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"slices"
	"strings"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// todoSection is which of Home Assistant's to-do and shopping lists this device shows on its To-do tab
// (config.Todo), a box for each. A list chosen that Home Assistant no longer offers is kept in the list,
// so it can be unticked.
func todoSection(w http.ResponseWriter, token string) {
	s := screen.Load()
	if s == nil || !s.Todo {
		return
	}
	var offered []hass.Entity
	if hass.Get().Ready() {
		offered, _ = hass.Get().Entities("todo")
	}
	chosen := config.Get().Todo.Lists
	for _, id := range chosen {
		if !slices.ContainsFunc(offered, func(e hass.Entity) bool { return e.ID == id }) {
			offered = append(offered, hass.Entity{ID: id})
		}
	}
	sectionOpen(w, "todo", "To-do lists", todoSummary(offered, chosen)...)
	fmt.Fprint(w, `<form method="post" action="/setup/save">`)
	hidden(w, token, "todo")
	if len(offered) == 0 {
		fmt.Fprint(w, `<p style="margin:0">Home Assistant has no to-do lists yet, or this device cannot reach it.</p>`)
	}
	for _, e := range offered {
		name := e.Name
		if name == "" {
			name = e.ID
		}
		fmt.Fprintf(w, `<p style="margin:.3rem 0"><label><input type="checkbox" name="list" value="%s"%s> %s</label></p>`,
			html.EscapeString(e.ID), checked(slices.Contains(chosen, e.ID)), html.EscapeString(name))
	}
	fmt.Fprint(w, `<p class="note">The lists the To-do tab in the drawer shows, with their open items. A tap on
	 an item marks it done in Home Assistant. None ticked leaves the tab off. Also the
	 <code>todo_lists</code> action in Home Assistant.</p>
	 <p><button type="submit">Save</button></p></form>`)
	sectionClose(w)
}

// todoSummary is the line on the shut card: the lists chosen, by their names where Home Assistant gave
// them, or Off for none. It reads only what the section has already been given.
func todoSummary(offered []hass.Entity, chosen []string) []string {
	if len(chosen) == 0 {
		return []string{"Off"}
	}
	var names []string
	for _, id := range chosen {
		name := id
		for _, e := range offered {
			if e.ID == id && e.Name != "" {
				name = e.Name
			}
		}
		names = append(names, name)
	}
	return []string{strings.Join(names, ", ")}
}

func saveTodo(r *http.Request) string {
	if s := screen.Load(); s == nil || !s.Todo {
		return "this device has no To-do tab"
	}
	if err := r.ParseForm(); err != nil {
		return "could not read the form"
	}
	if err := home.Get().SetTodoLists(r.PostForm["list"]); err != nil {
		return "could not keep the lists: " + err.Error()
	}
	slog.Info("setup page: to-do lists set", "count", len(r.PostForm["list"]))
	return ""
}
