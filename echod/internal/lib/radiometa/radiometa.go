// Package radiometa finds what a radio station is playing, and what it looks like, from the
// services the stations come from: iHeartRadio, TuneIn and, for the BBC's networks, the BBC's own
// now-playing service. Station names on the screen's list carry the service ("101.1 WXYZ on
// iHeartRadio", "1450 WKRP on TuneIn"); a bare name is tried on iHeartRadio and TuneIn, or on the
// BBC when it names one of its networks. Nothing here is a documented API: these are the endpoints
// the services' own apps use, read with the same shapes they return today, and every failure is
// just "no information".
package radiometa

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"
)

// Station is a resolved station: where its live data comes from, and its logo.
type Station struct {
	Name    string // as the list has it
	Service string // "iHeartRadio", "TuneIn" or "BBC"
	ID      string // the service's own id
	Logo    string // URL, may be empty
	Music   bool   // a music station rather than talk, as far as the service says
}

// Now is what a station is playing.
type Now struct {
	Title, Artist, Album string
	Art                  string // URL of the cover, may be empty
}

var client = &http.Client{Timeout: 8 * time.Second}

// errNothing is a service saying there is nothing on: talk, adverts, between songs.
var errNothing = errors.New("radiometa: nothing on")

var (
	mu       sync.Mutex
	resolved = map[string]Station{} // by list name; a failed lookup is not cached
)

// SplitName splits a list label such as "101.1 WXYZ on iHeartRadio" into the station's name and the
// service the label names, or "" when it names none. The labels come from outside the device (they
// are what Alexa was told), so the tails are matched without regard to case and in the spellings the
// lists use.
func SplitName(label string) (name, svc string) {
	l := strings.TrimSpace(label)
	low := strings.ToLower(l)
	for _, tail := range []struct{ words, service string }{
		{" on iheartradio", "iHeartRadio"},
		{" on iheart", "iHeartRadio"},
		{" on tunein", "TuneIn"},
		{" on tune in", "TuneIn"},
	} {
		if strings.HasSuffix(low, tail.words) {
			return strings.TrimSpace(l[:len(l)-len(tail.words)]), tail.service
		}
	}
	return l, ""
}

// Resolve finds the station behind a list name.
//
// A label that names a BBC network is answered from the BBC's catalogue first: the directories do
// not carry those networks, and a search for one can answer a station in another country that
// happens to share the letters. A label that names a service goes to that service; a bare name is
// tried on iHeartRadio, then TuneIn.
func Resolve(ctx context.Context, name string) (Station, error) {
	mu.Lock()
	st, ok := resolved[name]
	mu.Unlock()
	if ok {
		return st, nil
	}
	query, svc := SplitName(name)
	var err error
	if n, ok := bbcNetworkFor(query); ok {
		st = Station{Service: "BBC", ID: n.service, Logo: n.logo, Music: n.music}
	} else {
		switch svc {
		case "iHeartRadio":
			st, err = iheartFind(ctx, query)
		case "TuneIn":
			st, err = tuneinFind(ctx, query)
		case "":
			if st, err = iheartFind(ctx, query); err != nil {
				st, err = tuneinFind(ctx, query)
			}
		default:
			return Station{}, fmt.Errorf("radiometa: unknown service %q", svc)
		}
	}
	if err != nil {
		return Station{}, err
	}
	st.Name = name
	mu.Lock()
	resolved[name] = st
	mu.Unlock()
	return st, nil
}

// Playing is what the station has on now. A talk station, or a music one between songs, gives
// an empty Now and no error.
func Playing(ctx context.Context, st Station) (Now, error) {
	switch st.Service {
	case "iHeartRadio":
		return iheartNow(ctx, st.ID)
	case "TuneIn":
		return tuneinNow(ctx, st.ID)
	case "BBC":
		return bbcNow(ctx, st.ID)
	}
	return Now{}, errors.New("radiometa: unknown service")
}

func get(ctx context.Context, u string, into any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "TECHO5/1.0")
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNoContent {
		return errNothing
	}
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("radiometa: %s: %s", u, res.Status)
	}
	return json.NewDecoder(io.LimitReader(res.Body, maxReply)).Decode(into)
}

