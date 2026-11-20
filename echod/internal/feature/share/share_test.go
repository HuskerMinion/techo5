package share

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/announce"
)

// rig is this device as "Kitchen", three settings of its own, and what it sends, kept.
type rig struct {
	f       *Feature
	bass    *esphome.Number
	theme   *esphome.Select
	shuffle *esphome.Switch

	mu         sync.Mutex
	sent       map[string][]message
	linkAnswer string // what a device answers a link message
	at         time.Time
}

func (r *rig) tick(d time.Duration) {
	r.mu.Lock()
	r.at = r.at.Add(d)
	r.mu.Unlock()
}

func newRig(t *testing.T) *rig {
	t.Helper()
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if err := config.Set().Share().On(true); err != nil {
		t.Fatal(err)
	}
	r := &rig{sent: map[string][]message{}, linkAnswer: Asked}
	r.bass = &esphome.Number{Base: esphome.Base{ObjectID: "bass"}, Min: -6, Max: 6, Step: 1}
	r.bass.OnCommand = func(v float32) { r.bass.Set(v) }
	r.theme = &esphome.Select{Base: esphome.Base{ObjectID: "screen_theme"}, Options: []string{"Dark", "Light"}}
	r.theme.OnCommand = func(v string) { r.theme.Set(v) }
	r.theme.Set("Dark")
	r.shuffle = &esphome.Switch{Base: esphome.Base{ObjectID: "slideshow_shuffle"}}
	r.shuffle.OnCommand = func(v bool) { r.shuffle.Set(v) }
	own := map[string]esphome.Entity{"bass": r.bass, "screen_theme": r.theme, "slideshow_shuffle": r.shuffle}
	was := entities
	entities = func(id string) (esphome.Entity, bool) { e, ok := own[id]; return e, ok }
	t.Cleanup(func() { entities = was })

	r.at = time.Unix(1_000_000, 0)
	r.f = &Feature{
		me:   func() string { return "Kitchen" },
		now:  func() time.Time { r.mu.Lock(); defer r.mu.Unlock(); return r.at },
		wake: make(chan struct{}, 1),
		last: map[string]string{},
	}
	r.f.send = func(_ context.Context, to string, body []byte) ([]byte, error) {
		var m message
		if err := json.Unmarshal(body, &m); err != nil {
			t.Errorf("sent something that is not a message: %v", err)
		}
		r.mu.Lock()
		r.sent[to] = append(r.sent[to], m)
		answer := r.linkAnswer
		r.mu.Unlock()
		results := map[string]string{}
		for _, it := range m.Items {
			results[it.ID] = Applied
		}
		if m.Kind == "link" {
			results[m.ID] = answer
		}
		return json.Marshal(reply{Results: results})
	}
	return r
}

// link links a setting here with devices, settled both ways, as though accepted long ago.
func link(t *testing.T, id string, devices ...string) {
	t.Helper()
	if err := config.Set().Share().Update(func(s *config.Share) {
		for _, d := range devices {
			config.AddLink(s, id, d)
		}
	}); err != nil {
		t.Fatal(err)
	}
}

func (r *rig) receive(t *testing.T, from string, m message) map[string]string {
	t.Helper()
	body, _ := json.Marshal(m)
	raw, err := r.f.receive(from, body)
	if err != nil {
		t.Fatal(err)
	}
	var rep reply
	if err := json.Unmarshal(raw, &rep); err != nil {
		t.Fatal(err)
	}
	return rep.Results
}

func set(id, value string, n uint64, origin string) message {
	return message{Kind: "set", Items: []item{{ID: id, Value: value, N: n, Origin: origin}}}
}

func (r *rig) sentTo(name string) []message {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]message(nil), r.sent[name]...)
}

func TestALinkedChangeIsTakenOnlyFromALinkedDevice(t *testing.T) {
	r := newRig(t)
	link(t, "bass", "Office")
	if got := r.receive(t, "Office", set("bass", "3", 1, "Office")); got["bass"] != Applied || r.bass.Get() != 3 {
		t.Fatalf("from a linked device: %v, bass %v", got, r.bass.Get())
	}
	if from := config.Get().Share.From["bass"]; from.Device != "Office" || from.N != 1 {
		t.Errorf("where it came from: %+v", from)
	}
	if got := r.receive(t, "Bedroom", set("bass", "5", 9, "Bedroom")); got["bass"] != NotLinked || r.bass.Get() != 3 {
		t.Fatalf("from a device it is not linked with: %v, bass %v", got, r.bass.Get())
	}
}

