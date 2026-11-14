package setup

import (
	"context"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
)

// Photos from an Immich server, read by the device itself (feature/home/immich.go): the server's
// address and an API key, then an album or the whole library as the slideshow's photos. The key is a
// secret - it opens the owner's whole library - so the page only ever says one is kept, as it does for
// a calendar's address.

// immichAsk is how long saving a server waits for Immich to answer with its albums.
const immichAsk = 8 * time.Second

// immichListAsk is how long the setup page waits for Immich's album list as it is drawn.
const immichListAsk = 3 * time.Second

func immichSection(w http.ResponseWriter, token string) {
	srv := config.Get().Home.Immich
	fieldsetOpen(w, "immich", "Photos from Immich")
	fmt.Fprint(w, `<p style="margin:0">Show photos from your own Immich server, with no Home Assistant needed.</p>
	 <form method="post" action="/setup/save">`)
	hidden(w, token, "immich")
	keyNote := `In Immich: your account, <strong>Account Settings</strong>, <strong>API Keys</strong>, <strong>New API Key</strong>. It needs to read albums and photos.`
	if srv.Key != "" {
		keyNote = `Paste a new one to replace it.`
	}
	fmt.Fprintf(w, `<label for="immichsrv">Server address</label>
	 <input id="immichsrv" name="server" value="%s" placeholder="http://192.168.1.20:2283" autocomplete="off">
	 %s
	 <p class="note">%s The key stays on this device and this page never shows it again.</p>
	 <p><button type="submit">Save</button></p></form>`,
		html.EscapeString(srv.Server), secretField("immichkey", "API key", "key", srv.Key != "", ""), keyNote)
	if srv.Set() {
		immichAlbumForm(w, token, srv)
		immichShowForm(w, token)
		fmt.Fprint(w, `<form method="post" action="/setup/save">`)
		hidden(w, token, "immich-forget")
		fmt.Fprint(w, `<p><button type="submit" class="danger">Forget this server</button></p></form>`)
	}
	fmt.Fprint(w, `</fieldset>`)
}

// immichAlbumForm offers the whole library and each album as the slideshow's photos, with the one
// showing now chosen.
func immichAlbumForm(w http.ResponseWriter, token string, srv config.Immich) {
	// Short: the whole page waits for it, and an Immich that is down should not hold the page up.
	ctx, cancel := context.WithTimeout(context.Background(), immichListAsk)
	defer cancel()
	albums, err := home.ImmichAlbums(ctx, srv)
	if err != nil {
		fmt.Fprintf(w, `<p class="note">The albums could not be listed: %s</p>`, html.EscapeString(err.Error()))
		return
	}
	source, _, _ := home.Get().SlideshowSettings()
	fmt.Fprint(w, `<form method="post" action="/setup/save">`)
	hidden(w, token, "immich-album")
	fmt.Fprint(w, `<label for="immichalbum">Photos to show</label><select id="immichalbum" name="source">`)
	option := func(value, label string) {
		sel := ""
		if value == source {
			sel = " selected"
		}
		fmt.Fprintf(w, `<option value="%s"%s>%s</option>`, html.EscapeString(value), sel, html.EscapeString(label))
	}
	option(home.ImmichAll, "The whole library")
	for _, a := range albums {
		option(home.ImmichAlbumPrefix+a.ID, fmt.Sprintf("%s (%d photos)", a.Name, a.Photos))
	}
	fmt.Fprint(w, `</select>
	 <p class="note">The slideshow shows them in place of any it showed before. Turn the slideshow on above,
	  or on the device under Settings, Display, Slideshow.</p>
	 <p><button type="submit">Show these photos</button></p></form>`)
}