// maxReply is how much of a service's answer is decoded. A search result or a now-playing record is a
// few kilobytes; a megabyte is far past anything these have ever returned. Neither service is ours and
// neither promises anything, so the bound is what keeps a station's metadata host — or whatever has
// taken its name today — from feeding a device with half a gigabyte until it dies. A reply cut off
// here fails to parse, which is the same "no information" every other failure here comes to.
const maxReply = 1 << 20

var word = regexp.MustCompile(`[A-Za-z0-9.]+`)

// noiseWords are the words a list adds around a station's name that say nothing about which
// station it is. "BBC" is among them: it is a broadcaster, not a station, and sharing it is how a
// BBC search came to answer a US station called KUT.
var noiseWords = map[string]bool{
	"radio": true, "fm": true, "am": true, "the": true, "station": true, "uk": true, "bbc": true,
}

var (
	// tagline is what a list puts after a station's name: a description after a dash or colon. An
	// en or em dash separates without spaces often enough to be worth taking either way.
	tagline = regexp.MustCompile(`\s*[:|]\s*|\s+-\s+|\s*[–—]\s*`)
	// brackets is what a list puts around a quality or a description.
	brackets = regexp.MustCompile(`\s*[\(\[][^\)\]]*[\)\]]`)
)

// cutTagline drops a list's description of a station.
func cutTagline(s string) string {
	if loc := tagline.FindStringIndex(s); loc != nil {
		return s[:loc[0]]
	}
	return s
}

// wordList is a name's words, lowercased and with sentence punctuation taken off their ends.
func wordList(s string) []string {
	var out []string
	for _, w := range word.FindAllString(strings.ToLower(s), -1) {
		if w = strings.Trim(w, "."); w != "" {
			out = append(out, w)
		}
	}
	return out
}

// nameWords are the words that say which station a name is: a tagline and a bracketed quality
// dropped, the words every list adds dropped, and a bare frequency dropped when there is a name
// beside it. "Magic 105.4" and "Magic Radio" both come to "magic"; "98.7 WZZZ" to "wzzz".
func nameWords(s string) []string {
	s = brackets.ReplaceAllString(cutTagline(strings.ToLower(s)), " ")
	var words, frequencies []string
	for _, w := range wordList(s) {
		switch {
		case noiseWords[w], formatWord(w):
		case frequencyWord(w):
			frequencies = append(frequencies, w)
		default:
			words = append(words, w)
		}
	}
	if len(words) == 0 {
		return frequencies
	}
	return words
}

// frequencyWord reports whether a word is a dial position rather than part of a name.
func frequencyWord(w string) bool {
	return strings.Contains(w, ".") && strings.Trim(w, "0123456789.") == ""
}

// dialWords are the dial positions a name gives, tagline and brackets aside.
func dialWords(s string) []string {
	s = brackets.ReplaceAllString(cutTagline(strings.ToLower(s)), " ")
	var out []string
	for _, w := range wordList(s) {
		if frequencyWord(w) {
			out = append(out, w)
		}
	}
	return out
}

// dialsAgree reports whether two names can be the same station as far as their dial positions go.
// One with no position is compatible with any; two different positions are different stations, so
// "Magic 105.4" is not "Magic 96.5".
func dialsAgree(a, b string) bool {
	ad, bd := dialWords(a), dialWords(b)
	return len(ad) == 0 || len(bd) == 0 || sharesAny(ad, bd)
}

// formatWord reports whether a word says how a stream is encoded rather than which station it is.
func formatWord(w string) bool {
	switch w {
	case "hd", "hls", "aac", "mp3", "ogg":
		return true
	}
	if strings.HasPrefix(w, "hd") && strings.Trim(w[2:], "0123456789") == "" {
		return true // HD2, HD3: an iHeart subchannel, not part of the name
	}
	if len(w) < 3 {
		return false
	}
	unit := w[len(w)-1]
	return (unit == 'k' || unit == 'p') && strings.Trim(w[:len(w)-1], "0123456789") == ""
}

// strongWords are the words of a name long enough to identify a station, rather than a number that
// many stations share.
func strongWords(words []string) []string {
	var out []string
	for _, w := range words {
		if len(w) >= 3 && strings.Trim(w, "0123456789.") != "" {
			out = append(out, w)
		}
	}
	return out
}