// A device asked here and not yet accepted there is not taken from either.
func TestAWaitingLinkTakesNothing(t *testing.T) {
	r := newRig(t)
	if _, err := r.f.Link(context.Background(), "bass", []string{"Office"}); err != nil {
		t.Fatal(err)
	}
	if !config.Get().Share.WaitingFor("bass", "Office") {
		t.Fatal("not waiting on the device asked")
	}
	if got := r.receive(t, "Office", set("bass", "3", 1, "Office")); got["bass"] != NotLinked {
		t.Fatalf("%v", got)
	}
}

func TestTheOrderOfChanges(t *testing.T) {
	r := newRig(t)
	link(t, "bass", "Office")
	r.receive(t, "Office", set("bass", "3", 5, "Office"))
	for _, c := range []struct {
		value  string
		n      uint64
		origin string
		want   string
	}{
		{"1", 4, "Office", Older},                  // earlier in the order
		{"1", 5, "Attic", Older},                   // the same place, and a name that comes first
		{"2", 5, "Pantry", Applied},                // the same place, and a name that comes after
		{"4", 5 + maxJump + 1, "Office", BadValue}, // a number from nowhere
	} {
		if got := r.receive(t, "Office", set("bass", c.value, c.n, c.origin)); got["bass"] != c.want {
			t.Errorf("n %d from %s: %q, want %q", c.n, c.origin, got["bass"], c.want)
		}
	}
	if r.bass.Get() != 2 {
		t.Errorf("bass %v", r.bass.Get())
	}
}

func TestAKeptSettingIsNotChanged(t *testing.T) {
	r := newRig(t)
	link(t, "bass", "Office")
	_ = config.Set().Share().Keep("bass", true)
	for _, kind := range []string{"set", "copy"} {
		m := set("bass", "3", 1, "Office")
		m.Kind = kind
		if got := r.receive(t, "Office", m); got["bass"] != Kept || r.bass.Get() != 0 {
			t.Fatalf("a %s: %v, bass %v", kind, got, r.bass.Get())
		}
	}
}

// A copy needs no link, comes in as the latest change, and is passed on to the setting's links here.
func TestACopy(t *testing.T) {
	r := newRig(t)
	link(t, "screen_theme", "Bedroom")
	_ = config.Set().Share().Update(func(s *config.Share) {
		s.From = map[string]config.ShareFrom{"screen_theme": {Device: "Bedroom", N: 40}}
	})
	got := r.receive(t, "Office", message{Kind: "copy", Items: []item{
		{ID: "screen_theme", Value: "Light", N: 2, Origin: "Office"},
		{ID: "slideshow_shuffle", Value: "on", N: 0, Origin: "Office"},
	}})
	if got["screen_theme"] != Applied || r.theme.Get() != "Light" || got["slideshow_shuffle"] != Applied || !r.shuffle.Get() {
		t.Fatalf("%v theme %q shuffle %v", got, r.theme.Get(), r.shuffle.Get())
	}
	if from := config.Get().Share.From["screen_theme"]; from.N != 41 || from.Device != "Kitchen" || from.Via != "Office" {
		t.Errorf("a copy is not the latest change, numbered here: %+v", from)
	}
	waitFor(t, func() bool { return len(r.sentTo("Bedroom")) == 1 })
	if m := r.sentTo("Bedroom")[0]; m.Kind != "set" || m.Items[0].Value != "Light" || m.Items[0].N != 41 {
		t.Errorf("passed on as %+v", m)
	}
}

// A copy, then a link and a change on the device that copied: the two settle on one value. The copy was
// put down as the sender's, under a number the sender never had; its next change took the same number,
// and each device refused the other's as older, linked and apart for good.
func TestACopyThenALinkSettles(t *testing.T) {
	r := newRig(t)
	r.receive(t, "Office", message{Kind: "copy", Items: []item{{ID: "bass", Value: "3", N: 0, Origin: "Office"}}})
	link(t, "bass", "Office")
	// Office changes bass by itself, numbered from what it has: nothing, so 1.
	// Before, the copy was put down here as Office's number 1, and this was refused as older with
	// Office's own answer refused the same way. Now the two number 1s are by different devices, and the
	// names settle it the same way on both.
	if got := r.receive(t, "Office", set("bass", "-2", 1, "Office")); got["bass"] != Applied || r.bass.Get() != -2 {
		t.Fatalf("%v, bass %v", got, r.bass.Get())
	}
	if here := config.Get().Share.From["bass"]; here.N != 1 || here.Device != "Office" {
		t.Errorf("%+v", here)
	}
}

