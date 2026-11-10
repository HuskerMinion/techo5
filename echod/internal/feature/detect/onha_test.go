package detect

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
)

func TestMissingWakeVerificationCredentialsLeaveSwitchOffAndNotify(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	config.Started(config.Device{Name: "Office"})
	var calls []component.Call
	t.Cleanup(component.CallService.Listen(func(call component.Call) { calls = append(calls, call) }))

	hasAccess := false
	o := newOnHAEntities(func() {}, func() bool { return hasAccess })
	o.verify.OnCommand(true)
	o.verify.OnCommand(true)
	if o.verify.Get() || config.Get().Wake.OnHA.VerifySTT {
		t.Fatal("wake verification was enabled without credentials")
	}
	if len(calls) != 2 {
		t.Fatalf("got %d notifications, want one per attempted enable", len(calls))
	}
	if calls[0].Data["notification_id"] == "" || calls[0].Data["notification_id"] != calls[1].Data["notification_id"] {
		t.Fatal("repeated attempts should replace the same notification")
	}
	if calls[0].Service != "persistent_notification.create" ||
		!strings.Contains(calls[0].Data["title"], "Office") ||
		!strings.Contains(calls[0].Data["message"], "]("+wakeVerificationSetupURL+")") {
		t.Fatalf("wrong setup notification: %+v", calls[0])
	}

	hasAccess = true
	o.verify.OnCommand(true)
	if !o.verify.Get() || !config.Get().Wake.OnHA.VerifySTT || len(calls) != 2 {
		t.Fatal("configured credentials should allow enabling without another notification")
	}
}