// brandWord reports whether a word is a station's own brand — a call sign written in capitals, or
// a name with a number in it such as "Z100".
func brandWord(query, w string) bool {
	if strings.ContainsAny(w, "0123456789") {
		return true
	}
	for _, raw := range word.FindAllString(query, -1) {
		raw = strings.Trim(raw, ".")
		if strings.EqualFold(raw, w) && len(raw) >= 3 && len(raw) <= 5 &&
			raw == strings.ToUpper(raw) && strings.Trim(raw, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") == "" {
			return true
		}
	}
	return false
}

// matchEnough reports whether a candidate is the station the query names. An exact name is that
// station, as is one with a detail left out when what it keeps is the station's own brand: "Z100
// New York" is "Z100 WHTZ-FM". Otherwise a candidate may add to the query's name only what a
// directory adds around the same station: a call sign ("HOT 97 WQHT") or one more word, such as its
// city ("Capital London"), and only when it leads with the query's own name. Two or more words of
// its own make it another station ("Absolute Radio" is not "Absolute Classic Hits"), as does a word
// with a number in it ("Absolute Radio 90s"), and so does leading with another name ("The Bay's
// Greatest Hits" is not "Greatest Hits Radio"). Sharing a word every list has — "BBC", "radio",
// "FM" — is never enough.
func matchEnough(query, candidate string) bool {
	if !dialsAgree(query, candidate) {
		return false
	}
	if sameName(query, candidate) {
		return true
	}
	strong := strongWords(nameWords(query))
	cwords := nameWords(candidate)
	if len(strong) == 0 || !contains(cwords, strong[0]) {
		return false
	}
	if brandWord(query, strong[0]) {
		return true
	}
	cstrong := strongWords(cwords)
	if len(cstrong) == 0 || cstrong[0] != strong[0] {
		return false
	}
	for _, w := range strong {
		if !contains(cwords, w) {
			return false
		}
	}
	extra := 0
	for _, w := range cstrong {
		switch {
		case contains(strong, w), callSign(candidate, w):
		case strings.ContainsAny(w, "0123456789"):
			return false
		default:
			extra++
		}
	}
	return extra <= 1
}

// callSign reports whether a word of a name is a North American call sign as the name writes it:
// four letters in capitals starting with K or W, such as "WQHT" or "KPWR", or five in "WBEAT".
func callSign(name, w string) bool {
	for _, raw := range word.FindAllString(name, -1) {
		raw = strings.Trim(raw, ".")
		if strings.EqualFold(raw, w) && len(raw) >= 4 && len(raw) <= 5 && (raw[0] == 'K' || raw[0] == 'W') &&
			strings.Trim(raw, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") == "" {
			return true
		}
	}
	return false
}

func contains(words []string, want string) bool {
	for _, w := range words {
		if w == want {
			return true
		}
	}
	return false
}

func sharesAny(words, want []string) bool {
	for _, w := range want {
		if contains(words, w) {
			return true
		}
	}
	return false
}

// sameName reports whether two names say the same station, ignoring the words a list adds around it
// and a call sign repeated after the name. Two different dial positions are never the same name.
func sameName(a, b string) bool {
	if !dialsAgree(a, b) {
		return false
	}
	aw, bw := nameWords(a), nameWords(b)
	if len(aw) == 0 || len(bw) == 0 {
		return false
	}
	as, bs := map[string]bool{}, map[string]bool{}
	for _, w := range aw {
		as[w] = true
	}
	for _, w := range bw {
		bs[w] = true
	}
	if len(as) != len(bs) {
		return false
	}
	for w := range as {
		if !bs[w] {
			return false
		}
	}
	return true
}

// score counts how many words of the query a candidate carries, call letters weighing most:
// "98.7 WZZZ" must pick WZZZ over another 97.3. An exact name wins outright, whatever else the
// candidates carry, so the one that names the station beats one that merely repeats more words.
func score(query, candidate string) int {
	c := strings.ToLower(candidate)
	n := 0
	for _, w := range word.FindAllString(strings.ToLower(query), -1) {
		if !strings.Contains(c, w) {
			continue
		}
		if len(w) >= 3 && strings.Trim(w, "0123456789.") == w {
			n += 3 // letters: call sign or a name
		} else {
			n++
		}
	}
	if sameName(query, candidate) {
		n += 100
	}
	return n
}