// A change taken from one link is passed on to the others, and not back.
func TestAChangeIsPassedOn(t *testing.T) {
	r := newRig(t)
	link(t, "bass", "Office", "Bedroom")
	r.receive(t, "Office", set("bass", "3", 7, "Office"))
	waitFor(t, func() bool { return len(r.sentTo("Bedroom")) == 1 })
	m := r.sentTo("Bedroom")[0]
	if it := m.Items[0]; it.N != 7 || it.Origin != "Office" || it.Value != "3" {
		t.Fatalf("passed on as %+v", it)
	}
	time.Sleep(50 * time.Millisecond)
	if len(r.sentTo("Office")) != 0 {
		t.Error("sent back to where it came from")
	}
}

func TestWhatIsNeverTaken(t *testing.T) {
	r := newRig(t)
	got := r.receive(t, "Office", message{Kind: "copy", Items: []item{
		{ID: "sendspin", Value: "on"},          // opens a port: never on the list
		{ID: "ring_volume", Value: "0"},        // how loud an alarm rings: never on the list
		{ID: "screen_theme", Value: "Plaid"},   // no such option
		{ID: "bass", Value: "99"},              // past its range
		{ID: "screen_clock_style", Value: "x"}, // on the list, not on this device
	}})
	want := map[string]string{"sendspin": NotShared, "ring_volume": NotShared, "screen_theme": BadValue,
		"bass": BadValue, "screen_clock_style": NotHere}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("%s: %q, want %q", id, got[id], w)
		}
	}
	if r.theme.Get() != "Dark" || r.bass.Get() != 0 {
		t.Error("a value refused was applied")
	}
	if _, err := r.f.receive("Kitchen", []byte(`{"kind":"copy"}`)); err == nil {
		t.Error("a message in this device's own name was taken")
	}
	if _, err := r.f.receive("Office", []byte(`{"kind":"reboot"}`)); err == nil {
		t.Error("an unknown kind was taken")
	}
}

func TestNothingIsTakenWithSharingOff(t *testing.T) {
	r := newRig(t)
	_ = config.Set().Share().On(false)
	got := r.receive(t, "Office", message{Kind: "copy", Items: []item{{ID: "bass", Value: "3"}}})
	if got["bass"] != NotTaking || r.bass.Get() != 0 {
		t.Fatalf("%v bass %v", got, r.bass.Get())
	}
	if got := r.receive(t, "Office", message{Kind: "link", Link: "request", ID: "bass"}); got["bass"] != NotTaking ||
		len(config.Get().Share.Requests) != 0 {
		t.Fatalf("a request with sharing off: %v", got)
	}
}

// A change made here goes to the devices linked, numbered one past the last, and one taken from another
// device is not sent back.
func TestAChangeHereGoesToTheLinksAndIsNotEchoed(t *testing.T) {
	r := newRig(t)
	link(t, "bass", "Office", "Bedroom")
	ctx := context.Background()
	r.f.watch(ctx) // learns the value
	r.bass.OnCommand(2)
	r.f.watch(ctx)
	r.f.watch(ctx)
	waitFor(t, func() bool { return len(r.sentTo("Office")) == 1 && len(r.sentTo("Bedroom")) == 1 })
	it := r.sentTo("Office")[0].Items[0]
	if it.ID != "bass" || it.Value != "2" || it.N != 1 || it.Origin != "Kitchen" {
		t.Fatalf("sent %+v", it)
	}

	r.receive(t, "Office", set("bass", "4", 2, "Office"))
	waitFor(t, func() bool { return len(r.sentTo("Bedroom")) == 2 }) // passed on to Bedroom
	r.f.watch(ctx)
	time.Sleep(50 * time.Millisecond)
	if n := len(r.sentTo("Office")); n != 1 {
		t.Fatalf("a change taken from Office was sent back to it: %d messages", n)
	}
}

// Another device can ask, accept what was asked, or take itself out: never link this one by itself.
func TestLinksNeedConsent(t *testing.T) {
	r := newRig(t)
	if got := r.receive(t, "Office", message{Kind: "link", Link: "request", ID: "bass"}); got["bass"] != Asked {
		t.Fatalf("a request: %v", got)
	}
	s := config.Get().Share
	if s.LinkedWith("bass", "Office") || len(s.Requests) != 1 || s.Requests[0].Device != "Office" {
		t.Fatalf("a request linked by itself, or was not kept: %+v", s)
	}
	if got := r.receive(t, "Office", message{Kind: "link", Link: "accept", ID: "screen_theme"}); got["screen_theme"] != NotLinked ||
		config.Get().Share.LinkedWith("screen_theme", "Office") {
		t.Fatalf("an accept nobody asked for linked: %v", got)
	}
	if got := r.receive(t, "Office", message{Kind: "link", Link: "request", ID: "sendspin"}); got["sendspin"] != NotShared {
		t.Fatalf("%v", got)
	}

	// Accepted here: linked, and Office is told.
	r.linkAnswer = Linked
	if out, err := r.f.Accept(context.Background(), "bass", "Office"); err != nil || out != Linked {
		t.Fatalf("%q %v", out, err)
	}
	s = config.Get().Share
	if !s.LinkedWith("bass", "Office") || len(s.Requests) != 0 {
		t.Fatalf("accepting: %+v", s)
	}
	if m := r.sentTo("Office"); len(m) != 1 || m[0].Link != "accept" {
		t.Fatalf("Office was told %+v", m)
	}

	// Office takes itself out.
	r.receive(t, "Office", message{Kind: "link", Link: "remove", ID: "bass"})
	if config.Get().Share.LinkedWith("bass", "Office") {
		t.Fatal("still linked")
	}
}

