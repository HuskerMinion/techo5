package setup

import (
	"fmt"
	"log/slog"
	"net/http"

	"github.com/HuskerMinion/techo5/echod/internal/feature/security"
)

// lockSection is the settings lock (feature/security lock.go): a PIN the device asks for before its
// settings change. Only on a device with a screen. The PIN is never shown.
func lockSection(w http.ResponseWriter, token string) {
	if !hasScreen {
		return
	}
	sectionOpen(w, "lock", "Settings lock", onOff(security.LockSet()))
	fmt.Fprint(w, `<form method="post" action="/setup/save">`)
	hidden(w, token, "lock")
	state := "Off: anyone can change the settings on the screen."
	if security.LockSet() {
		state = "On: the screen asks for the PIN before most of its settings change."
	}
	// The PIN is a secret as the others are (secret.go): never written into the page, with a Show box for
	// a PIN typed on a phone. It is not secretField's "leave it empty to keep it": an empty PIN is
	// refused, and the state above already says whether one is set.
	fmt.Fprintf(w, `<p class="note" style="margin-top:0">%s Everything else on the device (the clock, music, the voice
	  assistant, calls) works without it, and so do the settings for how it looks, sounds and is used: alarms and
	  timers, the volume, the brightness, the theme and the clock.</p>
	 %s
	 <p><button type="submit" name="do" value="set">Set the PIN</button></p></form>`, state,
		onceField("lockpin", "New PIN, 4 to 8 digits", "pin", `inputmode="numeric" pattern="[0-9]{4,8}" required`))
	if security.LockSet() {
		// Turning it off is a form of its own, behind a box to tick: it was a button beside Set the
		// PIN, and one press there left the settings open to anyone at the screen.
		fmt.Fprint(w, `<form method="post" action="/setup/save">`)
		hidden(w, token, "lock")
		fmt.Fprint(w, `<p><label><input type="checkbox" name="understood" value="yes">
		  I know that with the lock off, anyone can change the settings on the screen</label></p>
		 <p><button type="submit" name="do" value="clear" class="danger">Turn the lock off</button></p></form>`)
	}
	sectionClose(w)
}

func saveLock(r *http.Request) string {
	pin := ""
	if r.PostFormValue("do") == "clear" && r.PostFormValue("understood") != "yes" {
		return "tick the box to say you know anyone can change the settings on the screen with the lock off"
	}
	if r.PostFormValue("do") != "clear" {
		pin = r.PostFormValue("pin")
		if pin == "" {
			return "type a PIN first"
		}
	}
	if err := security.Get().SetPIN(pin); err != nil {
		return err.Error()
	}
	slog.Info("setup page: settings lock", "on", pin != "")
	return ""
}