// ---- BBC ----

// The BBC's networks are not on iHeartRadio: its search for "BBC Radio 6 Music" answers with a US
// station that happens to share the letters (KUT 90.5 HD2), which is where the wrong cover came
// from. The BBC's own service knows what each network plays, and TuneIn carries the network logo.
// Both are keyed by the name the list uses.
type bbcNetwork struct {
	service string // the BBC Sounds service in its now-playing address
	logo    string // TuneIn's large logo for the network
	music   bool   // music rather than speech, for the screen's visualiser
}

var bbcNetworks = map[string]bbcNetwork{
	"1":   {"bbc_radio_one", "https://cdn-profiles.tunein.com/s24939/images/logog.png", true},
	"one": {"bbc_radio_one", "https://cdn-profiles.tunein.com/s24939/images/logog.png", true},

	"1xtra":    {"bbc_1xtra", "https://cdn-profiles.tunein.com/s20277/images/logog.png", true},
	"1 xtra":   {"bbc_1xtra", "https://cdn-profiles.tunein.com/s20277/images/logog.png", true},
	"one xtra": {"bbc_1xtra", "https://cdn-profiles.tunein.com/s20277/images/logog.png", true},

	"2":   {"bbc_radio_two", "https://cdn-profiles.tunein.com/s24940/images/logog.png", true},
	"two": {"bbc_radio_two", "https://cdn-profiles.tunein.com/s24940/images/logog.png", true},

	"3":     {"bbc_radio_three", "https://cdn-profiles.tunein.com/s24941/images/logog.png", true},
	"three": {"bbc_radio_three", "https://cdn-profiles.tunein.com/s24941/images/logog.png", true},

	"4":    {"bbc_radio_fourfm", "https://cdn-profiles.tunein.com/s25419/images/logog.png", false},
	"four": {"bbc_radio_fourfm", "https://cdn-profiles.tunein.com/s25419/images/logog.png", false},

	"4 extra":    {"bbc_radio_four_extra", "https://cdn-profiles.tunein.com/s6839/images/logog.png", false},
	"four extra": {"bbc_radio_four_extra", "https://cdn-profiles.tunein.com/s6839/images/logog.png", false},

	"5 live":    {"bbc_radio_five_live", "https://cdn-profiles.tunein.com/s24943/images/logog.png", false},
	"five live": {"bbc_radio_five_live", "https://cdn-profiles.tunein.com/s24943/images/logog.png", false},

	"5 sports extra":    {"bbc_radio_five_live_sports_extra", "https://ichef.bbci.co.uk/images/ic/raw/p0mw75dn.png", false},
	"five sports extra": {"bbc_radio_five_live_sports_extra", "https://ichef.bbci.co.uk/images/ic/raw/p0mw75dn.png", false},

	"5 live sports extra":    {"bbc_radio_five_live_sports_extra", "https://ichef.bbci.co.uk/images/ic/raw/p0mw75dn.png", false},
	"five live sports extra": {"bbc_radio_five_live_sports_extra", "https://ichef.bbci.co.uk/images/ic/raw/p0mw75dn.png", false},

	"6 music":   {"bbc_6music", "https://cdn-profiles.tunein.com/s44491/images/logog.png", true},
	"6music":    {"bbc_6music", "https://cdn-profiles.tunein.com/s44491/images/logog.png", true},
	"six music": {"bbc_6music", "https://cdn-profiles.tunein.com/s44491/images/logog.png", true},

	"asian network": {"bbc_asian_network", "https://cdn-profiles.tunein.com/s44490/images/logog.png", true},
	"world service": {"bbc_world_service", "https://cdn-profiles.tunein.com/s24948/images/logog.jpg", false},
	"worldservice":  {"bbc_world_service", "https://cdn-profiles.tunein.com/s24948/images/logog.jpg", false},
}

