package setup

import (
	"fmt"
	"html"
	"log/slog"
	"net/http"
)

func init() { routes["/setup/done"] = (*Feature).done }

// pageTop is the top of the settings page: the device's name, and Done.
//
// Done is the way to leave on purpose. There was none: a browser stayed let in until the page was left
// alone long enough to close itself, on a phone put back in a pocket or a computer somebody else sits
// down at next. It is a form with the page's token like every other, so only the page can press it.
func pageTop(w http.ResponseWriter, token string) {
	fmt.Fprintf(w, `<div class="wrap"><div class="top"><div><h1>%s</h1><p class="sub">Setup</p></div>
	 <form method="post" action="/setup/done"><input type="hidden" name="token" value="%s">
	 <button type="submit" class="quiet">Done</button></form></div>`,
		html.EscapeString(deviceName()), html.EscapeString(token))
}

// done ends the session of the browser that pressed Done, and sends it to the locked page, which says
// the page is closed. Behind the press and the page's own token, as a save is.
//
// It is that browser's leaving and nothing more: another browser that was let in stays in, and the
// page itself closes when it has been left alone, as it always has. With nobody in it, nothing keeps
// it open.
func (f *Feature) done(w http.ResponseWriter, r *http.Request) {
	token, in := f.session(r)
	if !in {
		notLetIn(w)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "post to leave", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "that form was too big or malformed", http.StatusBadRequest)
		return
	}
	if !sameToken(r.PostFormValue("token"), token) {
		slog.Warn("setup page: a Done arrived without the page's own token")
		http.Error(w, "that form did not come from this page", http.StatusForbidden)
		return
	}
	f.leave(token)
	slog.Info("setup page: a browser left by its Done")
	// The cookie names nothing any more; it is taken back so the browser does not go on sending it.
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: "", Path: "/",
		HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: -1,
	})
	http.Redirect(w, r, "/setup?closed=1", http.StatusSeeOther)
}
