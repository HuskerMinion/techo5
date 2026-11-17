package setup

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// sendFakes stands in for the sender: what was asked to be saved, and what the Test said.
type sendFakes struct {
	saved   []config.WakeSend
	refuse  error
	pinged  []config.WakeSend
	pingErr error
	kept    bool
}

func fakeSender(t *testing.T) *sendFakes {
	t.Helper()
	fk := &sendFakes{kept: true}
	was := [4]any{setWakeSend, pingTrainer, wakeClipsKept, wakeClipsUnsent}
	setWakeSend = func(_ context.Context, w config.WakeSend) error {
		if fk.refuse != nil {
			return fk.refuse
		}
		fk.saved = append(fk.saved, w)
		return config.Set().SendUpdate(func(c *config.WakeSend) { *c = w })
	}
	pingTrainer = func(_ context.Context, w config.WakeSend) (string, error) {
		fk.pinged = append(fk.pinged, w)
		return "answered", fk.pingErr
	}
	wakeClipsKept = func() bool { return fk.kept }
	wakeClipsUnsent = func() (int, string) { return 7, "token refused" }
	t.Cleanup(func() {
		setWakeSend, pingTrainer = was[0].(func(context.Context, config.WakeSend) error), was[1].(func(context.Context, config.WakeSend) (string, error))
		wakeClipsKept, wakeClipsUnsent = was[2].(func() bool), was[3].(func() (int, string))
	})
	return fk
}

func TestSendingWakeClipsIsOnTheVoiceTabAndOffByDefault(t *testing.T) {
	f, c := in(t)
	fakeSender(t)
	body := get(f, "/setup?tab=voice", c).Body.String()
	for _, want := range []string{`id="sec-wakeclips"`, "Send wake clips to", `name="target"`, `<option value="" selected>Off`,
		"Tater Trainer", "wakesmith", "Custom", `name="kind" value="wake" style="width:auto" checked`, `name="kind" value="noise"`} {
		if !strings.Contains(body, want) {
			t.Errorf("the voice tab has no %q", want)
		}
	}
	if strings.Contains(body, ">Test<") {
		t.Error("a Test button with nowhere to send")
	}
	if got := config.Get().Wake.Send; got.Target != "" {
		t.Errorf("sending is on by default: %+v", got)
	}
}

func TestSavingWhereClipsGo(t *testing.T) {
	f, c := in(t)
	fk := fakeSender(t)
	form := func(v url.Values) url.Values { v.Set("what", "wakeclips"); return v }

	to := post(t, f, c, form(url.Values{"target": {"tater"}, "url": {"http://192.168.1.20:8789"}, "kind": {"wake", "nearmiss"}}))
	if p := to.Query().Get("problem"); p != "" {
		t.Fatalf("refused: %s", p)
	}
	got := config.Get().Wake.Send
	if got.Target != config.SendTater || got.URL != "http://192.168.1.20:8789" || len(got.Skip) != 2 ||
		got.Skip[0] != "mistake" || got.Skip[1] != "noise" {
		t.Fatalf("kept %+v", got)
	}
	if to.Query().Get("tab") != "voice" {
		t.Errorf("came back to %q", to.Query().Get("tab"))
	}

	// A token for wakesmith, then kept when the form is saved again with none typed.
	post(t, f, c, form(url.Values{"target": {"wakesmith"}, "url": {"http://trainer.local"}, "bearer": {"walnut-token"}, "kind": {"wake"}}))
	if config.Get().Wake.Send.Token != "walnut-token" {
		t.Fatalf("the token was not kept: %+v", config.Get().Wake.Send)
	}
	post(t, f, c, form(url.Values{"target": {"wakesmith"}, "url": {"http://trainer.local/"}, "kind": {"wake"}}))
	if config.Get().Wake.Send.Token != "walnut-token" {
		t.Fatal("a saved token was lost by saving the same address again")
	}
	// A kept token goes only to the address it was given for: a new address without one is refused, and
	// says why, rather than dropping the token unseen.
	to = post(t, f, c, form(url.Values{"target": {"wakesmith"}, "url": {"http://other.local"}, "kind": {"wake"}}))
	if p := to.Query().Get("problem"); !strings.Contains(p, "paste the token again") {
		t.Fatalf("a new address without the token: problem %q", p)
	}
	if got := config.Get().Wake.Send; got.URL != "http://trainer.local" || got.Token != "walnut-token" {
		t.Fatalf("a refused save changed what was kept: %+v", got)
	}
	// The same address typed with a final dot or the receiver's endpoint is the same address.
	post(t, f, c, form(url.Values{"target": {"wakesmith"}, "url": {"http://trainer.local./api/clips"}, "kind": {"wake"}}))
	if config.Get().Wake.Send.Token != "walnut-token" {
		t.Fatal("the token was lost for the same address typed another way")
	}
	// Tater asks for no token, so a new address for it just leaves the token behind.
	post(t, f, c, form(url.Values{"target": {"tater"}, "url": {"http://other.local:8789"}, "kind": {"wake"}}))
	if config.Get().Wake.Send.Token != "" {
		t.Fatal("a token was kept for another address")
	}
	post(t, f, c, form(url.Values{"target": {"custom"}, "url": {"http://other.local"}, "bearer": {"second"}, "kind": {"wake"}}))
	post(t, f, c, form(url.Values{"target": {"custom"}, "url": {"http://other.local"}, "forgettoken": {"yes"}, "kind": {"wake"}}))
	if config.Get().Wake.Send.Token != "" {
		t.Fatal("the token was not forgotten")
	}
	// Off keeps the address, so it is not typed again.
	post(t, f, c, form(url.Values{"target": {""}, "url": {"http://other.local"}}))
	if got := config.Get().Wake.Send; got.Target != "" || got.URL != "http://other.local" {
		t.Fatalf("off: %+v", got)
	}
	_ = fk
}

