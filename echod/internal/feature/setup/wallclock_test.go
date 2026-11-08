package setup

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

var errFake = errors.New("not saved")

type fakeWallClock struct {
	here     bool
	on       bool
	address  string
	status   string
	setErr   error
	pairErr  error
	setCalls int
	pairs    int
	gotOn    bool
	gotAddr  string
}

func (f *fakeWallClock) Here() bool      { return f.here }
func (f *fakeWallClock) Enabled() bool   { return f.on }
func (f *fakeWallClock) Address() string { return f.address }
func (f *fakeWallClock) Status() string  { return f.status }
func (f *fakeWallClock) Set(on bool, address string) error {
	f.setCalls++
	f.gotOn, f.gotAddr = on, address
	if f.setErr != nil {
		return f.setErr
	}
	f.on, f.address = on, address
	return nil
}
func (f *fakeWallClock) Pair() error {
	f.pairs++
	return f.pairErr
}

func useFakeWallClock(t *testing.T) *fakeWallClock {
	t.Helper()
	f := &fakeWallClock{here: true}
	was := wallClocks
	wallClocks = func() wallClockSetting { return f }
	t.Cleanup(func() { wallClocks = was })
	return f
}

// The section shows the switch, the address and what the clock is doing; a paired clock shows its
// address back.
func TestWallClockSection(t *testing.T) {
	wc := useFakeWallClock(t)
	wc.on, wc.address, wc.status = true, "AA:BB:CC:12:34:56", "Connected"

	rec := httptest.NewRecorder()
	wallClockSection(rec, "tok")
	got := rec.Body.String()
	for _, want := range []string{
		"Echo Wall Clock",
		`name="on" value="yes" style="width:auto" checked`,
		`value="AA:BB:CC:12:34:56"`,
		"Pair the clock",
		"Now: Connected",
		"pulses amber",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the section is missing %q:\n%s", want, got)
		}
	}

	wc.on = false
	rec = httptest.NewRecorder()
	wallClockSection(rec, "tok")
	if strings.Contains(rec.Body.String(), `name="on" value="yes" style="width:auto" checked`) {
		t.Error("the switch shows on while the wall clock is off")
	}

	wc.here = false
	rec = httptest.NewRecorder()
	wallClockSection(rec, "tok")
	if strings.Contains(rec.Body.String(), "Echo Wall Clock") {
		t.Error("the section is shown where Bluetooth is Android's")
	}
}

// Save stores the switch and the address; Pair the clock starts the search only when it is on.
func TestWallClockSave(t *testing.T) {
	wc := useFakeWallClock(t)

	post := func(values url.Values) string {
		r := httptest.NewRequest(http.MethodPost, "/setup/save", strings.NewReader(values.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		return saveWallClock(r)
	}

	if problem := post(url.Values{"on": {"yes"}, "address": {" AA:BB:CC:12:34:56 "}}); problem != "" {
		t.Fatal(problem)
	}
	if !wc.on || wc.gotAddr != "AA:BB:CC:12:34:56" || wc.pairs != 0 {
		t.Fatalf("saved on=%v address=%q, pairs=%d", wc.on, wc.gotAddr, wc.pairs)
	}

	if problem := post(url.Values{"on": {"yes"}, "pair": {"yes"}}); problem != "" || wc.pairs != 1 {
		t.Fatalf("pairing: problem %q, pairs %d", problem, wc.pairs)
	}

	if problem := post(url.Values{"pair": {"yes"}}); problem == "" || wc.pairs != 1 {
		t.Fatalf("pairing while off: problem %q, pairs %d", problem, wc.pairs)
	}
}

// A refused save says why and leaves the setting as it was.
func TestWallClockSaveRefused(t *testing.T) {
	wc := useFakeWallClock(t)
	wc.setErr = errFake
	r := httptest.NewRequest(http.MethodPost, "/setup/save", strings.NewReader(url.Values{"on": {"yes"}}.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if problem := saveWallClock(r); problem != errFake.Error() {
		t.Fatalf("problem = %q, want %q", problem, errFake)
	}
	if wc.pairs != 0 {
		t.Error("pairing started after a failed save")
	}
}
