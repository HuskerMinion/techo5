//go:build !dot && !spot

package setup

import (
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"unicode"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// mealieSection writes the form for the Mealie server the recipe sheet reads (config.Mealie) to w. The
// token is never shown: it is written, or left alone.
func mealieSection(w http.ResponseWriter, token string) {
	mealieConfig := config.Get().Mealie
	fieldsetOpen(w, "mealie", "Mealie")
	fmt.Fprint(w, `<form method="post" action="/setup/save">`)
	hidden(w, token, "mealie")
	fmt.Fprintf(w, `<p class="note" style="margin-top:0">The recipes this screen shows, with their steps, from your
	  Mealie server. Mealie asks for an API token to list recipes: create one in Mealie under your user
	  profile's API tokens. Leave it empty only if your server lets requests through without one.</p>
	 <label for="mealieurl">Address</label>
	 <input id="mealieurl" name="url" value="%s" placeholder="http://192.168.1.20:9000" autocomplete="off">
	 %s
	 <p><button type="submit">Save</button></p></form></fieldset>`,
		html.EscapeString(mealieConfig.URL), secretField("mealietoken", "Token", "mealietoken", mealieConfig.Token != "", "notoken"))
}

// saveMealie saves the Mealie form: the address, and the token as the form says (a new one, none, or
// the saved one left alone). A form that is refused saves none of it. It returns what is wrong with the
// form, or the empty string when it was saved.
func saveMealie(r *http.Request) string {
	addr := strings.TrimRight(strings.TrimSpace(r.PostFormValue("url")), "/")
	if addr != "" {
		u, err := url.Parse(addr)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return "the address should be like http://192.168.1.20:9000"
		}
		if u.User != nil {
			return "the address should not have a name or password in it, use the token field for authorization"
		}
	}
	var tok *string
	switch t := strings.TrimSpace(r.PostFormValue("mealietoken")); {
	case r.PostFormValue("notoken") == "yes":
		empty := ""
		tok = &empty
	case t != "":
		if strings.ContainsFunc(t, func(c rune) bool { return unicode.IsSpace(c) || unicode.IsControl(c) }) {
			return "the token must be one uninterrupted sequence of letters"
		}
		tok = &t
	}
	if err := config.Set().Mealie().Server(addr, tok); err != nil {
		return "could not save it: " + err.Error()
	}
	slog.Info("setup page: mealie set", "url", addr, "token", tok != nil)
	return ""
}