// immichShowForm is which of Immich's photos to show, and how: its favorites only, the days they were
// taken, a person; two tall ones side by side; the day and place under each.
func immichShowForm(w http.ResponseWriter, token string) {
	favorites, from, to, person := home.Get().ImmichFilters()
	checked := func(on bool) string {
		if on {
			return " checked"
		}
		return ""
	}
	fmt.Fprint(w, `<form method="post" action="/setup/save">`)
	hidden(w, token, "immich-show")
	fmt.Fprintf(w, `<p><label><input type="checkbox" name="favorites" value="yes" style="width:auto"%s> Favorites only</label></p>
	 <label for="immichfrom">Taken from</label><input id="immichfrom" name="from" type="date" value="%s">
	 <label for="immichto">Taken through</label><input id="immichto" name="to" type="date" value="%s">
	 <label for="immichperson">Of this person</label><input id="immichperson" name="person" value="%s" maxlength="100" autocomplete="off" placeholder="A name as Immich knows them">
	 <p class="note">Leave a day or the person empty for all of them.</p>`,
		checked(favorites), html.EscapeString(from), html.EscapeString(to), html.EscapeString(person))
	if home.PairsHere() {
		fmt.Fprintf(w, `<p><label><input type="checkbox" name="pairs" value="yes" style="width:auto"%s> Photo pairs: two tall photos side by side</label></p>`,
			checked(home.Get().SlideshowPairs()))
	}
	fmt.Fprintf(w, `<p><label><input type="checkbox" name="caption" value="yes" style="width:auto"%s> Date and place under each photo, on the screensaver</label></p>
	 <p><button type="submit">Save</button></p></form>`, checked(home.Get().SlideshowCaption()))
}

// saveImmichShow keeps which of Immich's photos to show, and how.
func saveImmichShow(r *http.Request) string {
	if !config.Get().Home.Immich.Set() {
		return "save an Immich server first"
	}
	if err := home.Get().SetImmichDays(r.PostFormValue("from"), r.PostFormValue("to")); err != nil {
		return err.Error()
	}
	h := home.Get()
	h.SetImmichFavorites(r.PostFormValue("favorites") == "yes")
	h.SetImmichPerson(r.PostFormValue("person"))
	if home.PairsHere() {
		h.SetSlideshowPairs(r.PostFormValue("pairs") == "yes")
	}
	h.SetSlideshowCaption(r.PostFormValue("caption") == "yes")
	slog.Info("setup page: which Immich photos to show was set")
	return ""
}

// saveImmich keeps a server and key, once Immich has answered with them.
func saveImmich(r *http.Request) string {
	cur := config.Get().Home.Immich
	server, err := home.CheckImmichServer(r.PostFormValue("server"))
	if err != nil {
		return err.Error()
	}
	key := strings.TrimSpace(r.PostFormValue("key"))
	if strings.ContainsAny(key, " \r\n\t") {
		return "the API key is one word, with no spaces"
	}
	if key == "" {
		if cur.Key == "" || server != cur.Server {
			// A kept key goes only to the server it was given for.
			return "paste the API key from Immich"
		}
		key = cur.Key
	}
	next := config.Immich{Server: server, Key: key}
	if server == cur.Server {
		next.Album, next.AlbumID = cur.Album, cur.AlbumID
	}
	ctx, cancel := context.WithTimeout(context.Background(), immichAsk)
	defer cancel()
	albums, err := home.ImmichAlbums(ctx, next)
	if err != nil {
		if errors.Is(err, home.ErrImmichKey) {
			return "Immich refused that API key"
		}
		return "could not reach Immich: " + err.Error()
	}
	if err := config.Set().Home().Immich(next); err != nil {
		return "could not keep it: " + err.Error()
	}
	if cur.Server != "" && server != cur.Server {
		home.Get().ResetImmich() // the photos listed, and an album chosen, were the other server's
	}
	host := ""
	if u, err := url.Parse(server); err == nil {
		host = u.Host
	}
	slog.Info("setup page: an Immich server was set", "host", host, "albums", len(albums))
	return ""
}

// chooseImmichAlbum makes the whole library or an album the slideshow's photos.
func chooseImmichAlbum(r *http.Request) string {
	if !config.Get().Home.Immich.Set() {
		return "save an Immich server first"
	}
	source := r.PostFormValue("source")
	if !home.ImmichSourceOK(source) {
		return "that is not one of Immich's albums"
	}
	_, album := strings.CutPrefix(source, home.ImmichAlbumPrefix)
	home.Get().SetSlideshowSource(source)
	slog.Info("setup page: the slideshow shows Immich's photos", "album", album)
	return ""
}

// forgetImmich drops the server and its key, and the slideshow's photos with them if they were
// Immich's.
func forgetImmich() string {
	if source, _, _ := home.Get().SlideshowSettings(); home.IsImmich(source) {
		home.Get().SetSlideshowSource("")
	}
	if err := config.Set().Home().Immich(config.Immich{}); err != nil {
		return "could not forget it: " + err.Error()
	}
	slog.Info("setup page: the Immich server was forgotten")
	return ""
}
