package setup

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Find a setting: a box at the top of every tab (tabHead), a plain form that asks for /setup?find=words.
// What it finds is the cards of every tab this device has, drawn by the same code the tabs draw them
// with (drawTab) into a page that is never sent, and matched on their titles, their lines, a few words
// people look for that the cards do not say (findKeywords), and the words on them. The cards found are
// drawn as they are on their tabs, open, so a setting is changed where it was found; a save from one
// comes back to its own tab, as every save does (back).
//
// A tab drawn to be searched does nothing a tab drawn to be seen does not, and less: no network scan,
// and no waiting on a server's answer (waitFor); what the last save did is not said, and a refused form
// is not handed back.

// maxFind is the most of a find that is read, in characters, and maxFindWords the most words.
const (
	maxFind      = 80
	maxFindWords = 6
)

// findShown is a find as it is read and shown back: trimmed, its spaces one, and cut to maxFind.
func findShown(q string) string {
	q = strings.Join(strings.Fields(q), " ")
	if utf8.RuneCountInString(q) > maxFind {
		q = string([]rune(q)[:maxFind])
	}
	return q
}

// findStop are words a find says that would match nearly every card.
var findStop = map[string]bool{"a": true, "an": true, "the": true, "to": true, "of": true, "for": true,
	"on": true, "in": true, "my": true, "how": true, "do": true, "i": true, "is": true, "it": true, "and": true, "or": true}

// findWords are the words of a find to match, lowercase and once each, the common ones left out unless
// they are all there is.
func findWords(q string) []string {
	var all, words []string
	seen := map[string]bool{}
	for _, w := range strings.Fields(strings.ToLower(findShown(q))) {
		if seen[w] {
			continue
		}
		seen[w] = true
		all = append(all, w)
		if !findStop[w] {
			words = append(words, w)
		}
	}
	if len(words) == 0 {
		words = all
	}
	if len(words) > maxFindWords {
		words = words[:maxFindWords]
	}
	return words
}

// findKeywords are words people look for that a card does not say, or not where a find would rank it.
// Only cards a tab draws are found, so a word here for a card this device does not have finds nothing.
var findKeywords = map[string][]string{
	"now":         {"status", "connected", "version"},
	"switchedon":  {"ssh", "status"},
	"diagnostics": {"help", "log", "bug", "debug", "problem"},

	"alarm_edit":  {"alarm", "alarms", "timer", "timers", "reminder", "reminders"},
	"alarm_prefs": {"alarm", "snooze", "ringtone"},

	"speaker":   {"volume", "loud", "quiet", "mute", "sound", "disturb"},
	"stations":  {"radio", "stream"},
	"streaming": {"airplay", "spotify", "dlna", "cast"},
	"bluetooth": {"pair", "headphones"},
	"music":     {"music", "sendspin"},
	"sounds":    {"sound", "chime", "ringtone"},

	"brain":     {"language", "assistant", "speech", "pipeline"},
	"house":     {"announce", "intercom", "broadcast"},
	"phonebook": {"contacts", "call", "phone"},
	"wakewords": {"wake", "hotword"},

	"screen":    {"clock", "display", "slideshow", "screensaver"},
	"themes":    {"night", "dim", "dark", "light", "color", "brightness"},
	"photos":    {"pictures", "slideshow"},
	"immich":    {"photos", "pictures", "album", "slideshow"},
	"dashpanel": {"panel"},

	"doorbell": {"bell", "chime"},

	"wifi":       {"wifi", "network", "password", "ssid", "internet", "router"},
	"address":    {"ip", "static", "dhcp"},
	"haaccess":   {"key", "token", "sign", "login"},
	"adopt":      {"ha", "adopt"},
	"timeserver": {"ntp", "time"},
	"tailscale":  {"vpn", "tailnet", "away"},
	"netbird":    {"vpn", "away"},

	"lock":   {"pin", "lock", "code"},
	"baby":   {"monitor"},
	"remote": {"api", "key"},

	"name":         {"rename", "hostname"},
	"place":        {"location", "weather", "city"},
	"timezone":     {"zone", "timezone", "time", "clock", "dst"},
	"calendar":     {"calendar", "ical", "ics"},
	"updates":      {"update", "upgrade", "firmware", "version", "software"},
	"settingsfile": {"backup", "restore", "export", "import"},
	"giftreset":    {"reset", "factory", "wipe"},
	"ringwait":     {"led", "light"},
}

// findTitleOnly are cards a find matches by their title, line and keywords only: what is on Now is the
// state of everything else, and would be found by nearly every find.
var findTitleOnly = map[string]bool{"now": true}

// hit is a card a find found: the tab it is on, the card, how it was drawn, and how well it matched.
type hit struct {
	tab   tab
	card  card
	html  []byte
	score int
}

// findCards draws every tab this device has into a page that is not sent and finds the cards on them
// that every word matches, best first (findScore), and in the tabs' order where they match as well. A
// card drawn on two tabs is found on the first.
func (f *Feature) findCards(ctx context.Context, page http.ResponseWriter, token string, words []string) []hit {
	var hits []hit
	seen := map[string]bool{}
	for _, t := range shownTabs() {
		s := &sectionWriter{ResponseWriter: page, open: "all", finding: true}
		f.drawTab(ctx, s, token, t.id, false)
		body := s.body.Bytes()
		for _, c := range s.cards {
			if seen[c.name] || c.end <= c.start || c.end > len(body) {
				continue
			}
			seen[c.name] = true
			drawn := body[c.start:c.end]
			text := ""
			if !findTitleOnly[c.name] {
				text = cardText(drawn)
			}
			if score := findScore(words, c, text); score > 0 {
				hits = append(hits, hit{t, c, drawn, score})
			}
		}
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].score > hits[j].score })
	return hits
}