func TestWhereClipsGoIsRefusedWhenItIsNotRight(t *testing.T) {
	f, c := in(t)
	fk := fakeSender(t)
	cases := []struct {
		name string
		v    url.Values
		fake error
		want string
	}{
		{"no address", url.Values{"target": {"tater"}}, nil, "address"},
		{"not a place", url.Values{"target": {"cloud"}, "url": {"http://192.168.1.2"}}, nil, "choose where"},
		{"a token with spaces", url.Values{"target": {"custom"}, "url": {"http://192.168.1.2"}, "bearer": {"a b"}}, nil, "no spaces"},
		{"off the home network", url.Values{"target": {"tater"}, "url": {"http://8.8.8.8"}}, errors.New("that address is not on the home network"), "home network"},
	}
	for _, tc := range cases {
		fk.refuse = tc.fake
		tc.v.Set("what", "wakeclips")
		to := post(t, f, c, tc.v)
		if p := to.Query().Get("problem"); !strings.Contains(p, tc.want) {
			t.Errorf("%s: %q", tc.name, p)
		}
		if got := config.Get().Wake.Send; got.Target != "" {
			t.Errorf("%s: was kept anyway: %+v", tc.name, got)
		}
	}
}

func TestTheTestButtonAsksTheSavedAddress(t *testing.T) {
	f, c := in(t)
	fk := fakeSender(t)
	to := post(t, f, c, url.Values{"what": {"wakeclips-test"}})
	if !strings.Contains(to.Query().Get("problem"), "save it first") {
		t.Fatalf("testing nothing: %v", to.Query())
	}
	post(t, f, c, url.Values{"what": {"wakeclips"}, "target": {"wakesmith"}, "url": {"http://192.168.1.20:9000"}, "bearer": {"walnut"}, "kind": {"wake"}})
	body := get(f, "/setup?tab=voice", c).Body.String()
	if !strings.Contains(body, ">Test<") || !strings.Contains(body, "7 clips waiting") || !strings.Contains(body, "token refused") {
		t.Errorf("the section with a place chosen: %s", first(body))
	}
	to = post(t, f, c, url.Values{"what": {"wakeclips-test"}})
	if to.Query().Get("problem") != "" || to.Query().Get("saved") != "reached" {
		t.Fatalf("a trainer that answered: %v", to.Query())
	}
	if len(fk.pinged) != 1 || fk.pinged[0].Token != "walnut" || fk.pinged[0].URL != "http://192.168.1.20:9000" {
		t.Fatalf("asked %+v", fk.pinged)
	}
	if body := get(f, "/setup?tab=voice&saved=reached", c).Body.String(); !strings.Contains(body, "The trainer answered.") {
		t.Error("the page does not say the trainer answered")
	}
	fk.pingErr = errors.New("could not reach it: connection refused")
	to = post(t, f, c, url.Values{"what": {"wakeclips-test"}})
	if !strings.Contains(to.Query().Get("problem"), "could not reach it") {
		t.Fatalf("a trainer that did not: %v", to.Query())
	}
	fk.kept = false
	if body := get(f, "/setup?tab=voice", c).Body.String(); !strings.Contains(body, "Keep wake clips is off") {
		t.Error("the section does not say nothing is sent while Keep wake clips is off")
	}
}

