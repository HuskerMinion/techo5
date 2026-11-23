//go:build spot

package display

import (
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/notify"
	"github.com/HuskerMinion/techo5/echod/internal/feature/remind"
)

// A critical notification takes the face over a reminder and an announcement: it rings until it is
// answered, and its face is how. A plain one stays under them.
func TestACriticalNotificationTakesTheFaceOverAReminder(t *testing.T) {
	at := time.Date(2025, 9, 10, 14, 7, 0, 0, time.Local)
	r := spotRenderer()
	frame := func(s roundScene) string {
		r.draw(s)
		return string(r.dst.Pix)
	}
	crit := notify.Notification{Kind: notify.Text, Message: "Water in the basement", Critical: true}
	alone := roundScene{now: at, phase: "idle", showNotification: true, notification: crit}
	over := alone
	over.showReminder, over.reminder = true, remind.Reminder{Label: "Take the trash out"}
	over.showAnnouncement = true
	if frame(over) != frame(alone) {
		t.Error("a reminder or an announcement took the face over a critical notification")
	}
	plain := over
	plain.notification.Critical = false
	if frame(plain) == frame(alone) {
		t.Error("a plain notification took the face over the reminder too")
	}
}
