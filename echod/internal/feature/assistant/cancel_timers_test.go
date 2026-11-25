package assistant

import (
	"slices"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/feature/timer"
)

// The assistant's "cancel the timers" leaves a silent timer running: it is an automation's wait, which the
// automation ends, and canceling it would only take the countdown away (#723).
func TestCancelTimersLeavesSilentOnesToTheirAutomation(t *testing.T) {
	list := []timer.Countdown{
		{ID: "local:pasta", Name: "Pasta", Local: true, Active: true},
		{ID: "local:join", Name: "Pairing", Local: true, Active: true, Silent: true},
	}
	for _, c := range []struct {
		label string
		want  []string
	}{
		{"", []string{"local:pasta"}},
		{"pasta", []string{"local:pasta"}},
		// Said by its label, the silent one is still left alone, and the one timer of the person's own
		// is what "cancel the timer" is taken to mean, as it is for any label that matches nothing.
		{"Pairing", []string{"local:pasta"}},
	} {
		var gone []string
		n, kept := cancelTimers(list, c.label, func(id string) bool { gone = append(gone, id); return true })
		if !slices.Equal(gone, c.want) || n != len(c.want) || kept != 1 {
			t.Errorf("label %q canceled %v (n %d, kept %d), want %v and the silent one kept", c.label, gone, n, kept, c.want)
		}
	}

	// With only a silent timer counting down, nothing is canceled, even though it is the only one.
	var gone []string
	n, kept := cancelTimers(list[1:], "", func(id string) bool { gone = append(gone, id); return true })
	if n != 0 || kept != 1 || len(gone) != 0 {
		t.Errorf("canceled %v (n %d, kept %d), want the silent timer left running", gone, n, kept)
	}
}
