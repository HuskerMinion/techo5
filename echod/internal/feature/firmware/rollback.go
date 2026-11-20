package firmware

import (
	"errors"
	"log/slog"
	"strings"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/feedback"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
	"github.com/HuskerMinion/techo5/echod/internal/update"
)

// Rolling back, on a device that boots from slots: a regression found after the new slot committed
// itself (audio, an alarm, the camera) is otherwise undone only over SSH, with slotctl rollback. The
// button is in the diagnostic category, off the main card, beside the other update tools (Check for
// updates, Update status, Update channel) and the sensor that says what a rollback would go back to.
// It refuses unless the other slot is one that committed itself, so a stray press cannot leave the
// device without a system that works.

func (u *Firmware) buildRollback() {
	u.rollback = &esphome.Button{
		Base: esphome.Base{
			ObjectID: "roll_back",
			Name:     "Roll back",
			Icon:     "mdi:restore",
			Category: esphome.CategoryDiagnostic,
		},
		OnPress: func() { safe.Go("roll back", u.RollBack) },
	}
	u.other = &esphome.TextSensor{
		Base: esphome.Base{
			ObjectID: "other_slot",
			Name:     "Other slot",
			Icon:     "mdi:layers-outline",
			Category: esphome.CategoryDiagnostic,
		},
	}
	// The slots only change by an install or a rollback, both of which end in a reboot, so once is
	// enough.
	if s, ok := update.OtherSlot(); ok {
		u.other.Set(component.Fit(s.String()))
	}
}

// RollBack boots the release in the other slot, or says in Home Assistant why it will not. The
// release rolled back from is held back from installing by itself (auto.go) before the reboot.
func (u *Firmware) RollBack() {
	from := layout.Version
	err := update.RollBack(func() {
		if err := config.Set().Update().HeldBack(from); err != nil {
			slog.Error("saving the release rolled back from failed", "version", from, "err", err)
		}
	})
	switch {
	case err == nil:
	case errors.Is(err, update.ErrRebootAfterRollback):
		// Not refused: the other slot is set to boot, the restart into it is what failed.
		slog.Error("rolled back, but the reboot failed", "err", err)
		u.Settled(EventFailed, strings.TrimPrefix(update.ErrRebootAfterRollback.Error(), "update: "))
		feedback.Failure()
	default:
		if errors.Is(err, update.ErrInstalling) {
			// The lock is held by an install or by another press's rollback.
			err = errors.New("update: an update or a rollback is already running")
		}
		slog.Error("rolling back refused", "err", err)
		u.Settled(EventFailed, "roll back refused: "+strings.TrimPrefix(err.Error(), "update: "))
		feedback.Failure()
	}
}

// heldBack says whether the release offered tonight is the one rolled back from (or no newer), which
// installing by itself leaves alone.
func heldBack(offered string) bool {
	return update.HeldBack(offered, config.Get().Update.HeldBack)
}