func TestACertificateTheTestRefusedCanBeTrustedFromThePage(t *testing.T) {
	f, c := in(t)
	fakeSender(t)
	seen, trusted := "", ""
	wasSeen, wasTrust := wakeCertSeen, trustCert
	wakeCertSeen = func() string { return seen }
	trustCert = func(_ context.Context, fp string) error { trusted = fp; return nil }
	t.Cleanup(func() { wakeCertSeen, trustCert = wasSeen, wasTrust })

	post(t, f, c, url.Values{"what": {"wakeclips"}, "target": {"wakesmith"}, "url": {"https://192.168.1.20:8765"}, "kind": {"wake"}})
	if body := get(f, "/setup?tab=voice", c).Body.String(); strings.Contains(body, "Trust this certificate") {
		t.Fatal("a trust button with no certificate refused")
	}
	seen = "ab12cd"
	body := get(f, "/setup?tab=voice", c).Body.String()
	if !strings.Contains(body, "Trust this certificate") || !strings.Contains(body, "ab12cd") {
		t.Fatalf("no button for the refused certificate: %s", first(body))
	}
	to := post(t, f, c, url.Values{"what": {"wakeclips-trust"}, "fingerprint": {"ab12cd"}})
	if trusted != "ab12cd" || to.Query().Get("problem") != "" || to.Query().Get("saved") != "trusted" {
		t.Fatalf("trusted %q: %v", trusted, to.Query())
	}
}

func TestKeepWakeClipsIsOnThePageAndOffNeedsTheBoxTicked(t *testing.T) {
	f, c := in(t)
	fk := fakeSender(t)
	fk.kept = false
	var calls []bool
	was := setKeepClips
	setKeepClips = func(on bool) error { calls = append(calls, on); fk.kept = on; return nil }
	t.Cleanup(func() { setKeepClips = was })

	body := get(f, "/setup?tab=voice", c).Body.String()
	for _, want := range []string{`name="keep"`, "Delete the wake clips on this device", "deletes every kept clip", "Tailscale network"} {
		if !strings.Contains(body, want) {
			t.Errorf("the section has no %q", want)
		}
	}
	if strings.Contains(body, "in Home Assistant, under this device") {
		t.Error("the section still sends people to Home Assistant for the switch")
	}

	if p := post(t, f, c, url.Values{"what": {"wakeclips-keep"}, "keep": {"on"}}).Query().Get("problem"); p != "" || !fk.kept {
		t.Fatalf("on: %q kept=%v", p, fk.kept)
	}
	// Off without the box: refused, and nothing changes.
	p := post(t, f, c, url.Values{"what": {"wakeclips-keep"}, "keep": {"off"}}).Query().Get("problem")
	if !strings.Contains(p, "tick Delete the wake clips") || !fk.kept || len(calls) != 1 {
		t.Fatalf("off without the box: %q kept=%v calls=%v", p, fk.kept, calls)
	}
	// Off with it: done.
	if p := post(t, f, c, url.Values{"what": {"wakeclips-keep"}, "keep": {"off"}, "confirm": {"yes"}}).Query().Get("problem"); p != "" || fk.kept || len(calls) != 2 || calls[1] {
		t.Fatalf("off with the box: %q kept=%v calls=%v", p, fk.kept, calls)
	}
	// Off when it is off already needs nothing.
	if p := post(t, f, c, url.Values{"what": {"wakeclips-keep"}, "keep": {"off"}}).Query().Get("problem"); p != "" || len(calls) != 2 {
		t.Fatalf("off again: %q %v", p, calls)
	}
}

func TestTheAddressIsComparedAsItWillBeKept(t *testing.T) {
	f, c := in(t)
	fakeSender(t)
	save := func(u, tok string) {
		v := url.Values{"what": {"wakeclips"}, "target": {"wakesmith"}, "url": {u}, "kind": {"wake"}}
		if tok != "" {
			v.Set("bearer", tok)
		}
		post(t, f, c, v)
	}
	save("http://192.168.1.20:8765/", "walnut")
	save(" http://192.168.1.20:8765 ", "")
	if got := config.Get().Wake.Send; got.Token != "walnut" || got.URL != "http://192.168.1.20:8765" {
		t.Fatalf("%+v", got)
	}
}
