package setup

import (
	"encoding/json"
	"fmt"
	"html"
	"net/http"

	"github.com/HuskerMinion/techo5/echod/internal/feature/plexcast"
)

// The Plex account card (feature/plexcast): link it with the four characters plex.tv hands out, and
// unlink it. The characters are shown as they are and at a size that can be read off a phone, which is
// what the card is for -- the linking itself happens at plex.tv/link, on the phone or the computer the
// page is open on, so the plane has a link on it that opens in a new tab.
//
// A linked device is a player: the phone's Plex app and Plexamp list it, and what they send plays on
// it. Only a device with a screen is one; the Dot's page never draws this card.

// plexStatus answers the card's poll, so the card says the account is linked the moment it is, without
// the owner having to reload. A let-in browser only, as every other read of this page is.
func init() { routes["/setup/plex"] = (*Feature).plexStatus }

func (f *Feature) plexStatus(w http.ResponseWriter, r *http.Request) {
	if _, in := f.session(r); !in {
		http.Error(w, "not let in", http.StatusForbidden)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "read the status with GET", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_ = json.NewEncoder(w).Encode(plexcast.Current())
}

// plexSection is the card: where the account stands, and the button that changes it.
func plexSection(w http.ResponseWriter, token string) {
	st := plexcast.Current()
	sectionOpen(w, "plex", "Plex", plexSummary(st)...)
	fmt.Fprint(w, `<form method="post" action="/setup/save">`)
	hidden(w, token, "plex")
	switch {
	case st.Linked:
		who := st.User
		if who == "" {
			who = "your account"
		}
		fmt.Fprintf(w, `<p>Linked as <strong>%s</strong>. The Plex apps on your network list this device as a player,\n\t\t and so do the ones signed in to that account from anywhere else.</p>
\t\t <p><button type="submit" name="unlink" value="yes" class="danger">Unlink Account</button></p>
\t\t <p class="note">Unlinking stops the device being a player at once. What has been watched stays with the account.</p>`, html.EscapeString(who))
	case st.Code != "":
		fmt.Fprintf(w, `<p>Go to <a href="https://plex.tv/link" target="_blank" rel="noreferrer">plex.tv/link</a>\n\t\t and enter this code:</p>
\t\t <p class="code" style="font-size:2.2rem;letter-spacing:.18em;font-variant-numeric:tabular-nums;margin:.4rem 0 1rem"><strong>%s</strong></p>
\t\t <p class="note" id="plexwait">Waiting for link... The code is good for five minutes.</p>
\t\t <p><button type="submit" name="cancel" value="yes" class="quiet">Cancel</button></p>`, html.EscapeString(st.Code))
	default:
		if st.Err != "" {
			fmt.Fprintf(w, `<p class="bad" role="alert">%s</p>`, html.EscapeString(st.Err))
		}
		fmt.Fprint(w, `<p>Link a Plex account and this device shows up as a player in the Plex apps, ready to cast\n\t\t music and films to. It plays what the app sends with its own speaker and screen: music as a track on\n\t\t Now Playing, films full screen, asked for at a size this hardware decodes.</p>
\t\t <p class="note">Your password is never typed into this device: it asks plex.tv for a code, and you enter the\n\t\t code at plex.tv/link. The device keeps only the token plex.tv gives it, and forgetting the account is one\n\t\t button here or on the device's own screen.</p>
\t\t <p><button type="submit" name="link" value="yes">Link Account</button></p>`)
	}
	fmt.Fprint(w, `</form>`)
	if !st.Linked && st.Code != "" {
		fmt.Fprint(w, `<script>
 (function(){
  var t=setInterval(function(){
   fetch('/setup/plex',{headers:{'Accept':'application/json'}}).then(function(r){return r.ok?r.json():null})
    .then(function(s){if(s&&(s.linked||!s.code)){clearInterval(t);location.reload();}}).catch(function(){});
  },3000);
 })();
</script>`)
	}
	sectionClose(w)
}

// plexSummary is the line on the shut card.
func plexSummary(st plexcast.Status) []string {
	switch {
	case st.Linked && st.User != "":
		return []string{"Linked as " + st.User}
	case st.Linked:
		return []string{"Linked"}
	case st.Code != "":
		return []string{"Waiting for link, code " + st.Code}
	case st.Err != "":
		return []string{"Not linked · " + st.Err}
	}
	return []string{"Not linked"}
}

// savePlex is the card's three buttons: link, cancel a link being made, and unlink.
func savePlex(r *http.Request) string {
	switch {
	case r.PostFormValue("unlink") != "":
		if err := plexcast.UnlinkAccount(); err != nil {
			return "the account could not be forgotten: " + err.Error()
		}
	case r.PostFormValue("cancel") != "":
		plexcast.CancelPinLink()
	case r.PostFormValue("link") != "":
		if _, err := plexcast.StartPinLink(); err != nil {
			return "plex.tv could not be reached; try again in a moment"
		}
	}
	return ""
}