// findScore is how well every word matches a card, or 0 when one of them does not: a word in the title
// is the best match, then a keyword (a card the word was put there for), then the line, then the words
// on the card.
func findScore(words []string, c card, text string) int {
	title, line := strings.ToLower(c.title), strings.ToLower(c.summary)
	total := 0
	for _, w := range words {
		switch {
		case hasWord(title, w):
			total += 4
		case isKeyword(findKeywords[c.name], w):
			total += 3
		case hasWord(line, w):
			total += 2
		case hasWord(text, w):
			total++
		default:
			return 0
		}
	}
	return total
}

// hasWord is whether w starts a word in text: "dim" finds "dimmed", "pin" does not find "spinning".
func hasWord(text, w string) bool {
	for from := 0; from <= len(text); {
		i := strings.Index(text[from:], w)
		if i < 0 {
			return false
		}
		i += from
		if r, _ := utf8.DecodeLastRuneInString(text[:i]); i == 0 || !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return true
		}
		from = i + 1
	}
	return false
}

// isKeyword is whether w is one of a card's keywords, or the start of one ("vol", "volume").
func isKeyword(keys []string, w string) bool {
	for _, k := range keys {
		if k == w || len(w) >= 3 && strings.HasPrefix(k, w) {
			return true
		}
	}
	return false
}

// findMaxOptions is the most choices a list on a card may have and still be searched: the time zones'
// hundreds of places would have a find for "new" or "york" find the time zone.
const findMaxOptions = 24

var (
	findOwn    = regexp.MustCompile(`(?is)<summary>.*?</summary>`)
	findSkip   = regexp.MustCompile(`(?is)<script\b.*?</script>|<style\b.*?</style>|<textarea\b.*?</textarea>|<fieldset class="also">.*?</fieldset>`)
	findSelect = regexp.MustCompile(`(?is)<select\b.*?</select>`)
	findTag    = regexp.MustCompile(`<[^>]*>`)
)

// cardText is the words on a drawn card, lowercase: its labels, notes and choices, without its title
// and line (matched on their own), its scripts, what is typed in its boxes, or a long list of choices.
func cardText(drawn []byte) string {
	// The card's own summary (its title and line) is the first; the ones inside it, an alarm's or a
	// shared setting's, are words on the card.
	s := string(drawn)
	if at := findOwn.FindStringIndex(s); at != nil {
		s = s[:at[0]] + " " + s[at[1]:]
	}
	s = findSkip.ReplaceAllString(s, " ")
	s = findSelect.ReplaceAllStringFunc(s, func(sel string) string {
		if strings.Count(sel, "<option") > findMaxOptions {
			return " "
		}
		return sel
	})
	s = html.UnescapeString(findTag.ReplaceAllString(s, " "))
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

// tabHead is the top of a tab, or of a find: the Find a setting box and the heading, the box beside it
// where there is room and above it on a phone (head). heading is HTML; shown is the find in the box, if
// any. A tab's box carries the tab, so a find with no words in it stays on the tab.
func tabHead(w http.ResponseWriter, tab, shown, heading string) {
	fmt.Fprint(w, `<div class="tabhead"><form class="find" role="search" method="get" action="/setup">`)
	if tab != "" {
		fmt.Fprintf(w, `<input type="hidden" name="tab" value="%s">`, html.EscapeString(tab))
	}
	fmt.Fprintf(w, `<label class="vh" for="find">Find a setting</label><input id="find" name="find" type="search" value="%s" maxlength="%d" placeholder="Find a setting" autocomplete="off" enterkeyhint="search"><button type="submit" class="quiet">Find</button></form><h2>%s</h2></div>`,
		html.EscapeString(shown), maxFind, heading)
}

// findPage is what a find found: the cards, best first, each under a line naming its tab with a link to
// it there. The rail has every tab and none of them marked; there is nothing on the page to keep open.
func (f *Feature) findPage(ctx context.Context, w http.ResponseWriter, token, shown string, words []string) {
	hits := f.findCards(ctx, w, token, words)
	head(w)
	pageTop(w, token)
	fmt.Fprint(w, `<div class="layout">`)
	nav(w, "", nil)
	fmt.Fprint(w, `<section>`)
	tabHead(w, "", shown, "Find: “"+html.EscapeString(shown)+"”")
	if len(hits) == 0 {
		fmt.Fprint(w, `<p class="sub">No setting matches that. Try other words or fewer of them, or look through
		 <a href="/setup?tab=network">Network</a> or <a href="/setup?tab=system">System</a>.</p>`)
	} else {
		fmt.Fprintf(w, `<p class="sub">%s</p><div class="found">`, count(len(hits), "setting found", "settings found"))
		for _, h := range hits {
			fmt.Fprintf(w, `<div class="hit"><p class="where">On <a href="/setup?tab=%s&amp;open=%s#sec-%s">%s</a></p>`,
				h.tab.id, h.card.name, h.card.name, html.EscapeString(h.tab.title))
			_, _ = w.Write(h.html)
			// What is coming up is found by "alarm", "timer" and "reminder", which people mostly look
			// for to add one: the drawer that adds them, which is not a card, comes with it.
			if h.card.name == "alarm_edit" {
				addDrawer(w, token)
			}
			fmt.Fprint(w, `</div>`)
		}
		fmt.Fprint(w, `</div>`)
	}
	fmt.Fprint(w, `</section></div></div>`)
	pageScript(w)
}
