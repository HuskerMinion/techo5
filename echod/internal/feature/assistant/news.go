package assistant

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/mail"
	"net/url"
	"strings"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
	"github.com/HuskerMinion/techo5/echod/internal/lib/llm"
	"github.com/HuskerMinion/techo5/echod/internal/lib/radiobrowser"
)

// News updates by voice: "play the Fox news update", "what's the news". A news update is a short
// newscast put out again every hour or so as a podcast, and what plays is its newest episode, once,
// as a track of its own: not a station, so it is never fetched again when it ends.

// builtInNews is the news updates every device has, in alphabetical order: each is a few minutes long
// and put out again about every hour.
var builtInNews = []config.NewsSource{
	{Name: "ABC News Update", Feed: "https://feeds.megaphone.fm/ESP9792844572"},
	{Name: "Fox News Hourly Update", Feed: "https://feeds.megaphone.fm/FOXM2252206613"},
	{Name: "NPR News Now", Feed: "https://feeds.npr.org/500005/podcast.xml"},
}

// newsSources is the household's own news updates (config.Brain.News), then the built-in ones not
// named the same. own is how many of them are the household's.
func newsSources() (all []config.NewsSource, own int) {
	mine, _ := config.Get().Brain.NewsSources()
	all = append(all, mine...)
	for _, b := range builtInNews {
		if !hasNews(mine, b.Name) {
			all = append(all, b)
		}
	}
	return all, len(mine)
}

func hasNews(ss []config.NewsSource, name string) bool {
	for _, s := range ss {
		if strings.EqualFold(s.Name, name) {
			return true
		}
	}
	return false
}

func newsNames(ss []config.NewsSource) string {
	n := make([]string, len(ss))
	for i, s := range ss {
		n[i] = s.Name
	}
	return strings.Join(n, ", ")
}

func newsTools() []tool {
	all, _ := newsSources()
	return []tool{
		{llm.Tool{Name: "play_news", Description: "Play a news update: the newest episode of a short newscast, put out again every hour or so. " +
			"For the news, a news update or a news briefing. The sources: " + newsNames(all) + ".",
			Parameters: object(map[string]any{"source": str("Which news update, as asked for (Fox, NPR); empty when none was named.")})},
			func(a map[string]any) (string, error) { return playNews(argString(a, "source")) }},
	}
}

// newsWords are words any source's name might share, which say nothing about which one is meant.
var newsWords = map[string]bool{"news": true, "update": true, "updates": true, "hourly": true, "now": true,
	"the": true, "latest": true, "today": true, "todays": true, "today's": true, "briefing": true,
	"podcast": true, "newscast": true, "headlines": true, "my": true, "radio": true, "a": true, "an": true}

// newsWordsOf is the words of s that tell one source from another.
func newsWordsOf(s string) []string {
	var out []string
	for _, w := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '\'')
	}) {
		if !newsWords[w] {
			out = append(out, w)
		}
	}
	return out
}

// pickNews is the source asked for: the one sharing the most telling words with it, the first of
// those on a tie. Nothing telling asked - "the news" - is the household's first one, or, with none of
// its own, a question back.
func pickNews(asked string, all []config.NewsSource, own int) (config.NewsSource, error) {
	want := newsWordsOf(asked)
	if len(want) == 0 {
		if own > 0 {
			return all[0], nil
		}
		return config.NewsSource{}, fmt.Errorf("no source was named: ask which of these: %s", newsNames(all))
	}
	best, score := -1, 0
	for i, s := range all {
		have := newsWordsOf(s.Name)
		n := 0
		for _, w := range want {
			for _, h := range have {
				if w == h {
					n++
					break
				}
			}
		}
		if n > score {
			best, score = i, n
		}
	}
	if best < 0 {
		return config.NewsSource{}, fmt.Errorf("there is no news update called %q; the sources are: %s", asked, newsNames(all))
	}
	return all[best], nil
}

// episode is a feed's newest episode.
type episode struct {
	Title string
	Audio string
	At    time.Time
}

// rssItem is as much of a feed's item as is read.
type rssItem struct {
	Title     string `xml:"title"`
	PubDate   string `xml:"pubDate"`
	Enclosure struct {
		URL  string `xml:"url,attr"`
		Type string `xml:"type,attr"`
	} `xml:"enclosure"`
}

