package detect

import (
	"context"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

func setupSend(t *testing.T) *Detect {
	t.Helper()
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	d := &Detect{sender: newSender(newClips(t.TempDir())), send: sendEntities{to: &esphome.Select{}}}
	d.sender.resolve = func(_ context.Context, host string) ([]net.IP, error) {
		if strings.HasSuffix(host, ".example") {
			return []net.IP{net.ParseIP("93.184.216.34")}, nil
		}
		return []net.IP{net.ParseIP("192.168.1.9")}, nil
	}
	return d
}

func TestOnlyClipsKeptAfterTheTargetIsSetAreCountedFrom(t *testing.T) {
	d := setupSend(t)
	ctx := context.Background()
	before := time.Now().UTC().Add(-2 * time.Second)
	if err := d.SetSend(ctx, config.WakeSend{Target: config.SendTater, URL: "http://trainer.local:8789/", Token: "x"}); err != nil {
		t.Fatal(err)
	}
	got := config.Get().Wake.Send
	since, err := time.Parse(time.RFC3339, got.Since)
	if err != nil || since.Before(before) || since.After(time.Now().Add(time.Second)) {
		t.Fatalf("since %q: %v", got.Since, err)
	}
	if got.URL != "http://trainer.local:8789" || got.Token != "x" {
		t.Fatalf("kept %+v: a token stays for the other targets, and the address has no trailing slash", got)
	}

	// Saved again for the same target: the moment stays, so clips kept since are still sent.
	const was = "2001-01-01T00:00:00Z"
	config.Set().SendUpdate(func(w *config.WakeSend) { w.Since = was })
	if err := d.SetSend(ctx, config.WakeSend{Target: config.SendTater, URL: "http://trainer.local:8789", Skip: []string{"noise"}}); err != nil {
		t.Fatal(err)
	}
	if got := config.Get().Wake.Send; got.Since != was || len(got.Skip) != 1 {
		t.Fatalf("saving the same target again moved the moment: %+v", got)
	}

	// Another target starts over; off clears it.
	if err := d.SetSend(ctx, config.WakeSend{Target: config.SendCustom, URL: "http://trainer.local:8789"}); err != nil {
		t.Fatal(err)
	}
	if config.Get().Wake.Send.Since == was {
		t.Fatal("a new target kept the old moment")
	}
	if err := d.SetSend(ctx, config.WakeSend{URL: "http://trainer.local:8789"}); err != nil {
		t.Fatal(err)
	}
	if got := config.Get().Wake.Send; got.Since != "" || got.Target != "" || got.On() {
		t.Fatalf("off: %+v", got)
	}
	// And on again is a new moment: what was kept while it was off is not sent.
	if err := d.SetSend(ctx, config.WakeSend{Target: config.SendTater, URL: "http://trainer.local:8789"}); err != nil || config.Get().Wake.Send.Since == "" {
		t.Fatalf("on again: %v %+v", err, config.Get().Wake.Send)
	}
}

func TestAnAddressOffTheHomeNetworkIsNotSaved(t *testing.T) {
	d := setupSend(t)
	for _, raw := range []string{"http://trainer.example", "http://8.8.8.8:8789", "https://203.0.113.5", "ftp://192.168.1.2", "trainer.local"} {
		if err := d.SetSend(context.Background(), config.WakeSend{Target: config.SendWakesmith, URL: raw, Token: "t"}); err == nil {
			t.Errorf("%s was saved", raw)
		}
	}
	if got := config.Get().Wake.Send; got.Target != "" || got.URL != "" || got.Token != "" {
		t.Fatalf("something was kept: %+v", got)
	}
	if err := d.SetSend(context.Background(), config.WakeSend{Target: config.SendCustom, URL: "http://192.168.1.2", Skip: []string{"voices"}}); err == nil {
		t.Error("a kind that is not one was saved")
	}
}

func TestTheSelectNeedsAnAddressAndChangesOnlyTheTarget(t *testing.T) {
	d := setupSend(t)
	ctx := context.Background()
	if err := d.SetSendTarget(ctx, config.SendTater); err == nil {
		t.Fatal("the select chose a place with no address saved")
	}
	if err := d.SetSend(ctx, config.WakeSend{Target: config.SendCustom, URL: "http://192.168.1.2:80", Token: "walnut"}); err != nil {
		t.Fatal(err)
	}
	if err := d.SetSendTarget(ctx, config.SendWakesmith); err != nil {
		t.Fatal(err)
	}
	if got := config.Get().Wake.Send; got.Target != config.SendWakesmith || got.Token != "walnut" || got.URL != "http://192.168.1.2:80" {
		t.Fatalf("%+v", got)
	}
	if err := d.SetSendTarget(ctx, config.SendOff); err != nil || config.Get().Wake.Send.On() {
		t.Fatalf("off: %v", err)
	}
	// An address that now points off the home network cannot be switched to.
	config.Set().SendUpdate(func(w *config.WakeSend) { w.URL = "http://trainer.example" })
	if err := d.SetSendTarget(ctx, config.SendTater); err == nil {
		t.Fatal("switched to an address off the home network")
	}
	if config.Get().Wake.Send.Target != "" {
		t.Fatal("the target changed anyway")
	}
}

func TestEveryTargetHasALabelThatMapsBack(t *testing.T) {
	for _, tg := range config.SendTargets {
		back, ok := config.SendTargetOf(config.SendLabel(tg))
		if !ok || back != tg {
			t.Errorf("%q -> %q -> %q", tg, config.SendLabel(tg), back)
		}
	}
	if _, ok := config.SendTargetOf("Somewhere else"); ok {
		t.Error("an unknown label named a target")
	}
}

// A settings file is made without the token.
func TestTheSettingsFileDoesNotCarryTheToken(t *testing.T) {
	c := config.Config{}
	c.Wake.Send = config.WakeSend{Target: config.SendCustom, URL: "http://192.168.1.2", Token: "walnut-token"}
	config.ClearSecrets(&c)
	if c.Wake.Send.Token != "" || c.Wake.Send.URL == "" {
		t.Fatalf("%+v", c.Wake.Send)
	}
}