// Asked here and accepted there: settled, and this device's value goes over.
func TestAnAcceptedRequest(t *testing.T) {
	r := newRig(t)
	r.theme.Set("Light")
	if _, err := r.f.Link(context.Background(), "screen_theme", []string{"Office"}); err != nil {
		t.Fatal(err)
	}
	if m := r.sentTo("Office"); len(m) != 1 || m[0].Link != "request" {
		t.Fatalf("asked %+v", m)
	}
	if got := r.receive(t, "Office", message{Kind: "link", Link: "accept", ID: "screen_theme"}); got["screen_theme"] != Linked {
		t.Fatalf("%v", got)
	}
	if config.Get().Share.WaitingFor("screen_theme", "Office") {
		t.Fatal("still waiting")
	}
	waitFor(t, func() bool { return len(r.sentTo("Office")) == 2 })
	if m := r.sentTo("Office")[1]; m.Kind != "set" || m.Items[0].Value != "Light" {
		t.Fatalf("then sent %+v", m)
	}

	// Taken out here: unlinked, and told.
	if _, err := r.f.Link(context.Background(), "screen_theme", nil); err != nil {
		t.Fatal(err)
	}
	if m := r.sentTo("Office"); m[len(m)-1].Link != "remove" || config.Get().Share.LinkedWith("screen_theme", "Office") {
		t.Fatalf("taking it out: %+v", m[len(m)-1])
	}
}

func TestLinkLimits(t *testing.T) {
	r := newRig(t)
	if _, err := r.f.Link(context.Background(), "sendspin", []string{"Office"}); err == nil {
		t.Error("a setting not on the list was linked")
	}
	var many []string
	for i := range config.MaxLinked + 1 {
		many = append(many, fmt.Sprintf("Room %d", i))
	}
	if _, err := r.f.Link(context.Background(), "bass", many); err == nil {
		t.Error("more devices than a setting may have")
	}
	for i := range config.MaxRequests + 5 {
		r.receive(t, fmt.Sprintf("Room %d", i), message{Kind: "link", Link: "request", ID: "bass"})
	}
	if n := len(config.Get().Share.Requests); n != config.MaxRequests {
		t.Errorf("%d requests kept", n)
	}
}