// feedItems is how many of a feed's items are looked at for the newest: they come newest first, and a
// feed can hold years of them.
const feedItems = 30

// feedMost is the most of a feed that is read.
const feedMost = 4 << 20

// newestEpisode reads a podcast feed for its newest episode with audio.
func newestEpisode(r io.Reader) (episode, error) {
	d := xml.NewDecoder(io.LimitReader(r, feedMost))
	d.Strict = false
	// A feed that says it is in an older character set (ISO-8859-1, windows-1252) is read as it is:
	// the addresses and dates that matter are plain ASCII either way.
	d.CharsetReader = func(_ string, in io.Reader) (io.Reader, error) { return in, nil }
	var best episode
	for seen := 0; seen < feedItems; {
		tok, err := d.Token()
		if err != nil {
			break
		}
		se, ok := tok.(xml.StartElement)
		if !ok || se.Name.Local != "item" {
			continue
		}
		seen++
		var it rssItem
		if d.DecodeElement(&it, &se) != nil {
			break
		}
		u, err := url.Parse(strings.TrimSpace(it.Enclosure.URL))
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			continue
		}
		if t := it.Enclosure.Type; t != "" && !strings.HasPrefix(t, "audio/") {
			continue
		}
		at, _ := mail.ParseDate(strings.TrimSpace(it.PubDate))
		if best.Audio == "" || at.After(best.At) {
			best = episode{Title: strings.TrimSpace(it.Title), Audio: u.String(), At: at}
		}
	}
	if best.Audio == "" {
		return episode{}, errors.New("its feed has no episode with audio")
	}
	return best, nil
}

// feedClient reads feeds. A feed on the internet may not redirect into the home, for the same reason
// its audio may not be there (fetchEpisode).
var feedClient = &http.Client{
	Timeout: 20 * time.Second,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		if radiobrowser.Resolves(req.Context(), via[0].URL.String()) && !radiobrowser.Resolves(req.Context(), req.URL.String()) {
			return errors.New("a feed on the internet redirected to an address that is not public")
		}
		return nil
	},
}

// fetchEpisode is a source's newest episode; a variable for the tests.
var fetchEpisode = func(ctx context.Context, feed string) (episode, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, feed, nil)
	if err != nil {
		return episode{}, err
	}
	req.Header.Set("User-Agent", "TECHO5/1.0 (github.com/HuskerMinion/techo5)")
	resp, err := feedClient.Do(req)
	if err != nil {
		// Without the feed's address, which the model is told and a household's may hold a token in.
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return episode{}, fmt.Errorf("its feed could not be read: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return episode{}, fmt.Errorf("its feed could not be read: %s", resp.Status)
	}
	ep, err := newestEpisode(resp.Body)
	if err != nil {
		return episode{}, err
	}
	// A feed on the internet names audio on the internet: one that points into the home would have
	// the device fetch from its own network for a stranger.
	if radiobrowser.Resolves(ctx, feed) && !radiobrowser.Resolves(ctx, ep.Audio) {
		return episode{}, errors.New("its newest episode is not at a public address")
	}
	return ep, nil
}

// playEpisode plays an episode, named on the screen; a variable for the tests.
var playEpisode = func(audio string, title media.Title) error {
	return media.Get().PlayTitledChecked(audio, title, playCheck)
}

// staleNews is how old a news update may be before the model is told how old it is.
const staleNews = 6 * time.Hour

func playNews(asked string) (string, error) {
	all, own := newsSources()
	src, err := pickNews(asked, all, own)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	ep, err := fetchEpisode(ctx, src.Feed)
	if err != nil {
		return "", fmt.Errorf("%s did not play: %v", src.Name, err)
	}
	if err := playEpisode(ep.Audio, media.Title{Title: src.Name, Subtitle: ep.Title}); err != nil {
		return "", fmt.Errorf("%s did not play: %v", src.Name, err)
	}
	out := "playing " + src.Name
	if !ep.At.IsZero() && time.Since(ep.At) > staleNews {
		// A source that has stopped putting new ones out is not today's news, and is not said to be.
		out += " (its newest is from " + ep.At.Local().Format("Monday January 2 at 3 PM") + ")"
	}
	return out, nil
}
