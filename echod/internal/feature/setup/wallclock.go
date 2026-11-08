package setup

import (
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"strings"

	"github.com/HuskerMinion/techo5/echod/internal/feature/wallclock"
)

func init() { saves["wallclock"] = saveWallClock }

// wallClockSetting is the part of the wall clock the section uses.
type wallClockSetting interface {
	Here() bool
	Enabled() bool
	Address() string
	Status() string
	Set(on bool, address string) error
	Pair() error
}

// wallClocks is wallclock's singleton. A variable for the tests, which do not start Bluetooth.
var wallClocks = func() wallClockSetting { return wallclock.Get() }

// wallClockSection is the Echo Wall Clock: off by default, and paired from here by putting the clock
// in pairing mode and pressing its button. On every device whose Bluetooth is the daemon's; not the
// panel, whose Bluetooth is Android's.
func wallClockSection(w http.ResponseWriter, token string) {
	wc := wallClocks()
	if !wc.Here() {
		return
	}
	fmt.Fprint(w, `<fieldset><legend>Echo Wall Clock</legend><form method="post" action="/setup/save">`)
	hidden(w, token, "wallclock", "connections")
	fmt.Fprintf(w, `
	 <p><label><input type="checkbox" name="on" value="yes" style="width:auto"%s> Show this device's timers
	  on an Echo Wall Clock</label></p>
	 <label for="wcaddr">Clock address</label>
	 <input id="wcaddr" name="address" value="%s" placeholder="found by name when empty" autocomplete="off">
	 <p class="note">The original wall clock, the round one with the LED ring. It must be unpaired from
	  Alexa first: hold its center button until the middle LED pulses amber, then choose <strong>Pair the
	  clock</strong>. It stays paired to one controller, and while it is paired here its ring shows this
	  device's timers. A timer that ends pulses the ring, and one that is canceled or paused puts its
	  marker out. Leave the address empty to find the clock by the name it advertises.</p>`, checkedIf(wc.Enabled()),
		html.EscapeString(wc.Address()))
	if s := wc.Status(); s != "" {
		fmt.Fprintf(w, `<p class="note">Now: %s</p>`, html.EscapeString(s))
	}
	fmt.Fprint(w, `
	 <p><button type="submit">Save</button>
	  <button type="submit" name="pair" value="yes" class="quiet">Pair the clock</button></p>
	 </form></fieldset>`)
}

func saveWallClock(r *http.Request) string {
	wc := wallClocks()
	if !wc.Here() {
		return "this device has no wall clock"
	}
	on := r.PostFormValue("on") == "yes"
	address := strings.TrimSpace(r.PostFormValue("address"))
	if err := wc.Set(on, address); err != nil {
		return err.Error()
	}
	if r.PostFormValue("pair") != "yes" {
		return ""
	}
	if !on {
		return "turn the wall clock on, then pair"
	}
	if err := wc.Pair(); err != nil {
		return err.Error()
	}
	slog.Info("setup page: wall clock pairing started")
	return ""
}
