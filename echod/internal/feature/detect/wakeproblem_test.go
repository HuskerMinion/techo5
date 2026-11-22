package detect

import (
	"errors"
	"strings"
	"testing"
)

func TestWakeWordProblemSaysWhyWithoutThePath(t *testing.T) {
	err := errors.New("wake: loading /data/misc/techo5/wake/hey_alfred_v7test.tflite: the .tflite is not a microWakeWord model the engine can run: it has more than 256 operators")
	got := wakeProblemText(err)
	if strings.Contains(got, "/data") || !strings.HasSuffix(got, "it has more than 256 operators") {
		t.Errorf("got %q", got)
	}
	if got := wakeProblemText(errors.New("something else")); got != "something else" {
		t.Errorf("got %q", got)
	}
}

func TestWakeWordProblemIsNoneOrEachSlot(t *testing.T) {
	d := &Detect{wakeProblem: newWakeProblemEntity()}
	d.setWakeProblem(nil)
	if got := d.wakeProblem.Get(); got != "None" {
		t.Errorf("no problems: %q", got)
	}
	d.setWakeProblem([]string{"Wake word 1 (a): x", "Wake word 2 (b): y"})
	if got := d.wakeProblem.Get(); got != "Wake word 1 (a): x; Wake word 2 (b): y" {
		t.Errorf("two problems: %q", got)
	}
	d.setWakeProblem([]string{strings.Repeat("z", 400)})
	if got := d.wakeProblem.Get(); len(got) != 255 || !strings.HasSuffix(got, "...") {
		t.Errorf("long: %d %q", len(got), got[len(got)-5:])
	}
	(&Detect{}).setWakeProblem([]string{"no entity: nothing to set"})
}
