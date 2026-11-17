//go:build !dot

package display

import (
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/takes"
)

// Each of the setup page's askings is a page of its own: a recording's card once it has started is
// another than its Start card, so the tap that started it does not answer its Stop, and a browser asking
// to be let in is another again. A tap answers the page drawn only once it has been up to be read.
func TestEachSetupPageAskingIsItsOwnPage(t *testing.T) {
	armed := takes.Card{ID: 3, Phase: takes.Armed}
	recording := takes.Card{ID: 3, Phase: takes.Recording}
	seen := map[uint64]bool{}
	for _, k := range []uint64{takesKey(armed), takesKey(recording), takesKey(takes.Card{ID: 4, Phase: takes.Armed}),
		letInKey("one browser"), letInKey("another")} {
		if k == 0 || seen[k] {
			t.Errorf("two askings share the key %x", k)
		}
		seen[k] = true
	}
	if takesKey(takes.Card{}) != 0 {
		t.Error("no card has a key")
	}

	var l askLatch
	at := time.Now()
	l.drawn(takesKey(armed), at)
	if _, ok := l.answerable(at.Add(askSettle / 2)); ok {
		t.Error("Start answered before it could be read")
	}
	l.drawn(takesKey(recording), at.Add(askSettle))
	if k, ok := l.answerable(at.Add(askSettle + askSettle/2)); ok || k != takesKey(recording) {
		t.Error("Stop answered the moment Start became it")
	}
	if k, ok := l.answerable(at.Add(2 * askSettle)); !ok || k != takesKey(recording) {
		t.Error("Stop never answered")
	}
}

// Record wake word is Start with the count and the gap under it, says what has to be set first, and
// its steppers keep to a run's range.
func TestRecordWakeWordRows(t *testing.T) {
	rows := recordWordRows()
	if len(rows) != 3 || rows[0].id != "recordword" || rows[0].kind != ctlButton || rows[1].id != "recordcount" || rows[2].id != "recordgap" {
		t.Fatalf("rows %+v", rows)
	}
	// Record takes of a word is off and Send wake clips to is not set: the switch is not asked for here.
	if rows[0].sub != tr("ui_record_set_wakesmith_first") {
		t.Errorf("with nothing set it says %q", rows[0].sub)
	}
	takes.Get().SetRunPlan(takes.RunCountMax, takes.RunGapMin)
	t.Cleanup(func() { takes.Get().SetRunPlan(takes.RunCountDefault, takes.RunGapDefault) })
	recordWordTap("recordcount", partPlus)
	recordWordTap("recordgap", partMinus)
	if c, g := takes.Get().RunPlan(); c != takes.RunCountMax || g != takes.RunGapMin {
		t.Errorf("stepped past the ends: %d, %d", c, g)
	}
	recordWordTap("recordcount", partMinus)
	recordWordTap("recordgap", partPlus)
	if c, g := takes.Get().RunPlan(); c != takes.RunCountMax-5 || g != takes.RunGapMin+1 {
		t.Errorf("stepped to %d, %d", c, g)
	}
	recordWordTap("recordword", partMain)
	if takes.Get().Card().Up() {
		t.Error("Start began a run with nowhere to send the takes")
	}
}
