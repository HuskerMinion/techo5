package detect

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/voice"
	"github.com/HuskerMinion/techo5/echod/internal/feature/wakeword"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
)

// A wake word whose model failed on live audio is taken out of its slot (Engine.dropFailed). Home
// Assistant's select still names it, so the device went deaf with nothing to show for it until the word
// was chosen again (#692). Now Wake word problem says so, and the saved selection is loaded again after
// reloadAfter: a failure brought on by one stretch of audio is gone, and the word listens again. A model
// that fails again within holdFor is left out until it is chosen again, so a model that cannot run is not
// reloaded in a loop; Wake word problem keeps saying why.
var (
	reloadAfter = 10 * time.Second
	holdFor     = 10 * time.Minute
	// later is time.AfterFunc; a test runs it at once.
	later = func(d time.Duration, f func()) { time.AfterFunc(d, f) }
)

// dropped is the engine having taken a model out for failing on the audio: a command word's, or a wake
// word's.
func (d *Detect) dropped(slot int, id string) {
	if c := d.commandAt(slot); c != nil {
		c.dropped(id)
		return
	}
	if slot < 0 || slot >= wakeword.Slots {
		return
	}
	d.wakeDropped(slot, id, time.Now())
}

func (d *Detect) wakeDropped(slot int, id string, now time.Time) {
	d.dropMu.Lock()
	last, again := d.lastDrop[id]
	if d.lastDrop == nil {
		d.lastDrop = map[string]time.Time{}
	}
	d.lastDrop[id] = now
	d.dropMu.Unlock()

	if again && now.Sub(last) < holdFor {
		slog.Error("wake word failed again soon after it was put back; left out until it is chosen again", "slot", slot+1, "id", id)
		d.addWakeProblem(fmt.Sprintf("Wake word %d (%s): its model keeps failing on live audio; choose it again, or another word", slot+1, id))
		return
	}
	d.addWakeProblem(fmt.Sprintf("Wake word %d (%s): its model failed on live audio; putting it back", slot+1, id))
	later(reloadAfter, func() {
		safe.Go("wake word put back", func() {
			slog.Info("putting a dropped wake word back", "slot", slot+1, "id", id)
			d.reloadSaved()
		})
	})
}

// addWakeProblem says one more thing on Wake word problem, after whatever the last load said.
func (d *Detect) addWakeProblem(s string) {
	if d.wakeProblem == nil {
		return
	}
	cur := d.wakeProblem.Get()
	if cur == "" || cur == "None" {
		d.setWakeProblem([]string{s})
		return
	}
	d.setWakeProblem([]string{cur, s})
}

// reloadSaved loads the saved selection again and says it is the active one, reading the selection under
// the load lock, so a choice made in Home Assistant at the same moment is not overwritten with the one it
// replaced.
func (d *Detect) reloadSaved() {
	d.loading.Lock()
	defer d.loading.Unlock()
	d.refused = nil
	ids := voice.SavedWakeWords(d.library().Ours())
	voice.Get().SetActiveWakeWords(d.loadLocked(ids))
}
