package assistant

import (
	"strings"

	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/lib/llm"
)

// To-do and shopping lists by voice, for the direct assistant: "add milk to the shopping list", "what's
// on my to-do list". With Home Assistant's own Assist as the voice pipeline this already works there
// (its lists answer natively), so these are offered only to the direct assistant, and only when the
// device has been given lists (todo_lists).

func todoTools() []tool {
	if len(home.Get().TodoLists()) == 0 {
		return nil
	}
	return []tool{
		{llm.Tool{Name: "add_todo", Description: "Add an item to one of this device's to-do or shopping lists.",
			Parameters: object(map[string]any{
				"item": str("What to add, like milk."),
				"list": str("Which list, as asked for (shopping); empty when none was named."),
			}, "item")},
			func(a map[string]any) (string, error) {
				name, err := home.Get().AddTodoItem(argString(a, "list"), argString(a, "item"))
				if err != nil {
					return "", err
				}
				return "added to the " + name + " list", nil
			}},

		{llm.Tool{Name: "read_todo", Description: "Read out what is still to do on one of this device's to-do or shopping lists.",
			Parameters: object(map[string]any{
				"list": str("Which list, as asked for (shopping); empty when none was named."),
			})},
			func(a map[string]any) (string, error) {
				name, items, err := home.Get().ReadTodoList(argString(a, "list"))
				if err != nil {
					return "", err
				}
				if len(items) == 0 {
					return "nothing is left on the " + name + " list", nil
				}
				return "on the " + name + " list: " + strings.Join(items, "; "), nil
			}},
	}
}
