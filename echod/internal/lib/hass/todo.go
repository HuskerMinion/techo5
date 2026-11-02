package hass

import (
	"encoding/json"
	"fmt"
)

// To-do and shopping lists, from Home Assistant's todo services. Every list - the Shopping List
// integration, Local To-do, Google Tasks, Todoist - is a todo.* entity, read and changed the same way.

// TodoItem is one item on a to-do list.
type TodoItem struct {
	UID     string
	Summary string
	Done    bool
}

// TodoItems is a list's items still to do, in the list's order.
func (c *Client) TodoItems(entity string) ([]TodoItem, error) {
	out, err := c.CallResponse("todo", "get_items", map[string]any{"entity_id": entity, "status": "needs_action"})
	if err != nil {
		return nil, err
	}
	return parseTodoItems(entity, out)
}

// parseTodoItems reads get_items' answer, which is keyed by the list's entity.
func parseTodoItems(entity string, out map[string]any) ([]TodoItem, error) {
	b, err := json.Marshal(out[entity])
	if err != nil {
		return nil, err
	}
	var list struct {
		Items []struct {
			UID     string `json:"uid"`
			Summary string `json:"summary"`
			Status  string `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(b, &list); err != nil {
		return nil, fmt.Errorf("hass: %s answered: %w", entity, err)
	}
	var items []TodoItem
	for _, i := range list.Items {
		if i.Summary == "" {
			continue
		}
		uid := i.UID
		if uid == "" {
			uid = i.Summary // update_item takes the summary where it has no uid
		}
		items = append(items, TodoItem{UID: uid, Summary: i.Summary, Done: i.Status == "completed"})
	}
	return items, nil
}

// CompleteTodo marks an item done. The item is named by its uid, which Home Assistant takes in place of
// its summary.
func (c *Client) CompleteTodo(entity, uid string) error {
	return c.Call("todo", "update_item", map[string]any{"entity_id": entity, "item": uid, "status": "completed"})
}

// AddTodo adds an item to a list.
func (c *Client) AddTodo(entity, item string) error {
	return c.Call("todo", "add_item", map[string]any{"entity_id": entity, "item": item})
}
