package config

// Todo is which of Home Assistant's to-do and shopping lists this device shows (docs/setup.md). Each
// device keeps its own, like its calendars: one in a kitchen shows the shopping list.
type Todo struct {
	// Lists are todo entities, like "todo.shopping_list", in the order chosen. None: no To-do page on
	// this device.
	Lists []string `json:"lists,omitempty"`
}

type TodoWriter struct{ st *Store }

func (w TodoWriter) Lists(v []string) error {
	return w.st.Update(func(c *Config) { c.Todo.Lists = v })
}