// bbcNetworkName is the table key a list name normalizes to, or "" when the name does not say BBC.
// The name must say BBC: "Radio 4" alone could be a station anywhere, and a false match would be a
// wrong logo and a wrong song. A list often adds a tagline after a dash or colon, and a stream its
// quality in brackets: "BBC RADIO 1 - The biggest new pop" and "BBC Radio 4 (128k)" are Radio 1 and
// Radio 4.
func bbcNetworkName(query string) string {
	q := brackets.ReplaceAllString(cutTagline(strings.ToLower(query)), " ")
	words := strings.FieldsFunc(q, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	bbc := false
	rest := make([]string, 0, len(words))
	for _, w := range words {
		switch {
		case w == "bbc":
			bbc = true
		case w == "radio", w == "fm", w == "uk", w == "the":
		case formatWord(w):
		default:
			rest = append(rest, w)
		}
	}
	if !bbc {
		return ""
	}
	return strings.Join(rest, " ")
}

// bbcNetworkFor is the BBC network a list name is, if it is one.
func bbcNetworkFor(query string) (bbcNetwork, bool) {
	n, ok := bbcNetworks[bbcNetworkName(query)]
	return n, ok
}

const (
	// bbcArtSize is the recipe the BBC's picture addresses are filled with. The page wants it for a
	// 1280x800 background, so the largest square keeps it sharp.
	bbcArtSize = "1024x1024"
	// bbcNoArt is the picture the BBC serves for a track with none of its own: a flat grey square.
	// The network logo says more.
	bbcNoArt = "p0bqcdzf"
)

// bbcNow is the segment the BBC marks as playing. Its titles have the artist first and the song
// second, the other way round from iHeartRadio's.
func bbcNow(ctx context.Context, service string) (Now, error) {
	var out struct {
		Data []struct {
			SegmentType string `json:"segment_type"`
			Titles      struct {
				Primary   string `json:"primary"`
				Secondary string `json:"secondary"`
			} `json:"titles"`
			ImageURL string `json:"image_url"`
			Offset   struct {
				NowPlaying bool `json:"now_playing"`
			} `json:"offset"`
		} `json:"data"`
	}
	u := "https://rms.api.bbc.co.uk/v2/services/" + url.PathEscape(service) + "/segments/latest"
	if err := get(ctx, u, &out); err != nil {
		return Now{}, err
	}
	for _, s := range out.Data {
		if s.SegmentType != "music" || !s.Offset.NowPlaying {
			continue
		}
		title, artist := strings.TrimSpace(s.Titles.Secondary), strings.TrimSpace(s.Titles.Primary)
		if title == "" && artist == "" {
			continue // the next segment along may still be the one on
		}
		art := ""
		if s.ImageURL != "" && !strings.Contains(s.ImageURL, bbcNoArt) {
			art = strings.ReplaceAll(s.ImageURL, "{recipe}", bbcArtSize)
		}
		return Now{Title: title, Artist: artist, Art: art}, nil
	}
	return Now{}, nil
}

// ---- iHeartRadio ----

func iheartFind(ctx context.Context, query string) (Station, error) {
	var out struct {
		Results struct {
			Stations []struct {
				ID          json.Number `json:"id"`
				Name        string      `json:"name"`
				CallLetters string      `json:"callLetters"`
				Description string      `json:"description"`
				ImageURL    string      `json:"imageUrl"`
			} `json:"stations"`
		} `json:"results"`
	}
	u := "https://api.iheart.com/api/v3/search/all?countryCode=US&stations=true&albums=false&artists=false&tracks=false&bundles=false&playlists=false&podcasts=false&limit=8&keywords=" + url.QueryEscape(query)
	if err := get(ctx, u, &out); err != nil {
		return Station{}, err
	}
	best, bestScore := -1, 0
	for i, s := range out.Results.Stations {
		if !matchEnough(query, s.Name+" "+s.CallLetters) {
			continue
		}
		if sc := score(query, s.Name+" "+s.CallLetters); sc > bestScore {
			best, bestScore = i, sc
		}
	}
	if best < 0 {
		return Station{}, fmt.Errorf("radiometa: iHeartRadio knows no %q", query)
	}
	s := out.Results.Stations[best]
	d := strings.ToLower(s.Description + " " + s.Name)
	music := !(strings.Contains(d, "news") || strings.Contains(d, "talk") || strings.Contains(d, "sports"))
	return Station{Service: "iHeartRadio", ID: s.ID.String(), Logo: s.ImageURL, Music: music}, nil
}

type iheartTrack struct {
	Code          int     `json:"code"`
	Title         string  `json:"title"`
	Artist        string  `json:"artist"`
	Album         string  `json:"album"`
	ImagePath     string  `json:"imagePath"`
	TrackDuration float64 `json:"trackDuration"`
	StartTime     float64 `json:"startTime"` // unix seconds
}

func (t iheartTrack) now() Now {
	return Now{Title: t.Title, Artist: t.Artist, Album: t.Album, Art: t.ImagePath}
}

func iheartNow(ctx context.Context, id string) (Now, error) {
	var out iheartTrack
	u := "https://us.api.iheart.com/api/v3/live-meta/stream/" + id + "/currentTrackMeta?defaultMetadata=true"
	err := get(ctx, u, &out)
	switch {
	case err == nil && out.Code == 0 && out.Title != "":
		return out.now(), nil
	case err != nil && !errors.Is(err, errNothing) && !strings.Contains(err.Error(), "410"):
		return Now{}, err
	}
	// "Nothing on" from the live endpoint also happens mid-song for a while; the history knows
	// what started last, and if that song is still within its own length it is the one playing.
	var hist struct {
		Data []iheartTrack `json:"data"`
	}
	if err := get(ctx, "https://us.api.iheart.com/api/v3/live-meta/stream/"+id+"/trackHistory?limit=1", &hist); err != nil || len(hist.Data) == 0 {
		return Now{}, nil
	}
	t := hist.Data[0]
	age := float64(time.Now().Unix()) - t.StartTime
	if t.Title == "" || age < 0 || age > t.TrackDuration+90 {
		return Now{}, nil
	}
	return t.now(), nil
}

// ---- TuneIn ----

type tuneinOutline struct {
	Element      string `json:"element"`
	Type         string `json:"type"`
	Item         string `json:"item"`
	Text         string `json:"text"`
	GuideID      string `json:"guide_id"`
	Image        string `json:"image"`
	CurrentTrack string `json:"current_track"`
	Subtext      string `json:"subtext"`
}

func tuneinFind(ctx context.Context, query string) (Station, error) {
	var out struct {
		Body []tuneinOutline `json:"body"`
	}
	u := "https://opml.radiotime.com/Search.ashx?render=json&query=" + url.QueryEscape(query)
	if err := get(ctx, u, &out); err != nil {
		return Station{}, err
	}
	best, bestScore := -1, 0
	for i, o := range out.Body {
		if o.Item != "station" {
			continue
		}
		if !matchEnough(query, o.Text) {
			continue
		}
		if sc := score(query, o.Text); sc > bestScore {
			best, bestScore = i, sc
		}
	}
	if best < 0 {
		return Station{}, fmt.Errorf("radiometa: TuneIn knows no %q", query)
	}
	o := out.Body[best]
	// The larger logo is the same path with a different size letter.
	logo := strings.Replace(o.Image, "q.png", "g.png", 1)
	logo = strings.Replace(logo, "logoq.", "logog.", 1)
	var desc struct {
		Body []struct {
			IsMusic bool   `json:"is_music"`
			Logo    string `json:"logo"`
		} `json:"body"`
	}
	music := false
	if err := get(ctx, "https://opml.radiotime.com/Describe.ashx?render=json&id="+o.GuideID, &desc); err == nil && len(desc.Body) > 0 {
		music = desc.Body[0].IsMusic
		if logo == "" {
			logo = desc.Body[0].Logo
		}
	}
	return Station{Service: "TuneIn", ID: o.GuideID, Logo: logo, Music: music}, nil
}

func tuneinNow(ctx context.Context, id string) (Now, error) {
	var out struct {
		Body []struct {
			CurrentSong     string `json:"current_song"`
			CurrentArtist   string `json:"current_artist"`
			CurrentAlbum    string `json:"current_album"`
			CurrentAlbumArt string `json:"current_album_art"`
		} `json:"body"`
	}
	if err := get(ctx, "https://opml.radiotime.com/Describe.ashx?render=json&id="+id, &out); err != nil {
		return Now{}, err
	}
	if len(out.Body) == 0 || out.Body[0].CurrentSong == "" {
		return Now{}, nil
	}
	b := out.Body[0]
	return Now{Title: b.CurrentSong, Artist: b.CurrentArtist, Album: b.CurrentAlbum, Art: b.CurrentAlbumArt}, nil
}
