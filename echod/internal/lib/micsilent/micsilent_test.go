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
