package setup

import (
	"fmt"
	"log/slog"
	"net/http"

	"github.com/HuskerMinion/techo5/echod/internal/feature/security"
)

// lockSection is the settings lock (feature/security lock.go): a PIN the device asks for before its
// settings screen opens. Only on a device with a screen. The PIN is never shown.
func lockSection(w http.ResponseWriter, token string) {
	if !hasScreen {
		return
	}
	fmt.Fprint(w, `<fieldset><legend>Settings lock</legend><form method="post" action="/setup/save">`)
	hidden(w, token, "lock", "privacy")
	state := "Off: anyone can open the settings on the screen."
	if security.LockSet() {
		state = "On: the screen asks for the PIN before its settings open."
	}
	fmt.Fprintf(w, `<p class="note" style="margin-top:0">%s Everything else on the device (the clock, music, the voice
	  assistant, calls) works without it.</p>
	 <label for="lockpin">New PIN, 4 to 8 digits</label>
	 <input id="lockpin" name="pin" type="password" inputmode="numeric" pattern="[0-9]{4,8}" autocomplete="new-password">
	 <p><button type="submit" name="do" value="set">Set the PIN</button>`, state)
	if security.LockSet() {
		fmt.Fprint(w, ` <button type="submit" name="do" value="clear">Turn the lock off</button>`)
	}
	fmt.Fprint(w, `</p></form></fieldset>`)
}

func saveLock(r *http.Request) string {
	pin := ""
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
