//go:build !dot && !spot

package all

import (
	"slices"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/component"
)

// The To-do tab's two actions are on the devices that draw it: todo_lists chooses the lists and
// todo_show puts the tab up.
func TestTheTodoActionsRegister(t *testing.T) {
	var got []string
	for _, a := range component.Default().Actions() {
		got = append(got, a.Name)
	}
	for _, name := range []string{"todo_lists", "todo_show"} {
		if !slices.Contains(got, name) {
			t.Errorf("action %q is not registered on this device", name)
		}
	}
}
