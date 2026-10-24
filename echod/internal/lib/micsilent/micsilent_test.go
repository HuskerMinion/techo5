package micsilent

import "testing"

// Set tells the listeners once per change, never for the state it already had.
func TestSetSaysEachChangeOnce(t *testing.T) {
	t.Cleanup(func() { Set(false) })
	var heard []bool
	stop := Changed.Listen(func(on bool) { heard = append(heard, on) })
	defer stop()

	Set(false)
	Set(true)
	Set(true)
	if !Silent() {
		t.Fatal("not silent after Set(true)")
	}
	Set(false)
	if len(heard) != 2 || !heard[0] || heard[1] {
		t.Fatalf("heard %v, want [true false]", heard)
	}
}

func TestCutDoesNotRaiseDiagnosticProblem(t *testing.T) {
	Set(false)
	SetCut(false)
	t.Cleanup(func() { Set(false); SetCut(false) })
	var cuts, problems []bool
	stopCut := CutChanged.Listen(func(on bool) { cuts = append(cuts, on) })
	defer stopCut()
	stopProblem := Changed.Listen(func(on bool) { problems = append(problems, on) })
	defer stopProblem()

	SetCut(true)
	if !Cut() || Silent() || len(problems) != 0 {
		t.Fatal("short mute cut raised the diagnostic problem")
	}
	SetCut(false)
	if len(cuts) != 2 || !cuts[0] || cuts[1] {
		t.Fatalf("cut transitions: %v", cuts)
	}
}
