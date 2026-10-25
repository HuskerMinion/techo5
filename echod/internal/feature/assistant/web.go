package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"

	"golang.org/x/net/html"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/lib/llm"
	"github.com/HuskerMinion/techo5/echod/internal/lib/radiobrowser"
)

// Looking things up: a search through a SearXNG server (config.Brain.Search), and reading a page it
// found, for what the model cannot know - a game's date, a store's hours, the news. Without a search
// server set the tools are not offered, and the model is told it cannot look anything up.

const (
	// searchResults is how many results go back to the model: enough to find the page that answers,
	// few enough not to fill its context with snippets.
	searchResults = 6

	// pageChars is how much of a page's text goes back: a schedule or the parts of an article asked
	// about, within what the model can read quickly (some 2,000 tokens, under a second on a desktop GPU).
	pageChars = 8000

	// pageMax bounds what is downloaded of a page.
	pageMax = 3 << 20
)

var webClient = &http.Client{Timeout: 15 * time.Second}

// pageAddrOK is whether pageClient may connect to an address; a variable for the tests.
var pageAddrOK = func(a netip.AddrPort) bool { return radiobrowser.PublicAddr(a.Addr()) }

// pageClient reads pages: the internet only, never this house's network or the device itself, at
// whatever address a name resolves to and wherever a redirect leads. A page the model is asked to read
// may have been named by another page, and a GET on a router or a gadget at home can change something.
// The search server, on the house's own network, is reached with webClient.
var pageClient = &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{
	// No proxy: the check below is on the address connected to, which a proxy's would stand in for.
	DialContext: (&net.Dialer{Timeout: 10 * time.Second, Control: func(_, address string, _ syscall.RawConn) error {
		a, err := netip.ParseAddrPort(address)
		if err != nil || !pageAddrOK(a) {
			return fmt.Errorf("%s is not on the internet", address)
		}
		return nil
	}}).DialContext,
	TLSHandshakeTimeout: 10 * time.Second,
	IdleConnTimeout:     90 * time.Second,
}}

func webTools() []tool {
	if config.Get().Brain.Search == "" {
		return nil
	}
	return []tool{
		{llm.Tool{Name: "web_search", Description: "Search the web. Use it for anything current or that you are not sure of: sports schedules and scores, news, weather elsewhere, business hours, prices. Returns titles, addresses and snippets; read a page for details.",
			Parameters: object(map[string]any{"query": str("What to search for, as you would type it into a search engine.")}, "query")},
			func(a map[string]any) (string, error) { return search(argString(a, "query")) }},

		{llm.Tool{Name: "read_page", Description: "Read the text of a web page, such as one web_search found. Schedules on espn.com read well.",
			Parameters: object(map[string]any{"url": str("The page's address."),
				"looking_for": str("What you want from the page, in a few words, like: Nebraska next game date and time.")}, "url")},
			func(a map[string]any) (string, error) {
				want := argString(a, "looking_for")
				if strings.TrimSpace(want) == "" {
					want = getLastQuery()
				}
				return readPage(argString(a, "url"), want)
			}},
	}
}

