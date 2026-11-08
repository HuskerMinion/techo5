//go:build !dot

package display

import (
	"sync"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/firstrun"
	"github.com/HuskerMinion/techo5/echod/internal/lib/wifi"
)

// frFake is first run's view of the rest of the device, made up.
type frFake struct {
	mu       sync.Mutex
	home     bool
	chimes   int
	adopt    time.Time
	opened   int
	renamed  string
	checks   int
	offered  string
	auto     bool
	areaSets []string
}

func (f *frFake) get(read func()) { f.mu.Lock(); read(); f.mu.Unlock() }

// stubFirstRun makes first run still to do on the test's config, and puts frFake in place of what first
// run reads and does (feature/firstrun), until the test ends.
func stubFirstRun(t *testing.T, joined, home bool) (*frFake, wifi.Status) {
	t.Helper()
	// A screen an earlier call drew in this same test may still be reading the hooks replaced below.
	frBackground.Wait()
	if err := config.Set().FirstRun().Settle(false); err != nil {
		t.Fatal(err)
	}
	firstrun.ResetForTests()
	f := &frFake{home: home}
	st := wifi.Status{}
	if joined {
		st = wifi.Status{Connected: true, SSID: "Example Network", Address: "192.168.1.20"}
	}
	w, h, k, c := firstrun.WifiStatus, firstrun.HomeConnected, firstrun.Key, firstrun.Chime
	au, ao, sa, rn := firstrun.AdoptUntil, firstrun.OpenAdoption, firstrun.SetArea, firstrun.Rename
	uc, of, ai, sai := firstrun.CheckUpdates, firstrun.Offered, firstrun.AutoInstall, firstrun.SetAutoInstall
	t.Cleanup(func() {
		frBackground.Wait() // what the screen started reads these
		firstrun.ResetForTests()
		firstrun.WifiStatus, firstrun.HomeConnected, firstrun.Key, firstrun.Chime = w, h, k, c
		firstrun.AdoptUntil, firstrun.OpenAdoption, firstrun.SetArea, firstrun.Rename = au, ao, sa, rn
		firstrun.CheckUpdates, firstrun.Offered, firstrun.AutoInstall, firstrun.SetAutoInstall = uc, of, ai, sai
		firstrun.ResetForTests()
		_ = config.Set().FirstRun().Finish() // the next test's config may be this one
	})
	firstrun.WifiStatus = func() wifi.Status { return st }
	firstrun.HomeConnected = func() bool { f.mu.Lock(); defer f.mu.Unlock(); return f.home }
	firstrun.Key = func() string { return "Zm9yIHRoZSBmaXJzdCBydW4gdGVzdHMgb25seSEhIQ==" }
	firstrun.Chime = func() { f.mu.Lock(); f.chimes++; f.mu.Unlock() }
	firstrun.AdoptUntil = func() time.Time { f.mu.Lock(); defer f.mu.Unlock(); return f.adopt }
	firstrun.OpenAdoption = func() (time.Time, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.opened++
		f.adopt = time.Now().Add(15 * time.Minute)
		return f.adopt, nil
	}
	firstrun.SetArea = func(room string) error {
		f.mu.Lock()
		f.areaSets = append(f.areaSets, room)
		f.mu.Unlock()
		return config.Set().Home().Area(room)
	}
	firstrun.Rename = func(to string) string { f.mu.Lock(); f.renamed = to; f.mu.Unlock(); return "" }
	firstrun.CheckUpdates = func() { f.mu.Lock(); f.checks++; f.mu.Unlock() }
	firstrun.Offered = func() string { f.mu.Lock(); defer f.mu.Unlock(); return f.offered }
	firstrun.AutoInstall = func() bool { f.mu.Lock(); defer f.mu.Unlock(); return f.auto }
	firstrun.SetAutoInstall = func(on bool) { f.mu.Lock(); f.auto = on; f.mu.Unlock() }
	return f, st
}
