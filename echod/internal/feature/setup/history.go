package setup

import (
	"fmt"
	"html"
	"net/http"
	"strconv"

	"github.com/HuskerMinion/techo5/echod/internal/feature/activity"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
)

// Recent voice requests: the last ten turns the device took, kept in memory only (feature/activity).
// The words follow Send voice text, as the event and the log do.

func init() { routes["/setup/history/clear"] = (*Feature).historyClear }

// pastTurns is the history; a test has its own.
var pastTurns = activity.History

// clearTurns forgets it; a test has its own.
var clearTurns = activity.ClearHistory

// historyClear is the section's Clear button.
func (f *Feature) historyClear(w http.ResponseWriter, r *http.Request) {
	token, in := f.session(r)
	if !in {
		http.Error(w, "not let in", http.StatusForbidden)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "clear the history with POST", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	if err := r.ParseForm(); err != nil || !sameToken(r.PostFormValue("token"), token) {
		http.Error(w, "that did not come from this page", http.StatusForbidden)
		return
	}
	if !layout.HasMicrophone() {
		http.Error(w, "this device has no voice requests", http.StatusNotFound)
		return
	}
	clearTurns()
	http.Redirect(w, r, back("sound", "", "")+"#history", http.StatusSeeOther)
}

// historySection lists the last voice requests, newest first, on a device with microphones.
func historySection(w http.ResponseWriter, token string) {
	if !layout.HasMicrophone() {
		return
	}
	fmt.Fprint(w, `<fieldset id="history"><legend>Recent voice requests</legend>
	 <p class="note" style="margin-top:0">The last 10 voice requests this device took, newest first: when, which
	  assistant, the wake word's score, what was heard, the reply, how it ended and how long each part took. Kept
	  in memory only: never saved, never in the diagnostics download, and gone when the device restarts. Like
	  Send voice text, the words are left out while that is off.</p>`)
	turns := pastTurns()
	if len(turns) == 0 {
		fmt.Fprint(w, `<p class="note">Nothing yet.</p>`)
	} else {
		fmt.Fprint(w, `<div style="overflow-x:auto"><table class="scores"><thead><tr><th>Time</th><th>Assistant</th>
		 <th>Score</th><th>Heard</th><th>Reply</th><th>Ended</th><th>Took</th></tr></thead><tbody>`)
		for _, t := range turns {
			heard, reply := html.EscapeString(t.Heard), html.EscapeString(t.Reply)
			if t.Withheld {
				heard, reply = `<span class="note">(not kept: Send voice text is off)</span>`, ""
			}
			score := t.Score
			if score == "" {
				score = "-"
			}
			fmt.Fprintf(w, `<tr><td>%s</td><td>%d</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td></tr>`,
				html.EscapeString(t.At.Format("Jan 2 15:04:05")), t.Slot, html.EscapeString(score), heard, reply,
				html.EscapeString(t.End), html.EscapeString(took(t)))
		}
		fmt.Fprint(w, `</tbody></table></div>`)
	}
	// Refresh differs from the page's own address in more than its fragment, so following it reloads.
	fmt.Fprintf(w, `<form method="post" action="/setup/history/clear" class="btns"><input type="hidden" name="token" value="%s">
	 <a href="/setup?tab=sound&amp;history=1#history">Refresh</a>
	 <button type="submit">Clear</button></form></fieldset>`, html.EscapeString(token))
}

// took is the parts of a turn that have a time, in seconds.
func took(t activity.Past) string {
	out := ""
	for _, p := range []struct{ name, ms string }{{"listen", t.ListenMS}, {"think", t.ThinkMS}, {"reply", t.ReplyMS}} {
		ms, err := strconv.Atoi(p.ms)
		if err != nil {
			continue
		}
		if out != "" {
			out += ", "
		}
		out += fmt.Sprintf("%s %.1f seconds", p.name, float64(ms)/1000)
	}
	return out
}