func TestCopy(t *testing.T) {
	r := newRig(t)
	got := r.f.Copy(context.Background(), []string{"bass", "sendspin", "screen_theme"}, []string{"Office", "Kitchen"})
	if len(got) != 1 || len(r.sentTo("Kitchen")) != 0 {
		t.Fatalf("copied to %v", got)
	}
	m := r.sentTo("Office")[0]
	if m.Kind != "copy" || len(m.Items) != 2 {
		t.Fatalf("sent %+v: sendspin is never sent", m)
	}
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	for range 200 {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("not in time")
}

func (r *rig) receiveReply(t *testing.T, from string, m message) reply {
	t.Helper()
	body, _ := json.Marshal(m)
	raw, err := r.f.receive(from, body)
	if err != nil {
		t.Fatal(err)
	}
	var rep reply
	if err := json.Unmarshal(raw, &rep); err != nil {
		t.Fatal(err)
	}
	return rep
}

// A change refused as older is answered with what this device has.
func TestAnOlderChangeIsAnsweredWithWhatIsHere(t *testing.T) {
	r := newRig(t)
	link(t, "bass", "Office")
	r.receive(t, "Office", set("bass", "5", 40, "Office"))
	rep := r.receiveReply(t, "Office", set("bass", "1", 2, "Office"))
	cur, ok := rep.Current["bass"]
	if rep.Results["bass"] != Older || !ok || cur.Value != "5" || cur.N != 40 || cur.Origin != "Office" {
		t.Fatalf("%+v", rep)
	}
}

// Told it is behind, a device catches up.
func TestASenderCatchesUp(t *testing.T) {
	r := newRig(t)
	link(t, "bass", "Office")
	r.f.send = func(_ context.Context, to string, body []byte) ([]byte, error) {
		return json.Marshal(reply{Results: map[string]string{"bass": Older},
			Current: map[string]item{"bass": {ID: "bass", Value: "4", N: 50, Origin: "Office"}}})
	}
	r.f.deliver(context.Background(), "Office", set("bass", "1", 3, "Kitchen"))
	if from := config.Get().Share.From["bass"]; r.bass.Get() != 4 || from.N != 50 {
		t.Fatalf("bass %v from %+v", r.bass.Get(), from)
	}
}

// Linked anew, the two settle on the later of what they have, whichever asked.
func TestANewLinkSettlesOnTheLater(t *testing.T) {
	r := newRig(t)
	_ = config.Set().Share().Update(func(s *config.Share) {
		s.From = map[string]config.ShareFrom{"screen_theme": {Device: "Kitchen", N: 1}}
	})
	var sets atomic.Int32
	r.f.send = func(_ context.Context, to string, body []byte) ([]byte, error) {
		var m message
		_ = json.Unmarshal(body, &m)
		if m.Kind == "link" {
			return json.Marshal(reply{Results: map[string]string{m.ID: Asked}})
		}
		sets.Add(1)
		return json.Marshal(reply{Results: map[string]string{"screen_theme": Older},
			Current: map[string]item{"screen_theme": {ID: "screen_theme", Value: "Light", N: 40, Origin: "Office"}}})
	}
	if _, err := r.f.Link(context.Background(), "screen_theme", []string{"Office"}); err != nil {
		t.Fatal(err)
	}
	r.receive(t, "Office", message{Kind: "link", Link: "accept", ID: "screen_theme"})
	// The theme is applied before the reply's origin is written down: both are waited for.
	waitFor(t, func() bool { return r.theme.Get() == "Light" && config.Get().Share.From["screen_theme"].N == 40 })
	if from := config.Get().Share.From["screen_theme"]; from.N != 40 || from.Device != "Office" || sets.Load() != 1 {
		t.Fatalf("from %+v, %d sets", from, sets.Load())
	}
}

func TestACopyIsHeldToTheOrderToo(t *testing.T) {
	r := newRig(t)
	_ = config.Set().Share().Update(func(s *config.Share) {
		s.From = map[string]config.ShareFrom{"bass": {Device: "Kitchen", N: 7}}
	})
	copyOf := func(value string, n uint64) string {
		return r.receive(t, "Office", message{Kind: "copy", Items: []item{{ID: "bass", Value: value, N: n, Origin: "Office"}}})["bass"]
	}
	if got := copyOf("2", 7+maxJump+1); got != BadValue {
		t.Errorf("a number from nowhere: %q", got)
	}
	if got := copyOf("2", math.MaxUint64); got != BadValue {
		t.Errorf("the last number there is: %q", got)
	}
	if got := copyOf("0", 3); got != Same || config.Get().Share.From["bass"].N != 7 {
		t.Errorf("a copy of what is here: %q, from %+v", got, config.Get().Share.From["bass"])
	}
	if got := copyOf("2", 3); got != Applied || config.Get().Share.From["bass"].N != 8 {
		t.Errorf("a copy: %q, from %+v", got, config.Get().Share.From["bass"])
	}
	if next(math.MaxUint64) != math.MaxUint64 {
		t.Error("the order wrapped round")
	}
}

func TestNamesAreHeldToALength(t *testing.T) {
	r := newRig(t)
	long := strings.Repeat("x", maxName+1)
	if _, err := r.f.receive(long, []byte(`{"kind":"copy"}`)); err == nil {
		t.Error("a name past the limit was taken")
	}
	if _, err := r.f.receive("Office\x07", []byte(`{"kind":"copy"}`)); err == nil {
		t.Error("a name with a control character was taken")
	}
	body, _ := json.Marshal(message{Kind: "set", Items: []item{{ID: "bass", Value: "1", N: 1, Origin: long}}})
	if _, err := r.f.receive("Office", body); err == nil {
		t.Error("an origin past the limit was taken")
	}
	if !ValidName("Kitchen Show 5") || ValidName("") || ValidName("\xff") {
		t.Error("ValidName")
	}
}

func TestAcceptingPastTheLimit(t *testing.T) {
	r := newRig(t)
	for i := range config.MaxLinked {
		link(t, "bass", fmt.Sprintf("Room %d", i))
	}
	_ = config.Set().Share().Update(func(s *config.Share) { config.AddRequest(s, "bass", "Office", time.Now()) })
	if out, err := r.f.Accept(context.Background(), "bass", "Office"); err == nil || out != Full {
		t.Fatalf("%q %v", out, err)
	}
	if config.Get().Share.LinkedWith("bass", "Office") {
		t.Fatal("linked past the limit")
	}
}

// A device that could not be asked is asked again: on the next save, and later, less and less often; one
// the request reached is not asked again; after a week with no answer, nobody is asked.
func TestALinkNotYetAcceptedIsAskedAgain(t *testing.T) {
	r := newRig(t)
	reach := false
	r.f.send = func(_ context.Context, to string, body []byte) ([]byte, error) {
		var m message
		_ = json.Unmarshal(body, &m)
		r.mu.Lock()
		r.sent[to] = append(r.sent[to], m)
		r.mu.Unlock()
		if !reach {
			return nil, fmt.Errorf("no route to %s", to)
		}
		return json.Marshal(reply{Results: map[string]string{m.ID: Asked}})
	}
	ask := func() int { r.f.askDue(context.Background()); return len(r.sentTo("Office")) }

	out, _ := r.f.Link(context.Background(), "bass", []string{"Office"})
	if out["Office"] != Unreachable || !config.Get().Share.WaitingFor("bass", "Office") {
		t.Fatalf("%v", out)
	}
	if n := ask(); n != 1 {
		t.Fatalf("asked again at once: %d", n)
	}
	r.tick(askFirst)
	if n := ask(); n != 2 {
		t.Fatalf("not asked again after %v: %d", askFirst, n)
	}
	r.tick(askFirst) // the wait doubles
	if n := ask(); n != 2 {
		t.Fatalf("asked again before the wait doubled: %d", n)
	}
	reach = true
	r.tick(askFirst)
	if n := ask(); n != 3 {
		t.Fatalf("not asked when due: %d", n)
	}
	r.tick(askMost)
	if n := ask(); n != 3 {
		t.Fatalf("asked again after it got through: %d", n)
	}
	// A save with the same devices asks again too, and writes nothing.
	_, _ = r.f.Link(context.Background(), "bass", []string{"Office"})
	if n := len(r.sentTo("Office")); n != 4 {
		t.Fatalf("a save: %d", n)
	}

	// A week on, still not accepted: no answer, and nobody is asked.
	r.f.askMu.Lock()
	r.f.asks = nil
	r.f.askMu.Unlock()
	r.tick(giveUpAfter)
	if !NoAnswer(config.Get().Share, "bass", "Office", r.f.now()) {
		t.Fatal("not said to have no answer")
	}
	if n := ask(); n != 4 {
		t.Fatalf("asked after giving up: %d", n)
	}
}

// A declined request is remembered: the same request again is answered as declined and not shown, and
// the device that asked takes the link back. Asking from here undoes it.
func TestADeclineIsRemembered(t *testing.T) {
	r := newRig(t)
	r.receive(t, "Office", message{Kind: "link", Link: "request", ID: "bass"})
	r.f.Decline(context.Background(), "bass", "Office")
	if s := config.Get().Share; len(s.Requests) != 0 || !s.WasDeclined("bass", "Office") {
		t.Fatalf("%+v", s)
	}
	if got := r.receive(t, "Office", message{Kind: "link", Link: "request", ID: "bass"}); got["bass"] != Declined ||
		len(config.Get().Share.Requests) != 0 {
		t.Fatalf("asked again: %v %+v", got, config.Get().Share.Requests)
	}
	r.linkAnswer = Asked
	if _, err := r.f.Link(context.Background(), "bass", []string{"Office"}); err != nil {
		t.Fatal(err)
	}
	if config.Get().Share.WasDeclined("bass", "Office") {
		t.Fatal("asking from here did not undo the decline")
	}

	// Declined there: the link here goes.
	r.linkAnswer = Declined
	if _, err := r.f.Link(context.Background(), "screen_theme", []string{"Bedroom"}); err != nil {
		t.Fatal(err)
	}
	if config.Get().Share.LinkedWith("screen_theme", "Bedroom") {
		t.Fatal("still linked with a device that declined")
	}
}

// What a device is told back is taken only for what was sent to it.
func TestAReplyIsOnlyForWhatWasSent(t *testing.T) {
	r := newRig(t)
	link(t, "bass", "Office")
	link(t, "screen_theme", "Office")
	r.f.send = func(_ context.Context, to string, body []byte) ([]byte, error) {
		return json.Marshal(reply{
			Results: map[string]string{"bass": Older, "screen_theme": Older},
			Current: map[string]item{
				"bass":         {ID: "bass", Value: "6", N: 9, Origin: strings.Repeat("x", maxName+1)},
				"screen_theme": {ID: "screen_theme", Value: "Light", N: 9, Origin: "Office"},
			},
		})
	}
	got := r.f.deliver(context.Background(), "Office", set("bass", "1", 1, "Kitchen"))
	if _, ok := got["screen_theme"]; ok || r.theme.Get() != "Dark" {
		t.Fatalf("an answer for a setting not sent was taken: %v, theme %q", got, r.theme.Get())
	}
	if r.bass.Get() != 0 {
		t.Fatal("caught up with a change from a name past the limit")
	}
}

// Changes made here and changes taken from another device, at once, never let an older one win: each
// change sent from here is numbered past every one before it.
func TestChangesHereAndFromElsewhereAtOnce(t *testing.T) {
	r := newRig(t)
	link(t, "bass", "Office")
	var sentN []uint64
	r.f.send = func(_ context.Context, to string, body []byte) ([]byte, error) {
		var m message
		_ = json.Unmarshal(body, &m)
		r.mu.Lock()
		for _, it := range m.Items {
			sentN = append(sentN, it.N)
		}
		r.mu.Unlock()
		return json.Marshal(reply{Results: map[string]string{"bass": Applied}})
	}
	ctx := context.Background()
	r.f.watch(ctx)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := range 100 {
			r.receive(t, "Office", set("bass", fmt.Sprint(i%5), uint64(2*i+2), "Office"))
		}
	}()
	go func() {
		defer wg.Done()
		for i := range 100 {
			r.bass.OnCommand(float32(-(i % 5)))
			r.f.watch(ctx)
		}
	}()
	wg.Wait()
	time.Sleep(100 * time.Millisecond)
	from := config.Get().Share.From["bass"]
	if from.N < 200 {
		t.Errorf("the last change taken was lost: %+v", from)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	seen := map[uint64]bool{}
	for _, n := range sentN {
		if seen[n] {
			t.Fatalf("two changes sent with the same number %d: %v", n, sentN)
		}
		seen[n] = true
	}
}

// Unticked and ticked again while the device cannot be reached, it is asked again in good time, whatever
// happened to the asking before; and nothing is kept about asking once a link is settled or gone.
func TestAskingStartsOverWithTheLink(t *testing.T) {
	r := newRig(t)
	reach := true
	r.f.send = func(_ context.Context, to string, body []byte) ([]byte, error) {
		var m message
		_ = json.Unmarshal(body, &m)
		r.mu.Lock()
		r.sent[to] = append(r.sent[to], m)
		r.mu.Unlock()
		if !reach {
			return nil, fmt.Errorf("no route to %s", to)
		}
		return json.Marshal(reply{Results: map[string]string{m.ID: Asked}})
	}
	ctx := context.Background()
	_, _ = r.f.Link(ctx, "bass", []string{"Office"}) // delivered
	_, _ = r.f.Link(ctx, "bass", nil)                // taken out
	r.f.askMu.Lock()
	n := len(r.f.asks)
	r.f.askMu.Unlock()
	if n != 0 {
		t.Fatalf("%d asks kept after the link went", n)
	}
	reach = false
	_, _ = r.f.Link(ctx, "bass", []string{"Office"}) // not delivered this time
	sent := len(r.sentTo("Office"))
	r.tick(askFirst)
	r.f.askDue(ctx)
	if got := len(r.sentTo("Office")); got != sent+1 {
		t.Fatalf("not asked again after %v: %d messages, was %d", askFirst, got, sent)
	}

	// Accepted there: nothing kept about asking.
	reach = true
	before := len(r.sentTo("Office"))
	r.receive(t, "Office", message{Kind: "link", Link: "accept", ID: "bass"})
	waitFor(t, func() bool { return len(r.sentTo("Office")) == before+1 }) // the value it sends then
	r.f.askMu.Lock()
	n = len(r.f.asks)
	r.f.askMu.Unlock()
	if n != 0 {
		t.Fatalf("%d asks kept after it was accepted", n)
	}
}

// A device unplugged while a linked setting changed here is brought up to date: the change is sent
// again later, and at once when the device comes back on the network.
func TestADeviceThatWasAwayIsBroughtUpToDate(t *testing.T) {
	r := newRig(t)
	link(t, "screen_theme", "Office")
	var away sync.Mutex
	gone := true
	send := r.f.send
	r.f.send = func(ctx context.Context, to string, body []byte) ([]byte, error) {
		away.Lock()
		g := gone
		away.Unlock()
		if g {
			return nil, fmt.Errorf("%s is not on the network now", to)
		}
		return send(ctx, to, body)
	}
	var peers []string
	r.f.peers = func() []string { away.Lock(); defer away.Unlock(); return append([]string(nil), peers...) }
	r.f.lookForPeers(context.Background()) // nobody there

	r.f.watch(context.Background()) // learns the value
	r.theme.Set("Light")
	r.f.watch(context.Background())
	behind := func() bool {
		r.f.askMu.Lock()
		defer r.f.askMu.Unlock()
		return r.f.behind[config.AskKey("screen_theme", "Office")] != nil
	}
	waitFor(t, behind)

	// Not again before its time; then again, and still not there.
	r.f.catchUp(context.Background(), nil, false)
	r.tick(askFirst)
	r.f.catchUp(context.Background(), nil, false)
	if !behind() {
		t.Fatal("given up on after one more try")
	}

	// Back on the network: sent at once, numbered as the change was.
	away.Lock()
	gone, peers = false, []string{"Office"}
	away.Unlock()
	r.f.lookForPeers(context.Background())
	waitFor(t, func() bool { return len(r.sentTo("Office")) > 0 })
	m := r.sentTo("Office")[0]
	if m.Kind != "set" || len(m.Items) != 1 || m.Items[0].Value != "Light" || m.Items[0].N != 1 || m.Items[0].Origin != "Kitchen" {
		t.Fatalf("%+v", m)
	}
	waitFor(t, func() bool { return !behind() })

	// Still there at the next look: not sent again.
	r.f.lookForPeers(context.Background())
	time.Sleep(50 * time.Millisecond)
	if n := len(r.sentTo("Office")); n != 1 {
		t.Fatalf("sent %d times", n)
	}
}

// A device that comes and goes while it is being brought up to date is not sent everything again on top.
func TestCatchingUpIsOneAtATime(t *testing.T) {
	r := newRig(t)
	link(t, "screen_theme", "Office")
	hold := make(chan struct{})
	var mu sync.Mutex
	sends := 0
	r.f.send = func(ctx context.Context, to string, body []byte) ([]byte, error) {
		mu.Lock()
		sends++
		mu.Unlock()
		<-hold
		return nil, fmt.Errorf("%s is not on the network now", to)
	}
	var peers []string
	r.f.peers = func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), peers...) }
	for range 3 {
		mu.Lock()
		peers = []string{"Office"}
		mu.Unlock()
		r.f.lookForPeers(context.Background())
		mu.Lock()
		peers = nil
		mu.Unlock()
		r.f.lookForPeers(context.Background())
	}
	waitFor(t, func() bool { mu.Lock(); defer mu.Unlock(); return sends == 1 })
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	n := sends
	mu.Unlock()
	close(hold)
	if n != 1 {
		t.Fatalf("sent %d times at once", n)
	}
}

