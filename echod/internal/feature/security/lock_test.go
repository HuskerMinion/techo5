package security

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// A PIN is 4 to 8 digits and is kept hashed; the right one opens the settings until they close, wrong
// ones in a row make the device wait, and clearing it takes the lock away.
func TestTheSettingsLock(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	f := Get()
	lock.mu.Lock()
	lock.unlockedUntil = time.Time{}
	lock.mu.Unlock()

	if Locked() {
		t.Fatal("locked with no PIN")
	}
	for _, bad := range []string{"123", "123456789", "12a4"} {
		if err := f.SetPIN(bad); err == nil {
			t.Errorf("PIN %q was taken", bad)
		}
	}
	if err := f.SetPIN("2468"); err != nil {
		t.Fatal(err)
	}
	if stored := config.Get().Security.LockPIN; stored == "" || stored == "2468" || !strings.HasPrefix(stored, "pbkdf2:") {
		t.Fatalf("the PIN is kept as %q", stored)
	}
	if !webPages {
		if Locked() {
			t.Error("a device without a screen is locked")
		}
		return
	}
	if !Locked() {
		t.Fatal("not locked with a PIN")
	}
	if ok, _ := TryPIN("2468"); !ok || Locked() {
		t.Fatal("the right PIN did not open the settings")
	}
	Relock()
	if !Locked() {
		t.Fatal("not locked again once the settings closed")
	}
	for i := 1; i < freeTries; i++ {
		if ok, wait := TryPIN("0000"); ok || wait != 0 {
			t.Fatalf("wrong PIN %d: ok=%v wait=%v", i, ok, wait)
		}
	}
	if ok, wait := TryPIN("0000"); ok || wait < firstWait-time.Second {
		t.Fatalf("the fifth wrong PIN: ok=%v wait=%v", ok, wait)
	}
	if ok, wait := TryPIN("2468"); ok || wait <= 0 {
		t.Fatal("the right PIN was taken while the device was making people wait")
	}
	// Kept in the saved settings, so a restart does not start the count over.
	if c := config.Get().Security; c.LockFails != freeTries || c.LockBlockedUntil <= time.Now().Unix() {
		t.Errorf("saved tries = %d, blocked until %d", c.LockFails, c.LockBlockedUntil)
	}
	if err := f.SetPIN(""); err != nil || Locked() || LockSet() {
		t.Fatalf("clearing the PIN: %v locked=%v", err, Locked())
	}
}
