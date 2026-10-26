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