func search(q string) (string, error) {
	if q == "" {
		return "", errors.New("nothing to search for")
	}
	base := strings.TrimRight(config.Get().Brain.Search, "/")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/search?"+url.Values{"q": {q}, "format": {"json"}}.Encode(), nil)
	if err != nil {
		return "", err
	}
	resp, err := webClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("the search server did not answer")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("the search server answered %s (is its JSON format turned on?)", resp.Status)
	}
	var out struct {
		Results []struct {
			Title, URL, Content string
		} `json:"results"`
		Answers   []any `json:"answers"`
		Infoboxes []struct {
			Infobox, Content string
			Attributes       []struct{ Label, Value any } `json:"attributes"`
		} `json:"infoboxes"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&out); err != nil {
		return "", fmt.Errorf("the search server's answer was not JSON")
	}
	setLastQuery(q)
	var s []string
	for _, a := range out.Answers {
		if t := answerText(a); t != "" {
			s = append(s, "answer: "+t)
		}
	}
	// An infobox is Wikipedia's or Wikidata's summary of what was searched for: a team, a person, a place.
	for i, b := range out.Infoboxes {
		if i >= 2 {
			break
		}
		t := b.Infobox + ": " + clip(strings.Join(strings.Fields(b.Content), " "), 600)
		var facts []string
		for _, at := range b.Attributes {
			if l, v := fmt.Sprint(at.Label), fmt.Sprint(at.Value); at.Label != nil && at.Value != nil && len(facts) < 12 {
				facts = append(facts, l+" "+clip(v, 120))
			}
		}
		if len(facts) > 0 {
			t += " (" + strings.Join(facts, "; ") + ")"
		}
		s = append(s, "about "+t)
	}
	var found []string
	for i, r := range out.Results {
		if i >= searchResults {
			break
		}
		found = append(found, r.URL)
		s = append(s, fmt.Sprintf("%d. %s <%s> %s", i+1, r.Title, r.URL, strings.Join(strings.Fields(r.Content), " ")))
	}
	searchFound(found)
	if len(s) == 0 {
		return "no results", nil
	}
	return strings.Join(s, "\n"), nil
}

// lastFound is the addresses the latest search returned, from the server's own list rather than the
// text the model reads, whose titles and snippets the web wrote. webTurn.check takes them for its turn,
// one search at a time (searching).
var lastFound struct {
	sync.Mutex
	addrs []string
}

// searchFound keeps a search's addresses for webTurn.check; a variable for the tests.
var searchFound = func(addrs []string) {
	lastFound.Lock()
	lastFound.addrs = addrs
	lastFound.Unlock()
}

// takeFound is the addresses the latest search returned, once.
func takeFound() []string {
	lastFound.Lock()
	defer lastFound.Unlock()
	a := lastFound.addrs
	lastFound.addrs = nil
	return a
}

// answerText is one of the search server's quick answers: older SearXNG sends each as text, newer as
// an object with the text under "answer". Newer ones were all dropped, which left only snippets.
func answerText(a any) string {
	switch v := a.(type) {
	case string:
		return strings.Join(strings.Fields(v), " ")
	case map[string]any:
		if t, ok := v["answer"].(string); ok {
			return strings.Join(strings.Fields(t), " ")
		}
	}
	return ""
}

// clip is s cut to about n bytes at a space, with an ellipsis when cut.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := s[:n]
	if i := strings.LastIndexByte(cut, ' '); i > n/2 {
		cut = cut[:i]
	}
	return strings.ToValidUTF8(cut, "") + " ..."
}

// lastQuery is what the latest search was for, which read_page looks for on a long page when the
// model does not say what it wants from it.
var lastQuery struct {
	sync.Mutex
	q string
}

func setLastQuery(q string) {
	lastQuery.Lock()
	lastQuery.q = q
	lastQuery.Unlock()
}

func getLastQuery() string {
	lastQuery.Lock()
	defer lastQuery.Unlock()
	return lastQuery.q
}

// samePage is an address as a comparison goes by: the model writes one back with a slash added or
// dropped, the host in another case, or an escape undone.
func samePage(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return strings.TrimSpace(raw)
	}
	u.Host = strings.ToLower(u.Host)
	u.Fragment = ""
	s := u.String()
	if uq, err := url.QueryUnescape(s); err == nil {
		s = uq
	}
	return strings.TrimSuffix(s, "/")
}

func readPage(addr, want string) (string, error) {
	u, err := url.Parse(addr)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", fmt.Errorf("%q is not a web address", addr)
	}
	page, err := fetchPage(u)
	var text, data string
	if err == nil {
		text, data = readable(page)
	}
	// A page that is all scripts, or a site that turns away a plain request, is read again by the page
	// reader, a browser. Only a page that answered: one this device would not connect to (pageClient)
	// is not handed to something that might.
	var answered *pageStatus
	if r := config.Get().Brain.Reader; r != "" && ((err == nil && len(text) < thinPage) || errors.As(err, &answered)) {
		if h, rerr := renderPage(r, u); rerr != nil {
			slog.Info("assistant: the page reader could not read it", "host", u.Host, "err", rerr)
		} else if t, d := readable(h); len(t) > len(text) {
			text, data, err = t, d, nil
		}
	}
	if err != nil {
		return "", err
	}
	if text == "" && data == "" {
		return "the page has no text to read (it may only fill itself in with scripts); try another result", nil
	}
	// The data has the room the text leaves, and some always: a schedule's text beats its data, but a
	// page filled in by scripts often has only the data it gives search engines.
	room := min(len(data), max(pageChars-len(text), pageChars/6))
	text = pick(text, want, pageChars-room)
	if data != "" {
		text = strings.TrimSpace(text + "\n\nThe page's own data:\n" + clip(data, room))
	}
	return "The page says (answer only from this; if it does not say, read another result):\n" + text, nil
}

// thinPage is less text than a page that says anything has: a page that fills itself in with
// scripts has its title, a "loading", and the cookie notice.
const thinPage = 500

// pageStatus is a page that answered, but not with the page.
type pageStatus struct {
	host, status string
}

func (e *pageStatus) Error() string { return e.host + " answered " + e.status }

// fetchPage is a page as it comes, from the internet only (pageClient).
func fetchPage(u *url.URL) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", err
	}
	// Some sites send nothing to a client that does not look like a browser.
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux armv7l) TECHO5 voice assistant")
	resp, err := pageClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("%s did not answer", u.Host)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", &pageStatus{u.Host, resp.Status}
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, pageMax))
	return string(b), err
}

// renderPage is a page as a browser ends up with it, from the page reader (config.Brain.Reader): on
// this house's network like the search server, so readerClient, which may reach it, and not
// pageClient; its own firewall keeps its browser on the internet (tools/page-reader).
func renderPage(reader string, u *url.URL) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(reader, "/")+"/read?"+url.Values{"url": {u.String()}}.Encode(), nil)
	if err != nil {
		return "", err
	}
	resp, err := readerClient.Do(req)
	if err != nil {
		return "", errors.New("the page reader did not answer")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("the page reader answered %s", resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, pageMax))
	return string(b), err
}

// readerClient waits for a browser: loading a page and its scripts takes seconds.
var readerClient = &http.Client{Timeout: 30 * time.Second}

// pageText is a page's readable text: its main content when it marks one, without scripts, menus,
// headers, footers, sidebars and cookie notices, a line for each block. A sports site's schedule page is
// some twenty thousand characters of menus before the schedule, and only part of a page goes back to the
// model: it answered from what it had not seen.
func pageText(s string) string {
	t, _ := readable(s)
	return t
}

// readable is a page's text (pageText) and what its structured data and description say, which a
// page built by scripts has when it has no text.
func readable(s string) (text, data string) {
	doc, err := html.Parse(strings.NewReader(s))
	if err != nil {
		return "", ""
	}
	root := doc
	if m := find(doc, "main"); m != nil && len(textOf(m)) > 500 {
		root = m
	} else if a := find(doc, "article"); a != nil && len(textOf(a)) > 500 {
		root = a
	}
	text = textOf(root)
	var d []string
	if desc := metaDescription(doc); desc != "" && !strings.Contains(text, desc) {
		d = append(d, desc)
	}
	for _, l := range linkedData(doc) {
		// Not the description the meta tag gave already. Other repeats stay: a schedule's every game
		// has its home team.
		if _, v, _ := strings.Cut(l, ": "); len(d) == 0 || v != d[0] {
			d = append(d, l)
		}
	}
	return text, strings.Join(d, "\n")
}

// metaDescription is the page's description for search engines and link previews.
func metaDescription(doc *html.Node) string {
	var desc string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if desc != "" {
			return
		}
		if n.Type == html.ElementNode && n.Data == "meta" {
			if k := strings.ToLower(attr(n, "name") + attr(n, "property")); k == "description" || k == "og:description" {
				desc = strings.Join(strings.Fields(attr(n, "content")), " ")
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return clip(desc, 400)
}

// linkedData is the page's JSON-LD (schema.org) as lines of "what: value": the game, its start and its
// place on a schedule, a store's opening hours, an article's date. Addresses and pictures are left out.
func linkedData(doc *html.Node) []string {
	var out []string
	size := 0
	var leaf func(path string, v any)
	leaf = func(path string, v any) {
		if size > pageChars {
			return
		}
		switch x := v.(type) {
		case map[string]any:
			if t, _ := x["@type"].(string); ldTypesSkipped[t] {
				return
			}
			// In a steady order, what it is first: a map's own order changes from one read to the next.
			keys := make([]string, 0, len(x))
			for k := range x {
				if !ldSkipped[k] {
					keys = append(keys, k)
				}
			}
			sort.Slice(keys, func(a, b int) bool {
				if ta, tb := keys[a] == "@type", keys[b] == "@type"; ta != tb {
					return ta
				}
				return keys[a] < keys[b]
			})
			for _, k := range keys {
				leaf(ldPath(path, k), x[k])
			}
		case []any:
			for _, c := range x {
				leaf(path, c)
			}
		case string, float64, bool:
			s := strings.Join(strings.Fields(fmt.Sprint(x)), " ")
			if s == "" || strings.HasPrefix(s, "http") {
				return
			}
			l := path + ": " + clip(s, 300)
			out = append(out, l)
			size += len(l)
		}
	}
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "script" && strings.EqualFold(attr(n, "type"), "application/ld+json") {
			if n.FirstChild != nil {
				var v any
				if json.Unmarshal([]byte(n.FirstChild.Data), &v) == nil {
					leaf("", v)
				}
			}
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return out
}

// ldSkipped are JSON-LD keys whose values are never the answer.
var ldSkipped = map[string]bool{"@context": true, "@id": true, "url": true, "image": true, "logo": true,
	"sameAs": true, "thumbnailUrl": true, "potentialAction": true, "contentUrl": true, "embedUrl": true}

// ldTypesSkipped are JSON-LD things that are the site's, not the page's: its trail of links, its search
// box, its pictures.
var ldTypesSkipped = map[string]bool{"BreadcrumbList": true, "ListItem": true, "SearchAction": true,
	"WebSite": true, "ImageObject": true, "SiteNavigationElement": true}

// ldPath names a JSON-LD value by its last two keys, which is enough to read: "homeTeam name".
func ldPath(path, k string) string {
	if k == "@type" {
		k = "type"
	}
	if i := strings.LastIndexByte(path, ' '); i >= 0 {
		path = path[i+1:]
	}
	return strings.TrimSpace(path + " " + k)
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

// pick is up to limit bytes of a page's text: all of it when it fits, else its opening and the lines
// that have the most of what is wanted, with a few lines after each (a schedule's row runs over
// several), in the page's order. The first part alone was often menus and cookie text, or the season
// before the game asked about.
func pick(text, want string, limit int) string {
	if len(text) <= limit {
		return text
	}
	terms := keywords(want)
	if len(terms) == 0 {
		return clip(text, limit)
	}
	var lines []string
	for _, l := range strings.Split(text, "\n") {
		// A page without blocks is one long line: in pieces, a match is not the whole page.
		for len(l) > 400 {
			i := strings.LastIndexByte(l[:300], ' ')
			if i < 150 {
				i = 300
			}
			lines = append(lines, l[:i])
			l = strings.TrimSpace(l[i:])
		}
		lines = append(lines, l)
	}
	score := make([]int, len(lines))
	var order []int
	for i, l := range lines {
		low := strings.ToLower(l)
		for _, t := range terms {
			if strings.Contains(low, t) {
				score[i]++
			}
		}
		if score[i] > 0 {
			order = append(order, i)
		}
	}
	if len(order) == 0 {
		return clip(text, limit)
	}
	sort.SliceStable(order, func(a, b int) bool { return score[order[a]] > score[order[b]] })
	keep := make([]bool, len(lines))
	size := 0
	add := func(i int) {
		// Each line costs its newline too, and room for the gap mark that may come before it.
		if i < 0 || i >= len(lines) || keep[i] {
			return
		}
		if n := len(lines[i]) + 5; size+n <= limit {
			keep[i] = true
			size += n
		}
	}
	for i := 0; i < len(lines) && size < limit/8; i++ {
		add(i) // the opening: what the page is
	}
	for _, i := range order {
		if size >= limit {
			break
		}
		// The line, then what follows it, then the one before, as room allows.
		for _, j := range []int{i, i + 1, i + 2, i + 3, i - 1} {
			add(j)
		}
	}
	var b strings.Builder
	gap := false
	for i, l := range lines {
		if !keep[i] {
			gap = true
			continue
		}
		if gap && b.Len() > 0 {
			b.WriteString("...\n")
		}
		gap = false
		b.WriteString(l)
		b.WriteByte('\n')
	}
	return strings.TrimSpace(b.String())
}

// keywords are the words of what is wanted that find it on a page: lower case, without the little
// words every page has.
func keywords(want string) []string {
	var out []string
	seen := map[string]bool{}
	for _, w := range strings.FieldsFunc(strings.ToLower(want), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if seen[w] || stopWords[w] || (len(w) < 3 && !unicode.IsDigit(rune(w[0]))) {
			continue
		}
		seen[w] = true
		out = append(out, w)
	}
	return out
}

var stopWords = map[string]bool{"the": true, "and": true, "for": true, "with": true, "what": true, "when": true,
	"where": true, "who": true, "why": true, "how": true, "does": true, "did": true, "are": true, "was": true,
	"were": true, "this": true, "that": true, "from": true, "into": true, "about": true, "will": true,
	"can": true, "you": true, "your": true, "our": true, "its": true, "has": true, "have": true, "page": true}

// skipped are elements whose text is never what the page is about.
var skipped = map[string]bool{"script": true, "style": true, "noscript": true, "svg": true, "head": true,
	"nav": true, "header": true, "footer": true, "aside": true, "form": true, "template": true, "iframe": true,
	"select": true, "option": true} // a drop-down's choices: every season back to 1890

// blocks end a line.
var blocks = map[string]bool{"p": true, "div": true, "li": true, "tr": true, "br": true, "h1": true, "h2": true,
	"h3": true, "h4": true, "h5": true, "h6": true, "section": true, "article": true, "table": true, "dt": true, "dd": true}

// clutterRoles are the roles of a page's menus and banners, however they are built.
var clutterRoles = map[string]bool{"navigation": true, "banner": true, "contentinfo": true, "complementary": true,
	"dialog": true, "alertdialog": true, "menu": true, "menubar": true, "search": true}

// clutterWords, as a whole word of an element's class or id, mark what is not the page's content:
// cookie notices, site menus, ads, share buttons. A whole word, so "navy-schedule" stays. Not "menu":
// a restaurant's menu is what its page is about.
var clutterWords = map[string]bool{"cookie": true, "cookies": true, "consent": true, "gdpr": true, "nav": true,
	"navbar": true, "navigation": true, "sidebar": true, "breadcrumb": true, "breadcrumbs": true,
	"ad": true, "ads": true, "advert": true, "advertisement": true, "newsletter": true, "share": true,
	"social": true, "promo": true, "popup": true, "modal": true, "subscribe": true, "paywall": true}

// neverClutter are the elements that hold the whole page, whatever their class says ("has-nav").
var neverClutter = map[string]bool{"html": true, "body": true, "main": true, "article": true}

// clutter is whether an element is a page's menus, notices or ads rather than what it is about. A
// class word alone does not make a wrapper clutter: "with-sidebar" or "ad-free" can hold the whole
// page, so one that holds the page's heading, its main part, an article or a table, or most of the
// text being read (total, its length) is kept.
func clutter(n *html.Node, total func() int) bool {
	if neverClutter[n.Data] {
		return false
	}
	if hasAttr(n, "hidden") || strings.EqualFold(attr(n, "aria-hidden"), "true") || clutterRoles[strings.ToLower(attr(n, "role"))] {
		return true
	}
	for _, w := range strings.FieldsFunc(strings.ToLower(attr(n, "class")+" "+attr(n, "id")), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if clutterWords[w] {
			return !holdsContent(n) && 2*rawLen(n) <= total()
		}
	}
	return false
}

// holdsContent is whether an element has the page's heading, main part, an article or a table in it.
func holdsContent(n *html.Node) bool {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode {
			continue
		}
		switch c.Data {
		case "h1", "main", "article", "table":
			return true
		}
		if strings.EqualFold(attr(c, "role"), "main") || holdsContent(c) {
			return true
		}
	}
	return false
}

// rawLen is how much text an element has, scripts and styles aside, before anything is dropped.
func rawLen(n *html.Node) int {
	if n.Type == html.TextNode {
		return len(strings.TrimSpace(n.Data))
	}
	if n.Type == html.ElementNode && (n.Data == "script" || n.Data == "style" || n.Data == "noscript" || n.Data == "template") {
		return 0
	}
	l := 0
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		l += rawLen(c)
	}
	return l
}

func hasAttr(n *html.Node, key string) bool {
	for _, a := range n.Attr {
		if a.Key == key {
			return true
		}
	}
	return false
}

func find(n *html.Node, tag string) *html.Node {
	if n.Type == html.ElementNode && n.Data == tag {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if f := find(c, tag); f != nil {
			return f
		}
	}
	return nil
}

func textOf(n *html.Node) string {
	total := -1
	totalLen := func() int {
		if total < 0 {
			total = rawLen(n)
		}
		return total
	}
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && (skipped[n.Data] || clutter(n, totalLen)) {
			return
		}
		if n.Type == html.TextNode {
			if t := strings.Join(strings.Fields(n.Data), " "); t != "" {
				b.WriteString(t)
				b.WriteByte(' ')
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
		if n.Type == html.ElementNode && blocks[n.Data] {
			b.WriteByte('\n')
		}
	}
	walk(n)
	var lines []string
	for _, l := range strings.Split(b.String(), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	return strings.Join(lines, "\n")
}