// A get is answered with what this device has for the settings on the list, never one kept its own nor
// one off the list, and nothing at all with sharing off.
func TestAGetIsAnswered(t *testing.T) {
	r := newRig(t)
	r.bass.Set(2)
	_ = config.Set().Share().Update(func(s *config.Share) { s.Keep = append(s.Keep, "slideshow_shuffle") })
	rep := r.receiveReply(t, "Office", message{Kind: "get", Items: []item{{ID: "bass"}, {ID: "screen_theme"},
		{ID: "slideshow_shuffle"}, {ID: "sendspin"}, {ID: "screen_clock_style"}}})
	want := map[string]string{"bass": "2", "screen_theme": "Dark"}
	if len(rep.Values) != len(want) || rep.Values["bass"] != "2" || rep.Values["screen_theme"] != "Dark" {
		t.Errorf("answered %v, want %v", rep.Values, want)
	}
	_ = config.Set().Share().On(false)
	if rep := r.receiveReply(t, "Office", message{Kind: "get", Items: []item{{ID: "bass"}}}); len(rep.Values) != 0 {
		t.Errorf("answered %v with sharing off", rep.Values)
	}
}

// Values asks each device and keeps only what was asked; one that fails is left out.
func TestValues(t *testing.T) {
	r := newRig(t)
	r.f.send = func(_ context.Context, to string, body []byte) ([]byte, error) {
		switch to {
		case "Garage":
			return nil, errors.New("not on the network now")
		case "Den":
			return json.Marshal(reply{Results: map[string]string{"bass": NotTaking}})
		case "Attic":
			return nil, announce.ErrShareNotTaking
		case "Hall":
			return json.Marshal(reply{Results: map[string]string{}})
		}
		return json.Marshal(reply{Values: map[string]string{"bass": "1", "treble": "4", "sendspin": "on"}})
	}
	got := r.f.Values(context.Background(), []string{"bass", "sendspin"}, []string{"Office", "Garage", "Kitchen", "Den", "Attic", "Hall"})
	if len(got) != 4 || len(got["Office"]) != 1 || got["Office"]["bass"] != "1" {
		t.Errorf("%v", got)
	}
	if v, ok := got["Den"]; !ok || v != nil {
		t.Errorf("a device with sharing off is not said to be: %v", got)
	}
	if v, ok := got["Attic"]; !ok || v != nil {
		t.Errorf("a device whose share route is shut (sharing off) is not said to be: %v", got)
	}
	if v, ok := got["Hall"]; !ok || v == nil || len(v) != 0 {
		t.Errorf("a device that keeps every setting its own is not said to have answered: %v", got)
	}
}
